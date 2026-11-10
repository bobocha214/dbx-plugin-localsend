package im

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Peer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Key      string `json:"key"`
	Trusted  bool   `json:"trusted"`
	LastSeen int64  `json:"lastSeen"`
}
type PeerMessage struct {
	ID           string `json:"id"`
	SenderID     string `json:"senderId"`
	Text         string `json:"text"`
	CreatedAt    int64  `json:"createdAt"`
	AttachmentID string `json:"attachmentId,omitempty"`
}
type peerDisk struct {
	Private  string                   `json:"private"`
	Peers    map[string]*Peer         `json:"peers"`
	Messages map[string][]PeerMessage `json:"messages"`
	Seen     map[string]int64         `json:"seen"`
}
type peerEnvelope struct {
	Kind         string `json:"kind,omitempty"`
	Recipient    string `json:"recipient"`
	Sender       string `json:"sender"`
	ID           string `json:"id"`
	Text         string `json:"text"`
	AttachmentID string `json:"attachmentId,omitempty"`
	Timestamp    int64  `json:"timestamp"`
	Nonce        string `json:"nonce"`
	Signature    string `json:"signature"`
}
type peerAdvertisement struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
}
type Peers struct {
	mu    sync.Mutex
	dir   string
	key   ed25519.PrivateKey
	state peerDisk
	name  string
	conn  *net.UDPConn
	stop  chan struct{}
	wg    sync.WaitGroup
}

