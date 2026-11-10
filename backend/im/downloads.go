package im

import (
	"encoding/json"
	"sort"
)

type DownloadRecord struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	AttachmentID string `json:"attachmentId"`
	Prefix       string `json:"prefix"`
	Scope        string `json:"scope"`
	Size         int64  `json:"size"`
	Received     int64  `json:"received"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	At           int64  `json:"at"`
}

func (s *Service) downloadHistory(method string, a account, body json.RawMessage) (int, any) {
	if method == "GET" {
		list := s.state.Downloads[a.ID]
		if list == nil {
			list = []DownloadRecord{}
		}
		return 200, map[string]any{"downloads": list}
	}
	if method != "POST" {
		return failure(405, "method not allowed")
	}
	var b DownloadRecord
	if json.Unmarshal(body, &b) != nil || b.ID == "" || len(b.ID) > 160 || len(b.Name) > 255 || len(b.Scope) > 600 || len(b.Prefix) > 200 || len(b.AttachmentID) > 128 || len(b.Error) > 1000 || b.Size < 0 || b.Received < 0 || b.Received > b.Size {
		return failure(400, "invalid download record")
	}
	switch b.Status {
	case "saved":
		if b.Received != b.Size {
			return failure(400, "download is incomplete")
		}
	case "paused", "failed", "browser":
	default:
		return failure(400, "invalid download status")
	}
	if s.state.Downloads == nil {
		s.state.Downloads = map[string][]DownloadRecord{}
	}
	list := s.state.Downloads[a.ID]
	found := false
	b.At = now()
	for i := range list {
		if list[i].ID == b.ID {
			list[i] = b
			found = true
			break
		}
	}
	if !found {
		list = append(list, b)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].At > list[j].At })
	if len(list) > 500 {
		list = list[:500]
	}
	s.state.Downloads[a.ID] = list
	return 200, b
}
