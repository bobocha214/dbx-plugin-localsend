package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bobocha214/dbx-plugin-localsend/im"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

// imRuntime owns the new service separately from the legacy LocalSend server.
type imRuntime struct {
	mu             sync.Mutex
	service        *im.Service
	peers          *im.Peers
	server         *http.Server
	port           int
	center         string
	discoveryError string
	archives       map[string]*folderArchive
}

func validateCenter(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("请输入局域网服务地址，例如 http://192.168.1.10:53318")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !(ip.IsPrivate() || ip.IsLoopback()) {
		return "", errors.New("中心地址必须使用局域网或本机 IP，不支持公网、域名或链路本地地址")
	}
	if p := u.Port(); p != "" {
		if _, e := net.LookupPort("tcp", p); e != nil {
			return "", errors.New("端口无效")
		}
	}
	return u.Scheme + "://" + u.Host, nil
}

func validIMPath(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, "\\") || strings.Contains(u.Path, "..") {
		return false
	}
	for _, prefix := range []string{"/status", "/guest", "/logout", "/me", "/users", "/conversations", "/channels", "/attachments", "/stickers", "/downloads", "/peers"} {
		if u.Path == prefix || strings.HasPrefix(u.Path, prefix+"/") {
			return true
		}
	}
	return false
}

func findIMUI() (string, error) {
	exe, _ := os.Executable()
	candidates := []string{filepath.Join(filepath.Dir(exe), "..", "ui"), filepath.Join(filepath.Dir(exe), "..", "..", "ui"), filepath.Join("..", "ui"), "ui"}
	for _, p := range candidates {
		if st, e := os.Stat(filepath.Join(p, "im.js")); e == nil && !st.IsDir() {
			return filepath.Abs(p)
		}
	}
	return "", errors.New("找不到 IM 静态资源，请将 ui 目录与插件一起打包")
}

func (p *Plugin) initIMLocked() error {
	if p.imState.service != nil {
		return nil
	}
	dir := filepath.Join(filepath.Dir(p.cfgPath), "im")
	s, e := im.New(dir)
	if e != nil {
		return e
	}
	peers, e := im.NewPeers(filepath.Join(dir, "node"))
	if e != nil {
		_ = s.Close()
		return e
	}
	s.SetPeerHandler(peers.Handler(), peers.Request)
	if ui, e := findIMUI(); e == nil {
		s.SetUI(os.DirFS(ui))
	}
	p.imState.service = s
	p.imState.peers = peers
	return nil
}

func (r *imRuntime) status() any {
	addresses := []string{}
	interfaces := []map[string]any{}
	if r.server != nil {
		ifaces, _ := net.Interfaces()
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			addrs, _ := iface.Addrs()
			ifaceAddresses := []string{}
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
					url := fmt.Sprintf("http://%s:%d", ipn.IP.String(), r.port)
					addresses = append(addresses, url)
					ifaceAddresses = append(ifaceAddresses, url)
				}
			}
			if len(ifaceAddresses) > 0 {
				interfaces = append(interfaces, map[string]any{"name": iface.Name, "addresses": ifaceAddresses})
			}
		}
		addresses = append(addresses, fmt.Sprintf("http://127.0.0.1:%d", r.port))
	}
	nodeID := ""
	if r.peers != nil {
		nodeID = r.peers.ID()
	}
	return map[string]any{"running": r.server != nil, "port": r.port, "addresses": addresses, "interfaces": interfaces, "center": r.center, "nodeId": nodeID, "discoveryError": r.discoveryError}
}

