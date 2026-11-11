package im

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestChannelMembershipRolesInvitesAndArchive(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	host := call(t, s, "POST", "/guest", "", `{}`, 200)["token"].(string)
	_, v := s.request("POST", "/guest", "", json.RawMessage(`{"nickname":"reader"}`), false)
	reader := v.(map[string]any)["token"].(string)
	a, _ := s.auth(reader)
	c := call(t, s, "POST", "/channels", host, `{"name":"财务","members":["`+a.ID+`"]}`, 200)
	id := c["id"].(string)
	call(t, s, "POST", "/conversations/"+id+"/messages", reader, `{"id":"no","text":"read only"}`, 403)
	call(t, s, "POST", "/attachments", reader, `{"conversationId":"`+id+`","name":"no.txt","size":0}`, 403)
	call(t, s, "PATCH", "/channels/"+id, reader, `{"name":"wrong"}`, 403)
	call(t, s, "PATCH", "/channels/"+id, host, `{"name":"财务","members":["`+a.ID+`"],"writers":["`+a.ID+`"]}`, 200)
	call(t, s, "POST", "/conversations/"+id+"/messages", reader, `{"id":"yes","text":"hello"}`, 200)
	inv := call(t, s, "POST", "/channels/"+id+"/invites", host, `{"hours":1,"uses":1}`, 200)
	key := inv["token"].(string)
	_, v = s.request("POST", "/guest", "", json.RawMessage(`{"nickname":"visitor"}`), false)
	visitor := v.(map[string]any)["token"].(string)
	call(t, s, "GET", "/conversations/"+id+"/messages", visitor, ``, 404)
	call(t, s, "POST", "/channels/join", visitor, `{"invite":"`+key+`"}`, 200)
	call(t, s, "GET", "/conversations/"+id+"/messages", visitor, ``, 200)
	call(t, s, "GET", "/conversations/"+id+"/files?limit=2", visitor, ``, 200)
	call(t, s, "PATCH", "/channels/"+id, host, `{"name":"财务","members":[],"writers":[],"archived":true}`, 200)
	call(t, s, "GET", "/conversations/"+id+"/messages", reader, ``, 404)
	call(t, s, "POST", "/conversations/"+id+"/messages", host, `{"id":"archived","text":"no"}`, 403)
	s.Close()
	s, _ = New(dir)
	defer s.Close()
	call(t, s, "POST", "/channels/join", reader, `{"invite":"`+key+`"}`, 410)
	call(t, s, "DELETE", "/channels/"+id, host, ``, 200)
	call(t, s, "GET", "/conversations/"+id+"/messages", host, ``, 404)
}

func TestChannelFileAccessRevokedEverywhere(t *testing.T) {
	s, _ := New(t.TempDir())
	defer s.Close()
	host := call(t, s, "POST", "/guest", "", `{}`, 200)["token"].(string)
	_, v := s.request("POST", "/guest", "", json.RawMessage(`{}`), false)
	reader := v.(map[string]any)["token"].(string)
	a, _ := s.auth(reader)
	id := call(t, s, "POST", "/channels", host, `{"name":"Private","members":["`+a.ID+`"]}`, 200)["id"].(string)
	s.state.Attachments["shared"] = storedAttachment{Attachment: Attachment{ID: "shared", Name: "report.xlsx", Size: 3, Finished: true, ConversationID: id}}
	if err := os.WriteFile(s.filePath("shared"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	s.state.Messages[id] = []Message{{ID: "msg", AttachmentID: "shared", SenderID: a.ID, CreatedAt: 123}}
	ticket := func() string {
		return call(t, s, "POST", "/attachments/shared/download-ticket", reader, `{}`, 200)["url"].(string)
	}
	get := func(path string, want int) {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+reader)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d want %d", path, w.Code, want)
		}
	}
	get("/api/im/attachments/shared/content", 200)
	call(t, s, "GET", "/attachments/shared/chunks?offset=0&length=3", reader, ``, 200)
	issued := ticket()
	call(t, s, "PATCH", "/channels/"+id, host, `{"name":"Private","members":[]}`, 200)
	call(t, s, "GET", "/attachments/shared", reader, ``, 404)
	call(t, s, "GET", "/attachments/shared/chunks?offset=0&length=3", reader, ``, 404)
	call(t, s, "POST", "/attachments/shared/download-ticket", reader, `{}`, 404)
	call(t, s, "GET", "/conversations/"+id+"/files", reader, ``, 404)
	get("/api/im/attachments/shared/content", 404)
	get(issued, 404)
	call(t, s, "PATCH", "/channels/"+id, host, `{"name":"Private","members":["`+a.ID+`"]}`, 200)
	issued = ticket()
	call(t, s, "DELETE", "/channels/"+id, host, ``, 200)
	get(issued, 404)
	get("/api/im/attachments/shared/content", 404)
}

