// 传输历史 — 出站 / 入站文件传输记录
// 每个 server 各自落盘到自己的 saveDir/transfers/ 下
package localsend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// TransferDirection 传输方向
type TransferDirection string

const (
	TransferSent     TransferDirection = "sent"     // 我发出
	TransferReceived TransferDirection = "received" // 我收到
)

// TransferStatus 传输结果
type TransferStatus string

const (
	TransferStatusSuccess TransferStatus = "success"
	TransferStatusDecline TransferStatus = "decline"
	TransferStatusFailed  TransferStatus = "failed"
	TransferStatusCancel  TransferStatus = "canceled"
)

// TransferLog 一条传输记录
type TransferLog struct {
	ID           string            `json:"id"`
	Direction    TransferDirection `json:"direction"` // sent / received
	Status       TransferStatus    `json:"status"`
	SessionID    string            `json:"sessionId"`

	// 对方
	PeerIP      string `json:"peerIp"`
	PeerPort    int    `json:"peerPort"`
	PeerName    string `json:"peerName"`
	PeerFP      string `json:"peerFingerprint"`

	// 文件列表
	Files []FileMetadata `json:"files"`

	// 统计
	TotalSize   int64  `json:"totalSize"`
	FileCount   int    `json:"fileCount"`

	// 时间
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  time.Time  `json:"finishedAt"`
	DurationMs  int64      `json:"durationMs"`
	ErrorMsg    string     `json:"error,omitempty"`
}

// TransferStore 管理传输记录落盘
type TransferStore struct {
	dir string
}

func NewTransferStore(saveDir string) *TransferStore {
	if saveDir == "" {
		return nil
	}
	return &TransferStore{dir: filepath.Join(saveDir, "transfers")}
}

func (s *TransferStore) dirPath() string {
	if s == nil { return "" }
	return s.dir
}

func (s *TransferStore) ensureDir() {
	if s == nil || s.dir == "" { return }
	os.MkdirAll(s.dir, 0o755)
}

// SaveLog 追加一条记录
func (s *TransferStore) SaveLog(log *TransferLog) error {
	if s == nil { return nil }
	s.ensureDir()
	filename := log.Direction.String() + "-" + log.ID + ".json"
	data, err := json.MarshalIndent(log, "", "  ")
	if err != nil { return err }
	return os.WriteFile(filepath.Join(s.dir, filename), data, 0o644)
}

// List 返回所有记录（倒序）
func (s *TransferStore) List() []TransferLog {
	if s == nil { return nil }
	s.ensureDir()
	entries, err := os.ReadDir(s.dir)
	if err != nil { return nil }
	var out []TransferLog
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" { continue }
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil { continue }
		var log TransferLog
		if err := json.Unmarshal(data, &log); err != nil { continue }
		out = append(out, log)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// ListByDirection 返回某个方向的记录
func (s *TransferStore) ListByDirection(dir TransferDirection) []TransferLog {
	all := s.List()
	var out []TransferLog
	for _, l := range all {
		if l.Direction == dir { out = append(out, l) }
	}
	return out
}

// String 让 TransferDirection 可以当文件名用
func (d TransferDirection) String() string { return string(d) }
