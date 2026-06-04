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
	LastTime     int64   `json:"last_time"` // unix seconds
	WaitingHours float64 `json:"waiting_hours"`
	IsGroup      bool    `json:"is_group"`
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
	includeGroups := db.getIntSetting("replies_include_groups", 0) == 1

	now := time.Now()
	staleCutoff := now.Add(-time.Duration(maxStaleDays) * 24 * time.Hour).Unix()

	groupFilter := "AND is_group = 0"
	if includeGroups {
		groupFilter = ""
	}

	// One row per chat: its most recent message. Window function picks rn=1.
	rows, err := db.conn.Query(`
		SELECT chat_jid, chat_name, sender_name, content, timestamp, is_from_me, is_group
		FROM (
			SELECT chat_jid, chat_name, sender_name, content, timestamp, is_from_me, is_group,
			       ROW_NUMBER() OVER (PARTITION BY chat_jid ORDER BY timestamp DESC, id DESC) AS rn
			FROM messages
			WHERE timestamp >= ? `+groupFilter+`
		)
		WHERE rn = 1
		  AND chat_jid NOT IN (SELECT chat_jid FROM muted_chats)
		ORDER BY timestamp ASC`, staleCutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	needsReply := []*ReplyItem{}
	awaitingReply := []*ReplyItem{}

	for rows.Next() {
		var chatJID, chatName, senderName, content string
		var ts int64
		var fromMe, group int
		if err := rows.Scan(&chatJID, &chatName, &senderName, &content, &ts, &fromMe, &group); err != nil {
			return nil, err
		}

		isFromMe := fromMe == 1
		waitingHours := now.Sub(time.Unix(ts, 0)).Hours()

		person := chatName
		if person == "" && !isFromMe {
			person = senderName
		}
		if person == "" {
			person = "Unknown"
		}

		item := &ReplyItem{
			ChatJID:      chatJID,
			ChatName:     chatName,
			PersonName:   person,
			LastText:     snippet(content, 140),
			LastFromMe:   isFromMe,
			LastTime:     ts,
			WaitingHours: waitingHours,
			IsGroup:      group == 1,
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

func snippet(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}