func NewPeers(dir string) (*Peers, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	p := &Peers{dir: dir, name: "LAN node"}
	p.state = peerDisk{Peers: map[string]*Peer{}, Messages: map[string][]PeerMessage{}, Seen: map[string]int64{}}
	data, err := os.ReadFile(filepath.Join(dir, "peers.json"))
	if err == nil {
		if err = json.Unmarshal(data, &p.state); err != nil {
			return nil, err
		}
		p.key, err = base64.StdEncoding.DecodeString(p.state.Private)
		if err != nil || len(p.key) != ed25519.PrivateKeySize {
			return nil, errors.New("invalid node key")
		}
		if !bytes.Equal(ed25519.NewKeyFromSeed(p.key[:32]), p.key) {
			return nil, errors.New("invalid node key")
		}
	} else if os.IsNotExist(err) {
		_, p.key, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		p.state.Private = base64.StdEncoding.EncodeToString(p.key)
	} else {
		return nil, err
	}
	if p.state.Peers == nil {
		p.state.Peers = map[string]*Peer{}
	}
	if p.state.Messages == nil {
		p.state.Messages = map[string][]PeerMessage{}
	}
	if p.state.Seen == nil {
		p.state.Seen = map[string]int64{}
	}
	if err = p.save(); err != nil {
		return nil, err
	}
	return p, nil
}
func (p *Peers) ID() string {
	sum := sha256.Sum256(p.key.Public().(ed25519.PublicKey))
	return hex.EncodeToString(sum[:])
}
func (p *Peers) publicKey() string {
	return base64.StdEncoding.EncodeToString(p.key.Public().(ed25519.PublicKey))
}
func (p *Peers) SetName(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(name) > 128 {
		name = name[:128]
	}
	p.name = name
}
func (p *Peers) save() error {
	data, err := json.Marshal(p.state)
	if err != nil {
		return err
	}
	tmp := filepath.Join(p.dir, "peers.json.tmp")
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(p.dir, "peers.json"))
}
func peerSignData(v any) []byte { b, _ := json.Marshal(v); return b }
func (p *Peers) envelope(recipient, id, text string) peerEnvelope {
	n := make([]byte, 24)
	rand.Read(n)
	e := peerEnvelope{Sender: p.ID(), ID: id, Text: text, Timestamp: time.Now().Unix(), Nonce: hex.EncodeToString(n)}
	e.Recipient = recipient
	e.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(p.key, peerSignData(e)))
	return e
}
func validPeerKey(id, key string) bool {
	k, e := base64.StdEncoding.DecodeString(key)
	if e != nil || len(k) != 32 {
		return false
	}
	s := sha256.Sum256(k)
	return hex.EncodeToString(s[:]) == id
}
func (p *Peers) remember(id, key, name, address string) {
	if id == p.ID() || !validPeerKey(id, key) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	old := p.state.Peers[id]
	if old != nil && old.Key != key {
		return
	}
	trusted := old != nil && old.Trusted
	p.state.Peers[id] = &Peer{id, name, address, key, trusted, time.Now().UnixMilli()}
	p.save()
}
func (p *Peers) receive(e peerEnvelope) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.Recipient != p.ID() {
		return 401
	}
	peer := p.state.Peers[e.Sender]
	if peer == nil || !peer.Trusted {
		return 403
	}
	sig, err := base64.StdEncoding.DecodeString(e.Signature)
	if err != nil {
		return 401
	}
	key, _ := base64.StdEncoding.DecodeString(peer.Key)
	unsigned := e
	unsigned.Signature = ""
	if !ed25519.Verify(key, peerSignData(unsigned), sig) {
		return 401
	}
	now := time.Now().Unix()
	if e.Timestamp < now-300 || e.Timestamp > now+60 {
		return 401
	}
	if e.ID == "" || len(e.ID) > 128 || (e.Kind == "" && len(e.Text) > 32768) || len(e.Text) > 360000 || len(e.Nonce) != 48 {
		return 400
	}
	replayKey := e.Sender + ":" + e.Nonce
	if _, ok := p.state.Seen[replayKey]; ok {
		return 409
	}
	if e.Kind == "file" {
		before, _ := json.Marshal(p.state)
		for key, timestamp := range p.state.Seen {
			if timestamp < now-300 {
				delete(p.state.Seen, key)
			}
		}
		code := p.receiveFile(e)
		if code == 200 {
			p.state.Seen[replayKey] = e.Timestamp
			if p.save() != nil {
				p.state = peerDisk{}
				json.Unmarshal(before, &p.state)
				return 500
			}
		}
		return code
	}
	if e.Kind != "" {
		return 400
	}
	if e.AttachmentID != "" && !p.fileReady(e.Sender, e.AttachmentID) {
		return 400
	}
	for _, m := range p.state.Messages[e.Sender] {
		if m.ID == e.ID {
			if m.SenderID == e.Sender && m.Text == e.Text && m.AttachmentID == e.AttachmentID {
				return 200
			}
			return 409
		}
	}
	before, _ := json.Marshal(p.state)
	p.state.Seen[replayKey] = e.Timestamp
	for key, timestamp := range p.state.Seen {
		if timestamp < now-300 {
			delete(p.state.Seen, key)
		}
	}
	p.state.Messages[e.Sender] = append(p.state.Messages[e.Sender], PeerMessage{ID: e.ID, SenderID: e.Sender, Text: e.Text, CreatedAt: e.Timestamp * 1000, AttachmentID: e.AttachmentID})
	if p.save() != nil {
		p.state = peerDisk{}
		json.Unmarshal(before, &p.state)
		return 500
	}
	return 200
}
func (p *Peers) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/im/node/info" && r.Method == "GET" {
			challenge := r.URL.Query().Get("challenge")
			if len(challenge) > 128 {
				w.WriteHeader(400)
				return
			}
			p.mu.Lock()
			name := p.name
			p.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"id": p.ID(), "name": name, "key": p.publicKey(), "proof": base64.StdEncoding.EncodeToString(ed25519.Sign(p.key, []byte("im-node-proof:"+challenge)))})
			return
		}
		if r.URL.Path != "/api/im/node/message" || r.Method != "POST" {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		var e peerEnvelope
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 400000))
		if dec.Decode(&e) != nil {
			w.WriteHeader(400)
			return
		}
		if dec.Decode(new(any)) != io.EOF {
			w.WriteHeader(400)
			return
		}
		code := p.receive(e)
		w.WriteHeader(code)
		if code != 200 {
			json.NewEncoder(w).Encode(map[string]string{"error": http.StatusText(code)})
		} else {
			json.NewEncoder(w).Encode(map[string]any{"accepted": true, "proof": base64.StdEncoding.EncodeToString(ed25519.Sign(p.key, []byte("im-node-accepted:"+e.Nonce)))})
		}
	})
}
func peerError(code int, s string) (int, any) { return code, map[string]string{"error": s} }
func (p *Peers) Request(method, path string, body json.RawMessage) (int, any) {
	path = strings.TrimPrefix(path, "/api/im")
	if strings.Contains(path, "/attachments") {
		return p.fileRequest(method, path, body)
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	p.mu.Lock()
	if path == "/peers" && method == "GET" {
		list := []Peer{}
		for _, q := range p.state.Peers {
			list = append(list, *q)
		}
		p.mu.Unlock()
		return 200, map[string]any{"peers": list, "nodeId": p.ID()}
	}
	if path == "/peers/trust" && method == "POST" {
		var v struct {
			ID      string `json:"id"`
			Trusted *bool  `json:"trusted"`
		}
		if json.Unmarshal(body, &v) != nil {
			p.mu.Unlock()
			return peerError(400, "invalid request")
		}
		q := p.state.Peers[v.ID]
		if q == nil {
			p.mu.Unlock()
			return peerError(404, "peer not discovered")
		}
		q.Trusted = true
		if v.Trusted != nil {
			q.Trusted = *v.Trusted
		}
		trusted := q.Trusted
		err := p.save()
		p.mu.Unlock()
		if err != nil {
			return peerError(500, "cannot save trust")
		}
		return 200, map[string]bool{"trusted": trusted}
	}
	if len(parts) != 3 || parts[0] != "peers" || parts[2] != "messages" {
		p.mu.Unlock()
		return peerError(404, "not found")
	}
	id := parts[1]
	q := p.state.Peers[id]
	if q == nil || !q.Trusted {
		p.mu.Unlock()
		return peerError(403, "peer not trusted")
	}
	if method == "GET" {
		history := p.state.Messages[id]
		if len(history) > 1000 {
			history = history[len(history)-1000:]
		}
		messages := append([]PeerMessage{}, history...)
		p.mu.Unlock()
		return 200, map[string]any{"messages": messages}
	}
	if method != "POST" {
		p.mu.Unlock()
		return peerError(405, "method not allowed")
	}
	var v struct {
		ID           string `json:"id"`
		Text         string `json:"text"`
		AttachmentID string `json:"attachmentId"`
	}
	if json.Unmarshal(body, &v) != nil || v.ID == "" || len(v.ID) > 128 || len(v.Text) > 32768 || (strings.TrimSpace(v.Text) == "" && v.AttachmentID == "") {
		p.mu.Unlock()
		return peerError(400, "invalid message")
	}
	for _, m := range p.state.Messages[id] {
		if m.ID == v.ID {
			p.mu.Unlock()
			if m.SenderID != p.ID() || m.Text != v.Text || m.AttachmentID != v.AttachmentID {
				return peerError(409, "message id conflict")
			}
			return 200, map[string]any{"message": m}
		}
	}
	if v.AttachmentID != "" && !p.fileDelivered(id, v.AttachmentID) {
		p.mu.Unlock()
		return peerError(400, "attachment incomplete")
	}
	p.mu.Unlock()
	env := p.envelope(id, v.ID, v.Text)
	env.AttachmentID = v.AttachmentID
	if err := p.sendEnvelope(id, env); err != nil {
		return peerError(502, err.Error())
	}
	m := PeerMessage{ID: v.ID, SenderID: p.ID(), Text: v.Text, CreatedAt: env.Timestamp * 1000, AttachmentID: v.AttachmentID}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, old := range p.state.Messages[id] {
		if old.ID == m.ID {
			return 200, map[string]any{"message": old}
		}
	}
	oldMessages := p.state.Messages[id]
	p.state.Messages[id] = append(p.state.Messages[id], m)
	if p.save() != nil {
		p.state.Messages[id] = oldMessages
		return peerError(500, "cannot save message")
	}
	return 200, map[string]any{"message": m}
}
func (p *Peers) Start(port int) error {
	if port < 1 || port > 65535 {
		return errors.New("invalid port")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		return nil
	}
	group := &net.UDPAddr{IP: net.IPv4(239, 255, 53, 19), Port: 53319}
	conn, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return err
	}
	p.conn = conn
	p.stop = make(chan struct{})
	stop := p.stop
	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		buf := make([]byte, 4096)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if !src.IP.IsPrivate() && !src.IP.IsLoopback() && !src.IP.IsLinkLocalUnicast() {
				continue
			}
			var a peerAdvertisement
			if json.Unmarshal(buf[:n], &a) != nil || a.Port < 1 || a.Port > 65535 || len(a.Name) > 128 || !validPeerKey(a.ID, a.Key) {
				continue
			}
			now := time.Now().Unix()
			if a.Timestamp < now-60 || a.Timestamp > now+30 {
				continue
			}
			sig, _ := base64.StdEncoding.DecodeString(a.Signature)
			a.Signature = ""
			key, _ := base64.StdEncoding.DecodeString(a.Key)
			if !ed25519.Verify(key, peerSignData(a), sig) {
				continue
			}
			p.remember(a.ID, a.Key, a.Name, "http://"+net.JoinHostPort(src.IP.String(), strconv.Itoa(a.Port)))
		}
	}()
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		send := func() {
			p.mu.Lock()
			name := p.name
			p.mu.Unlock()
			a := peerAdvertisement{ID: p.ID(), Key: p.publicKey(), Name: name, Port: port, Timestamp: time.Now().Unix()}
			a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(p.key, peerSignData(a)))
			c, e := net.DialUDP("udp4", nil, group)
			if e == nil {
				c.Write(peerSignData(a))
				c.Close()
			}
		}
		send()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				send()
			}
		}
	}()
	return nil
}
func (p *Peers) Close() error {
	p.mu.Lock()
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
		close(p.stop)
	}
	p.mu.Unlock()
	p.wg.Wait()
	return nil
}

