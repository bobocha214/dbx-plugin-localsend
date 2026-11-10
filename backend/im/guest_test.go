package im

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestGuestHostPersistenceAndVisitors(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	host := call(t, s, "POST", "/guest", "", `{"nickname":"Host"}`, 200)
	hid := host["user"].(map[string]any)["id"]
	s.Close()
	s, _ = New(dir)
	again := call(t, s, "POST", "/guest", "", `{}`, 200)
	if again["user"].(map[string]any)["id"] != hid {
		t.Fatal("host identity changed")
	}
	visit := func() map[string]any {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/im/guest", strings.NewReader(`{"nickname":"Visitor"}`)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	a, b := visit(), visit()
	if a["user"].(map[string]any)["id"] == b["user"].(map[string]any)["id"] {
		t.Fatal("visitors share identity")
	}
	if a["user"].(map[string]any)["admin"] == true {
		t.Fatal("visitor admin")
	}
	tok := a["token"].(string)
	call(t, s, "GET", "/conversations/lan-lobby/messages", tok, ``, 200)
	call(t, s, "POST", "/attachments", tok, `{"conversationId":"lan-lobby","name":"huge.bin","size":1099511627776}`, 200)
}

func TestGuestTicketAndPrivateIsolation(t *testing.T) {
	s, _ := New(t.TempDir())
	c, a := s.Request("POST", "/guest", "", json.RawMessage(`{}`))
	if c != 200 {
		t.Fatal(c)
	}
	token := a.(map[string]any)["token"].(string)
	s.mu.Lock()
	s.state.Attachments["testfile"] = storedAttachment{Attachment: Attachment{ID: "testfile", Name: "a.txt", Size: 3, Finished: true, ConversationID: "lan-lobby"}}
	s.mu.Unlock()
	os.WriteFile(s.filePath("testfile"), []byte("abc"), 0600)
	mint := func() string {
		code, v := s.Request("POST", "/attachments/testfile/download-ticket", token, json.RawMessage(`{}`))
		if code != 200 {
			t.Fatal(code, v)
		}
		return v.(map[string]any)["url"].(string)
	}
	url := mint()
	get := func(url string) int {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		return w.Code
	}
	if get(url) != 200 || get(url) != 404 {
		t.Fatal("ticket not single use")
	}
	url = mint()
	key := strings.TrimPrefix(url, "/api/im/download/")
	s.mu.Lock()
	ticket := s.downloadTickets[key]
	ticket.Expires = now() - 1
	s.downloadTickets[key] = ticket
	s.mu.Unlock()
	if get(url) != 404 {
		t.Fatal("expired ticket accepted")
	}
	code, _ := s.Request("PATCH", "/conversations/lan-lobby", token, json.RawMessage(`{"members":[]}`))
	if code != 409 {
		t.Fatal("public lobby editable")
	}
	code, _ = s.Request("POST", "/login", "", json.RawMessage(`{}`))
	if code != 410 {
		t.Fatal("legacy login remains exposed")
	}
}

func TestGuestPrivateHistoryAndFileMembership(t *testing.T) {
	s, _ := New(t.TempDir())
	guest := func() (string, string) {
		code, v := s.request("POST", "/guest", "", json.RawMessage(`{}`), false)
		if code != 200 {
			t.Fatal(code)
		}
		m := v.(map[string]any)
		return m["token"].(string), m["user"].(User).ID
	}
	a, aid := guest()
	b, bid := guest()
	c, _ := guest()
	conv := call(t, s, "POST", "/conversations", a, `{"kind":"dm","members":["`+bid+`"]}`, 200)
	id := conv["id"].(string)
	call(t, s, "POST", "/conversations/"+id+"/messages", a, `{"id":"hello","text":"private"}`, 200)
	call(t, s, "GET", "/conversations/"+id+"/messages", b, ``, 200)
	call(t, s, "GET", "/conversations/"+id+"/messages", c, ``, 404)
	file := call(t, s, "POST", "/attachments", a, `{"conversationId":"`+id+`","name":"large.bin","size":9223372036854775807}`, 200)
	fid := file["id"].(string)
	call(t, s, "GET", "/attachments/"+fid, c, ``, 404)
	s.mu.Lock()
	v := s.state.Attachments[fid]
	v.Offset = v.Size - 1
	s.state.Attachments[fid] = v
	s.mu.Unlock()
	call(t, s, "PUT", "/attachments/"+fid+"/chunks", a, `{"offset":9223372036854775806,"dataBase64":"YWJj"}`, 400)
	s.mu.Lock()
	s.state.Users["old"] = account{User: User{ID: "old", Username: "old-password-user"}, Hash: "secret"}
	s.state.Sessions["oldtoken"] = session{UserID: "old", Expires: now() + 60000}
	s.mu.Unlock()
	call(t, s, "GET", "/me", "oldtoken", ``, 401)
	users := call(t, s, "GET", "/users", a, ``, 200)
	if strings.Contains(string(mustJSON(users)), "old-password-user") {
		t.Fatal("legacy account leaked")
	}
	_ = aid
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestGuestMessagingRegression(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	host := call(t, s, "POST", "/guest", "", `{}`, 200)
	ht := host["token"].(string)
	_, v := s.request("POST", "/guest", "", json.RawMessage(`{}`), false)
	m := v.(map[string]any)
	bt := m["token"].(string)
	bid := m["user"].(User).ID
	c := call(t, s, "POST", "/conversations", ht, `{"kind":"group","name":"team","members":["`+bid+`"]}`, 200)
	cid := c["id"].(string)
	call(t, s, "PATCH", "/conversations/"+cid, bt, `{"name":"stolen"}`, 403)
	for _, id := range []string{"one", "two"} {
		call(t, s, "POST", "/conversations/"+cid+"/messages", ht, `{"id":"`+id+`","text":"hello"}`, 200)
	}
	call(t, s, "POST", "/conversations/"+cid+"/messages", ht, `{"id":"one","text":"hello"}`, 200)
	call(t, s, "POST", "/conversations/"+cid+"/messages", ht, `{"id":"one","text":"different"}`, 409)
	s.mu.Lock()
	list := s.state.Messages[cid]
	list[1].CreatedAt = list[0].CreatedAt
	s.state.Messages[cid] = list
	s.mu.Unlock()
	msgs := call(t, s, "GET", "/conversations/"+cid+"/messages?limit=1&beforeId=two", bt, ``, 200)
	if len(msgs["messages"].([]any)) != 1 || msgs["messages"].([]any)[0].(map[string]any)["id"] != "one" {
		t.Fatal(msgs)
	}
	convs := call(t, s, "GET", "/conversations", bt, ``, 200)
	found := false
	for _, raw := range convs["conversations"].([]any) {
		c := raw.(map[string]any)
		if c["id"] == cid {
			found = c["unread"] == float64(2)
		}
	}
	if !found {
		t.Fatal(convs)
	}
	call(t, s, "POST", "/conversations/"+cid+"/read", bt, `{}`, 200)
	f := call(t, s, "POST", "/attachments", ht, `{"conversationId":"`+cid+`","name":"a.txt","size":3}`, 200)
	fid := f["id"].(string)
	call(t, s, "PUT", "/attachments/"+fid+"/chunks", ht, `{"offset":1,"dataBase64":"YWJj"}`, 409)
	call(t, s, "PUT", "/attachments/"+fid+"/chunks", ht, `{"offset":0,"dataBase64":"YWJj"}`, 200)
	call(t, s, "POST", "/attachments/"+fid+"/finish", ht, `{"sha256":"bad"}`, 400)
	call(t, s, "POST", "/attachments/"+fid+"/finish", ht, `{"sha256":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}`, 200)
	s.Close()
	s, _ = New(dir)
	msgs = call(t, s, "GET", "/conversations/"+cid+"/messages", bt, ``, 200)
	if len(msgs["messages"].([]any)) != 2 {
		t.Fatal(msgs)
	}
	s.SetPeerHandler(nil, func(string, string, json.RawMessage) (int, any) { return 200, map[string]any{"ok": true} })
	call(t, s, "GET", "/peers", ht, ``, 200)
	call(t, s, "GET", "/peers", bt, ``, 403)
	code, _ := s.request("GET", "/peers", ht, nil, false)
	if code != 403 {
		t.Fatal("HTTP peer access", code)
	}
}
