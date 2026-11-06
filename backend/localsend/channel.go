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
//   pin：可空，空则自动生成 6 位数字
func NewChannel(name, visibility string, port int, localIP, saveDir, pin, ownerName, ownerIP, ownerDeviceID string) *Channel {
	if visibility == "" { visibility = VisibilityPrivate }
	if pin == "" { pin = genPIN() }
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

// DefaultLobby 大厅（公有，自动生成）
func DefaultLobby(localIP, saveDir, ownerName, ownerDeviceID string) *Channel {
	return NewChannel("大厅", VisibilityPublic, 0, localIP, saveDir, "", ownerName, localIP, ownerDeviceID)
}

// Start 启动频道自己的 LocalSend server
func (c *Channel) Start(saveDir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.saveDir = saveDir
	c.server = NewServer(saveDir, ServerCallbacks{
		OnSendRequest: func(sender *SendRequest) *SendResponse {
			return &SendResponse{Status: "accept"}
		},
	}, c.LocalIP)
	c.server.SetChannelInfo(c.Name, c.PIN, c.ID, c.Visibility)
	actual, err := c.server.Listen(c.Port)
	if err != nil {
		return fmt.Errorf("channel listen on port %d: %w", c.Port, err)
	}
	c.Port = actual
	return nil
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
	c.mu.Lock()
	defer c.mu.Unlock()
	m := Message{
		ID: genMessageID(), ChannelID: c.ID, SenderID: senderID, Sender: sender,
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

// AddFile / RemoveFile
func (c *Channel) AddFile(f FileMetadata)            { c.mu.Lock(); defer c.mu.Unlock(); c.Files = append(c.Files, f) }
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
	for _, f := range c.Files { files = append(files, f) }
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
