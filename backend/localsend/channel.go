// 频道：公有（大厅默认）或私有；聊天记录落盘到 saveDir/chat-records/<id>.json
package localsend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

// Member 频道成员
type Member struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	DeviceID string    `json:"deviceId"` // MAC@IP，唯一标识
	IP       string    `json:"ip"`
	IsOwner  bool      `json:"isOwner"`
	JoinedAt time.Time `json:"joinedAt"`
}

// Message 消息
type Message struct {
	ID        string    `json:"id"`
	ChannelID string    `json:"channelId"`
	SenderID  string    `json:"senderId"`  // member ID
	DeviceID  string    `json:"deviceId"`  // MAC@IP（唯一标识不同设备）
	Sender    string    `json:"sender"`
	Content   string    `json:"content"`
	Type      string    `json:"type"` // text / file / system
	CreatedAt time.Time `json:"createdAt"`
}

// Channel 频道本体
type Channel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PIN        string `json:"pin"`
	Port       int    `json:"port"`
	LocalIP    string `json:"localIp"`
	Visibility string `json:"visibility"` // public / private
	Deleted    bool   `json:"-"`

	CreatedAt time.Time `json:"createdAt"`

	Files    []FileMetadata `json:"files"`
	Members  []Member       `json:"members"`
	Messages []Message      `json:"-"` // 不落 JSON 返回，Snapshot 里手动给

	server *Server
	mu     sync.Mutex
	saveDir string // 聊天记录落盘目录
}

func genRandom(n int) string    { b := make([]byte, n); rand.Read(b); return hex.EncodeToString(b) }
func genChannelID() string      { return genRandom(6) }
func genMemberID() string       { return genRandom(8) }
func GenMemberID() string       { return genMemberID() } // 导出给 main 用
func genMessageID() string      { return genRandom(8) }
func genPIN() string {
	b := make([]byte, 3)
	rand.Read(b)
	return fmt.Sprintf("%06d", int(b[0])<<16|int(b[1])<<8|int(b[2]))
}

// NewChannel 创建频道
//   pin：可空；private 频道空则自动生成 6 位，public 频道空则保持空
func NewChannel(name, visibility string, port int, localIP, saveDir, pin, ownerName, ownerIP, ownerDeviceID string) *Channel {
	if visibility == "" { visibility = VisibilityPrivate }
	if pin == "" && visibility == VisibilityPrivate {
		pin = genPIN()
	}
	c := &Channel{
		ID:         genChannelID(),
		Name:       name,
		PIN:        pin,
		Port:       port,
		LocalIP:    localIP,
		Visibility: visibility,
		CreatedAt:  time.Now(),
		Files:      []FileMetadata{},
		Members: []Member{{
			ID: genMemberID(), Name: ownerName, DeviceID: ownerDeviceID,
			IP: ownerIP, IsOwner: true, JoinedAt: time.Now(),
		}},
		saveDir: saveDir,
	}
	c.loadMessages()
	if len(c.Messages) == 0 {
		c.AppendMessage("", "系统", fmt.Sprintf("频道 \"%s\" 已创建", name), "system")
	}
	return c
}

// DefaultLobby 公有频道（自动生成，public 无 PIN）
func DefaultLobby(localIP, saveDir, ownerName, ownerDeviceID string) *Channel {
	return NewChannel("公有频道", VisibilityPublic, 0, localIP, saveDir, "", ownerName, localIP, ownerDeviceID)
}

// Start —— 频道不需要独立 server 了，所有频道共享 main server（一个端口）
// 这里只记录 saveDir，server 的注册/listen/路由分发全由 main server 统一处理
func (c *Channel) Start(saveDir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.saveDir = saveDir
	// 不再创建独立 server —— 由 Plugin 的 main server 统一承载所有频道
	return nil
}

// SaveMeta 立即把频道元数据（名称/PIN/可见性/成员）落盘，重启后可恢复
func (c *Channel) SaveMeta() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.saveMessages()
}

// Delete 彻底删除频道（停 server + 删磁盘记录）
func (c *Channel) Delete() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Deleted = true
	if c.server != nil {
		c.server.Close()
		c.server = nil
	}
	os.Remove(c.chatLogPath())
}

// StopServer 只停 server，保留频道实体和磁盘记录
func (c *Channel) StopServer() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.server != nil {
		c.server.Close()
		c.server = nil
	}
}

