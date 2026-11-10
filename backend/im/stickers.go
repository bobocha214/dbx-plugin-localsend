package im

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Sticker bytes are owned by the collector, independent of their source conversation.
func (s *Service) stickerRoute(method string, u *url.URL, a account, b input) (int, any) {
	if s.state.Stickers == nil {
		s.state.Stickers = map[string]storedAttachment{}
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/stickers"), "/")
	if u.Path == "/stickers" {
		if method == "GET" {
			list := []Attachment{}
			for _, f := range s.state.Stickers {
				if f.OwnerID == a.ID && f.Finished {
					list = append(list, f.Attachment)
				}
			}
			return 200, map[string]any{"stickers": list}
		}
		if method == "POST" {
			if b.Size < 1 || b.Size > 16<<20 || !strings.Contains("|image/png|image/jpeg|image/gif|image/webp|", "|"+b.Mime+"|") || b.Mime == "" || len(b.Name) > 255 {
				return failure(400, "invalid sticker image")
			}
			id := randomID()
			f, e := os.OpenFile(s.filePath(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return failure(500, "cannot create sticker")
			}
			f.Close()
			v := storedAttachment{Attachment: Attachment{ID: id, Name: b.Name, Size: b.Size, Mime: b.Mime}, OwnerID: a.ID}
			s.state.Stickers[id] = v
			return 200, v.Attachment
		}
	}
	if len(parts) < 2 {
		return failure(404, "sticker not found")
	}
	v, ok := s.state.Stickers[parts[1]]
	if !ok || v.OwnerID != a.ID {
		return failure(404, "sticker not found")
	}
	if len(parts) == 2 {
		if method == "GET" {
			return 200, v.Attachment
		}
		if method == "DELETE" {
			delete(s.state.Stickers, v.ID)
			s.pendingDeletes = append(s.pendingDeletes, v.ID)
			return 200, map[string]bool{"ok": true}
		}
	}
	if len(parts) == 3 && parts[2] == "chunks" {
		if method == "PUT" {
			data, e := base64.StdEncoding.DecodeString(b.DataBase64)
			if e != nil || len(data) == 0 || len(data) > 262144 || v.Finished || b.Offset != v.Offset || int64(len(data)) > v.Size-v.Offset {
				return failure(409, "invalid sticker offset")
			}
			f, e := os.OpenFile(s.filePath(v.ID), os.O_WRONLY, 0600)
			if e != nil {
				return failure(500, "cannot write sticker")
			}
			n, e := f.WriteAt(data, v.Offset)
			if e == nil {
				e = f.Sync()
			}
			f.Close()
			if e != nil {
				return failure(500, "cannot write sticker")
			}
			v.Offset += int64(n)
			s.state.Stickers[v.ID] = v
			return 200, map[string]any{"offset": v.Offset}
		}
		if method == "GET" && v.Finished {
			offset, e := strconv.ParseInt(u.Query().Get("offset"), 10, 64)
			length, e2 := strconv.Atoi(u.Query().Get("length"))
			if e != nil || e2 != nil || offset < 0 || offset > v.Size || length < 1 || length > 262144 {
				return failure(400, "invalid range")
			}
			f, e := os.Open(s.filePath(v.ID))
			if e != nil {
				return failure(500, "cannot read sticker")
			}
			defer f.Close()
			buf := make([]byte, length)
			n, e := f.ReadAt(buf, offset)
			if e != nil && e != io.EOF {
				return failure(500, "cannot read sticker")
			}
			return 200, map[string]any{"dataBase64": base64.StdEncoding.EncodeToString(buf[:n])}
		}
	}
	if len(parts) == 3 && parts[2] == "finish" && method == "POST" {
		if v.Offset != v.Size {
			return failure(409, "incomplete sticker")
		}
		f, e := os.Open(s.filePath(v.ID))
		if e != nil {
			return failure(500, "cannot read sticker")
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil {
			return failure(500, "cannot hash sticker")
		}
		v.SHA256 = hex.EncodeToString(h.Sum(nil))
		v.Finished = true
		s.state.Stickers[v.ID] = v
		return 200, v.Attachment
	}
	return failure(404, "sticker route not found")
}
