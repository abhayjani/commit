package store

import (
	"strconv"
	"strings"
	"time"
)

// ReplyItem is a chat that needs attention based purely on message direction
// and timing — no LLM involved. Derived from the messages table.
type ReplyItem struct {
	ChatJID      string  `json:"chat_jid"`
	ChatName     string  `json:"chat_name"`
	PersonName   string  `json:"person_name"`
	LastText     string  `json:"last_text"`
	LastFromMe   bool    `json:"last_from_me"`
	LastSender   string  `json:"last_sender"` // who sent the last msg (for group previews)
	LastTime     int64   `json:"last_time"`   // unix seconds
	WaitingHours float64 `json:"waiting_hours"`
	IsGroup      bool    `json:"is_group"`
	Priority     string  `json:"priority"` // from chat_meta
	Tags         string  `json:"tags"`     // from chat_meta
}

// ReplyQueues splits chats into the two follow-up views Outscroll cares about.
type ReplyQueues struct {
	NeedsReply    []*ReplyItem `json:"needs_reply"`    // they messaged last, you owe a reply
	AwaitingReply []*ReplyItem `json:"awaiting_reply"` // you messaged last, they've gone quiet
}

// Tunable thresholds, overridable via settings so they "can be defined" later
// (and exposed to the team/CRM build). Defaults are sensible for an agency inbox.
func (db *DB) getIntSetting(key string, def int) int {
	v := db.GetSetting(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

// GetReplyQueues computes the needs-reply / awaiting-reply lists from the latest
// message in each chat. Groups and muted chats are excluded by default.
func (db *DB) GetReplyQueues() (*ReplyQueues, error) {
	minNeedsMinutes := db.getIntSetting("needs_reply_min_minutes", 0)  // show all unanswered inbound
	minAwaitHours := db.getIntSetting("awaiting_reply_min_hours", 12)  // you pinged, silence for 12h+
	maxStaleDays := db.getIntSetting("replies_max_stale_days", 60)     // ignore long-dead threads
	includeGroups := db.getIntSetting("replies_include_groups", 1) == 1 // track all chats by default

	now := time.Now()
	staleCutoff := now.Add(-time.Duration(maxStaleDays) * 24 * time.Hour).Unix()

	groupFilter := "AND is_group = 0"
	if includeGroups {
		groupFilter = ""
	}

	// One row per chat: its most recent message. Window function picks rn=1.
	rows, err := db.conn.Query(`
		SELECT m.chat_jid, m.chat_name, m.sender_name, m.content, m.timestamp, m.is_from_me, m.is_group,
		       COALESCE(cm.priority, ''), COALESCE(cm.tags, '')
		FROM (
			SELECT chat_jid, chat_name, sender_name, content, timestamp, is_from_me, is_group,
			       ROW_NUMBER() OVER (PARTITION BY chat_jid ORDER BY timestamp DESC, id DESC) AS rn
			FROM messages
			WHERE timestamp >= ? `+groupFilter+`
		) m
		LEFT JOIN chat_meta cm ON cm.chat_jid = m.chat_jid
		WHERE m.rn = 1
		  AND m.chat_jid NOT IN (SELECT chat_jid FROM muted_chats)
		  AND m.chat_jid NOT IN (SELECT chat_jid FROM archived_chats)
		ORDER BY m.timestamp DESC`, staleCutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	needsReply := []*ReplyItem{}
	awaitingReply := []*ReplyItem{}

	for rows.Next() {
		var chatJID, chatName, senderName, content, priority, tags string
		var ts int64
		var fromMe, group int
		if err := rows.Scan(&chatJID, &chatName, &senderName, &content, &ts, &fromMe, &group, &priority, &tags); err != nil {
			return nil, err
		}

		isFromMe := fromMe == 1
		waitingHours := now.Sub(time.Unix(ts, 0)).Hours()

		person := chatName
		if person == "" && !isFromMe {
			person = senderName
		}
		if person == "" {
			person = displayPhone(chatJID)
		}

		item := &ReplyItem{
			ChatJID:      chatJID,
			ChatName:     chatName,
			PersonName:   person,
			LastText:     snippet(content, 140),
			LastFromMe:   isFromMe,
			LastSender:   senderName,
			LastTime:     ts,
			WaitingHours: waitingHours,
			IsGroup:      group == 1,
			Priority:     priority,
			Tags:         tags,
		}

		if isFromMe {
			// You sent the last message — waiting on their reply.
			if waitingHours >= float64(minAwaitHours) {
				awaitingReply = append(awaitingReply, item)
			}
		} else {
			// They sent the last message — you owe a reply.
			if waitingHours*60 >= float64(minNeedsMinutes) {
				needsReply = append(needsReply, item)
			}
		}
	}
	return &ReplyQueues{NeedsReply: needsReply, AwaitingReply: awaitingReply}, rows.Err()
}

// GetRecentThread returns the last `limit` messages in a chat, oldest-first,
// so a draft generator sees the conversation in order.
func (db *DB) GetRecentThread(chatJID string, limit int) ([]*Message, error) {
	rows, err := db.conn.Query(`
		SELECT id, chat_jid, sender_jid, sender_name, chat_name, content, timestamp, is_from_me, is_group
		FROM messages WHERE chat_jid = ?
		ORDER BY timestamp DESC LIMIT ?`, chatJID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		m := &Message{}
		var ts int64
		var fromMe, group int
		if err := rows.Scan(&m.ID, &m.ChatJID, &m.SenderJID, &m.SenderName, &m.ChatName,
			&m.Content, &ts, &fromMe, &group); err != nil {
			return nil, err
		}
		m.Timestamp = time.Unix(ts, 0)
		m.IsFromMe = fromMe == 1
		m.IsGroup = group == 1
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse to oldest-first
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// GetMyVoiceSamples returns substantive messages the user themselves sent —
// preferring the current chat (matched tone), topped up with global samples —
// to use as few-shot style examples when drafting a reply in their voice.
func (db *DB) GetMyVoiceSamples(chatJID string, limit int) ([]string, error) {
	seen := map[string]bool{}
	samples := []string{}

	collect := func(query string, args ...any) error {
		rows, err := db.conn.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				return err
			}
			c = strings.TrimSpace(c)
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			samples = append(samples, c)
			if len(samples) >= limit {
				break
			}
		}
		return rows.Err()
	}

	// Prefer this contact's history (you write differently to different people).
	if err := collect(`
		SELECT content FROM messages
		WHERE is_from_me = 1 AND chat_jid = ? AND length(content) >= 12
		ORDER BY timestamp DESC LIMIT ?`, chatJID, limit); err != nil {
		return nil, err
	}
	// Top up with recent sent messages from anywhere.
	if len(samples) < limit {
		if err := collect(`
			SELECT content FROM messages
			WHERE is_from_me = 1 AND length(content) >= 12
			ORDER BY timestamp DESC LIMIT ?`, limit*3); err != nil {
			return nil, err
		}
	}
	return samples, nil
}

// displayPhone turns a chat JID into a human-ish label when we have no real
// name yet — a phone number beats "Unknown" every time.
func displayPhone(chatJID string) string {
	if strings.HasSuffix(chatJID, "@g.us") {
		return "Group chat"
	}
	id := chatJID
	if at := strings.IndexByte(chatJID, '@'); at >= 0 {
		id = chatJID[:at]
	}
	if id == "" {
		return "Unknown contact"
	}
	allDigits := true
	for _, r := range id {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits && len(id) >= 7 {
		return "+" + id
	}
	return "Unknown contact"
}

func snippet(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}
