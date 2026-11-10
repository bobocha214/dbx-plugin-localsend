package im

import (
	"encoding/json"
	"testing"
)

func TestDownloadHistoryPersistenceAndStatus(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	host := call(t, s, "POST", "/guest", "", `{}`, 200)
	tok := host["token"].(string)
	call(t, s, "POST", "/downloads", tok, `{"id":"task1","name":"real.zip","size":100,"received":30,"status":"paused","scope":"room"}`, 200)
	call(t, s, "POST", "/downloads", tok, `{"id":"task1","name":"real.zip","size":100,"received":100,"status":"saved","scope":"room"}`, 200)
	s.Close()
	s, _ = New(dir)
	h := call(t, s, "GET", "/downloads", tok, ``, 200)["downloads"].([]any)
	if len(h) != 1 || h[0].(map[string]any)["status"] != "saved" {
		t.Fatal(h)
	}
	call(t, s, "POST", "/downloads", tok, `{"id":"bad","size":100,"received":20,"status":"saved"}`, 400)
	call(t, s, "POST", "/downloads", tok, `{"id":"browser","name":"x","size":100,"received":0,"status":"browser"}`, 200)
	_, v := s.request("POST", "/guest", "", json.RawMessage(`{"nickname":"other"}`), false)
	other := v.(map[string]any)["token"].(string)
	list := call(t, s, "GET", "/downloads", other, ``, 200)["downloads"].([]any)
	if len(list) != 0 {
		t.Fatal("another visitor can see download history")
	}
}
