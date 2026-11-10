package im

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"regexp"
	"strings"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,40}$`)

func passwordHash(password, salt string) string {
	b, _ := pbkdf2.Key(sha256.New, password, []byte(salt), 120000, 32)
	return hex.EncodeToString(b)
}
func (s *Service) auth(token string) (account, bool) {
	v, ok := s.state.Sessions[token]
	if !ok || v.Expires < now() {
		return account{}, false
	}
	u, ok := s.state.Users[v.UserID]
	return u, ok && !u.Disabled && u.Hash == ""
}
func (s *Service) revoke(id string) {
	for t, v := range s.state.Sessions {
		if v.UserID == id {
			delete(s.state.Sessions, t)
		}
	}
}
func (s *Service) session(u account) map[string]any {
	tok := randomID()
	s.state.Sessions[tok] = session{ID: randomID(), CreatedAt: now(), UserID: u.ID, Expires: now() + 7*24*3600*1000}
	return map[string]any{"token": tok, "user": u.User}
}
func (s *Service) accountRoute(method, path, token string, b input, local bool) (int, any, bool) {
	finish := func(c int, v any) (int, any, bool) { return c, v, true }
	bad := func(c int, m string) (int, any, bool) { return finish(c, map[string]any{"error": m}) }
	if method == "GET" && path == "/status" {
		return finish(200, map[string]any{"initialized": len(s.state.Users) > 0})
	}
	if method == "POST" && (path == "/setup" || path == "/register" || path == "/login") {
		if path == "/setup" {
			if !local {
				return bad(403, "setup is only available locally")
			}
			if len(s.state.Users) > 0 {
				return bad(409, "already initialized")
			}
		}
		name := strings.ToLower(strings.TrimSpace(b.Username))
		fresh := s.credentialAttempts[:0]
		for _, stamp := range s.credentialAttempts {
			if stamp > now()-60000 {
				fresh = append(fresh, stamp)
			}
		}
		s.credentialAttempts = fresh
		if len(fresh) >= 60 {
			return bad(429, "try again later")
		}
		s.credentialAttempts = append(s.credentialAttempts, now())
		if !usernamePattern.MatchString(name) || len(b.Password) > 1024 {
			return bad(400, "invalid credentials format")
		}
		if len(s.attempts) > 10000 {
			for k, v := range s.attempts {
				if len(v) == 0 || v[len(v)-1] < now()-60000 {
					delete(s.attempts, k)
				}
			}
			if len(s.attempts) > 10000 {
				return bad(429, "try again later")
			}
		}
		key := name
		attempts := s.attempts[key][:0]
		for _, v := range s.attempts[key] {
			if v > now()-60000 {
				attempts = append(attempts, v)
			}
		}
		s.attempts[key] = attempts
		if len(attempts) >= 10 {
			return bad(429, "try again later")
		}
		s.attempts[key] = append(attempts, now())
		var u account
		found := false
		for _, a := range s.state.Users {
			if a.Username == name {
				u = a
				found = true
				break
			}
		}
		if path == "/login" {
			hash := passwordHash(b.Password, u.Salt)
			if !found || u.Disabled || subtle.ConstantTimeCompare([]byte(hash), []byte(u.Hash)) != 1 {
				return bad(401, "invalid credentials")
			}
			u.LastSeen = now()
			s.state.Users[u.ID] = u
			return finish(200, s.session(u))
		}
		if !usernamePattern.MatchString(name) || len(b.Password) < 8 || len(b.Password) > 1024 || len(b.Nickname) > 100 {
			return bad(400, "invalid username, password or nickname")
		}
		if found {
			return bad(409, "username already exists")
		}
		if path == "/register" {
			v, ok := s.state.Invites[b.Invite]
			if !ok || v.Uses <= 0 || v.Expires < now() {
				return bad(400, "invalid or expired invitation")
			}
			v.Uses--
			s.state.Invites[b.Invite] = v
		}
		salt := randomID()
		u = account{User: User{ID: randomID(), Username: name, Nickname: b.Nickname, Admin: path == "/setup", LastSeen: now()}, Salt: salt, Hash: passwordHash(b.Password, salt)}
		if u.Nickname == "" {
			u.Nickname = name
		}
		s.state.Users[u.ID] = u
		return finish(200, s.session(u))
	}
	return 0, nil, false
}
