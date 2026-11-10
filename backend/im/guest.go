package im

import (
	"encoding/json"
	"net/http"
	"strings"
)

type downloadTicket struct {
	AttachmentID, UserID string
	Expires              int64
}

func (s *Service) guest(b input, local bool) (int, any) {
	if len(b.Nickname) > 100 {
		return failure(400, "nickname too long")
	}
	if local && s.state.GuestHostID != "" {
		u, ok := s.state.Users[s.state.GuestHostID]
		if ok {
			if b.Nickname != "" {
				u.Nickname = b.Nickname
				s.state.Users[u.ID] = u
			}
			if _, ok := s.auth(s.state.GuestHostToken); ok {
				return 200, map[string]any{"token": s.state.GuestHostToken, "user": u.User}
			}
			r := s.session(u)
			s.state.GuestHostToken = r["token"].(string)
			return 200, r
		}
	}
	fresh := s.credentialAttempts[:0]
	for _, v := range s.credentialAttempts {
		if v > now()-60000 {
			fresh = append(fresh, v)
		}
	}
	s.credentialAttempts = fresh
	if len(fresh) >= 120 {
		return failure(429, "too many visitors, try again shortly")
	}
	s.credentialAttempts = append(fresh, now())
	id := randomID()
	name := strings.TrimSpace(b.Nickname)
	if name == "" {
		if local {
			name = "本机"
		} else {
			name = "访客-" + id[:6]
		}
	}
	u := account{User: User{ID: id, Username: "guest-" + id[:12], Nickname: name, Admin: local, LastSeen: now()}}
	s.state.Users[id] = u
	c, ok := s.state.Conversations["lan-lobby"]
	if !ok {
		c = Conversation{ID: "lan-lobby", Name: "局域网大厅", Kind: "group", Members: []string{}, UpdatedAt: now()}
	}
	c.Members = append(c.Members, id)
	s.state.Conversations[c.ID] = c
	r := s.session(u)
	if local {
		s.state.GuestHostID = id
		s.state.GuestHostToken = r["token"].(string)
	}
	return 200, r
}
func (s *Service) serveTicket(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/im/download/")
	s.mu.Lock()
	t, ok := s.downloadTickets[id]
	delete(s.downloadTickets, id)
	u, found := s.state.Users[t.UserID]
	v, exists := s.state.Attachments[t.AttachmentID]
	authorized := ok && t.Expires > now() && found && !u.Disabled && exists && v.Finished && member(s.state.Conversations[v.ConversationID], u.ID)
	s.mu.Unlock()
	if !authorized {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "download link expired or already used"})
		return
	}
	s.serveAttachment(w, r, v)
}
