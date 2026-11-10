package im

import (
	"encoding/json"
	"testing"
)

func TestLobbyCannotBeRemovedAndLegacyHiddenLobbyReturns(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	host := call(t, s, "POST", "/guest", "", `{}`, 200)
	token := host["token"].(string)
	call(t, s, "DELETE", "/conversations/lan-lobby", token, ``, 409)
	a, _ := s.auth(token)
	if s.state.Hidden == nil {
		s.state.Hidden = map[string]bool{}
	}
	s.state.Hidden[a.ID+":lan-lobby"] = true
	s.Close()
	s, _ = New(dir)
	defer s.Close()
	list := call(t, s, "GET", "/conversations", token, ``, 200)
	for _, raw := range list["conversations"].([]any) {
		if raw.(map[string]any)["id"] == "lan-lobby" {
			return
		}
	}
	t.Fatal("Previously hidden lobby must be visible after restart")
}

func TestConversationRemovalIsPersonalAndRestores(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	host := call(t, s, "POST", "/guest", "", `{}`, 200)
	token := host["token"].(string)
	_, v := s.request("POST", "/guest", "", json.RawMessage(`{"nickname":"friend"}`), false)
	friend := v.(map[string]any)
	other := friend["token"].(string)
	uid := friend["user"].(User).ID
	c := call(t, s, "POST", "/conversations", token, `{"kind":"dm","members":["`+uid+`"]}`, 200)
	id := c["id"].(string)
	call(t, s, "DELETE", "/conversations/"+id, token, ``, 200)
	check := func(tok string, want bool) {
		t.Helper()
		list := call(t, s, "GET", "/conversations", tok, ``, 200)
		found := false
		for _, raw := range list["conversations"].([]any) {
			if raw.(map[string]any)["id"] == id {
				found = true
			}
		}
		if found != want {
			t.Fatalf("visible %v want %v", found, want)
		}
	}
	check(token, false)
	check(other, true)
	s.Close()
	s, _ = New(dir)
	check(token, false)
	call(t, s, "POST", "/conversations", token, `{"kind":"dm","members":["`+uid+`"]}`, 200)
	check(token, true)
	call(t, s, "DELETE", "/conversations/"+id, token, ``, 200)
	call(t, s, "POST", "/conversations/"+id+"/messages", other, `{"id":"new-message","text":"hello"}`, 200)
	check(token, true)
}
