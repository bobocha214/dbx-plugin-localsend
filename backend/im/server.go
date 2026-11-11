package im

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
)

type input struct {
	Restricted     *bool    `json:"restricted"`
	Description    *string  `json:"description"`
	Archived       *bool    `json:"archived"`
	Writers        []string `json:"writers"`
	Username       string   `json:"username"`
	Password       string   `json:"password"`
	OldPassword    string   `json:"oldPassword"`
	Nickname       string   `json:"nickname"`
	Bio            string   `json:"bio"`
	Avatar         string   `json:"avatar"`
	Invite         string   `json:"invite"`
	Uses           int      `json:"uses"`
	Hours          int      `json:"hours"`
	Disabled       bool     `json:"disabled"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Members        []string `json:"members"`
	ID             string   `json:"id"`
	Text           string   `json:"text"`
	AttachmentID   string   `json:"attachmentId"`
	ConversationID string   `json:"conversationId"`
	Size           int64    `json:"size"`
	Mime           string   `json:"mime"`
	Offset         int64    `json:"offset"`
	DataBase64     string   `json:"dataBase64"`
	SHA256         string   `json:"sha256"`
}

func (s *Service) Request(method, path, token string, body json.RawMessage) (int, any) {
	return s.request(method, path, token, body, true)
}
func (s *Service) request(method, path, token string, body json.RawMessage, local bool) (int, any) {
	if u, e := url.ParseRequestURI(path); e == nil && (u.Path == "/peers" || strings.HasPrefix(u.Path, "/peers/")) {
		s.mu.Lock()
		a, ok := s.auth(token)
		f := s.peerRequest
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return failure(503, "service closed")
		}
		if !ok {
			return failure(401, "authentication required")
		}
		if !local || !a.Admin {
			return failure(403, "peer controls require the local administrator")
		}
		if len(body) > 400000 || len(body) > 0 && !json.Valid(body) {
			return failure(400, "invalid JSON body")
		}
		if f == nil {
			return failure(404, "peer service unavailable")
		}
		return f(method, path, body)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return failure(503, "service closed")
	}
	var before []byte
	if method != "GET" {
		before, _ = json.Marshal(s.state)
	}
	code, v := s.route(method, path, token, body, local)
	defer func() { s.pendingDeletes = nil }()
	if code < 400 && method != "GET" {
		if e := s.save(); e != nil {
			s.state = diskState{}
			json.Unmarshal(before, &s.state)
			return failure(500, "could not persist data")
		}
		for _, id := range s.pendingDeletes {
			_ = os.Remove(s.filePath(id))
		}
	}
	return code, v
}
func (s *Service) route(method, path, token string, body json.RawMessage, local bool) (int, any) {
	uurl, e := url.ParseRequestURI(path)
	if e != nil || !strings.HasPrefix(path, "/") {
		return failure(400, "invalid path")
	}
	path = strings.TrimSuffix(uurl.Path, "/")
	if path == "/setup" || path == "/login" || path == "/register" || path == "/password" || path == "/invites" || strings.HasPrefix(path, "/invites/") || path == "/sessions" || strings.HasPrefix(path, "/sessions/") || method == "PATCH" && strings.HasPrefix(path, "/users/") {
		return failure(410, "password accounts have been replaced by guest access")
	}
	var b input
	if len(body) > 0 {
		if len(body) > 400000 || json.Unmarshal(body, &b) != nil {
			return failure(400, "invalid JSON body")
		}
	}
	if path == "/guest" && method == "POST" {
		return s.guest(b, local)
	}
	if c, v, ok := s.accountRoute(method, path, token, b, local); ok {
		return c, v
	}
	if method == "GET" && strings.HasPrefix(path, "/attachments/") && strings.HasSuffix(path, "/chunks") {
		return s.downloadChunk(uurl.String(), token)
	}
	a, ok := s.auth(token)
	if !ok {
		return failure(401, "authentication required")
	}
	if s.presence == nil {
		s.presence = map[string]int64{}
	}
	s.presence[a.ID] = now()
	if path == "/conversations/join" && method == "POST" {
		return s.channelRoute(method, "/channels/join", a, b)
	}
	if strings.HasPrefix(path, "/conversations/") {
		parts := strings.Split(strings.TrimPrefix(path, "/conversations/"), "/")
		if len(parts) >= 2 && parts[1] == "invites" {
			return s.channelRoute(method, "/channels/"+strings.Join(parts, "/"), a, b)
		}
		if len(parts) == 2 && parts[1] == "dissolve" && method == "POST" {
			return s.channelRoute("DELETE", "/channels/"+parts[0], a, b)
		}
	}
	if path == "/channels" || strings.HasPrefix(path, "/channels/") {
		return s.channelRoute(method, path, a, b)
	}
	if strings.HasPrefix(path, "/peers") && s.peerRequest != nil {
		if !local || !a.Admin {
			return failure(403, "peer controls require the local administrator")
		}
		return s.peerRequest(method, uurl.String(), body)
	}
	if path == "/stickers" || strings.HasPrefix(path, "/stickers/") {
		return s.stickerRoute(method, uurl, a, b)
	}
	if path == "/downloads" {
		return s.downloadHistory(method, a, body)
	}
	if path == "/me" {
		if method == "PATCH" {
			if len(b.Nickname) > 100 || len(b.Bio) > 2000 || len(b.Avatar) > 150000 {
				return failure(400, "profile too large")
			}
			if !validAvatar(b.Avatar) {
				return failure(400, "avatar must be a PNG, JPEG or WebP image")
			}
			a.Nickname = b.Nickname
			a.Bio = b.Bio
			a.Avatar = b.Avatar
			s.state.Users[a.ID] = a
		}
		if method == "GET" || method == "PATCH" {
			return 200, a.User
		}
	}
	if path == "/logout" && method == "POST" {
		delete(s.state.Sessions, token)
		return 200, map[string]any{"ok": true}
	}
	if path == "/sessions" && method == "GET" {
		list := []map[string]any{}
		for key, v := range s.state.Sessions {
			if v.UserID == a.ID && v.Expires > now() {
				list = append(list, map[string]any{"id": v.ID, "current": key == token, "createdAt": v.CreatedAt, "expires": v.Expires})
			}
		}
		return 200, map[string]any{"sessions": list}
	}
	if strings.HasPrefix(path, "/sessions/") && method == "DELETE" {
		id := strings.TrimPrefix(path, "/sessions/")
		for key, v := range s.state.Sessions {
			if v.UserID == a.ID && v.ID == id {
				delete(s.state.Sessions, key)
				return 200, map[string]any{"ok": true}
			}
		}
		return failure(404, "session not found")
	}
	if strings.HasPrefix(path, "/invites/") && method == "DELETE" {
		if !a.Admin {
			return failure(403, "administrator required")
		}
		id := strings.TrimPrefix(path, "/invites/")
		for key, v := range s.state.Invites {
			if v.ID == id {
				delete(s.state.Invites, key)
				return 200, map[string]any{"ok": true}
			}
		}
		return failure(404, "invitation not found")
	}
	if path == "/password" && method == "POST" {
		if len(b.Password) < 8 || len(b.Password) > 1024 || passwordHash(b.OldPassword, a.Salt) != a.Hash {
			return failure(400, "invalid password")
		}
		a.Salt = randomID()
		a.Hash = passwordHash(b.Password, a.Salt)
		s.state.Users[a.ID] = a
		s.revoke(a.ID)
		return 200, s.session(a)
	}
	if path == "/users" && method == "GET" {
		out := []User{}
		for _, u := range s.state.Users {
			if a.Hash == "" && u.Hash != "" {
				continue
			}
			user := u.User
			user.LastSeen = s.presence[u.ID]
			user.Online = user.LastSeen > 0 && now()-user.LastSeen <= presenceTimeout
			out = append(out, user)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
		return 200, map[string]any{"users": out}
	}
	if strings.HasPrefix(path, "/users/") && method == "PATCH" {
		if !a.Admin {
			return failure(403, "administrator required")
		}
		id := strings.TrimPrefix(path, "/users/")
		u, ok := s.state.Users[id]
		if !ok {
			return failure(404, "user not found")
		}
		if id == a.ID && b.Disabled {
			return failure(400, "cannot disable yourself")
		}
		u.Disabled = b.Disabled
		s.state.Users[id] = u
		if b.Disabled {
			s.revoke(id)
		}
		return 200, u.User
	}
	if path == "/invites" {
		if !a.Admin {
			return failure(403, "administrator required")
		}
		if method == "POST" {
			if b.Uses < 1 || b.Uses > 1000 || b.Hours < 1 || b.Hours > 720 {
				return failure(400, "invalid invitation limits")
			}
			v := invite{ID: randomID(), Token: randomID(), Uses: b.Uses, Expires: now() + int64(b.Hours)*3600000}
			s.state.Invites[v.Token] = v
			return 200, v
		}
		if method == "GET" {
			list := []invite{}
			for _, v := range s.state.Invites {
				v.Token = ""
				list = append(list, v)
			}
			return 200, map[string]any{"invites": list}
		}
	}
	if path == "/conversations" {
		if method == "GET" {
			list := []Conversation{}
			for _, c := range s.state.Conversations {
				if member(c, a.ID) && (c.ID == "lan-lobby" || !s.state.Hidden[a.ID+":"+c.ID]) {
					for _, m := range s.state.Messages[c.ID] {
						if m.SenderID != a.ID && m.CreatedAt > s.state.Reads[a.ID+":"+c.ID] {
							c.Unread++
						}
					}
					list = append(list, c)
				}
			}
			sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt > list[j].UpdatedAt })
			return 200, map[string]any{"conversations": list}
		}
		if method == "POST" {
			if b.Kind != "dm" && b.Kind != "group" || len(b.Name) > 200 || len(b.Members) > 100 {
				return failure(400, "invalid conversation")
			}
			members := []string{a.ID}
			seen := map[string]bool{a.ID: true}
			for _, id := range b.Members {
				u, ok := s.state.Users[id]
				if !ok || u.Disabled {
					return failure(400, "invalid member")
				}
				if !seen[id] {
					members = append(members, id)
					seen[id] = true
				}
			}
			if b.Kind == "dm" && len(members) != 2 {
				return failure(400, "direct conversation needs two members")
			}
			if b.Kind == "dm" {
				for _, c := range s.state.Conversations {
					if c.Kind == "dm" && member(c, members[0]) && member(c, members[1]) {
						delete(s.state.Hidden, a.ID+":"+c.ID)
						return 200, c
					}
				}
			}
			c := Conversation{ID: randomID(), Name: b.Name, Kind: b.Kind, Members: members, UpdatedAt: now(), OwnerID: a.ID}
			if b.Kind == "group" {
				if len(strings.TrimSpace(b.Name)) == 0 || len(inputDescription(b)) > 2000 {
					return failure(400, "invalid group")
				}
				c.Description = inputDescription(b)
				if b.Restricted != nil {
					c.Restricted = *b.Restricted
				}
				if !s.setChannelMembers(&c, b) {
					return failure(400, "invalid group members or contributors")
				}
			}

			s.state.Conversations[c.ID] = c
			return 200, c
		}
	}
	if strings.HasPrefix(path, "/conversations/") {
		parts := strings.Split(strings.TrimPrefix(path, "/conversations/"), "/")
		c, ok := s.state.Conversations[parts[0]]
		if !ok || !member(c, a.ID) {
			return failure(404, "conversation not found")
		}
		if len(parts) == 1 && method == "DELETE" {
			if c.ID == "lan-lobby" {
				return failure(409, "局域网大厅是固定会话，不能移除")
			}
			if s.state.Hidden == nil {
				s.state.Hidden = map[string]bool{}
			}
			s.state.Hidden[a.ID+":"+c.ID] = true
			return 200, map[string]any{"ok": true}
		}
		if len(parts) == 2 && parts[1] == "open" && method == "POST" {
			delete(s.state.Hidden, a.ID+":"+c.ID)
			return 200, c
		}
		if len(parts) == 1 && method == "PATCH" {
			if c.ID == "lan-lobby" {
				return failure(409, "public lobby membership is automatic")
			}
			return s.channelRoute(method, "/channels/"+c.ID, a, b)
		}
		if len(parts) == 2 && parts[1] == "read" && method == "POST" {
			s.state.Reads[a.ID+":"+c.ID] = now()
			return 200, map[string]any{"ok": true}
		}
		if len(parts) == 2 && parts[1] == "files" && method == "GET" {
			return s.conversationFiles(c, uurl)
		}
		if len(parts) == 3 && parts[1] == "files" && method == "DELETE" {
			attachment, exists := s.state.Attachments[parts[2]]
			if !exists || attachment.ConversationID != c.ID || !attachment.Finished {
				return failure(404, "attachment not found")
			}
			if attachment.OwnerID != a.ID && (c.OwnerID != a.ID && !a.Admin) {
				return failure(403, "仅文件发送者、群主或本机管理员可以删除")
			}
			messages := s.state.Messages[c.ID][:0]
			for _, message := range s.state.Messages[c.ID] {
				if message.AttachmentID != attachment.ID {
					messages = append(messages, message)
				}
			}
			s.state.Messages[c.ID] = messages
			delete(s.state.Attachments, attachment.ID)
			for key, ticket := range s.downloadTickets {
				if ticket.AttachmentID == attachment.ID {
					delete(s.downloadTickets, key)
				}
			}
			s.pendingDeletes = append(s.pendingDeletes, attachment.ID)
			return 200, map[string]any{"ok": true}
		}
		if len(parts) == 2 && parts[1] == "messages" {
			if method == "GET" {
				list := []Message{}
				q := strings.ToLower(uurl.Query().Get("q"))
				before, _ := strconv.ParseInt(uurl.Query().Get("before"), 10, 64)
				beforeID := uurl.Query().Get("beforeId")
				for _, m := range s.state.Messages[c.ID] {
					if beforeID != "" && m.ID == beforeID {
						break
					}
					if (beforeID != "" || before == 0 || m.CreatedAt < before) && (q == "" || strings.Contains(strings.ToLower(m.Text), q)) {
						list = append(list, m)
					}
				}
				limit, _ := strconv.Atoi(uurl.Query().Get("limit"))
				if limit < 1 || limit > 500 {
					limit = 200
				}
				total := len(list)
				more := total > limit
				if more {
					list = list[total-limit:]
				}
				return 200, map[string]any{"messages": list, "total": total, "more": more}
			}
			if method == "POST" {
				if !canContribute(c, a.ID) {
					return failure(403, "群聊只读或已归档")
				}
				if len(b.ID) < 1 || len(b.ID) > 128 || len(b.Text) > 20000 || strings.TrimSpace(b.Text) == "" && b.AttachmentID == "" {
					return failure(400, "invalid message")
				}
				for _, m := range s.state.Messages[c.ID] {
					if m.ID == b.ID {
						if m.SenderID != a.ID || m.Text != b.Text || m.AttachmentID != b.AttachmentID {
							return failure(409, "message ID already used")
						}
						return 200, m
					}
				}
				if b.AttachmentID != "" {
					f, ok := s.state.Attachments[b.AttachmentID]
					if !ok || !f.Finished || f.ConversationID != c.ID {
						return failure(400, "attachment unavailable")
					}
				}
				m := Message{b.ID, c.ID, a.ID, b.Text, b.AttachmentID, now()}
				s.state.Messages[c.ID] = append(s.state.Messages[c.ID], m)
				for _, id := range c.Members {
					delete(s.state.Hidden, id+":"+c.ID)
				}
				c.UpdatedAt = m.CreatedAt
				s.state.Conversations[c.ID] = c
				return 200, m
			}
		}
	}
	if strings.HasPrefix(path, "/attachments") {
		return s.fileRoute(method, path, a, b)
	}
	return failure(404, "route not found")
}
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/im/download/") && r.Method == "GET" {
			s.serveTicket(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			if e != nil || u.Host != r.Host {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(403)
				json.NewEncoder(w).Encode(map[string]string{"error": "cross-origin request rejected"})
				return
			}
		}
		s.mu.Lock()
		network, ui := s.network, s.ui
		s.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/api/im/node/") && network != nil {
			network.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/im/") {
			allowed := r.URL.Path == "/" || r.URL.Path == "/index.html" || r.URL.Path == "/im.js" || r.URL.Path == "/im.css" || r.URL.Path == "/im-archive.js" || r.URL.Path == "/vendor/qrcode.min.js"
			if ui != nil && allowed {
				http.FileServer(http.FS(ui)).ServeHTTP(w, r)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		path := strings.TrimPrefix(r.URL.RequestURI(), "/api/im")
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.HasSuffix(r.URL.Path, "/content") && r.Method == "GET" {
			s.serveContent(w, r, token)
			return
		}
		raw, e := io.ReadAll(io.LimitReader(r.Body, 400001))
		if e != nil || len(raw) > 400000 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(413)
			json.NewEncoder(w).Encode(map[string]string{"error": "body too large"})
			return
		}
		code, v := s.request(r.Method, path, token, raw, false)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	})
}

func validAvatar(v string) bool {
	if v == "" {
		return true
	}
	parts := strings.SplitN(v, ",", 2)
	if len(parts) != 2 {
		return false
	}
	var expected string
	switch parts[0] {
	case "data:image/png;base64":
		expected = "image/png"
	case "data:image/jpeg;base64":
		expected = "image/jpeg"
	case "data:image/webp;base64":
		expected = "image/webp"
	default:
		return false
	}
	b, e := base64.StdEncoding.DecodeString(parts[1])
	return e == nil && len(b) > 0 && http.DetectContentType(b) == expected
}
