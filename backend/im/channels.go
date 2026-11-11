package im

import (
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type channelInvite struct {
	Token     string `json:"token"`
	ChannelID string `json:"channelId"`
	Uses      int    `json:"uses"`
	Expires   int64  `json:"expires"`
}

type sharedFile struct {
	Attachment
	SenderID string `json:"senderId"`
	SharedAt int64  `json:"sharedAt"`
}

func canContribute(c Conversation, id string) bool {
	if !member(c, id) {
		return false
	}
	if c.Kind != "group" && c.Kind != "channel" || c.ID == "lan-lobby" {
		return true
	}
	if c.Archived {
		return false
	}
	if !c.Restricted && c.Kind != "channel" {
		return true
	}
	if id == c.OwnerID {
		return true
	}
	for _, writer := range c.Writers {
		if writer == id {
			return true
		}
	}
	return false
}

func (s *Service) channelRoute(method, path string, a account, b input) (int, any) {
	if path == "/channels/join" && method == "POST" {
		inv, ok := s.state.ChannelInvites[b.Invite]
		c, exists := s.state.Conversations[inv.ChannelID]
		if !ok || !exists || (c.Kind != "group" && c.Kind != "channel") || c.ID == "lan-lobby" || c.Archived || inv.Expires <= now() || inv.Uses < 1 {
			return failure(410, "邀请已失效、用尽或群聊已归档")
		}
		if !member(c, a.ID) {
			if len(c.Members) >= 100 {
				return failure(409, "群聊成员已达 100 人上限")
			}
			c.Members = append(c.Members, a.ID)
			inv.Uses--
			s.state.ChannelInvites[b.Invite] = inv
			s.state.Conversations[c.ID] = c
		}
		delete(s.state.Hidden, a.ID+":"+c.ID)
		return 200, c
	}
	if path == "/channels" && method == "POST" {
		if strings.TrimSpace(b.Name) == "" || len(b.Name) > 200 || len(inputDescription(b)) > 2000 || len(b.Members) > 100 {
			return failure(400, "群聊名称、简介或成员无效")
		}
		c := Conversation{ID: randomID(), Kind: "group", Restricted: true, OwnerID: a.ID, Name: strings.TrimSpace(b.Name), Description: inputDescription(b), Members: []string{a.ID}, UpdatedAt: now()}
		if !s.setChannelMembers(&c, b) {
			return failure(400, "成员或上传权限无效")
		}
		s.state.Conversations[c.ID] = c
		return 200, c
	}
	parts := strings.Split(strings.TrimPrefix(path, "/channels/"), "/")
	c, ok := s.state.Conversations[parts[0]]
	if !ok || (c.Kind != "group" && c.Kind != "channel") || c.ID == "lan-lobby" {
		return failure(404, "群聊不存在")
	}
	if c.OwnerID != a.ID && !a.Admin {
		return failure(403, "仅群聊拥有者或本机管理员可以管理")
	}
	if len(parts) == 1 && method == "PATCH" {
		if len(b.Name) > 200 || len(inputDescription(b)) > 2000 || !s.setChannelMembers(&c, b) {
			return failure(400, "群聊设置无效")
		}
		if strings.TrimSpace(b.Name) != "" {
			c.Name = strings.TrimSpace(b.Name)
		}
		if b.Description != nil {
			c.Description = *b.Description
		}
		if b.Archived != nil {
			c.Archived = *b.Archived
		}
		if b.Restricted != nil {
			c.Restricted = *b.Restricted
		}
		c.UpdatedAt = now()
		s.state.Conversations[c.ID] = c
		return 200, c
	}
	if len(parts) == 1 && method == "DELETE" {
		delete(s.state.Conversations, c.ID)
		// Keep archived bytes for recovery, but revoke every access route immediately.
		for key, inv := range s.state.ChannelInvites {
			if inv.ChannelID == c.ID {
				delete(s.state.ChannelInvites, key)
			}
		}
		return 200, map[string]any{"ok": true}
	}
	if len(parts) == 2 && parts[1] == "invites" {
		if method == "GET" {
			items := []channelInvite{}
			for _, inv := range s.state.ChannelInvites {
				if inv.ChannelID == c.ID {
					items = append(items, inv)
				}
			}
			sort.Slice(items, func(i, j int) bool { return items[i].Expires > items[j].Expires })
			return 200, map[string]any{"invites": items}
		}
		if method == "POST" {
			if c.Archived {
				return failure(409, "已归档群聊不能创建邀请")
			}
			if b.Hours < 1 || b.Hours > 168 || b.Uses < 1 || b.Uses > 100 {
				return failure(400, "邀请有效期为 1–168 小时，次数为 1–100")
			}
			inv := channelInvite{Token: randomID(), ChannelID: c.ID, Expires: now() + int64(b.Hours)*3600000, Uses: b.Uses}
			if s.state.ChannelInvites == nil {
				s.state.ChannelInvites = map[string]channelInvite{}
			}
			s.state.ChannelInvites[inv.Token] = inv
			return 200, inv
		}
	}
	if len(parts) == 3 && parts[1] == "invites" && method == "DELETE" {
		inv, ok := s.state.ChannelInvites[parts[2]]
		if !ok || inv.ChannelID != c.ID {
			return failure(404, "邀请不存在")
		}
		delete(s.state.ChannelInvites, parts[2])
		return 200, map[string]any{"ok": true}
	}
	return failure(404, "route not found")
}

func (s *Service) setChannelMembers(c *Conversation, b input) bool {
	if b.Members == nil {
		return b.Writers == nil
	}
	if len(b.Members) > 100 || len(b.Writers) > 100 {
		return false
	}
	members := []string{c.OwnerID}
	seen := map[string]bool{c.OwnerID: true}
	for _, id := range b.Members {
		u, ok := s.state.Users[id]
		if !ok || u.Disabled {
			return false
		}
		if !seen[id] {
			if len(members) >= 100 {
				return false
			}
			members = append(members, id)
			seen[id] = true
		}
	}
	writers := []string{}
	used := map[string]bool{}
	for _, id := range b.Writers {
		if !seen[id] {
			return false
		}
		if !used[id] {
			writers = append(writers, id)
			used[id] = true
		}
	}
	c.Members = members
	c.Writers = writers
	return true
}

func (s *Service) conversationFiles(c Conversation, u *url.URL) (int, any) {
	q := strings.ToLower(u.Query().Get("q"))
	kind := u.Query().Get("type")
	// Only attachments actually sent in this conversation belong in its file library.
	sent := map[string]Message{}
	for _, m := range s.state.Messages[c.ID] {
		if m.AttachmentID != "" {
			sent[m.AttachmentID] = m
		}
	}
	files := []sharedFile{}
	for id, f := range s.state.Attachments {
		m, posted := sent[id]
		if f.ConversationID != c.ID || !f.Finished || !posted || !strings.Contains(strings.ToLower(f.Name), q) {
			continue
		}
		if kind == "image" && !strings.HasPrefix(f.Mime, "image/") {
			continue
		}
		if kind == "document" && !documentExtension(f.Name) {
			continue
		}
		files = append(files, sharedFile{Attachment: f.Attachment, SenderID: m.SenderID, SharedAt: m.CreatedAt})
	}
	sort.Slice(files, func(i, j int) bool {
		switch u.Query().Get("sort") {
		case "name":
			return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
		case "size":
			if files[i].Size != files[j].Size {
				return files[i].Size > files[j].Size
			}
		}
		return files[i].ID > files[j].ID
	})
	// Default ordering follows message order (latest shared first), not random IDs.
	if u.Query().Get("sort") == "" || u.Query().Get("sort") == "recent" {
		rank := map[string]int{}
		for i, m := range s.state.Messages[c.ID] {
			rank[m.AttachmentID] = i
		}
		sort.Slice(files, func(i, j int) bool { return rank[files[i].ID] > rank[files[j].ID] })
	}
	total := len(files)
	offset, _ := strconv.Atoi(u.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit, _ := strconv.Atoi(u.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 30
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return 200, map[string]any{"files": files[offset:end], "total": total, "more": end < total, "nextOffset": end}
}
func documentExtension(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".doc", ".docx", ".docm", ".xls", ".xlsx", ".xlsm", ".csv", ".ppt", ".pptx", ".pptm", ".pdf", ".odt", ".ods", ".odp", ".rtf", ".txt", ".md":
		return true
	}
	return false
}

func inputDescription(b input) string {
	if b.Description != nil {
		return *b.Description
	}
	return ""
}
