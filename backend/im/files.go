package im

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Service) filePath(id string) string { d:=s.fileDir; if d=="" { d=filepath.Join(s.dir,"files") }; return filepath.Join(d, id) }
func (s *Service) fileRoute(method, path string, a account, b input) (int, any) {
	if path == "/attachments" && method == "POST" {
		c, ok := s.state.Conversations[b.ConversationID]
		if !ok || !member(c, a.ID) {
			return failure(404, "conversation not found")
		}
		if b.Size < 0 || len(b.Name) < 1 || len(b.Name) > 255 || strings.ContainsAny(b.Name, "/\\\x00\r\n") || len(b.Mime) > 200 {
			return failure(400, "invalid file")
		}
		id := randomID()
		f, e := os.OpenFile(s.filePath(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return failure(500, "cannot create attachment")
		}
		f.Close()
		v := storedAttachment{Attachment: Attachment{ID: id, Name: b.Name, Size: b.Size, Mime: b.Mime, ConversationID: b.ConversationID}, OwnerID: a.ID}
		s.state.Attachments[id] = v
		return 200, v.Attachment
	}
	parts := strings.Split(strings.TrimPrefix(path, "/attachments/"), "/")
	v, ok := s.state.Attachments[parts[0]]
	c := s.state.Conversations[v.ConversationID]
	if !ok || !member(c, a.ID) {
		return failure(404, "attachment not found")
	}
	if len(parts) == 2 && parts[1] == "download-ticket" && method == "POST" {
		if !v.Finished {
			return failure(409, "upload incomplete")
		}
		if s.downloadTickets == nil {
			s.downloadTickets = map[string]downloadTicket{}
		}
		for key, t := range s.downloadTickets {
			if t.Expires < now() {
				delete(s.downloadTickets, key)
			}
		}
		if len(s.downloadTickets) > 10000 {
			return failure(429, "too many download requests")
		}
		key := randomID()
		s.downloadTickets[key] = downloadTicket{v.ID, a.ID, now() + 60000}
		return 200, map[string]any{"url": "/api/im/download/" + key}
	}
	if len(parts) == 1 && method == "GET" {
		return 200, map[string]any{"id": v.ID, "name": v.Name, "size": v.Size, "mime": v.Mime, "sha256": v.SHA256, "offset": v.Offset, "finished": v.Finished, "downloadUrl": "/api/im/attachments/" + v.ID + "/content"}
	}
	if len(parts) == 2 && parts[1] == "download-chunks" && method == "GET" {
		return failure(400, "use chunks query")
	}
	if len(parts) == 1 && method == "DELETE" {
		if v.OwnerID != a.ID {
			return failure(403, "owner required")
		}
		if v.Finished {
			for _, list := range s.state.Messages {
				for _, m := range list {
					if m.AttachmentID == v.ID {
						return failure(409, "attachment already sent")
					}
				}
			}
		}
		s.pendingDeletes = append(s.pendingDeletes, v.ID)
		delete(s.state.Attachments, v.ID)
		return 200, map[string]any{"ok": true}
	}
	if v.OwnerID != a.ID {
		return failure(403, "owner required")
	}
	if len(parts) == 2 && parts[1] == "chunks" && method == "PUT" {
		if v.Finished {
			return failure(409, "already finished")
		}
		if b.Offset != v.Offset {
			return failure(409, "incorrect chunk offset")
		}
		if len(b.DataBase64) > base64.StdEncoding.EncodedLen(256*1024) {
			return failure(413, "chunk too large")
		}
		data, e := base64.StdEncoding.DecodeString(b.DataBase64)
		if e != nil || len(data) == 0 || len(data) > 256*1024 || v.Offset < 0 || v.Offset > v.Size || int64(len(data)) > v.Size-v.Offset {
			return failure(400, "invalid chunk")
		}
		f, e := os.OpenFile(s.filePath(v.ID), os.O_WRONLY, 0600)
		if e != nil {
			return failure(500, "cannot open attachment")
		}
		n, e := f.WriteAt(data, v.Offset)
		if e == nil {
			e = f.Sync()
		}
		f.Close()
		if e != nil || n != len(data) {
			return failure(500, "cannot write attachment")
		}
		v.Offset += int64(n)
		s.state.Attachments[v.ID] = v
		return 200, map[string]any{"offset": v.Offset}
	}
	if len(parts) == 2 && parts[1] == "finish" && method == "POST" {
		if v.Offset != v.Size {
			return failure(409, "upload incomplete")
		}
		f, e := os.Open(s.filePath(v.ID))
		if e != nil {
			return failure(500, "cannot open attachment")
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil {
			return failure(500, "cannot hash attachment")
		}
		hash := hex.EncodeToString(h.Sum(nil))
		if hash != strings.ToLower(b.SHA256) {
			return failure(400, "hash mismatch")
		}
		v.SHA256 = hash
		v.Finished = true
		s.state.Attachments[v.ID] = v
		return 200, v.Attachment
	}
	return failure(404, "route not found")
}
func (s *Service) downloadChunk(path, token string) (int, any) {
	u, _ := url.ParseRequestURI(path)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 {
		return failure(404, "not found")
	}
	a, ok := s.auth(token)
	if !ok {
		return failure(401, "authentication required")
	}
	v, ok := s.state.Attachments[parts[1]]
	if !ok || !v.Finished || !member(s.state.Conversations[v.ConversationID], a.ID) {
		return failure(404, "attachment not found")
	}
	offset, e := strconv.ParseInt(u.Query().Get("offset"), 10, 64)
	if e != nil || offset < 0 || offset > v.Size {
		return failure(400, "invalid offset")
	}
	length, e := strconv.Atoi(u.Query().Get("length"))
	if e != nil || length < 1 || length > 262144 {
		return failure(400, "invalid length")
	}
	f, e := os.Open(s.filePath(v.ID))
	if e != nil {
		return failure(500, "cannot open attachment")
	}
	defer f.Close()
	buf := make([]byte, length)
	n, e := f.ReadAt(buf, offset)
	if e != nil && e != io.EOF {
		return failure(500, "cannot read attachment")
	}
	return 200, map[string]any{"dataBase64": base64.StdEncoding.EncodeToString(buf[:n]), "length": n, "eof": offset+int64(n) >= v.Size}
}
func (s *Service) serveContent(w http.ResponseWriter, r *http.Request, token string) {
	s.mu.Lock()
	a, ok := s.auth(token)
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/im/attachments/"), "/content")
	v, found := s.state.Attachments[id]
	authorized := ok && found && v.Finished && member(s.state.Conversations[v.ConversationID], a.ID)
	s.mu.Unlock()
	if !authorized {
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(401)
		} else {
			w.WriteHeader(404)
		}
		io.WriteString(w, `{"error":"attachment unavailable"}`)
		return
	}
	s.serveAttachment(w, r, v)
}
func (s *Service) serveAttachment(w http.ResponseWriter, r *http.Request, v storedAttachment) {
	id := v.ID
	f, e := os.Open(s.filePath(id))
	if e != nil {
		http.Error(w, `{"error":"attachment unavailable"}`, 404)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": v.Name}))
	http.ServeContent(w, r, v.Name, time.Time{}, f)
}