// AddMember 加成员；已存在则更新
func (c *Channel) AddMember(name, ip, deviceID, memberID string) Member {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, m := range c.Members {
		if m.DeviceID != "" && deviceID != "" && m.DeviceID == deviceID {
			if name != "" {
				c.Members[i].Name = name
			}
			if ip != "" {
				c.Members[i].IP = ip
			}
			return c.Members[i]
		}
		if m.ID == memberID {
			if name != "" {
				c.Members[i].Name = name
			}
			if ip != "" {
				c.Members[i].IP = ip
			}
			return c.Members[i]
		}
	}
	m := Member{ID: memberID, Name: name, DeviceID: deviceID, IP: ip, JoinedAt: time.Now()}
	c.Members = append(c.Members, m)
	c.AppendMessage("", "系统", fmt.Sprintf("%s 加入了频道", name), "system")
	return m
}

// RemoveMember 移除
func (c *Channel) RemoveMember(memberID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, m := range c.Members {
		if m.ID == memberID {
			c.Members = append(c.Members[:i], c.Members[i+1:]...)
			c.AppendMessage("", "系统", fmt.Sprintf("%s 离开了频道", m.Name), "system")
			return
		}
	}
}

// AppendMessage 加一条消息并落盘
func (c *Channel) AppendMessage(senderID, sender, content, msgType string) Message {
	return c.AppendMessageFrom(senderID, "", sender, content, msgType)
}

// AppendMessageFrom 带设备标识的消息：deviceID 用于识别“是不是本机发的”
func (c *Channel) AppendMessageFrom(senderID, deviceID, sender, content, msgType string) Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := Message{
		ID: genMessageID(), ChannelID: c.ID, SenderID: senderID, DeviceID: deviceID, Sender: sender,
		Content: content, Type: msgType, CreatedAt: time.Now(),
	}
	c.Messages = append(c.Messages, m)
	if len(c.Messages) > 500 {
		c.Messages = c.Messages[len(c.Messages)-500:]
	}
	c.saveMessages()
	return m
}

// UpdateInfo 更新频道可配置项（名称、PIN、可见性）
func (c *Channel) UpdateInfo(name, pin, visibility string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if name != "" { c.Name = name }
	if pin != "" { c.PIN = pin }
	if visibility != "" { c.Visibility = visibility }
	if c.server != nil {
		c.server.SetChannelInfo(c.Name, c.PIN, c.ID, c.Visibility)
	}
}

// fileDir 频道文件落盘目录（saveDir/channel-files/<channelID>）
func (c *Channel) fileDir() string {
	if c.saveDir == "" {
		return ""
	}
	return filepath.Join(c.saveDir, "channel-files", c.ID)
}

// FileDir 导出给 main 打开目录用
func (c *Channel) FileDir() string { return c.fileDir() }

// sanitizeFID 只保留字母数字和 - _，避免路径穿越
func sanitizeFID(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return -1
	}, s)
}

// StoreFileData 把上传的文件字节写入频道目录，并登记到 Files
func (c *Channel) StoreFileData(fid, name, mime string, data []byte) (FileMetadata, error) {
	if c.saveDir == "" {
		return FileMetadata{}, fmt.Errorf("未配置保存目录")
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		name = "unnamed"
	}
	fid = sanitizeFID(fid)
	if fid == "" {
		fid = genRandom(6)
	}
	dir := c.fileDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return FileMetadata{}, err
	}
	path := filepath.Join(dir, fid+"_"+name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return FileMetadata{}, err
	}
	fm := FileMetadata{ID: fid, FileName: name, Size: int64(len(data)), MIME: mime, LocalPath: path}
	c.AddFile(fm)
	return fm, nil
}

// FileByID 按 ID 找已登记的文件
func (c *Channel) FileByID(id string) (FileMetadata, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Files {
		if c.Files[i].ID == id {
			return c.Files[i], true
		}
	}
	return FileMetadata{}, false
}

// AddFile / RemoveFile
func (c *Channel) AddFile(f FileMetadata) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Files = append(c.Files, f)
	c.saveMessages()
}
func (c *Channel) RemoveFile(id string) {
	c.mu.Lock(); defer c.mu.Unlock()
	for i, f := range c.Files {
		if f.ID == id { c.Files = append(c.Files[:i], c.Files[i+1:]...); return }
	}
}

