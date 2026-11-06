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

// handleRoot —— 浏览器访问 http://IP:port/ 时返回友好页面（非 API）
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// API 请求不走这里
	if len(r.URL.Path) > 1 && r.URL.Path[1] == 'a' {
		return
	}

	if s.ChannelInfo != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s · 局域网快传</title>
<style>
body{font-family:-apple-system,"Segoe UI",sans-serif;background:#f8fafc;margin:0;padding:40px 20px;text-align:center;color:#334155}
.card{max-width:420px;margin:auto;background:#fff;border:1px solid #e2e8f0;border-radius:12px;padding:28px;box-shadow:0 1px 2px rgba(0,0,0,.04)}
h1{font-size:16px;margin:0 0 6px}
.pin{font-family:monospace;font-size:28px;font-weight:700;letter-spacing:8px;background:#f1f5f9;padding:10px 18px;border-radius:8px;margin:14px 0}
.hint{color:#64748b;font-size:12.5px;line-height:1.6}
.ver{margin-top:16px;font-size:11px;color:#94a3b8}
</style></head><body>
<div class="card">
  <h1>%s</h1>
  <div style="font-size:12px;color:#64748b">PIN 码（在局域网快传客户端里输入）</div>
  <div class="pin">%s</div>
  <div class="hint">在同一局域网的设备上打开「局域网快传」<br/>粘贴链接或输入频道 ID：<b>%s</b> · 加入频道</div>
  <div class="ver">局域网快传 · %s 频道 · 端口 %d</div>
</div></html>`,
			s.ChannelInfo["name"],
			s.ChannelInfo["name"],
			s.ChannelInfo["pin"],
			s.ChannelInfo["id"],
			s.ChannelInfo["visibility"],
			s.ln.Addr().(*net.TCPAddr).Port,
		)
		return
	}
	// 主服务没有频道信息
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>局域网快传</title>
<style>body{font-family:-apple-system,"Segoe UI",sans-serif;background:#f8fafc;margin:0;padding:40px 20px;text-align:center;color:#334155}
.card{max-width:420px;margin:auto;background:#fff;border:1px solid #e2e8f0;border-radius:12px;padding:28px}
h1{font-size:16px;margin:0 0 8px}</style></head><body>
<div class="card"><h1>局域网快传 运行中</h1>
<p style="color:#64748b;font-size:12.5px">请用 DBX 里的局域网快传工作台来管理设备和创建频道。</p></div></body></html>`)
}

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
