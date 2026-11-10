package im

import (
	"encoding/json"

	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPCrossOriginRejected(t *testing.T) {
	s, _ := New(t.TempDir())
	r := httptest.NewRequest("POST", "http://node.local/api/im/login", strings.NewReader(`{}`))
	r.Header.Set("Origin", "http://evil.local")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross origin got %d", w.Code)
	}
}

func call(t *testing.T, s *Service, method, path, token, body string, want int) map[string]any {
	t.Helper()

	status, v := s.Request(method, path, token, json.RawMessage(body))
	if status != want {
		t.Fatalf("%s %s: %d %#v", method, path, status, v)
	}
	b, _ := json.Marshal(v)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}
func TestHTTPSetupForbidden(t *testing.T) {
	s, _ := New(t.TempDir())
	r := httptest.NewRequest("POST", "/api/im/setup", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 410 {
		t.Fatal(w.Code)
	}
}