// ShareURL 返回分享链接（HTTP + 自定义协议双版本）
// http://IP:port 浏览器访问会看到友好提示页
// localsend://... 客户端可以直接唤起
func (c *Channel) ShareURL(scheme string) string {
	httpURL := fmt.Sprintf("%s://%s:%d?channel=%s&pin=%s", scheme, c.LocalIP, c.Port, c.ID, c.PIN)
	if scheme == "http" {
		// 返回可点击的 HTTP URL；频道 server 的根路径会返回友好页面
		return httpURL
	}
	// 自定义协议唤起客户端
	return fmt.Sprintf("localsend://join?channel=%s&pin=%s&host=%s&port=%d", c.ID, c.PIN, c.LocalIP, c.Port)
}

// Snapshot 拷贝供 JSON 返回（消息也带进去）
func (c *Channel) Snapshot() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	members := make([]any, 0, len(c.Members))
	for _, m := range c.Members { members = append(members, m) }
	msgs := make([]any, 0, len(c.Messages))
	for _, m := range c.Messages { msgs = append(msgs, m) }
	files := make([]any, 0, len(c.Files))
	for _, f := range c.Files {
		f.LocalPath = "" // 不把本机磁盘路径暴露给网页/前端
		files = append(files, f)
	}
	return map[string]any{
		"id": c.ID, "name": c.Name, "pin": c.PIN, "port": c.Port,
		"localIp": c.LocalIP, "visibility": c.Visibility,
		"createdAt": c.CreatedAt,
		"members": members, "messages": msgs, "files": files,
	}
}

// ---------- 磁盘落盘 ----------

type chatLogFile struct {
	Channel  *Channel  `json:"channel"`
	Messages []Message `json:"messages"`
	SavedAt  time.Time `json:"savedAt"`
}

func (c *Channel) chatLogPath() string {
	if c.saveDir == "" {
		return ""
	}
	return filepath.Join(c.saveDir, "chat-records", c.ID+".json")
}

func (c *Channel) saveMessages() {
	p := c.chatLogPath()
	if p == "" {
		return
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	data, err := json.MarshalIndent(chatLogFile{
		Channel:  c,
		Messages: c.Messages,
		SavedAt:  time.Now(),
	}, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(p, data, 0o644)
}

func (c *Channel) loadMessages() {
	p := c.chatLogPath()
	if p == "" {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var log chatLogFile
	if err := json.Unmarshal(data, &log); err != nil {
		return
	}
	c.Messages = log.Messages
	// 按时间排序
	sort.Slice(c.Messages, func(i, j int) bool { return c.Messages[i].CreatedAt.Before(c.Messages[j].CreatedAt) })
}

// RestoreFromDisk 在插件启动时从 saveDir 恢复所有频道
func RestoreFromDisk(saveDir, localIP, ownerName, ownerDeviceID string) []*Channel {
	if saveDir == "" {
		return nil
	}
	dir := filepath.Join(saveDir, "chat-records")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*Channel
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var log chatLogFile
		if err := json.Unmarshal(data, &log); err != nil || log.Channel == nil {
			continue
		}
		log.Channel.LocalIP = localIP
		log.Channel.saveDir = saveDir
		log.Channel.Messages = log.Messages
		// 重新生成 ID/PIN/port 字段（磁盘里可能有），但保持 ID 不变用于关联
		if log.Channel.ID == "" {
			continue
		}
		// 兼容迁移：旧的 "大厅" 统一改名为 "公有频道"，且 public 频道强制清空 PIN
		if log.Channel.Visibility == VisibilityPublic {
			log.Channel.PIN = ""
			if log.Channel.Name == "大厅" {
				log.Channel.Name = "公有频道"
			}
		}
		// 确保 owner 成员在
		hasOwner := false
		for _, m := range log.Channel.Members {
			if m.IsOwner { hasOwner = true; break }
		}
		if !hasOwner {
			log.Channel.Members = append(log.Channel.Members, Member{
				ID: genMemberID(), Name: ownerName, DeviceID: ownerDeviceID,
				IP: localIP, IsOwner: true, JoinedAt: time.Now(),
			})
		}
		out = append(out, log.Channel)
	}
	return out
}
