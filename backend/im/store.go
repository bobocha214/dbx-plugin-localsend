package im

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Bio      string `json:"bio"`
	Avatar   string `json:"avatar"`
	Admin    bool   `json:"admin"`
	Disabled bool   `json:"disabled"`
	LastSeen int64  `json:"lastSeen"`
}
type account struct {
	User
	Salt string
	Hash string
}
type session struct {
	ID        string
	CreatedAt int64
	UserID    string
	Expires   int64
}
type invite struct {
	ID      string `json:"id"`
	Token   string `json:"token,omitempty"`
	Uses    int    `json:"uses"`
	Expires int64  `json:"expires"`
}
type Conversation struct {
	OwnerID   string   `json:"ownerId"`
	Unread    int      `json:"unread"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Members   []string `json:"members"`
	UpdatedAt int64    `json:"updatedAt"`
}
type Message struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	SenderID       string `json:"senderId"`
	Text           string `json:"text"`
	AttachmentID   string `json:"attachmentId"`
	CreatedAt      int64  `json:"createdAt"`
}
type Attachment struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	Mime           string `json:"mime"`
	SHA256         string `json:"sha256"`
	ConversationID string `json:"conversationId"`
	Owner          string `json:"-"`
	Offset         int64  `json:"offset"`
	Finished       bool   `json:"finished"`
}
type storedAttachment struct {
	Attachment
	OwnerID string
}
type diskState struct {
	GuestHostID    string
	GuestHostToken string
	Users          map[string]account
	Sessions       map[string]session
	Invites        map[string]invite
	Conversations  map[string]Conversation
	Messages       map[string][]Message
	Attachments    map[string]storedAttachment
	Reads          map[string]int64
	Hidden         map[string]bool
	Stickers       map[string]storedAttachment
	Downloads      map[string][]DownloadRecord
}
type Service struct {
	mu                 sync.Mutex
	dir                string
	fileDir            string
	state              diskState
	ui                 fs.FS
	network            http.Handler
	peerRequest        func(string, string, json.RawMessage) (int, any)
	attempts           map[string][]int64
	closed             bool
	credentialAttempts []int64
	pendingDeletes     []string
	downloadTickets    map[string]downloadTicket
}

func New(dir string) (*Service, error) {
	return NewWithFiles(dir, filepath.Join(dir, "files"))
}
func NewWithFiles(dir, files string) (*Service, error) {
	if e := os.MkdirAll(files, 0700); e != nil {
		return nil, e
	}
	s := &Service{dir: dir, fileDir: files, attempts: map[string][]int64{}}
	b, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if len(b) > 0 {
		if e = json.Unmarshal(b, &s.state); e != nil {
			return nil, e
		}
	}
	d := &s.state
	if d.Users == nil {
		d.Users = map[string]account{}
	}
	if d.Sessions == nil {
		d.Sessions = map[string]session{}
	}
	if d.Invites == nil {
		d.Invites = map[string]invite{}
	}
	if d.Conversations == nil {
		d.Conversations = map[string]Conversation{}
	}
	if d.Messages == nil {
		d.Messages = map[string][]Message{}
	}
	if d.Attachments == nil {
		d.Attachments = map[string]storedAttachment{}
	}
	if d.Reads == nil {
		d.Reads = map[string]int64{}
	}
	if d.Hidden == nil {
		d.Hidden = map[string]bool{}
	}
	return s, nil
}
func (s *Service) save() error {
	b, e := json.Marshal(s.state)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(s.dir, "state-*.tmp")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(name, filepath.Join(s.dir, "state.json"))
}
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.save()
	if e == nil {
		s.closed = true
	}
	return e
}
func (s *Service) SetUI(ui fs.FS)   { s.mu.Lock(); defer s.mu.Unlock(); s.ui = ui }
func (s *Service) Dir() string      { s.mu.Lock(); defer s.mu.Unlock(); return s.dir }
func (s *Service) FilesDir() string { s.mu.Lock(); defer s.mu.Unlock(); return s.fileDir }
func (s *Service) SetPeerHandler(h http.Handler, f func(string, string, json.RawMessage) (int, any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.network = h
	s.peerRequest = f
}
func randomID() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func now() int64                              { return time.Now().UnixMilli() }
func failure(code int, msg string) (int, any) { return code, map[string]any{"error": msg} }
func member(c Conversation, id string) bool {
	for _, m := range c.Members {
		if m == id {
			return true
		}
	}
	return false
}
