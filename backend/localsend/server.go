// LocalSend 接收端 HTTP 服务器：处理 register / send-request / upload / cancel
package localsend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// pluginLogoSVG 插件图标（与 assets/plugin.svg 一致），网页端内联使用
const pluginLogoSVG = `<svg class="brand-logo" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" aria-hidden="true"><rect x="0" y="0" width="64" height="64" rx="14" fill="#0F766E"/><path d="M22 18h10l8 8v20a2 2 0 0 1-2 2H22a2 2 0 0 1-2-2V20a2 2 0 0 1 2-2z" fill="#FFFFFF"/><path d="M32 18v8h8" stroke="#0F766E" stroke-width="2" fill="none" stroke-linejoin="round"/><path d="M26 30h12M26 36h8" stroke="#0F766E" stroke-width="2" stroke-linecap="round"/><circle cx="44" cy="24" r="1.6" fill="#FFFFFF"/><path d="M41 21a4 4 0 0 1 6 0" stroke="#FFFFFF" stroke-width="2" fill="none" stroke-linecap="round"/><path d="M38 18a8 8 0 0 1 12 0" stroke="#FFFFFF" stroke-width="2" fill="none" stroke-linecap="round"/></svg>`

// IncomingSession 表示一个入站传输会话
type IncomingSession struct {
	ID         string
	Sender     SendInfo
	Files      []FileMetadata
	Accepted   bool
	SaveDir    string
	Received   map[string]int64 // fileID -> received bytes
	completed  chan struct{}
	mu         sync.Mutex
}

type ServerCallbacks struct {
	OnRegister     func(sender *RegisterRequest) *RegisterResponse
	OnSendRequest  func(sender *SendRequest) *SendResponse
	OnUploadChunk  func(session *IncomingSession, fileID string, bytes int64)
	OnSessionDone  func(session *IncomingSession, err error)
}

// Server 是 LocalSend 接收端 HTTP 服务器
type Server struct {
	mu        sync.RWMutex
	sessions  map[string]*IncomingSession
	saveDir   string
	callbacks ServerCallbacks
	bindIP    string

	// ChannelRegistry 让外部注入一个函数，根据频道 ID 找到频道元数据
	// 主 server 用这个来做 /c/<channelID> 路由 + 私有频道 PIN 校验
	ChannelRegistry func(channelID string) (*ChannelMeta, bool)
	ChannelListFn   func() []ChannelMeta // 列出所有频道（用于 /api/channels 和 lobby）
	GetChannelFn    func(channelID string) (*Channel, bool) // 拿真实 Channel 对象（用于 chat endpoint 调 AppendMessage）

	// 可选：单频道模式的信息（兼容旧用法）
	ChannelInfo map[string]string // name / pin / id / visibility

	// /info endpoint 提供的本机信息
	info InfoProvider

	httpSrv *http.Server
	ln      net.Listener
}

// ChannelMeta 是频道元数据（不含敏感字段）
type ChannelMeta struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"` // public / private
	PIN        string `json:"-"`          // 不在 API 返回里（private 时前端校验用）
	Port       int    `json:"port"`       // 监听端口（统一用 cfg.Port）
}

func NewServer(saveDir string, cb ServerCallbacks, bindIP string) *Server {
	return &Server{
		sessions:  make(map[string]*IncomingSession),
		saveDir:   saveDir,
		callbacks: cb,
		bindIP:    bindIP,
	}
}

// SetChannelInfo 让根路径返回带频道信息的页面
func (s *Server) SetChannelInfo(name, pin, id, visibility string) {
	s.ChannelInfo = map[string]string{
		"name": name, "pin": pin, "id": id, "visibility": visibility,
	}
}

// ServerInfo 给 /info endpoint 返回的本机信息
type ServerInfo struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"`
	DeviceModel string `json:"deviceModel,omitempty"`
	DeviceType  string `json:"deviceType"`
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Download    bool   `json:"download"`
}

// InfoProvider 让外部注入 info 字段
type InfoProvider struct {
	Alias       string
	Version     string
	DeviceModel string
	DeviceType  string
	Fingerprint string
	Port        int
	Protocol    string
	Download    bool
}

func (s *Server) SetInfoProvider(p InfoProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info = p
}

func (s *Server) Listen(port int) (int, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/localsend/v2/register", s.handleRegister)
	mux.HandleFunc("/api/localsend/v2/send-request", s.handleSendRequest)
	mux.HandleFunc("/api/localsend/v2/upload", s.handleUpload)
	mux.HandleFunc("/api/localsend/v2/cancel", s.handleCancel)
	mux.HandleFunc("/api/localsend/v2/info", s.handleInfo)
	mux.HandleFunc("/api/channels", s.handleListChannels)           // 频道列表
	mux.HandleFunc("/api/channels/", s.handleChannelMessages)       // /api/channels/<id>/messages | /files
	mux.HandleFunc("/api/open-folder", s.handleOpenFolder)          // 网页端请求宿主电脑打开文件夹
	mux.HandleFunc("/api/open-file", s.handleOpenFile)              // 网页端请求宿主电脑直接打开文件
	mux.HandleFunc("/c/", s.handleChannelPage)                     // /c/<channelID> 频道页面
	mux.HandleFunc("/", s.handleRoot)

	host := s.bindIP
	if host == "" {
		host = "0.0.0.0"
	}
	// 端口重绑重试：关闭页面后重新启动时，旧监听可能仍在 TIME_WAIT，直接 Listen 会报 address already in use
	var ln net.Listener
	var err error
	for i := 0; i < 10; i++ {
		ln, err = net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		return 0, err
	}
	s.ln = ln
	s.httpSrv = &http.Server{Handler: corsHandler(mux)}
	go s.httpSrv.Serve(ln)
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// corsHandler 允许 DBX 插件页面（不同 origin）直接调用本机 HTTP 接口上传/下载文件
func corsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Close() {
	if s.httpSrv != nil { s.httpSrv.Close() }
	if s.ln != nil { s.ln.Close() }
}

// handleRoot —— 浏览器访问 http://IP:port/ 时返回简化的频道列表页
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")

	channels := []ChannelMeta{}
	if s.ChannelListFn != nil {
		channels = s.ChannelListFn()
	}
	if len(channels) == 0 {
		fmt.Fprint(w, "<!doctype html><meta charset='utf-8'><title>局域网快传</title>"+
			"<style>body{font-family:-apple-system,'Segoe UI','Microsoft YaHei',sans-serif;background:#f8fafc;color:#1e293b;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}"+
			"@media(prefers-color-scheme:dark){body{background:#0f172a;color:#f1f5f9}}"+
			".box{text-align:center}h1{font-size:16px;margin:0 0 4px}p{color:#64748b;font-size:12px;margin:0}</style>"+
			"<div class='box'><div style='font-size:32px;margin-bottom:8px;'>📡</div><h1>局域网快传 运行中</h1><p>还没有频道。在 DBX 里创建频道后刷新。</p></div>")
		return
	}

	// 渲染简化 lobby —— 直接注入频道数据，不需要 fetch
	var sb strings.Builder
	sb.WriteString(`<!doctype html><meta charset='utf-8'><meta name='viewport' content='width=device-width,initial-scale=1'><title>局域网快传 · 选择频道</title>`)
	sb.WriteString(`<style>`)
	sb.WriteString(`body{font-family:-apple-system,'Segoe UI','Microsoft YaHei',sans-serif;background:var(--bg,#f8fafc);color:var(--fg,#1e293b);min-height:100vh;margin:0;display:flex;flex-direction:column;align-items:center;padding:40px 20px;box-sizing:border-box}`)
	sb.WriteString(`@media(prefers-color-scheme:dark){body{background:#0f172a;color:#f1f5f9}}`)
	sb.WriteString(`h1{font-size:14px;color:#64748b;font-weight:600;margin:0 0 24px;display:flex;align-items:center;gap:8px}`)
	sb.WriteString(`.brand-logo{width:36px;height:36px;display:inline-block;flex-shrink:0}`)
	sb.WriteString(`.ch-list{display:flex;flex-direction:column;gap:10px;width:100%;max-width:420px}`)
	sb.WriteString(`.ch-item{display:flex;align-items:center;gap:12px;padding:14px 16px;background:var(--card,#fff);border:1px solid var(--border,#e2e8f0);border-radius:12px;cursor:pointer;text-decoration:none;color:inherit;transition:all .15s}`)
	sb.WriteString(`.ch-item:hover{border-color:#2563eb;box-shadow:0 2px 12px rgba(37,99,235,.1)}`)
	sb.WriteString(`@media(prefers-color-scheme:dark){.ch-item{background:#1e293b;border:#334155}.ch-item:hover{border:#3b82f6}}`)
	sb.WriteString(`.ch-icon{font-size:20px;width:40px;height:40px;display:flex;align-items:center;justify-content:center;background:var(--muted,#f1f5f9);border-radius:10px;flex-shrink:0}`)
	sb.WriteString(`@media(prefers-color-scheme:dark){.ch-icon{background:#334155}}`)
	sb.WriteString(`.ch-info{flex:1;min-width:0}`)
	sb.WriteString(`.ch-name{font-size:14px;font-weight:600;margin-bottom:2px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}`)
	sb.WriteString(`.ch-sub{font-size:11.5px;color:#64748b}`)
	sb.WriteString(`.ch-badge{font-size:10px;padding:2px 8px;border-radius:999px;font-weight:600;flex-shrink:0}`)
	sb.WriteString(`.badge-pub{background:rgba(34,197,94,.12);color:#16a34a}`)
	sb.WriteString(`.badge-priv{background:rgba(245,158,11,.12);color:#d97706}`)
	sb.WriteString(`</style>`)
	sb.WriteString("<h1>" + pluginLogoSVG + "局域网快传 · 选择频道</h1>")
	sb.WriteString(`<div class='ch-list'>`)
	for _, c := range channels {
		isPub := c.Visibility == "public"
		icon := "🔒"; badge := `<span class='ch-badge badge-priv'>私有</span>`
		if isPub { icon = "🌐"; badge = `<span class='ch-badge badge-pub'>公有</span>` }
		sb.WriteString(`<a class='ch-item' href='/c/`)
		sb.WriteString(c.ID)
		sb.WriteString(`'>`)
		sb.WriteString(`<div class='ch-icon'>`)
		sb.WriteString(icon)
		sb.WriteString(`</div><div class='ch-info'><div class='ch-name'>`)
		sb.WriteString(c.Name)
		sb.WriteString(`</div><div class='ch-sub'>`)
		if isPub { sb.WriteString("任何人可加入，无需 PIN") } else { sb.WriteString("需要 PIN 码才能加入") }
		sb.WriteString(`</div></div>`)
		sb.WriteString(badge)
		sb.WriteString(`</a>`)
	}
	sb.WriteString(`</div>`)
	fmt.Fprint(w, sb.String())
}

