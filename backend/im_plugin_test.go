package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCenterOriginValidation(t *testing.T) {
	for _, raw := range []string{"http://192.168.1.10:53318", "http://127.0.0.1:53318", "https://[fd00::1]:8443"} {
		if _, err := validateCenter(raw); err != nil {
			t.Errorf("reject LAN origin %s: %v", raw, err)
		}
	}
	for _, raw := range []string{"http://example.com", "http://8.8.8.8", "file:///etc/passwd", "http://user:pw@192.168.1.2", "http://192.168.1.2/path", "http://169.254.169.254", "http://192.168.1.2?token=x"} {
		if _, err := validateCenter(raw); err == nil {
			t.Errorf("accepted unsafe origin %s", raw)
		}
	}
}

func TestSlowCenterDoesNotBlockNodeControls(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"initialized":true}`)
	}))
	defer remote.Close()
	p := NewPlugin()
	p.cfgPath = filepath.Join(t.TempDir(), "config.json")
	p.handleIM("im/connect", json.RawMessage(`{"url":"`+remote.URL+`"}`))
	done := make(chan struct{})
	go func() { defer close(done); p.handleIM("im/request", json.RawMessage(`{"path":"/status"}`)) }()
	<-entered
	controls := make(chan struct{})
	go func() { p.handleIM("im/status", json.RawMessage(`{}`)); close(controls) }()
	select {
	case <-controls:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("slow center blocks local node controls")
	}
	close(release)
	<-done
}

func TestProxyPathValidation(t *testing.T) {
	for _, p := range []string{"/status", "/guest", "/conversations/abc/messages?q=hello", "/attachments/abc/chunks?offset=0"} {
		if !validIMPath(p) {
			t.Errorf("rejected %s", p)
		}
	}
	for _, p := range []string{"//evil.com", "/../secret", "/%2e%2e/secret", "/setup", "/login", "/register", "/node/message", "https://evil.com"} {
		if validIMPath(p) {
			t.Errorf("accepted unsafe path %s", p)
		}
	}
}

func TestIMPluginLifecycleAndBrowser(t *testing.T) {
	p := NewPlugin()
	p.cfgPath = filepath.Join(t.TempDir(), "config.json")
	call := func(method string, body any) any {
		t.Helper()
		b, _ := json.Marshal(body)
		v, e := p.handleIM(method, b)
		if e != nil {
			t.Fatalf("%s: %v", method, e)
		}
		return v
	}
	t.Cleanup(func() { call("im/stop", map[string]any{}) })
	v := call("im/request", map[string]any{"method": "POST", "path": "/guest", "body": map[string]any{"nickname": "本机用户"}}).(map[string]any)
	if v["status"] != 200 {
		t.Fatalf("setup: %#v", v)
	}
	token := v["body"].(map[string]any)["token"].(string)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	call("im/start", map[string]any{"port": port})
	for _, path := range []string{"/", "/im.js", "/im.css", "/api/im/node/info?challenge=test"} {
		resp, e := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("browser %s: %d", path, resp.StatusCode)
		}
	}
	call("im/connect", map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%d", port)})
	me := call("im/request", map[string]any{"path": "/me", "token": token}).(map[string]any)
	if me["status"] != 200 {
		t.Fatalf("remote proxy auth: %#v", me)
	}
	call("im/connect", map[string]any{"url": ""})
	call("im/stop", map[string]any{})
	call("im/start", map[string]any{"port": port})
	call("im/stop", map[string]any{})
	if _, e := os.Stat(filepath.Join(filepath.Dir(p.cfgPath), "im", "state.json")); e != nil {
		t.Fatal(e)
	}
}
