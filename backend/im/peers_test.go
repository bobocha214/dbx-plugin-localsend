package im

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPeerPersistenceFailureRetry(t *testing.T) {
	a, _ := NewPeers(t.TempDir())
	defer a.Close()
	dir := t.TempDir()
	b, _ := NewPeers(dir)
	defer b.Close()
	b.remember(a.ID(), a.publicKey(), "A", "http://127.0.0.1:1234")
	b.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+a.ID()+`"}`))
	blocker := filepath.Join(dir, "peers.json.tmp")
	if e := os.Mkdir(blocker, 0700); e != nil {
		t.Fatal(e)
	}
	env := a.envelope(b.ID(), "retry", "persist me")
	if code := b.receive(env); code != 500 {
		t.Fatal("save failure", code)
	}
	if len(b.state.Messages[a.ID()]) != 0 || len(b.state.Seen) != 0 {
		t.Fatal("failed save mutated live state")
	}
	os.Remove(blocker)
	if code := b.receive(env); code != 200 {
		t.Fatal("retry failed", code)
	}
	c, e := NewPeers(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if len(c.state.Messages[a.ID()]) != 1 {
		t.Fatal("ack not durable")
	}
}

func TestPeerFailedCreateCleanupAndFinishCancel(t *testing.T) {
	a, _ := NewPeers(t.TempDir())
	defer a.Close()
	b, _ := NewPeers(t.TempDir())
	defer b.Close()
	srv := httptest.NewServer(b.Handler())
	defer srv.Close()
	a.remember(b.ID(), b.publicKey(), "B", srv.URL)
	b.remember(a.ID(), a.publicKey(), "A", "http://127.0.0.1:1234")
	a.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+b.ID()+`"}`))
	prefix := "/peers/" + b.ID() + "/attachments"
	body := json.RawMessage(`{"name":"empty.txt","mime":"text/plain","size":0}`)
	if code, _ := a.Request("POST", prefix, body); code != 502 {
		t.Fatal("untrusted remote create", code)
	}
	entries, _ := os.ReadDir(filepath.Join(a.dir, "peer-files", b.ID()))
	if len(entries) != 0 {
		t.Fatal("failed create left files", len(entries))
	}
	b.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+a.ID()+`"}`))
	code, result := a.Request("POST", prefix, body)
	if code != 200 {
		t.Fatal(code, result)
	}
	meta := result.(map[string]any)["attachment"].(peerFile)
	b.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+a.ID()+`","trusted":false}`))
	hash := fmt.Sprintf("%x", sha256.Sum256(nil))
	path := prefix + "/" + meta.ID
	if code, _ = a.Request("POST", path+"/finish", json.RawMessage(`{"sha256":"`+hash+`"}`)); code != 502 {
		t.Fatal("failed finish", code)
	}
	b.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+a.ID()+`"}`))
	if _, e := b.finishPeerFile(a.ID(), meta.ID, hash); e != nil {
		t.Fatal(e)
	}
	if code, _ = a.Request("DELETE", path, nil); code != 200 {
		t.Fatal("cannot cancel completed undelivered", code)
	}
	if _, e := b.fileMeta(a.ID(), meta.ID); !os.IsNotExist(e) {
		t.Fatal("remote completed unreferenced file retained", e)
	}
}