// handleChannelPage —— /c/<channelID> 返回对应频道的 visitor SPA
func (s *Server) handleChannelPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")

	channelID := strings.TrimPrefix(r.URL.Path, "/c/")
	channelID = strings.Trim(channelID, "/")

	if s.ChannelRegistry == nil {
		http.NotFound(w, r)
		return
	}
	meta, ok := s.ChannelRegistry(channelID)
	if !ok {
		http.Error(w, `{"error":"频道不存在"}`, http.StatusNotFound)
		return
	}

	isPublic := meta.Visibility == "public"
	hostOnly := extractHostOnly(r.Host)
	port := s.currentPort()

	fmt.Fprint(w, strings.NewReplacer(
		`{{CHANNEL_NAME}}`, jsEscape(meta.Name),
		`{{CHANNEL_VISIBILITY}}`, map[bool]string{true: "public", false: "private"}[isPublic],
		`{{CHANNEL_VISIBILITY_LABEL}}`, map[bool]string{true: "🌐 公有频道", false: "🔒 私有频道"}[isPublic],
		`{{CHANNEL_ID}}`, jsEscape(meta.ID),
		`{{CHANNEL_PIN}}`, jsEscape(meta.PIN),
		`{{IS_PUBLIC}}`, map[bool]string{true: "true", false: "false"}[isPublic],
		`{{SERVER_PORT}}`, fmt.Sprintf("%d", port),
		`{{SERVER_HOST}}`, jsEscape(hostOnly),
		`{{PLUGIN_LOGO}}`, pluginLogoSVG,
	).Replace(visitorSPA))
}

// handleListChannels —— GET /api/channels 返回所有频道（private 频道只返回元数据，PIN 不返回）
func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.ChannelRegistry == nil {
		json.NewEncoder(w).Encode(map[string]any{"channels": []any{}})
		return
	}
	// 遍历：我们需要一个 channel list，但 registry 是按 ID 查的
	// 简单做法：Plugin 再额外注入一个 List 回调
	if s.ChannelListFn != nil {
		channels := s.ChannelListFn()
		// 擦除 private 频道的 PIN
		for i := range channels {
			if channels[i].Visibility != "public" {
				channels[i].PIN = "" // 不暴露
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"channels": channels})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"channels": []any{}})
}

// handleOpenFile —— 网页端点击「打开」时，用宿主电脑的默认程序打开该文件
func (s *Server) handleOpenFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ChannelID string `json:"channelId"`
		FileID    string `json:"fileId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
		return
	}
	ch, ok := s.GetChannelFn(body.ChannelID)
	if !ok {
		http.Error(w, `{"error":"频道不存在"}`, http.StatusNotFound)
		return
	}
	fm, ok := ch.FileByID(body.FileID)
	if !ok || fm.LocalPath == "" {
		http.Error(w, `{"error":"文件不存在"}`, http.StatusNotFound)
		return
	}
	if _, err := os.Stat(fm.LocalPath); err != nil {
		http.Error(w, `{"error":"文件不存在或已被移动"}`, http.StatusNotFound)
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", fm.LocalPath)
	case "darwin":
		cmd = exec.Command("open", fm.LocalPath)
	default:
		cmd = exec.Command("xdg-open", fm.LocalPath)
	}
	if err := cmd.Start(); err != nil {
		http.Error(w, `{"error":"打开文件失败: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": fm.LocalPath})
}

// handleOpenFolder —— 网页端点击「打开文件夹」时，在宿主电脑上打开资源管理器
func (s *Server) handleOpenFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ChannelID string `json:"channelId"`
		FileID    string `json:"fileId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
		return
	}
	ch, ok := s.GetChannelFn(body.ChannelID)
	if !ok {
		http.Error(w, `{"error":"频道不存在"}`, http.StatusNotFound)
		return
	}
	dir := ch.FileDir()
	if dir == "" {
		http.Error(w, `{"error":"未配置保存目录"}`, http.StatusInternalServerError)
		return
	}
	os.MkdirAll(dir, 0o755)

	target := ""
	if body.FileID != "" {
		if fm, ok := ch.FileByID(body.FileID); ok && fm.LocalPath != "" {
			if _, err := os.Stat(fm.LocalPath); err == nil {
				target = fm.LocalPath
			}
		}
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		if target != "" {
			cmd = exec.Command("explorer.exe", "/select,"+target)
		} else {
			cmd = exec.Command("explorer.exe", dir)
		}
	case "darwin":
		if target != "" {
			cmd = exec.Command("open", "-R", target)
		} else {
			cmd = exec.Command("open", dir)
		}
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		http.Error(w, `{"error":"打开目录失败: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "dir": dir, "file": target})
}