func TestDeleteSharedFileByOwnerOrChannelManager(t *testing.T) {
	s, _ := New(t.TempDir())
	defer s.Close()
	host := call(t, s, "POST", "/guest", "", `{}`, 200)["token"].(string)
	_, value := s.request("POST", "/guest", "", json.RawMessage(`{"nickname":"reader"}`), false)
	reader := value.(map[string]any)["token"].(string)
	readerAccount, _ := s.auth(reader)
	_, value = s.request("POST", "/guest", "", json.RawMessage(`{"nickname":"other"}`), false)
	other := value.(map[string]any)["token"].(string)
	otherAccount, _ := s.auth(other)
	id := call(t, s, "POST", "/channels", host, `{"name":"Team","members":["`+readerAccount.ID+`","`+otherAccount.ID+`"]}`, 200)["id"].(string)
	s.state.Attachments["owned"] = storedAttachment{Attachment: Attachment{ID: "owned", Name: "report.xlsx", Size: 3, Finished: true, ConversationID: id}, OwnerID: readerAccount.ID}
	s.state.Messages[id] = []Message{{ID: "shared-message", ConversationID: id, SenderID: readerAccount.ID, AttachmentID: "owned"}}
	if err := os.WriteFile(s.filePath("owned"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	ticket := call(t, s, "POST", "/attachments/owned/download-ticket", other, `{}`, 200)["url"].(string)
	ticketID := strings.TrimPrefix(ticket, "/api/im/download/")
	// A member cannot delete another member's file; the uploader can delete their own.
	call(t, s, "DELETE", "/conversations/"+id+"/files/owned", other, ``, 403)
	call(t, s, "DELETE", "/conversations/"+id+"/files/owned", reader, ``, 200)
	if _, exists := s.state.Attachments["owned"]; exists {
		t.Fatal("attachment metadata remains")
	}
	if len(s.state.Messages[id]) != 0 {
		t.Fatal("message reference remains", s.state.Messages[id])
	}
	if _, err := os.Stat(s.filePath("owned")); !os.IsNotExist(err) {
		t.Fatal("attachment data remains", err)
	}
	if _, exists := s.downloadTickets[ticketID]; exists {
		t.Fatal("download ticket remains valid")
	}
	files := call(t, s, "GET", "/conversations/"+id+"/files", host, ``, 200)["files"].([]any)
	if len(files) != 0 {
		t.Fatal("deleted file still listed", files)
	}
}

func TestChannelManagerCanDeleteMemberFile(t *testing.T) {
	s, _ := New(t.TempDir())
	defer s.Close()
	host := call(t, s, "POST", "/guest", "", `{}`, 200)["token"].(string)
	_, value := s.request("POST", "/guest", "", json.RawMessage(`{"nickname":"writer"}`), false)
	writer := value.(map[string]any)["token"].(string)
	writerAccount, _ := s.auth(writer)
	id := call(t, s, "POST", "/channels", host, `{"name":"Team","members":["`+writerAccount.ID+`"],"writers":["`+writerAccount.ID+`"]}`, 200)["id"].(string)
	s.state.Attachments["writer-file"] = storedAttachment{Attachment: Attachment{ID: "writer-file", Name: "file.txt", Finished: true, ConversationID: id}, OwnerID: writerAccount.ID}
	s.state.Messages[id] = []Message{{ID: "message", ConversationID: id, SenderID: writerAccount.ID, AttachmentID: "writer-file"}}
	call(t, s, "DELETE", "/conversations/"+id+"/files/writer-file", host, ``, 200)
}

func TestChannelInvitesAndFileLibrary(t *testing.T) {
	s, _ := New(t.TempDir())
	defer s.Close()
	host := call(t, s, "POST", "/guest", "", `{}`, 200)["token"].(string)
	id := call(t, s, "POST", "/channels", host, `{"name":"Files"}`, 200)["id"].(string)
	invite := func() string {
		return call(t, s, "POST", "/channels/"+id+"/invites", host, `{"hours":1,"uses":1}`, 200)["token"].(string)
	}
	guest := func() string {
		_, v := s.request("POST", "/guest", "", json.RawMessage(`{}`), false)
		return v.(map[string]any)["token"].(string)
	}
	a, b := guest(), guest()
	key := invite()
	call(t, s, "POST", "/channels/join", a, `{"invite":"`+key+`"}`, 200)
	call(t, s, "POST", "/channels/join", b, `{"invite":"`+key+`"}`, 410)
	key = invite()
	call(t, s, "DELETE", "/channels/"+id+"/invites/"+key, host, ``, 200)
	call(t, s, "POST", "/channels/join", b, `{"invite":"`+key+`"}`, 410)
	key = invite()
	inv := s.state.ChannelInvites[key]
	inv.Expires = now() - 1
	s.state.ChannelInvites[key] = inv
	call(t, s, "POST", "/channels/join", b, `{"invite":"`+key+`"}`, 410)
	for i, name := range []string{"b.xlsx", "a.PDF", "c.png", "hidden.docx"} {
		fid := fmt.Sprint(i)
		mime := "application/octet-stream"
		if i == 2 {
			mime = "image/png"
		}
		s.state.Attachments[fid] = storedAttachment{Attachment: Attachment{ID: fid, Name: name, Size: int64(i + 1), Mime: mime, Finished: true, ConversationID: id}}
		if i < 3 {
			s.state.Messages[id] = append(s.state.Messages[id], Message{ID: fid, AttachmentID: fid, CreatedAt: int64(i + 10), SenderID: "author"})
		}
	}
	first := call(t, s, "GET", "/conversations/"+id+"/files?limit=2", a, ``, 200)
	if first["total"] != float64(3) || first["more"] != true || first["nextOffset"] != float64(2) {
		t.Fatal(first)
	}
	files := first["files"].([]any)
	if files[0].(map[string]any)["name"] != "c.png" || files[0].(map[string]any)["sharedAt"] != float64(12) {
		t.Fatal(files)
	}
	second := call(t, s, "GET", "/conversations/"+id+"/files?offset=2&limit=2", a, ``, 200)
	if len(second["files"].([]any)) != 1 || second["more"] != false {
		t.Fatal(second)
	}
	for _, query := range []string{"type=image", "q=XLSX", "type=document&q=PDF"} {
		result := call(t, s, "GET", "/conversations/"+id+"/files?"+query, a, ``, 200)
		if result["total"] != float64(1) {
			t.Fatal(query, result)
		}
	}
	ordered := call(t, s, "GET", "/conversations/"+id+"/files?type=document&sort=name", a, ``, 200)
	if ordered["files"].([]any)[0].(map[string]any)["name"] != "a.PDF" {
		t.Fatal(ordered)
	}
}

func TestChannelMemberLimit(t *testing.T) {
	s, _ := New(t.TempDir())
	defer s.Close()
	host := call(t, s, "POST", "/guest", "", `{}`, 200)["token"].(string)
	id := call(t, s, "POST", "/channels", host, `{"name":"Full"}`, 200)["id"].(string)
	c := s.state.Conversations[id]
	for i := 1; i < 100; i++ {
		uid := fmt.Sprint(i)
		s.state.Users[uid] = account{User: User{ID: uid}}
		c.Members = append(c.Members, uid)
	}
	s.state.Conversations[id] = c
	key := call(t, s, "POST", "/channels/"+id+"/invites", host, `{"hours":1,"uses":1}`, 200)["token"].(string)
	_, v := s.request("POST", "/guest", "", json.RawMessage(`{}`), false)
	call(t, s, "POST", "/channels/join", v.(map[string]any)["token"].(string), `{"invite":"`+key+`"}`, 409)
	body, _ := json.Marshal(map[string]any{"name": "Full renamed", "members": c.Members, "archived": true})
	call(t, s, "PATCH", "/channels/"+id, host, string(body), 200)
}

func TestUnifiedGroupPublishingAndLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	host := call(t, s, "POST", "/guest", "", `{}`, 200)["token"].(string)
	_, v := s.request("POST", "/guest", "", json.RawMessage(`{}`), false)
	other := v.(map[string]any)["token"].(string)
	u, _ := s.auth(other)
	c := call(t, s, "POST", "/conversations", host, `{"kind":"group","name":"Team","description":"Keep this description","members":["`+u.ID+`"]}`, 200)
	id := c["id"].(string)
	call(t, s, "POST", "/conversations/"+id+"/messages", other, `{"id":"all","text":"ok"}`, 200)
	call(t, s, "PATCH", "/conversations/"+id, host, `{"name":"Team","restricted":true}`, 200)
	if s.state.Conversations[id].Description != "Keep this description" {
		t.Fatal("partial PATCH erased description")
	}
	call(t, s, "POST", "/conversations/"+id+"/messages", other, `{"id":"blocked","text":"no"}`, 403)
	call(t, s, "POST", "/attachments", other, `{"conversationId":"`+id+`","name":"no.txt","size":0}`, 403)
	call(t, s, "PATCH", "/conversations/"+id, other, `{"restricted":false}`, 403)
	call(t, s, "PATCH", "/conversations/"+id, host, `{"name":"Team","restricted":true,"members":["`+u.ID+`"],"writers":["`+u.ID+`"]}`, 200)
	call(t, s, "POST", "/conversations/"+id+"/messages", other, `{"id":"writer","text":"yes"}`, 200)
	call(t, s, "PATCH", "/conversations/"+id, host, `{"restricted":false}`, 200)
	call(t, s, "POST", "/conversations/"+id+"/messages", other, `{"id":"all-again","text":"ok"}`, 200)
	call(t, s, "PATCH", "/conversations/"+id, host, `{"restricted":true}`, 200)

	key := call(t, s, "POST", "/conversations/"+id+"/invites", host, `{"hours":1,"uses":1}`, 200)["token"].(string)
	if key == "" {
		t.Fatal("missing invitation")
	}
	call(t, s, "PATCH", "/conversations/"+id, host, `{"name":"Team","archived":true}`, 200)
	call(t, s, "POST", "/conversations/"+id+"/messages", other, `{"id":"archived","text":"no"}`, 403)
	call(t, s, "GET", "/conversations/"+id+"/files", other, ``, 200)
	old := Conversation{ID: "old-space", Kind: "channel", Name: "Private", OwnerID: u.ID, Members: []string{u.ID}, Writers: []string{}, Archived: true}
	s.state.Conversations[old.ID] = old
	s.state.Messages[old.ID] = []Message{{ID: "history", Text: "keep"}}
	s.Close()
	s, _ = New(dir)
	defer s.Close()
	migrated := s.state.Conversations[old.ID]
	if migrated.Kind != "group" || !restrictedForTest(migrated) || !migrated.Archived || len(s.state.Messages[old.ID]) != 1 {
		t.Fatal("migration lost kind, permissions, archive or history", migrated)
	}
	if !restrictedForTest(s.state.Conversations[id]) {
		t.Fatal("group policy not persisted")
	}
	call(t, s, "POST", "/conversations/join", host, `{"invite":"`+key+`"}`, 410)
	call(t, s, "POST", "/conversations/"+id+"/dissolve", host, `{}`, 200)
	call(t, s, "GET", "/conversations/"+id+"/messages", other, ``, 404)
}

func restrictedForTest(c Conversation) bool {
	b, _ := json.Marshal(c)
	var v map[string]any
	json.Unmarshal(b, &v)
	return v["restricted"] == true
}