func TestPeerLargeMetadataAndSignedNegativeSize(t *testing.T) {
	a, _ := NewPeers(t.TempDir())
	defer a.Close()
	b, _ := NewPeers(t.TempDir())
	defer b.Close()
	srv := httptest.NewServer(b.Handler())
	defer srv.Close()
	a.remember(b.ID(), b.publicKey(), "B", srv.URL)
	b.remember(a.ID(), a.publicKey(), "A", "http://127.0.0.1:1234")
	a.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+b.ID()+`"}`))
	b.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+a.ID()+`"}`))
	code, v := a.Request("POST", "/peers/"+b.ID()+"/attachments", json.RawMessage(`{"name":"large.bin","size":8589934592,"mime":"application/octet-stream"}`))
	if code != 200 {
		t.Fatal(code, v)
	}
	f := v.(map[string]any)["attachment"].(peerFile)
	for _, pair := range []struct {
		p    *Peers
		peer string
	}{{a, b.ID()}, {b, a.ID()}} {
		st, e := os.Stat(pair.p.peerFilePath(pair.peer, f.ID))
		if e != nil || st.Size() != 0 {
			t.Fatal("metadata allocated payload", e)
		}
	}
	op := peerFileOp{Op: "create", File: peerFile{ID: "012345678901234567890123456789012345678901234567", Name: "negative.bin", Size: -1, Mime: "application/octet-stream"}}
	data, _ := json.Marshal(op)
	env := a.envelope(b.ID(), op.File.ID, string(data))
	env.Kind = "file"
	env.Signature = ""
	env.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(a.key, peerSignData(env)))
	if code = b.receive(env); code != 400 {
		t.Fatal("signed negative size accepted", code)
	}
	if _, e := a.putPeerChunk(b.ID(), f.ID, int64(^uint64(0)>>1), "YQ=="); e == nil {
		t.Fatal("overflowing offset accepted")
	}
}

func TestPeerFilesSignedTransfer(t *testing.T) {
	a, _ := NewPeers(t.TempDir())
	defer a.Close()
	b, _ := NewPeers(t.TempDir())
	defer b.Close()
	srv := httptest.NewServer(b.Handler())
	defer srv.Close()
	a.remember(b.ID(), b.publicKey(), "B", srv.URL)
	b.remember(a.ID(), a.publicKey(), "A", "http://127.0.0.1:1234")
	a.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+b.ID()+`"}`))
	b.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+a.ID()+`"}`))
	prefix := "/peers/" + b.ID() + "/attachments"
	code, result := a.Request("POST", prefix, json.RawMessage(`{"name":"test.txt","size":5,"mime":"text/plain"}`))
	if code != 200 {
		t.Fatal(code, result)
	}
	meta := result.(map[string]any)["attachment"].(peerFile)
	path := prefix + "/" + meta.ID
	if code, _ = a.Request("PUT", path+"/chunks", json.RawMessage(`{"offset":1,"dataBase64":"aGVsbG8="}`)); code != 409 {
		t.Fatal("bad offset", code)
	}
	if code, _ = a.Request("PUT", path+"/chunks", json.RawMessage(`{"offset":0,"dataBase64":"aGVsbG8="}`)); code != 200 {
		t.Fatal(code)
	}
	if code, _ = a.Request("PUT", path+"/chunks", json.RawMessage(`{"offset":0,"dataBase64":"aGVsbG8="}`)); code != 200 {
		t.Fatal("chunk retry", code)
	}
	if code, _ = a.Request("POST", path+"/finish", json.RawMessage(`{"sha256":"0000000000000000000000000000000000000000000000000000000000000000"}`)); code != 400 {
		t.Fatal("wrong hash accepted", code)
	}
	op := peerFileOp{Op: "chunk", File: peerFile{ID: meta.ID}, Data: "d3Jvbmc=", Offset: 0}
	raw, _ := json.Marshal(op)
	bad := a.envelope(b.ID(), meta.ID, string(raw))
	bad.Kind = "file"
	if code = b.receive(bad); code != 401 {
		t.Fatal("unsigned kind accepted", code)
	}
	if code, _ = b.Request("GET", "/peers/"+a.ID()+"/attachments/../../peers.json", nil); code == 200 {
		t.Fatal("path traversal")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("hello")))
	// Local checksum completion alone cannot authorize an outgoing message.
	if _, e := a.finishPeerFile(b.ID(), meta.ID, hash); e != nil {
		t.Fatal(e)
	}
	if code, _ = a.Request("POST", "/peers/"+b.ID()+"/messages", json.RawMessage(`{"id":"premature","attachmentId":"`+meta.ID+`"}`)); code != 400 {
		t.Fatal("undelivered attachment accepted", code)
	}
	if code, result = a.Request("POST", path+"/finish", json.RawMessage(`{"sha256":"`+hash+`"}`)); code != 200 {
		t.Fatal(code, result)
	}
	if code, result = a.Request("POST", path+"/finish", json.RawMessage(`{"sha256":"`+hash+`"}`)); code != 200 {
		t.Fatal("finish retry", code, result)
	}
	code, result = b.Request("GET", "/peers/"+a.ID()+"/attachments/"+meta.ID+"/chunks?offset=0", nil)
	if code != 200 || result.(map[string]any)["dataBase64"] != "aGVsbG8=" {
		t.Fatal(code, result)
	}
	code, result = a.Request("POST", "/peers/"+b.ID()+"/messages", json.RawMessage(`{"id":"file-message","attachmentId":"`+meta.ID+`"}`))
	if code != 200 {
		t.Fatal(code, result)
	}
}

