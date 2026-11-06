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
	"os"
	"path/filepath"
	"strings"
	"sync"
)

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

	// 可选：频道信息。若设置了，根路径 "/" 会返回友好 HTML
	ChannelInfo map[string]string // name / pin / id / visibility

	httpSrv *http.Server
	ln      net.Listener
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

func (s *Server) Listen(port int) (int, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/localsend/v2/register", s.handleRegister)
	mux.HandleFunc("/api/localsend/v2/send-request", s.handleSendRequest)
	mux.HandleFunc("/api/localsend/v2/upload", s.handleUpload)
	mux.HandleFunc("/api/localsend/v2/cancel", s.handleCancel)
	mux.HandleFunc("/", s.handleRoot)

	host := s.bindIP
	if host == "" {
		host = "0.0.0.0"
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return 0, err
	}
	s.ln = ln
	s.httpSrv = &http.Server{Handler: mux}
	go s.httpSrv.Serve(ln)
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func (s *Server) Close() {
	if s.httpSrv != nil { s.httpSrv.Close() }
	if s.ln != nil { s.ln.Close() }
}

// handleRoot —— 浏览器访问 http://IP:port/ 时返回友好的加入频道页面
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	isAPI := strings.HasPrefix(r.URL.Path, "/api/")
	if isAPI {
		http.NotFound(w, r)
		return
	}

	q := r.URL.Query()
	pinFromURL := q.Get("pin")

	if s.ChannelInfo != nil {
		visibilityTag := "公有频道"
		if s.ChannelInfo["visibility"] == "private" { visibilityTag = "私有频道" }
		fmt.Fprintf(w, visitorTemplate,
			s.ChannelInfo["name"],              // 1 title
			s.ChannelInfo["name"],              // 2 h1 name
			visibilityTag,                      // 3 h1 tag
			s.ChannelInfo["id"],                // 4 channelId (hidden in URL)
			pinFromURL,                         // 5 pinInput prefill (from URL query)
			s.ln.Addr().(*net.TCPAddr).Port,    // 6 footer port
			s.ChannelInfo["pin"],               // 7 server-pin for JS validate
			s.ChannelInfo["id"],                // 8 channel id for join
			s.ln.Addr().(*net.TCPAddr).Port,    // 9 join port
			s.ChannelInfo["id"],                // 10 hint channel id
		)
		return
	}

	// 主服务访问页
	fmt.Fprint(w, gatewayTemplate)
}