func (p *Peers) sendEnvelope(id string, env peerEnvelope) error {
	p.mu.Lock()
	q := p.state.Peers[id]
	if q == nil || !q.Trusted {
		p.mu.Unlock()
		return errors.New("untrusted peer")
	}
	address := q.Address
	peerKey, _ := base64.StdEncoding.DecodeString(q.Key)
	p.mu.Unlock()
	env.Signature = ""
	env.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(p.key, peerSignData(env)))
	data := peerSignData(env)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }, Transport: &http.Transport{Proxy: nil}}
	// Prove possession of the pinned key before disclosing message contents.
	info, err := client.Get(address + "/api/im/node/info?challenge=" + env.Nonce)
	if err != nil {
		return errors.New("peer unavailable")
	}
	var proof struct {
		Proof string `json:"proof"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(info.Body, 4096)).Decode(&proof)
	info.Body.Close()
	proofBytes, _ := base64.StdEncoding.DecodeString(proof.Proof)
	if decodeErr != nil || info.StatusCode != 200 || !ed25519.Verify(peerKey, []byte("im-node-proof:"+env.Nonce), proofBytes) {
		return errors.New("peer identity verification failed")
	}
	resp, err := client.Post(address+"/api/im/node/message", "application/json", bytes.NewReader(data))
	if err != nil {
		return errors.New("peer unavailable")
	}
	proof.Proof = ""
	decodeErr = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&proof)
	resp.Body.Close()
	proofBytes, _ = base64.StdEncoding.DecodeString(proof.Proof)
	if resp.StatusCode != 200 || decodeErr != nil || !ed25519.Verify(peerKey, []byte("im-node-accepted:"+env.Nonce), proofBytes) {
		return errors.New("peer rejected message; both nodes must explicitly trust each other")
	}

	return nil
}