func TestPeerTrustRevocationAndSpoofProof(t *testing.T) {
	a, _ := NewPeers(t.TempDir())
	defer a.Close()
	b, _ := NewPeers(t.TempDir())
	defer b.Close()
	var leaked bool
	spoof := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/im/node/info" {
			leaked = true
		}
		io.WriteString(w, `{"proof":"ZmFrZQ=="}`)
	}))
	defer spoof.Close()
	a.remember(b.ID(), b.publicKey(), "B", spoof.URL)
	b.remember(a.ID(), a.publicKey(), "A", "http://127.0.0.1:1234")
	a.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+b.ID()+`"}`))
	b.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+a.ID()+`"}`))
	if code, _ := a.Request("POST", "/peers/"+b.ID()+"/messages", json.RawMessage(`{"id":"secret","text":"private"}`)); code != 502 || leaked {
		t.Fatal("spoof received content", code, leaked)
	}
	b.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+a.ID()+`","trusted":false}`))
	e := a.envelope(b.ID(), "file-id", "{}")
	e.Kind = "file"
	e.Signature = ""
	e.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(a.key, peerSignData(e)))
	if code := b.receive(e); code != 403 {
		t.Fatal("revoked file accepted", code)
	}
	if code, _ := b.Request("GET", "/peers/"+a.ID()+"/attachments/012345678901234567890123456789012345678901234567", nil); code != 403 {
		t.Fatal("revoked file download", code)
	}
}

func TestPeerIdentityRestart(t *testing.T) {
	dir := t.TempDir()
	p, e := NewPeers(dir)
	if e != nil {
		t.Fatal(e)
	}
	id := p.ID()
	p.Close()
	q, e := NewPeers(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	if q.ID() != id {
		t.Fatal("identity changed")
	}
}

func TestPeerSignedTrustTamperReplay(t *testing.T) {
	a, _ := NewPeers(t.TempDir())
	defer a.Close()
	b, _ := NewPeers(t.TempDir())
	defer b.Close()
	srv := httptest.NewServer(b.Handler())
	defer srv.Close()
	a.remember(b.ID(), b.publicKey(), "B", srv.URL)
	b.remember(a.ID(), a.publicKey(), "A", "http://127.0.0.1:1234")
	a.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+b.ID()+`"}`))
	env := a.envelope(b.ID(), "hello", "hello")
	if code := b.receive(env); code != 403 {
		t.Fatalf("untrusted: %d", code)
	}
	b.Request("POST", "/peers/trust", json.RawMessage(`{"id":"`+a.ID()+`"}`))
	bad := env
	bad.Text = "tampered"
	if b.receive(bad) != 401 {
		t.Fatal("tamper accepted")
	}
	if b.receive(env) != 200 {
		t.Fatal("valid rejected")
	}
	if b.receive(env) != 409 {
		t.Fatal("replay accepted")
	}
	dir := b.dir
	b.Close()
	c, e := NewPeers(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if c.receive(env) != 409 {
		t.Fatal("restart replay accepted")
	}
	code, body := a.Request("POST", "/peers/"+b.ID()+"/messages", json.RawMessage(`{"id":"next","text":"world"}`))
	if code != 200 {
		t.Fatalf("send %d %v", code, body)
	}
}
