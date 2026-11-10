package im

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const peerChunkLimit = 256 * 1024

type peerFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Mime      string `json:"mime"`
	SHA256    string `json:"sha256"`
	Complete  bool   `json:"complete"`
	Delivered bool   `json:"delivered"`
}
type peerFileOp struct {
	Op     string   `json:"op"`
	File   peerFile `json:"file"`
	Offset int64    `json:"offset"`
	Data   string   `json:"dataBase64"`
}

func safeFileID(id string) bool { b, e := hex.DecodeString(id); return e == nil && len(b) == 24 }
func (p *Peers) peerFilePath(peer, id string) string {
	return filepath.Join(p.dir, "peer-files", peer, id)
}
func (p *Peers) fileMeta(peer, id string) (peerFile, error) {
	var f peerFile
	if !safeFileID(id) || len(peer) != 64 {
		return f, fmt.Errorf("invalid file id")
	}
	b, e := os.ReadFile(p.peerFilePath(peer, id) + ".json")
	if e == nil {
		e = json.Unmarshal(b, &f)
	}
	return f, e
}
func (p *Peers) writeFileMeta(peer string, f peerFile) error {
	path := p.peerFilePath(peer, f.ID) + ".json"
	b, e := json.Marshal(f)
	if e != nil {
		return e
	}
	if e = os.WriteFile(path+".tmp", b, 0600); e != nil {
		return e
	}
	return os.Rename(path+".tmp", path)
}
func (p *Peers) fileReady(peer, id string) bool {
	f, e := p.fileMeta(peer, id)
	return e == nil && f.Complete
}
func (p *Peers) fileDelivered(peer, id string) bool {
	f, e := p.fileMeta(peer, id)
	return e == nil && f.Complete && f.Delivered
}
func (p *Peers) createPeerFile(peer string, f peerFile) (peerFile, error) {
	if !safeFileID(f.ID) || f.Size < 0 || len(f.Name) == 0 || len(f.Name) > 255 || strings.ContainsAny(f.Name, "/\\\x00") || len(f.Mime) > 255 {
		return f, fmt.Errorf("invalid file")
	}
	if _, _, e := mime.ParseMediaType(f.Mime); e != nil {
		return f, fmt.Errorf("invalid mime")
	}
	if old, e := p.fileMeta(peer, f.ID); e == nil {
		if old.Name != f.Name || old.Size != f.Size || old.Mime != f.Mime {
			return f, fmt.Errorf("file conflict")
		}
		return old, nil
	}
	path := p.peerFilePath(peer, f.ID)
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return f, e
	}
	handle, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return f, e
	}
	handle.Close()
	f.Complete = false
	f.Delivered = false
	f.SHA256 = ""
	return f, p.writeFileMeta(peer, f)
}
func (p *Peers) putPeerChunk(peer, id string, offset int64, data string) (int64, error) {
	f, e := p.fileMeta(peer, id)
	if e != nil {
		return 0, e
	}
	if len(data) > base64.StdEncoding.EncodedLen(peerChunkLimit) {
		return 0, fmt.Errorf("chunk too large")
	}
	b, e := base64.StdEncoding.DecodeString(data)
	if e != nil || len(b) > peerChunkLimit {
		return 0, fmt.Errorf("invalid chunk")
	}
	h, e := os.OpenFile(p.peerFilePath(peer, id), os.O_RDWR, 0600)
	if e != nil {
		return 0, e
	}
	defer h.Close()
	st, e := h.Stat()
	if e != nil {
		return 0, e
	}
	if offset < 0 || offset > f.Size-int64(len(b)) {
		return 0, fmt.Errorf("invalid offset")
	}
	if st.Size() != offset || f.Complete {
		if offset+int64(len(b)) <= st.Size() {
			old := make([]byte, len(b))
			h.ReadAt(old, offset)
			if string(old) == string(b) {
				return offset + int64(len(b)), nil
			}
		}
		return 0, fmt.Errorf("offset conflict")
	}
	if _, e = h.WriteAt(b, offset); e != nil {
		return 0, e
	}
	if e = h.Sync(); e != nil {
		return 0, e
	}
	return offset + int64(len(b)), nil
}
func (p *Peers) finishPeerFile(peer, id, hash string) (peerFile, error) {
	f, e := p.fileMeta(peer, id)
	if e != nil {
		return f, e
	}
	if len(hash) != 64 {
		return f, fmt.Errorf("invalid hash")
	}
	h, e := os.Open(p.peerFilePath(peer, id))
	if e != nil {
		return f, e
	}
	defer h.Close()
	digest := sha256.New()
	n, e := io.Copy(digest, h)
	if e != nil || n != f.Size || hex.EncodeToString(digest.Sum(nil)) != hash {
		return f, fmt.Errorf("file hash or size mismatch")
	}
	f.SHA256 = hash
	f.Complete = true
	return f, p.writeFileMeta(peer, f)
}
func (p *Peers) receiveFile(e peerEnvelope) int {
	var op peerFileOp
	if json.Unmarshal([]byte(e.Text), &op) != nil || op.File.ID != e.ID {
		return 400
	}
	var err error
	switch op.Op {
	case "create":
		_, err = p.createPeerFile(e.Sender, op.File)
	case "chunk":
		_, err = p.putPeerChunk(e.Sender, op.File.ID, op.Offset, op.Data)
	case "finish":
		_, err = p.finishPeerFile(e.Sender, op.File.ID, op.File.SHA256)
	case "delete":
		_, e2 := p.fileMeta(e.Sender, op.File.ID)
		if os.IsNotExist(e2) {
			return 200
		}
		if e2 != nil || p.fileReferenced(e.Sender, op.File.ID) {
			return 409
		}
		base := p.peerFilePath(e.Sender, op.File.ID)
		err = os.Remove(base)
		if err == nil {
			err = os.Remove(base + ".json")
		}
	default:
		return 400
	}
	if err != nil {
		return 400
	}
	return 200
}
func (p *Peers) fileReferenced(peer, id string) bool {
	for _, m := range p.state.Messages[peer] {
		if m.AttachmentID == id {
			return true
		}
	}
	return false
}
func (p *Peers) sendFileOp(peer string, op peerFileOp) error {
	data, _ := json.Marshal(op)
	e := p.envelope(peer, op.File.ID, string(data))
	e.Kind = "file"
	return p.sendEnvelope(peer, e)
}
func (p *Peers) fileRequest(method, path string, body json.RawMessage) (int, any) {
	u, e := url.Parse(path)
	if e != nil {
		return peerError(400, "invalid path")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || len(parts) > 5 || parts[0] != "peers" || parts[2] != "attachments" {
		return peerError(404, "not found")
	}
	peer := parts[1]
	p.mu.Lock()
	q := p.state.Peers[peer]
	if q == nil || !q.Trusted {
		p.mu.Unlock()
		return peerError(403, "peer not trusted")
	}
	if len(parts) == 3 && method == "POST" {
		var f peerFile
		if json.Unmarshal(body, &f) != nil {
			p.mu.Unlock()
			return peerError(400, "invalid file")
		}
		b := make([]byte, 24)
		rand.Read(b)
		f.ID = hex.EncodeToString(b)
		f, e = p.createPeerFile(peer, f)
		p.mu.Unlock()
		if e != nil {
			return peerError(400, e.Error())
		}
		if e = p.sendFileOp(peer, peerFileOp{Op: "create", File: f}); e != nil {
			p.mu.Lock()
			base := p.peerFilePath(peer, f.ID)
			os.Remove(base)
			os.Remove(base + ".json")
			p.mu.Unlock()
			return peerError(502, e.Error())
		}
		return 200, map[string]any{"attachment": f}
	}
	if len(parts) < 4 {
		p.mu.Unlock()
		return peerError(405, "method not allowed")
	}
	id := parts[3]
	f, e := p.fileMeta(peer, id)
	if e != nil {
		p.mu.Unlock()
		return peerError(404, "attachment not found")
	}
	if len(parts) == 4 && method == "GET" {
		p.mu.Unlock()
		return 200, map[string]any{"attachment": f}
	}
	if len(parts) == 4 && method == "DELETE" {
		if f.Delivered || p.fileReferenced(peer, id) {
			p.mu.Unlock()
			return peerError(409, "completed attachment cannot be removed")
		}
		base := p.peerFilePath(peer, id)
		os.Remove(base)
		os.Remove(base + ".json")
		p.mu.Unlock()
		_ = p.sendFileOp(peer, peerFileOp{Op: "delete", File: peerFile{ID: id}})
		return 200, map[string]bool{"deleted": true}
	}
	if len(parts) == 5 && parts[4] == "chunks" && method == "PUT" {
		var v struct {
			Offset int64  `json:"offset"`
			Data   string `json:"dataBase64"`
		}
		if len(body) > 360000 || json.Unmarshal(body, &v) != nil {
			p.mu.Unlock()
			return peerError(400, "invalid chunk")
		}
		offset, e := p.putPeerChunk(peer, id, v.Offset, v.Data)
		p.mu.Unlock()
		if e != nil {
			return peerError(409, e.Error())
		}
		if e = p.sendFileOp(peer, peerFileOp{Op: "chunk", File: peerFile{ID: id}, Offset: v.Offset, Data: v.Data}); e != nil {
			return peerError(502, e.Error())
		}
		return 200, map[string]any{"offset": offset}
	}
	if len(parts) == 5 && parts[4] == "chunks" && method == "GET" {
		offset, e := strconv.ParseInt(u.Query().Get("offset"), 10, 64)
		if u.Query().Get("offset") == "" {
			offset = 0
			e = nil
		}
		if e != nil || offset < 0 || offset > f.Size || !f.Complete {
			p.mu.Unlock()
			return peerError(400, "invalid download offset")
		}
		h, e := os.Open(p.peerFilePath(peer, id))
		if e != nil {
			p.mu.Unlock()
			return peerError(500, "file unavailable")
		}
		b := make([]byte, peerChunkLimit)
		n, readErr := h.ReadAt(b, offset)
		h.Close()
		p.mu.Unlock()
		if readErr != nil && readErr != io.EOF {
			return peerError(500, "read failed")
		}
		return 200, map[string]any{"dataBase64": base64.StdEncoding.EncodeToString(b[:n]), "length": n, "eof": offset+int64(n) >= f.Size}
	}
	if len(parts) == 5 && parts[4] == "finish" && method == "POST" {
		var v struct {
			SHA256 string `json:"sha256"`
		}
		if json.Unmarshal(body, &v) != nil {
			p.mu.Unlock()
			return peerError(400, "invalid hash")
		}
		f, e = p.finishPeerFile(peer, id, v.SHA256)
		p.mu.Unlock()
		if e != nil {
			return peerError(400, e.Error())
		}
		if e = p.sendFileOp(peer, peerFileOp{Op: "finish", File: f}); e != nil {
			return peerError(502, e.Error())
		}
		p.mu.Lock()
		f.Delivered = true
		e = p.writeFileMeta(peer, f)
		p.mu.Unlock()
		if e != nil {
			return peerError(500, "cannot persist delivery receipt")
		}
		return 200, map[string]any{"attachment": f}
	}
	p.mu.Unlock()
	return peerError(405, "method not allowed")
}
