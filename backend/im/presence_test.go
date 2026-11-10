package im

import (
	"encoding/json"
	"testing"
)

func TestPresenceExpiresWithoutDeletingIdentity(t *testing.T) {
	s, _ := New(t.TempDir())
	defer s.Close()
	first := call(t, s, "POST", "/guest", "", `{}`, 200)
	token := first["token"].(string)
	_, result := s.request("POST", "/guest", "", json.RawMessage(`{"nickname":"browser"}`), false)
	other := result.(map[string]any)["token"].(string)
	call(t, s, "GET", "/me", other, ``, 200)
	a, _ := s.auth(other)
	check := func(want bool) {
		t.Helper()
		list := call(t, s, "GET", "/users", token, ``, 200)
		for _, raw := range list["users"].([]any) {
			u := raw.(map[string]any)
			if u["id"] == a.ID {
				if u["online"] != want {
					t.Fatalf("online=%v want %v", u["online"], want)
				}
				return
			}
		}
		t.Fatal("identity missing")
	}
	check(true)
	s.presence[a.ID] = now() - presenceTimeout - 1
	check(false)
	call(t, s, "GET", "/me", other, ``, 200)
	check(true)
}