func (p *Plugin) handleIM(method string, params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	r := &p.imState
	r.mu.Lock()
	locked := true
	defer func() {
		if locked {
			r.mu.Unlock()
		}
	}()
	if e := p.initIMLocked(); e != nil {
		return nil, dbxpluginsdk.NewError(-32000, e.Error())
	}
	fail := func(e error) (any, *dbxpluginsdk.PluginError) { return nil, dbxpluginsdk.NewError(-32602, e.Error()) }
	switch method {
	case "im/archive-pick", "im/archive-status", "im/archive-read", "im/archive-release":
		r.mu.Unlock()
		locked = false
		v, e := r.handleArchive(method, params)
		if e != nil {
			return fail(e)
		}
		return v, nil
	case "im/pick-directory":
		r.mu.Unlock()
		locked = false
		path, e := pickDirectory()
		if e != nil {
			return fail(e)
		}
		return map[string]any{"path": path}, nil
	case "im/status":
		status := r.status()
		if m, ok := status.(map[string]any); ok {
			m["dataDir"] = r.service.Dir()
			m["filesDir"] = r.service.FilesDir()
		}
		return status, nil
	case "im/storage":
		var a struct {
			DataDir  string `json:"dataDir"`
			FilesDir string `json:"filesDir"`
		}
		if e := json.Unmarshal(params, &a); e != nil || strings.TrimSpace(a.DataDir) == "" || strings.TrimSpace(a.FilesDir) == "" {
			return fail(errors.New("存储目录不能为空"))
		}
		if e := r.service.Relocate(filepath.Clean(a.DataDir), filepath.Clean(a.FilesDir), nil); e != nil {
			return fail(e)
		}
		return r.status(), nil
	case "im/start":
		if r.server != nil {
			return r.status(), nil
		}
		var a struct {
			Port int `json:"port"`
		}
		if len(params) > 0 {
			if e := json.Unmarshal(params, &a); e != nil {
				return fail(e)
			}
		}
		if a.Port == 0 {
			a.Port = 53318
		}
		if a.Port < 1024 || a.Port > 65535 {
			return fail(errors.New("端口必须为 1024–65535"))
		}
		if _, e := findIMUI(); e != nil {
			return fail(e)
		}
		ln, e := net.Listen("tcp", fmt.Sprintf(":%d", a.Port))
		if e != nil {
			return fail(e)
		}
		r.port = a.Port
		r.server = &http.Server{Handler: r.service.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
		server := r.server
		go func() { _ = server.Serve(ln) }()
		name, _ := os.Hostname()
		r.peers.SetName(name)
		r.discoveryError = ""
		if e := r.peers.Start(a.Port); e != nil {
			r.discoveryError = e.Error()
		}
		return r.status(), nil
	case "im/stop":
		if r.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = r.server.Shutdown(ctx)
			r.server = nil
			_ = r.peers.Close()
		}
		return r.status(), nil
	case "im/connect":
		var a struct {
			URL string `json:"url"`
		}
		if e := json.Unmarshal(params, &a); e != nil {
			return fail(e)
		}
		if strings.TrimSpace(a.URL) == "" {
			r.center = ""
			return r.status(), nil
		}
		origin, e := validateCenter(a.URL)
		if e != nil {
			return fail(e)
		}
		r.center = origin
		return r.status(), nil
	case "im/request":
		var a struct {
			Method string          `json:"method"`
			Path   string          `json:"path"`
			Token  string          `json:"token"`
			Body   json.RawMessage `json:"body"`
		}
		if e := json.Unmarshal(params, &a); e != nil {
			return fail(e)
		}
		if a.Method == "" {
			a.Method = "GET"
		}
		a.Method = strings.ToUpper(a.Method)
		if a.Method != "GET" && a.Method != "POST" && a.Method != "PUT" && a.Method != "PATCH" && a.Method != "DELETE" {
			return fail(errors.New("不支持的请求方法"))
		}
		if len(a.Body) > 1<<20 {
			return fail(errors.New("单次请求过大"))
		}
		if r.center == "" {
			service := r.service
			r.mu.Unlock()
			locked = false
			code, body := service.Request(a.Method, a.Path, a.Token, a.Body)
			return map[string]any{"status": code, "body": body}, nil
		}
		if !validIMPath(a.Path) {
			return fail(errors.New("不允许的中心 API 路径"))
		}
		center := r.center
		r.mu.Unlock()
		locked = false
		client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("不允许中心服务重定向") }, Transport: &http.Transport{Proxy: nil}}
		defer client.CloseIdleConnections()
		req, e := http.NewRequest(a.Method, center+"/api/im"+a.Path, bytes.NewReader(a.Body))
		if e != nil {
			return fail(e)
		}
		req.Header.Set("Content-Type", "application/json")
		if a.Token != "" {
			req.Header.Set("Authorization", "Bearer "+a.Token)
		}
		resp, e := client.Do(req)
		if e != nil {
			return fail(fmt.Errorf("连接中心失败：%w", e))
		}
		defer resp.Body.Close()
		data, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
		if e != nil {
			return fail(e)
		}
		if len(data) > 8<<20 {
			return fail(errors.New("中心响应过大，请分块读取"))
		}
		var body any
		if e = json.Unmarshal(data, &body); e != nil {
			return fail(errors.New("中心响应不是有效 JSON"))
		}
		return map[string]any{"status": resp.StatusCode, "body": body}, nil
	}
	return nil, dbxpluginsdk.MethodNotFound(method)
}