const visitorTemplate = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s · 局域网快传</title>
<style>
  :root{color-scheme:light dark;--bg:#f8fafc;--fg:#1e293b;--card:#fff;--muted:#f1f5f9;--muted-fg:#64748b;--border:#e2e8f0;--primary:#2563eb;--primary-fg:#fff}
  @media (prefers-color-scheme:dark){--bg:#0f172a;--fg:#f1f5f9;--card:#1e293b;--muted:#334155;--muted-fg:#94a3b8;--border:#334155;--primary:#60a5fa}
  *{box-sizing:border-box}
  body{margin:0;font-family:-apple-system,"Segoe UI","Microsoft YaHei",sans-serif;background:var(--bg);color:var(--fg);padding:48px 20px;display:flex;justify-content:center;min-height:100vh}
  .card{max-width:440px;width:100%%;background:var(--card);border:1px solid var(--border);border-radius:14px;padding:28px;box-shadow:0 1px 3px rgba(0,0,0,.05)}
  h1{font-size:18px;margin:0 0 4px;font-weight:600}
  .tag{display:inline-block;font-size:11px;padding:2px 8px;border-radius:999px;background:var(--muted);color:var(--muted-fg);margin-left:6px}
  .sub{font-size:12.5px;color:var(--muted-fg);margin-bottom:18px}
  .row{margin-bottom:14px}
  .label{font-size:11.5px;color:var(--muted-fg);text-transform:uppercase;letter-spacing:.04em;margin-bottom:6px;font-weight:500}
  input,button{font:inherit}
  input[type=text]{width:100%%;padding:10px 12px;background:var(--muted);border:none;border-radius:8px;font-size:14px;color:var(--fg);outline:none;transition:box-shadow .15s}
  input[type=text]:focus{box-shadow:0 0 0 2px var(--primary) inset}
  input[name=pin]{font-family:ui-monospace,Menlo,monospace;font-size:22px;font-weight:700;letter-spacing:8px;text-align:center;padding:14px 12px}
  .actions{display:flex;gap:8px;margin-top:18px}
  button{flex:1;height:40px;border-radius:8px;border:none;cursor:pointer;font-size:14px;font-weight:500;transition:filter .15s}
  button.primary{background:var(--primary);color:var(--primary-fg)}
  button.primary:hover{filter:brightness(.94)}
  button.ghost{background:var(--muted);color:var(--fg)}
  .hint-box{margin-top:18px;padding:12px;background:var(--muted);border-radius:8px;font-size:12px;color:var(--muted-fg);line-height:1.6}
  .hint-box code{font-family:ui-monospace,Menlo,monospace;background:rgba(0,0,0,.06);padding:1px 5px;border-radius:3px}
  .footer{margin-top:16px;font-size:11px;color:var(--muted-fg);text-align:center}
  .err{color:#dc2626;font-size:12px;margin-top:4px;min-height:16px}
</style></head><body>
<div class="card">
  <h1>%s <span class="tag">%s</span></h1>
  <div class="sub">由局域网快传提供服务 · 加入频道后可互发消息和文件</div>

  <div class="row"><div class="label">频道 ID</div>
    <input type="text" id="channelId" value="%s" readonly style="background:var(--muted);cursor:default"></div>

  <div class="row"><div class="label">PIN 码（发送方会告诉你）</div>
    <input type="text" name="pin" id="pinInput" value="%s" placeholder="六位 PIN" inputmode="numeric" maxlength="8" autofocus></div>
  <div class="err" id="err"></div>

  <div class="actions">
    <button class="ghost" onclick="copyPin()">复制 PIN</button>
    <button class="primary" onclick="join()">打开局域网快传加入</button>
  </div>

  <div class="hint-box">
    <b>加入方法：</b><br>
    1. 在同一台设备上安装 <b>DBX</b>（豆包桌面）<br>
    2. 打开「局域网快传」插件工作台<br>
    3. 点 <code>在线设备 → 扫描</code>，或手动创建频道，然后输入频道 ID + PIN 加入<br>
    4. 加入后就可以互发消息和文件了
  </div>

  <div class="footer">端口 %d · 局域网快传 v0.1</div>
</div>
<script>
  function join(){
    var pin = document.getElementById('pinInput').value.trim();
    var err = document.getElementById('err');
    if(!pin){ err.textContent = '请输入 PIN'; return; }
    if(pin !== '%s'){ err.textContent = 'PIN 不正确，请向发送方确认'; return; }
    err.textContent = '';
    // 自定义协议唤起客户端（若已安装）
    window.location.href = 'localsend://join?channel=%s&pin=' + pin + '&host=' + location.hostname + '&port=' + %d;
    // 同时也提示手动复制
    document.querySelector('.hint-box').innerHTML = '<b>✓ PIN 正确！</b><br/>如果没有自动唤起客户端，请在 DBX 中手动打开局域网快传并输入频道 ID + PIN 加入。<br/>频道 ID：<code>%s</code>  PIN：<code>'+pin+'</code>';
  }
  function copyPin(){
    var btn = event.target; var old = btn.textContent; btn.textContent = '✓ 已复制';
    navigator.clipboard.writeText(document.getElementById('pinInput').value.trim());
    setTimeout(function(){ btn.textContent=old },1500);
  }
</script>
</body></html>`

const gatewayTemplate = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>局域网快传</title>
<style>
  :root{color-scheme:light dark;--bg:#f8fafc;--fg:#1e293b;--card:#fff;--muted:#f1f5f9;--muted-fg:#64748b;--border:#e2e8f0}
  @media (prefers-color-scheme:dark){--bg:#0f172a;--fg:#f1f5f9;--card:#1e293b;--muted:#334155;--muted-fg:#94a3b8;--border:#334155}
  body{margin:0;font-family:-apple-system,"Segoe UI","Microsoft YaHei",sans-serif;background:var(--bg);color:var(--fg);padding:60px 20px;text-align:center;min-height:100vh;display:flex;flex-direction:column;align-items:center}
  .logo{width:56px;height:56px;border-radius:14px;background:linear-gradient(135deg,#2563eb 0%,#8b5cf6 100%);display:inline-flex;align-items:center;justify-content:center;color:#fff;font-size:22px;font-weight:700;margin-bottom:16px}
  h1{font-size:20px;margin:0 0 6px;font-weight:600}
  p{color:var(--muted-fg);font-size:13px;max-width:360px;line-height:1.7;margin:0}
</style></head><body>
  <div class="logo">快</div>
  <h1>局域网快传 运行中</h1>
  <p>请在 DBX 中打开「局域网快传」插件工作台来管理设备、创建频道和互传文件。<br/>打开这个浏览器页面没有找到频道链接，直接在插件里操作即可。</p>
</body></html>`

func genSessionID() string {
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
		ID:        genSessionID(),
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