// handleChannelMessages —— GET /api/channels/<id>/messages 拉历史；POST 发消息
func (s *Server) handleChannelMessages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 拆路径 /api/channels/<id>/messages 或 /api/channels/<id>/files[/<fileID>]
	path := strings.TrimPrefix(r.URL.Path, "/api/channels/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 {
		http.Error(w, `{"error":"bad path"}`, http.StatusBadRequest)
		return
	}
	channelID := parts[0]
	kind := parts[1]

	ch, ok := s.GetChannelFn(channelID)
	if !ok {
		http.Error(w, `{"error":"频道不存在"}`, http.StatusNotFound)
		return
	}

	if kind == "files" {
		rest := ""
		if len(parts) > 2 { rest = parts[2] }
		s.handleChannelFiles(w, r, ch, rest)
		return
	}
	if kind != "messages" {
		http.Error(w, `{"error":"bad path"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		ch.mu.Lock()
		msgs := make([]Message, len(ch.Messages))
		copy(msgs, ch.Messages)
		ch.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"messages": msgs})

	case "POST":
		var body struct {
			SenderID string `json:"senderId"`
			DeviceID string `json:"deviceId"`
			Sender   string `json:"sender"`
			Content  string `json:"content"`
			Type     string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
			return
		}
		body.Content = strings.TrimSpace(body.Content)
		if body.Content == "" {
			http.Error(w, `{"error":"empty content"}`, http.StatusBadRequest)
			return
		}
		if len(body.Content) > 2000 {
			http.Error(w, `{"error":"content too long"}`, http.StatusBadRequest)
			return
		}
		if body.Sender == "" {
			body.Sender = "访客"
		}
		msgType := body.Type
		if msgType == "" {
			msgType = "chat"
		}
		m := ch.AppendMessageFrom(body.SenderID, body.DeviceID, body.Sender, body.Content, msgType)
		json.NewEncoder(w).Encode(m)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleChannelFiles —— POST /api/channels/<id>/files 上传文件落盘到频道目录；
// GET /api/channels/<id>/files/<fileID> 下载频道里的文件
func (s *Server) handleChannelFiles(w http.ResponseWriter, r *http.Request, ch *Channel, fileID string) {
	switch r.Method {
	case "POST":
		if err := r.ParseMultipartForm(256 << 20); err != nil {
			http.Error(w, `{"error":"解析上传内容失败: `+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"error":"缺少 file 字段"}`, http.StatusBadRequest)
			return
		}
		defer file.Close()

		name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
		if name == "." || name == "/" || name == "" {
			name = "unnamed"
		}
		fid := strings.TrimSpace(r.FormValue("fileId"))
		if fid == "" {
			fid = genRandom(6)
		}
		fid = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				return r
			}
			return -1
		}, fid)
		if fid == "" {
			fid = genRandom(6)
		}

		dir := ch.fileDir()
		if dir == "" {
			http.Error(w, `{"error":"未配置保存目录"}`, http.StatusInternalServerError)
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			http.Error(w, `{"error":"创建目录失败: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		path := filepath.Join(dir, fid+"_"+name)
		dst, err := os.Create(path)
		if err != nil {
			http.Error(w, `{"error":"写入文件失败: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		written, err := io.Copy(dst, file)
		dst.Close()
		if err != nil {
			os.Remove(path)
			http.Error(w, `{"error":"保存文件失败: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}

		fm := FileMetadata{
			ID: fid, FileName: name, Size: written,
			MIME: header.Header.Get("Content-Type"), LocalPath: path,
		}
		ch.AddFile(fm)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"file":    fm,
			"url":     "/api/channels/" + ch.ID + "/files/" + fid,
		})

	case "GET":
		if fileID == "" {
			http.Error(w, `{"error":"缺少 fileId"}`, http.StatusBadRequest)
			return
		}
		ch.mu.Lock()
		var target *FileMetadata
		for i := range ch.Files {
			if ch.Files[i].ID == fileID {
				f := ch.Files[i]
				target = &f
				break
			}
		}
		ch.mu.Unlock()
		if target == nil {
			http.Error(w, `{"error":"文件不存在"}`, http.StatusNotFound)
			return
		}
		path := target.LocalPath
		if path == "" && ch.saveDir != "" {
			// 兼容旧记录：按 <fileID>_ 前缀在频道目录里找
			matches, _ := filepath.Glob(filepath.Join(ch.fileDir(), fileID+"_*"))
			if len(matches) > 0 {
				path = matches[0]
			}
		}
		if path == "" {
			http.Error(w, `{"error":"文件已不存在"}`, http.StatusNotFound)
			return
		}
		w.Header().Del("Content-Type") // 让 ServeFile 按扩展名推断
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(target.FileName))
		http.ServeFile(w, r, path)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) currentPort() int {
	if s.ln != nil {
		return s.ln.Addr().(*net.TCPAddr).Port
	}
	return 0
}

func extractHostOnly(host string) string {
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end > 0 {
			return host[1:end]
		}
	}
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		return host[:idx]
	}
	return host
}

// jsEscape 简单转义 JS/HTML 字符串
func jsEscape(s string) string {
	s = strings.ReplaceAll(s, `<`, `\u003c`)
	s = strings.ReplaceAll(s, `>`, `\u003e`)
	s = strings.ReplaceAll(s, `"`, `&quot;`)
	s = strings.ReplaceAll(s, `'`, `&#39;`)
	s = strings.ReplaceAll(s, `&`, `&amp;`)
	return s
}

// lobbySPA —— 浏览器访问根路径 / 时看到的频道列表页（一个端口，所有频道）
const lobbySPA = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>局域网快传 · 频道列表</title>
<style>
:root{color-scheme:light dark;--bg:var(--cb,#ffffff);--fg:#27272a;--card:#ffffff;--muted:#f4f4f5;--muted-fg:#71717a;--border:#e4e4e7;--primary:#2563eb;--primary-fg:#fff;--success:#16a34a;--warning:#d97706;--radius:6px;}
@media(prefers-color-scheme:dark){--bg:#18181b;--fg:#f4f4f5;--card:#1b1b1f;--muted:#27272a;--muted-fg:#a1a1aa;--border:#3f3f46;--primary:#60a5fa;--primary-fg:#18181b;--success:#4ade80;}
*{box-sizing:border-box;margin:0;padding:0}
html,body{height:100%}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei",sans-serif;font-size:13px;line-height:1.5;background:var(--bg);color:var(--fg);-webkit-font-smoothing:antialiased;}
button{cursor:pointer;font:inherit;color:var(--fg);background:var(--card);border:1px solid var(--border);border-radius:var(--radius);padding:0 12px;height:32px;font-size:12.5px;transition:all .15s}
button:hover:not(:disabled){background:var(--muted)}
button.primary{background:var(--primary);color:var(--primary-fg);border-color:transparent;font-weight:500}
button.primary:hover{filter:brightness(.94)}
button.ghost{background:transparent;border-color:transparent}
input{font:inherit;color:var(--fg);background:var(--muted);border:none;border-radius:var(--radius);padding:8px 10px;outline:none}
input:focus{background:var(--bg);box-shadow:0 0 0 1px var(--primary) inset}
#app{display:flex;flex-direction:column;min-height:100%}
.topbar{display:flex;align-items:center;gap:14px;flex:none;height:48px;padding:0 20px;border-bottom:1px solid var(--border);background:var(--card)}
.brand{display:inline-flex;align-items:center;gap:8px;font-weight:600;font-size:15px}
.brand-logo{width:24px;height:24px;display:inline-block;flex-shrink:0}
.main{flex:1;max-width:880px;width:100%;margin:0 auto;padding:28px 20px;overflow-y:auto}
.page-title{font-size:18px;font-weight:600;margin-bottom:4px}
.page-sub{font-size:12px;color:var(--muted-fg);margin-bottom:20px}
.channel-card{background:var(--card);border:1px solid var(--border);border-radius:12px;padding:18px;margin-bottom:12px;transition:border-color .15s}
.channel-card:hover{border-color:var(--primary)}
.ch-header{display:flex;align-items:center;gap:10px;margin-bottom:10px}
.ch-name{font-size:15px;font-weight:600}
.badge{display:inline-block;font-size:11px;padding:2px 8px;border-radius:999px;background:var(--muted);color:var(--muted-fg);font-weight:400}
.badge.success{background:color-mix(in srgb,var(--success) 15%,var(--bg));color:var(--success)}
.badge.warning{background:color-mix(in srgb,var(--warning) 15%,var(--bg));color:var(--warning)}
.ch-actions{display:flex;gap:8px}
.empty{padding:48px 20px;text-align:center;color:var(--muted-fg);font-size:12px}
.loading{display:inline-block;width:14px;height:14px;border:2px solid var(--border);border-top-color:var(--primary);border-radius:50%;animation:spin .6s linear infinite;vertical-align:middle}
@keyframes spin{to{transform:rotate(360deg)}}
.modal-backdrop{position:fixed;inset:0;background:rgba(0,0,0,.45);display:flex;align-items:center;justify-content:center;z-index:999}
.modal{background:var(--card);border:1px solid var(--border);border-radius:12px;padding:22px;min-width:320px;box-shadow:0 8px 32px rgba(0,0,0,.18)}
.modal h3{margin:0 0 12px;font-size:14px}
.modal label{display:block;font-size:11.5px;color:var(--muted-fg);margin-bottom:6px}
.modal input{width:100%;margin-bottom:14px;font-family:ui-monospace,Menlo,monospace;font-size:20px;font-weight:700;letter-spacing:6px;text-align:center;padding:12px}
.modal .modal-actions{display:flex;gap:8px;justify-content:flex-end}
.footer-note{font-size:11px;color:var(--muted-fg);text-align:center;padding:20px;line-height:1.6}
</style>
</head>
<body>
<div id="app">
  <div class="topbar">
    <div class="brand">{{PLUGIN_LOGO}}局域网快传</div>
    <div style="flex:1"></div>
    <span style="font-size:11px;color:var(--muted-fg)">HTTP {{SERVER_HOST}}:{{SERVER_PORT}}</span>
  </div>
  <div class="main">
    <div class="page-title">选择一个频道</div>
    <div class="page-sub">共 <b id="chCount">—</b> 个频道。公有频道直接进入，私有频道需要输入 PIN 码。</div>
    <div id="chList"><div class="empty"><span class="loading"></span> 加载中...</div></div>
  </div>
  <div class="footer-note">本页面由局域网快传插件自动生成 · 同一局域网内可访问 · 一个端口承载所有频道</div>
</div>
<script>
(function(){
  var host="{{SERVER_HOST}}";var port={{SERVER_PORT}};
  var base=location.origin;

  function renderList(channels){
    var el=document.getElementById("chList");
    document.getElementById("chCount").textContent=channels.length;
    if(!channels.length){el.innerHTML='<div class="empty">当前没有任何频道</div>';return;}
    var html="";
    for(var i=0;i<channels.length;i++){
      var c=channels[i];
      var isPublic=c.visibility==="public";
      var icon=isPublic?"🌐":"🔒";
      var badge=isPublic?'<span class="badge success">公有 · 无需 PIN</span>':'<span class="badge warning">私有 · 需要 PIN</span>';
      var enterBtn=isPublic?'<button class="primary enter-btn" data-id="'+c.id+'">进入频道</button>':'<button class="primary private-btn" data-id="'+c.id+'" data-name="'+c.name+'">输入 PIN 进入</button>';
      html+='<div class="channel-card"><div class="ch-header"><span style="font-size:16px;">'+icon+'</span><span class="ch-name">'+c.name+'</span>'+badge+'</div><div class="ch-actions">'+enterBtn+'<a href="/c/'+c.id+'" class="ghost" style="font-size:12px;text-decoration:none;">直接打开 /c/'+c.id+' →</a></div></div>';
    }
    el.innerHTML=html;
    el.querySelectorAll(".enter-btn").forEach(function(b){b.addEventListener("click",function(){location.href="/c/"+b.dataset.id;});});
    el.querySelectorAll(".private-btn").forEach(function(b){b.addEventListener("click",function(){openPinModal(b.dataset.id,b.dataset.name);});});
  }

  function openPinModal(id,name){
    var old=document.getElementById("pinModal");if(old)old.remove();
    var m=document.createElement("div");m.id="pinModal";
    m.innerHTML='<div class="modal-backdrop"><div class="modal"><h3>🔒 私有频道 · '+name+'</h3><label>请输入 PIN 码</label><input type="text" id="pinInput" placeholder="六位 PIN" inputmode="numeric" maxlength="8" autofocus><div class="modal-actions"><button class="ghost" id="pinCancel">取消</button><button class="primary" id="pinOk">进入</button></div></div></div>';
    document.body.appendChild(m);
    var input=m.querySelector("#pinInput");input.focus();
    m.querySelector("#pinCancel").addEventListener("click",function(){m.remove();});
    m.querySelector(".modal-backdrop").addEventListener("click",function(e){if(e.target.classList.contains("modal-backdrop"))m.remove();});
    var ok=function(){var v=input.value.trim();if(!v){input.focus();return;}location.href="/c/"+id+"?pin="+encodeURIComponent(v);};
    m.querySelector("#pinOk").addEventListener("click",ok);
    input.addEventListener("keydown",function(e){if(e.key==="Enter")ok();});
  }

  async function load(){
    try{var r=await fetch(base+"/api/channels");var d=await r.json();renderList(d.channels||[]);}
    catch(err){document.getElementById("chList").innerHTML='<div class="empty">加载失败</div>';}
  }
  load();setInterval(load,8000);
})();
</script>
</body>
</html>`

// visitorSPA —— 完整的浏览器端 SPA，参考主页面风格
// 支持公/私有频道、设备扫描、文件发送
// 注意：此字符串内的 JS 不能使用反引号模板字符串，否则会与 Go 原始字符串冲突
const visitorSPA = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{CHANNEL_NAME}} · 局域网快传</title>
<style>
:root{color-scheme:light dark;--bg:var(--cb,#ffffff);--fg:#27272a;--card:#ffffff;--muted:#f4f4f5;--muted-fg:#71717a;--border:#e4e4e7;--primary:#2563eb;--primary-fg:#fff;--success:#16a34a;--warning:#d97706;--radius:6px;}
@media(prefers-color-scheme:dark){--bg:#18181b;--fg:#f4f4f5;--card:#1b1b1f;--muted:#27272a;--muted-fg:#a1a1aa;--border:#3f3f46;--primary:#60a5fa;--primary-fg:#18181b;--success:#4ade80;}
*{box-sizing:border-box;margin:0;padding:0}
html,body{height:100%}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei",sans-serif;font-size:13px;line-height:1.5;background:var(--bg);color:var(--fg);-webkit-font-smoothing:antialiased;}
button{cursor:pointer;font:inherit;color:var(--fg);background:var(--card);border:1px solid var(--border);border-radius:var(--radius);padding:0 12px;height:32px;font-size:12.5px;transition:all .15s}
button:hover:not(:disabled){background:var(--muted)}
button:disabled{opacity:.45;cursor:not-allowed}
button.primary{background:var(--primary);color:var(--primary-fg);border-color:transparent;font-weight:500}
button.primary:hover:not(:disabled){filter:brightness(.94)}
button.ghost{background:transparent;border-color:transparent}
button.icon{width:32px;padding:0;display:inline-flex;align-items:center;justify-content:center}
input,textarea,select{font:inherit;color:var(--fg);background:var(--muted);border:none;border-radius:var(--radius);padding:8px 10px;outline:none}
input:focus,textarea:focus,select:focus{background:var(--bg);box-shadow:0 0 0 1px var(--primary) inset}
#app{display:flex;flex-direction:column;height:100%}
.topbar{display:flex;align-items:center;gap:14px;flex:none;height:44px;padding:0 16px;border-bottom:1px solid var(--border);background:var(--card)}
.brand{display:inline-flex;align-items:center;gap:8px;font-weight:600;font-size:14px}
.brand-logo{width:22px;height:22px;display:inline-block;flex-shrink:0}
.status-chip{display:inline-flex;align-items:center;gap:6px;padding:3px 10px;border-radius:999px;font-size:11.5px;font-weight:500;background:var(--muted);color:var(--muted-fg)}
.status-chip.running{background:color-mix(in srgb,var(--success) 12%,var(--bg));color:var(--success)}
.status-chip .dot{width:6px;height:6px;border-radius:50%;background:var(--muted-fg)}
.status-chip.running .dot{background:var(--success);box-shadow:0 0 0 3px color-mix(in srgb,var(--success) 20%,transparent)}
.layout{display:grid;grid-template-columns:minmax(160px,220px) minmax(0,1fr);flex:1;min-height:0;overflow:hidden}
.col{display:flex;flex-direction:column;min-height:0;min-width:0;overflow:hidden}
.col.side{border-right:1px solid var(--border);background:var(--card);overflow-y:auto}
.col.main{background:var(--bg);overflow-y:auto}
.section-title{padding:12px 14px 6px;font-size:11px;font-weight:600;text-transform:uppercase;letter-spacing:.04em;color:var(--muted-fg)}
.card{background:var(--card);border:1px solid var(--border);border-radius:10px;padding:18px;margin:14px}
.card h3{font-size:14px;margin-bottom:12px;font-weight:600;display:flex;align-items:center;gap:8px}
.badge{display:inline-block;font-size:11px;padding:2px 8px;border-radius:999px;background:var(--muted);color:var(--muted-fg);font-weight:400}
.badge.primary{background:color-mix(in srgb,var(--primary) 15%,var(--bg));color:var(--primary)}
.badge.success{background:color-mix(in srgb,var(--success) 15%,var(--bg));color:var(--success)}
.info-row{display:flex;justify-content:space-between;align-items:center;padding:8px 0;border-bottom:1px solid var(--muted);font-size:12px}
.info-row:last-child{border-bottom:none}
.info-row .label{color:var(--muted-fg)}
.info-row .val{font-family:ui-monospace,Menlo,monospace;font-size:11.5px;background:var(--muted);padding:2px 8px;border-radius:4px}
.file-drop{border:2px dashed var(--border);border-radius:10px;padding:28px;text-align:center;cursor:pointer;transition:all .15s;background:var(--muted)}
.file-drop:hover,.file-drop.dragover{border-color:var(--primary);background:color-mix(in srgb,var(--primary) 8%,var(--muted))}
.file-drop .drop-icon{font-size:36px;margin-bottom:8px}
.file-drop .drop-title{font-size:14px;font-weight:500;margin-bottom:2px}
.file-drop .drop-hint{font-size:11.5px;color:var(--muted-fg)}
.file-drop input{display:none}
.file-list{margin-top:12px;display:flex;flex-direction:column;gap:6px}
.file-item{display:flex;align-items:center;gap:10px;padding:8px 10px;background:var(--muted);border-radius:8px;font-size:12px}
.file-item .f-name{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.file-item .f-size{color:var(--muted-fg);font-size:11px;flex-shrink:0}
.file-item .f-progress{width:40px;height:4px;background:var(--border);border-radius:2px;overflow:hidden;flex-shrink:0}
.file-item .f-progress .bar{height:100%;background:var(--primary);transition:width .2s}
.file-item .f-status{font-size:11px;flex-shrink:0}
.actions{display:flex;gap:8px;margin-top:14px}
.actions button{flex:1;height:38px;border-radius:8px}
.msg-banner{padding:12px 14px;background:color-mix(in srgb,var(--primary) 8%,var(--bg));border:1px solid color-mix(in srgb,var(--primary) 25%,var(--bg));border-radius:8px;font-size:12px;margin:0 14px 14px;color:var(--fg)}
.msg-banner.error{background:color-mix(in srgb,#ef4444 10%,var(--bg));border-color:color-mix(in srgb,#ef4444 30%,var(--bg))}
.msg-banner.success{background:color-mix(in srgb,var(--success) 10%,var(--bg));border-color:color-mix(in srgb,var(--success) 30%,var(--bg))}
.dev-item{display:flex;align-items:center;gap:10px;padding:10px 14px;border-radius:8px;cursor:pointer;transition:background .12s;font-size:12px}
.dev-item:hover{background:var(--muted)}
.dev-item .d-avatar{width:28px;height:28px;border-radius:6px;background:var(--muted);display:inline-flex;align-items:center;justify-content:center;font-size:11px;font-weight:600;flex-shrink:0}
.dev-item .d-info{flex:1;min-width:0}
.dev-item .d-name{font-weight:500;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.dev-item .d-sub{font-family:ui-monospace,Menlo,monospace;font-size:10.5px;color:var(--muted-fg)}
.empty{padding:32px 20px;text-align:center;color:var(--muted-fg);font-size:12px}
.loading{display:inline-block;width:14px;height:14px;border:2px solid var(--border);border-top-color:var(--primary);border-radius:50%;animation:spin .6s linear infinite;vertical-align:middle}
@keyframes spin{to{transform:rotate(360deg)}}
.footer-note{font-size:11px;color:var(--muted-fg);text-align:center;padding:14px;line-height:1.6}
</style>
</head>
<body>
<div id="app">
  <div class="topbar">
    <div class="brand">{{PLUGIN_LOGO}}局域网快传 · <span id="chNameInline">{{CHANNEL_NAME}}</span></div>
    <span id="chVisBadge" class="badge">{{CHANNEL_VISIBILITY_LABEL}}</span>
    <span id="nickBadge" class="badge" style="margin-left:auto">加载昵称中...</span>
  </div>
  <div class="layout">
    <div class="col side">
      <div class="section-title">所有频道</div>
      <div id="channelList" style="padding:0 14px 14px; display:flex; flex-direction:column; gap:6px;"></div>
    </div>
    <div class="col main">
      <!-- PIN 遮罩（private 频道第一次进入） -->
      <div id="pinGate" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.45);display:none;align-items:center;justify-content:center;z-index:999;">
        <div style="background:var(--card);border:1px solid var(--border);border-radius:14px;padding:28px 26px;min-width:320px;text-align:center;">
          <div style="font-size:22px;margin-bottom:8px;">🔒 私有频道</div>
          <div style="font-size:14px;font-weight:600;margin-bottom:4px;">{{CHANNEL_NAME}}</div>
          <div style="font-size:11.5px;color:var(--muted-fg);margin-bottom:18px;">请输入 PIN 码进入</div>
          <!-- 6 个独立输入框 -->
          <div id="pinBoxes" style="display:flex;gap:8px;justify-content:center;margin-bottom:18px;">
            <input class="pin-digit" data-i="0" inputmode="numeric" maxlength="1" autocomplete="off" style="width:42px;height:52px;text-align:center;font-family:ui-monospace,Menlo,monospace;font-size:28px;font-weight:700;border:1.5px solid var(--border);border-radius:10px;background:var(--bg);color:var(--fg);outline:none;transition:border-color .15s;" onfocus="this.select()" oninput="pinInputDigit(this,0)" onkeydown="pinKeyDown(event,0)">
            <input class="pin-digit" data-i="1" inputmode="numeric" maxlength="1" autocomplete="off" style="width:42px;height:52px;text-align:center;font-family:ui-monospace,Menlo,monospace;font-size:28px;font-weight:700;border:1.5px solid var(--border);border-radius:10px;background:var(--bg);color:var(--fg);outline:none;transition:border-color .15s;" onfocus="this.select()" oninput="pinInputDigit(this,1)" onkeydown="pinKeyDown(event,1)">
            <input class="pin-digit" data-i="2" inputmode="numeric" maxlength="1" autocomplete="off" style="width:42px;height:52px;text-align:center;font-family:ui-monospace,Menlo,monospace;font-size:28px;font-weight:700;border:1.5px solid var(--border);border-radius:10px;background:var(--bg);color:var(--fg);outline:none;transition:border-color .15s;" onfocus="this.select()" oninput="pinInputDigit(this,2)" onkeydown="pinKeyDown(event,2)">
            <input class="pin-digit" data-i="3" inputmode="numeric" maxlength="1" autocomplete="off" style="width:42px;height:52px;text-align:center;font-family:ui-monospace,Menlo,monospace;font-size:28px;font-weight:700;border:1.5px solid var(--border);border-radius:10px;background:var(--bg);color:var(--fg);outline:none;transition:border-color .15s;" onfocus="this.select()" oninput="pinInputDigit(this,3)" onkeydown="pinKeyDown(event,3)">
            <input class="pin-digit" data-i="4" inputmode="numeric" maxlength="1" autocomplete="off" style="width:42px;height:52px;text-align:center;font-family:ui-monospace,Menlo,monospace;font-size:28px;font-weight:700;border:1.5px solid var(--border);border-radius:10px;background:var(--bg);color:var(--fg);outline:none;transition:border-color .15s;" onfocus="this.select()" oninput="pinInputDigit(this,4)" onkeydown="pinKeyDown(event,4)">
            <input class="pin-digit" data-i="5" inputmode="numeric" maxlength="1" autocomplete="off" style="width:42px;height:52px;text-align:center;font-family:ui-monospace,Menlo,monospace;font-size:28px;font-weight:700;border:1.5px solid var(--border);border-radius:10px;background:var(--bg);color:var(--fg);outline:none;transition:border-color .15s;" onfocus="this.select()" oninput="pinInputDigit(this,5)" onkeydown="pinKeyDown(event,5)">
          </div>
          <div style="display:flex;gap:8px;justify-content:flex-end;">
            <button onclick="location.href='/'" style="padding:6px 14px;border-radius:6px;border:1px solid var(--border);background:var(--muted);cursor:pointer;font-size:12px;">返回</button>
            <button id="pinEnterBtn" class="primary" style="padding:6px 18px;">进入</button>
          </div>
        </div>
      </div>

      <!-- 聊天区域 -->
      <div id="chatArea" style="display:flex;flex-direction:column;height:calc(100vh - 96px);padding:0;">
        <div id="chatMessages" style="flex:1;overflow-y:auto;padding:16px;display:flex;flex-direction:column;"></div>
        <div style="border-top:1px solid var(--border);padding:10px 14px;background:var(--card);">
          <div id="filePreview" style="display:none;margin-bottom:8px;padding:8px 12px;background:var(--muted);border-radius:8px;font-size:12px;align-items:center;gap:8px;"></div>
          <div style="display:flex;gap:8px;align-items:flex-end;">
            <button id="attachBtn" title="发送文件" style="width:36px;height:36px;display:inline-flex;align-items:center;justify-content:center;border:1px solid var(--border);border-radius:8px;background:var(--card);cursor:pointer;color:var(--muted-fg);flex-shrink:0;" onmouseover="this.style.color='var(--primary)'" onmouseout="this.style.color='var(--muted-fg)'">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>
            </button>
            <input type="file" id="fileInput" multiple style="display:none">
            <textarea id="chatInput" placeholder="输入消息... Enter 发送，Shift+Enter 换行" rows="1" maxlength="2000" style="flex:1;max-height:120px;resize:none;padding:9px 12px;border:1px solid var(--border);border-radius:8px;background:var(--muted);color:var(--fg);font-size:13px;outline:none;min-width:0;font-family:inherit;line-height:1.4;"></textarea>
            <button class="primary" id="chatSend" style="flex-shrink:0;height:36px;">发送</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</div>
<script>
(function(){
  var $=function(s){return document.querySelector(s);};
  var base=location.origin;
  var port={{SERVER_PORT}};
  var isPublic={{IS_PUBLIC}};
  var CHANNEL_ID="{{CHANNEL_ID}}";
  var CHANNEL_PIN="{{CHANNEL_PIN}}";
  var myIP=window.location.hostname;
  var pendingFiles=[]; // 待发送的文件（先挂在 attachBtn，等用户点发送一起发）

  function fmtSize(b){if(!b)return"0 B";if(b<1024)return b+" B";if(b<1048576)return(b/1024).toFixed(1)+" KB";return(b/1048576).toFixed(1)+" MB";}
  function escapeHtml(s){return String(s||"").replace(/[&<>"']/g,function(c){return({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]||c);});}

  // ========= 1. PIN Gate（private 频道） =========
  function tryPassPIN(){
    // 记住已通过 private 频道的 PIN
    var passed=JSON.parse(localStorage.getItem("pin_passed")||"{}");
    if(passed[CHANNEL_ID]===CHANNEL_PIN){$("#pinGate").style.display="none";return true;}
    // URL 带了 pin 参数
    var params=new URLSearchParams(location.search);
    if(params.get("pin")===CHANNEL_PIN){passed[CHANNEL_ID]=CHANNEL_PIN;localStorage.setItem("pin_passed",JSON.stringify(passed));$("#pinGate").style.display="none";return true;}
    // public 频道不需要 PIN
    if(isPublic){$("#pinGate").style.display="none";return true;}
    // 弹 PIN 输入遮罩
    $("#pinGate").style.display="flex";
    // 聚焦第一个输入框
    setTimeout(function(){var first=document.querySelector(".pin-digit[data-i='0']");if(first)first.focus();},50);
    var tryEnter=function(){
      var boxes=document.querySelectorAll(".pin-digit");var v="";
      boxes.forEach(function(b){v+=b.value;});
      if(v===CHANNEL_PIN){passed[CHANNEL_ID]=CHANNEL_PIN;localStorage.setItem("pin_passed",JSON.stringify(passed));$("#pinGate").style.display="none";}
      else{boxes.forEach(function(b){b.style.borderColor="#ef4444";});setTimeout(function(){boxes.forEach(function(b){b.style.borderColor="";});},600);boxes.forEach(function(b){b.value="";});setTimeout(function(){document.querySelector(".pin-digit[data-i='0']").focus();},50);}
    };
    $("#pinEnterBtn").onclick=tryEnter;
    // 数字输入框自动跳转 + 回退退格（全局函数，HTML 内联调用）
    window.pinInputDigit=function(el,i){
      el.value=(el.value||"").replace(/[^0-9]/g,"");
      if(el.value.length>=1&&i<5){var next=document.querySelector(".pin-digit[data-i='"+(i+1)+"']");if(next)next.focus();}
      checkPinReady();
    };
    window.pinKeyDown=function(e,i){
      if(e.key==="Backspace"&&!e.target.value&&i>0){var prev=document.querySelector(".pin-digit[data-i='"+(i-1)+"']");if(prev){prev.focus();prev.value="";}}
      else if(e.key==="Enter")tryEnter();
    };
    function checkPinReady(){var boxes=document.querySelectorAll(".pin-digit");var filled=0;boxes.forEach(function(b){if(b.value)filled++;});if(filled===6)tryEnter();}
    return false;
  }
  if(!tryPassPIN())return; // 没通过 PIN 就停了（不加载聊天）

  // ========= 2. 昵称 =========
  var myNick=localStorage.getItem("lc_nick");
  if(!myNick){
    var emojis=["🌱","🌊","🍃","🦊","🐼","🐳","🦉","🌸","🍀","⭐","🍉","🎧","🚀","🌈","🍜","🎈"];
    myNick="访客"+emojis[Math.floor(Math.random()*emojis.length)]+String(1000+Math.floor(Math.random()*9000));
    localStorage.setItem("lc_nick",myNick);
  }
  $("#nickBadge").textContent="👤 "+myNick;
  // 稳定的本机客户端标识，用于刷新后仍能识别"自己发的消息"
  var myCid=localStorage.getItem("lc_cid");
  if(!myCid){
    myCid="c"+Math.random().toString(36).slice(2,10);
    localStorage.setItem("lc_cid",myCid);
  }

  // ========= 3. attachBtn 文件选择 + 预览 =========
  var fileInput=$("#fileInput"),attachBtn=$("#attachBtn"),preview=$("#filePreview");
  attachBtn.addEventListener("click",function(){fileInput.click();});
  // 拖放到聊天区域
  var chatArea=$("#chatArea");
  ["dragenter","dragover"].forEach(function(ev){chatArea.addEventListener(ev,function(e){e.preventDefault();});});
  chatArea.addEventListener("drop",function(e){e.preventDefault();for(var i=0;i<e.dataTransfer.files.length;i++){pendingFiles.push({file:e.dataTransfer.files[i]});}renderPreview();});
  fileInput.addEventListener("change",function(e){for(var i=0;i<e.target.files.length;i++){pendingFiles.push({file:e.target.files[i]});}fileInput.value="";renderPreview();});

  function renderPreview(){
    if(!pendingFiles.length){preview.style.display="none";preview.innerHTML="";return;}
    var h="<span style='color:var(--muted-fg);'>📎 已选 "+pendingFiles.length+" 个文件：</span>";
    for(var i=0;i<pendingFiles.length;i++){h+='<span style="background:var(--bg);padding:2px 8px;border-radius:4px;font-family:var(--mono);font-size:11px;margin-left:4px;" title="'+escapeHtml(pendingFiles[i].file.name)+'">'+escapeHtml(pendingFiles[i].file.name.slice(0,20))+(pendingFiles[i].file.name.length>20?'…':'')+' '+fmtSize(pendingFiles[i].file.size)+'</span>';}
    h+='<button id="clearPending" style="margin-left:auto;background:transparent;border:none;color:var(--muted-fg);cursor:pointer;font-size:11px;">清空</button>';
    preview.style.display="flex";preview.innerHTML=h;preview.querySelector("#clearPending").onclick=function(){pendingFiles=[];renderPreview();};
  }

  // ========= 4. 聊天消息发送（文字 + 文件） =========
  var chatInput=$("#chatInput"),chatSend=$("#chatSend"),chatMsgsEl=$("#chatMessages");
  chatInput.addEventListener("keydown",function(e){
    if(e.key==="Enter"&&!e.shiftKey){e.preventDefault();doSend();}
  });
  chatSend.addEventListener("click",doSend);

  async function doSend(){
    var text=chatInput.value.trim();
    if(!text&&!pendingFiles.length)return;
    chatSend.disabled=true;chatSend.textContent="发送中...";
    try{
      // 先发文字消息
      if(text){
        await fetch(base+"/api/channels/"+CHANNEL_ID+"/messages",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({deviceId:myCid,sender:myNick,content:text})});
        chatInput.value="";
      }
      // 发文件（直接上传到频道目录，所有设备都可下载）
      if(pendingFiles.length){
        for(var i=0;i<pendingFiles.length;i++){
          var f=pendingFiles[i].file;
          var fid="w"+Math.random().toString(36).slice(2,10);
          var fd=new FormData();
          fd.append("file",f,f.name);
          fd.append("fileId",fid);
          var up=await fetch(base+"/api/channels/"+CHANNEL_ID+"/files",{method:"POST",body:fd});
          if(!up.ok)throw new Error((await up.text()).slice(0,200)||("HTTP "+up.status));
          var upData=await up.json();
          var fm=upData.file||{};
          // 发一条文件消息到频道
          var fileMsg=JSON.stringify({name:fm.fileName||f.name,size:fm.size||f.size,fileId:fm.id||fid});
          await fetch(base+"/api/channels/"+CHANNEL_ID+"/messages",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({deviceId:myCid,sender:myNick,content:fileMsg,type:"file"})});
        }
        pendingFiles=[];renderPreview();
      }
      await loadMessages();
    }catch(err){alert("发送失败："+err.message);}
    chatSend.disabled=false;chatSend.textContent="发送";
  }

  // ========= 5. 消息渲染 =========
  var lastMsgCount=0;
  function renderMessages(msgs){
    chatMsgsEl.innerHTML="";
    if(!msgs.length){chatMsgsEl.innerHTML='<div style="text-align:center;color:var(--muted-fg);font-size:11.5px;padding:40px 0;">频道还没有消息，发第一条吧！</div>';lastMsgCount=0;chatMsgsEl.scrollTop=0;return;}
    var wasBottom=chatMsgsEl.scrollTop+chatMsgsEl.clientHeight>=chatMsgsEl.scrollHeight-30;
    var h="";
    for(var i=0;i<msgs.length;i++){
      var m=msgs[i];var isSelf=m.deviceId?m.deviceId===myCid:m.sender===myNick;
      var t=new Date(m.createdAt);
      var ts=("0"+t.getHours()).slice(-2)+":"+("0"+t.getMinutes()).slice(-2);
      if(m.type==="system"){
        h+='<div style="align-self:center;color:var(--muted-fg);font-size:11px;margin-bottom:10px;"><span style="background:var(--muted);padding:2px 10px;border-radius:999px;">'+escapeHtml(m.content)+'</span></div>';
      }else if(m.type==="file"){
        var fileMeta={};try{fileMeta=JSON.parse(m.content);}catch(e){}
        var fname=fileMeta.name||m.content;var fsize=fileMeta.size||0;
        var dlUrl=fileMeta.fileId?(base+"/api/channels/"+CHANNEL_ID+"/files/"+encodeURIComponent(fileMeta.fileId)):"";
        h+='<div style="align-self:'+(isSelf?'flex-end':'flex-start')+';align-items:'+(isSelf?'flex-end':'flex-start')+';max-width:78%;display:flex;flex-direction:column;gap:2px;margin-bottom:10px;">';
        h+='<div style="font-size:11px;color:var(--muted-fg);padding:0 2px;text-align:'+(isSelf?'right':'left')+';"><span style="font-weight:600;color:'+(isSelf?'var(--primary)':'var(--fg)')+';">'+escapeHtml(m.sender)+'</span> · '+ts+'</div>';
        h+='<div style="background:var(--muted);color:var(--fg);border:1px solid var(--border);border-radius:14px 14px 14px '+(isSelf?'4px':'14px')+'px;padding:10px 12px;display:flex;align-items:center;gap:10px;width:fit-content;max-width:100%;">';
        h+='<div style="width:34px;height:34px;background:color-mix(in srgb,var(--primary) 14%,var(--bg));border-radius:8px;display:flex;align-items:center;justify-content:center;font-size:17px;flex-shrink:0;">'+(fname.indexOf("/")>=0?'📁':'📄')+'</div>';
        h+='<div style="min-width:0;flex:1;"><div style="font-size:12.5px;font-weight:500;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">'+escapeHtml(fname)+'</div><div style="font-size:11px;color:var(--muted-fg);">'+fmtSize(fsize)+'</div></div>';
        if(dlUrl){
          h+='<a href="'+escapeHtml(dlUrl)+'" download="'+escapeHtml(fname)+'" style="flex-shrink:0;text-decoration:none;font-size:11.5px;padding:5px 10px;border-radius:7px;border:1px solid var(--border);background:var(--card);color:var(--fg);white-space:nowrap;">下载</a>';
          h+='<button type="button" data-fid="'+escapeHtml(fileMeta.fileId||'')+'" class="open-file-btn" style="flex-shrink:0;cursor:pointer;font-family:inherit;font-size:11.5px;padding:5px 10px;border-radius:7px;border:1px solid var(--border);background:var(--card);color:var(--fg);white-space:nowrap;">打开</button>';
          h+='<button type="button" data-fid="'+escapeHtml(fileMeta.fileId||'')+'" class="open-folder-btn" style="flex-shrink:0;cursor:pointer;font-family:inherit;font-size:11.5px;padding:5px 10px;border-radius:7px;border:1px solid var(--border);background:var(--card);color:var(--fg);white-space:nowrap;">文件夹</button>';
        }
        h+='</div></div>';
      }else{
        h+='<div style="align-self:'+(isSelf?'flex-end':'flex-start')+';align-items:'+(isSelf?'flex-end':'flex-start')+';max-width:78%;display:flex;flex-direction:column;gap:2px;margin-bottom:10px;">';
        h+='<div style="font-size:11px;color:var(--muted-fg);padding:0 2px;text-align:'+(isSelf?'right':'left')+';"><span style="font-weight:600;color:'+(isSelf?'var(--primary)':'var(--fg)')+';">'+escapeHtml(m.sender)+'</span> · '+ts+'</div>';
        h+='<div style="background:'+(isSelf?'color-mix(in srgb,var(--primary)20%,var(--primary))':'var(--card)')+';color:'+(isSelf?'var(--primary-fg)':'var(--fg)')+';border:1px solid '+(isSelf?'var(--primary)':'var(--border)')+';border-radius:14px 14px '+(isSelf?'4px':'14px')+' 14px;padding:8px 14px;font-size:13px;word-break:break-word;line-height:1.5;white-space:pre-wrap;width:fit-content;max-width:100%;">'+escapeHtml(m.content)+'</div>';
        h+='</div>';
      }
    }
    chatMsgsEl.innerHTML=h;
    // 绑定"打开文件夹"（在宿主电脑上打开资源管理器并定位文件）
    var ofBtns=chatMsgsEl.querySelectorAll(".open-folder-btn");
    for(var bi=0;bi<ofBtns.length;bi++){
      ofBtns[bi].onclick=function(){openFolderInHost(this.getAttribute("data-fid"));};
    }
    var fileBtns=chatMsgsEl.querySelectorAll(".open-file-btn");
    for(var fi=0;fi<fileBtns.length;fi++){
      fileBtns[fi].onclick=function(){openFileInHost(this.getAttribute("data-fid"));};
    }
    if(wasBottom||lastMsgCount===0){chatMsgsEl.scrollTop=chatMsgsEl.scrollHeight;}
    lastMsgCount=msgs.length;
  }

  async function openFileInHost(fid){
    try{
      var r=await fetch(base+"/api/open-file",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({channelId:CHANNEL_ID,fileId:fid||""})});
      if(!r.ok)throw new Error((await r.text()).slice(0,200)||("HTTP "+r.status));
    }catch(e){alert("打开文件失败："+(e.message||e));}
  }
  async function openFolderInHost(fid){
    try{
      var r=await fetch(base+"/api/open-folder",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({channelId:CHANNEL_ID,fileId:fid||""})});
      if(!r.ok)throw new Error((await r.text()).slice(0,200)||("HTTP "+r.status));
    }catch(e){alert("打开文件夹失败："+(e.message||e));}
  }

  async function loadMessages(){
    try{var r=await fetch(base+"/api/channels/"+CHANNEL_ID+"/messages");var d=await r.json();renderMessages(d.messages||[]);}catch(err){}
  }
  loadMessages();setInterval(loadMessages,2000);

  // ========= 6. 频道切换卡片列表 =========
  async function loadChannels(){
    try{var r=await fetch(base+"/api/channels");var d=await r.json();
      var list=document.getElementById("channelList");if(!list)return;
      list.innerHTML='';
      (d.channels||[]).forEach(function(c){
        var isCurrent=c.id===CHANNEL_ID;
        var div=document.createElement("div");
        div.style.cssText='padding:8px 10px;border-radius:8px;cursor:pointer;transition:all .15s;display:flex;align-items:center;gap:8px;font-size:12px;'+
          (isCurrent?'background:var(--primary);color:var(--primary-fg);':'background:var(--muted);border:1px solid transparent;');
        div.innerHTML='<span style="font-size:14px;">'+(c.visibility==="public"?'🌐':'🔒')+'</span>'+
          '<span style="flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:'+(isCurrent?'600':'400')+';">'+escapeHtml(c.name)+'</span>'+
          (isCurrent?'<span style="font-size:10px;opacity:.8;">当前</span>':'');
        div.addEventListener("click",function(){if(!isCurrent){location.href="/c/"+c.id;}});
        list.appendChild(div);
      });
    }catch(err){}
  }
  loadChannels();setInterval(loadChannels,15000);
})();
</script>
</body>
</html>`




func GenSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var resp *RegisterResponse
	if s.callbacks.OnRegister != nil {
		resp = s.callbacks.OnRegister(&req)
	}
	if resp == nil {
		resp = &RegisterResponse{
			Alias:       req.Alias,
			Version:     req.Version,
			DeviceModel: req.DeviceModel,
			DeviceType:  req.DeviceType,
			Fingerprint: req.Fingerprint,
			Port:        req.Port,
			Protocol:    req.Protocol,
			Download:    req.Download,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleSendRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	session := &IncomingSession{
		ID:        GenSessionID(),
		Sender:    req.Info,
		Files:     req.Files,
		Accepted:  true,
		SaveDir:   s.saveDir,
		Received:  make(map[string]int64),
		completed: make(chan struct{}),
	}

	s.mu.Lock()
	s.sessions[session.ID] = session
	s.mu.Unlock()

	resp := &SendResponse{SessionID: session.ID, Status: "accept"}
	if s.callbacks.OnSendRequest != nil {
		cb := s.callbacks.OnSendRequest(&req)
		if cb != nil {
			resp = cb
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("sessionId")
	fileID := r.URL.Query().Get("fileId")
	if sessionID == "" || fileID == "" {
		http.Error(w, "missing sessionId or fileId", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}

	// 找到对应的文件元数据
	var fileMeta *FileMetadata
	for i := range session.Files {
		if session.Files[i].ID == fileID {
			fileMeta = &session.Files[i]
			break
		}
	}
	if fileMeta == nil {
		http.Error(w, "unknown fileId", http.StatusNotFound)
		return
	}

	// 流式写入目标文件
	if err := os.MkdirAll(session.SaveDir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target := filepath.Join(session.SaveDir, fileMeta.FileName)
	f, err := os.Create(target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	var total int64
	buf := make([]byte, 64*1024)
	for {
		n, readErr := r.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				http.Error(w, werr.Error(), http.StatusInternalServerError)
				return
			}
			total += int64(n)
			session.mu.Lock()
			session.Received[fileID] = total
			session.mu.Unlock()
			if s.callbacks.OnUploadChunk != nil {
				s.callbacks.OnUploadChunk(session, fileID, int64(n))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			http.Error(w, readErr.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)

	// 清理
	allDone := true
	session.mu.Lock()
	for _, fm := range session.Files {
		if session.Received[fm.ID] < fm.Size {
			allDone = false
			break
		}
	}
	session.mu.Unlock()
	if allDone {
		s.mu.Lock()
		delete(s.sessions, sessionID)
		s.mu.Unlock()
		if s.callbacks.OnSessionDone != nil {
			s.callbacks.OnSessionDone(session, nil)
		}
	}
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	delete(s.sessions, req.SessionID)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	info := s.info
	s.mu.RUnlock()
	if info.Alias == "" {
		info = InfoProvider{
			Alias: "LocalSend", Version: "2.0", DeviceType: "server",
			Protocol: "http", Download: true, Port: 53317,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ServerInfo{
		Alias: info.Alias, Version: info.Version, DeviceModel: info.DeviceModel,
		DeviceType: info.DeviceType, Fingerprint: info.Fingerprint,
		Port: s.ln.Addr().(*net.TCPAddr).Port,
		Protocol: info.Protocol, Download: info.Download,
	})
}
