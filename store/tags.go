package store

import (
	"sort"
	"strings"
	"time"
)

// ChatMeta is the user-authored context layer on a chat: priority + tags.
// This is the "slowly teach it which threads matter" surface.
type ChatMeta struct {
	ChatJID  string `json:"chat_jid"`
	Priority string `json:"priority"` // "", "P0", "P1", "P2"
	Tags     string `json:"tags"`     // comma-separated, as typed
}

func normalizePriority(p string) string {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "P0":
		return "P0"
	case "P1":
		return "P1"
	case "P2":
		return "P2"
	}
	return ""
}

func normalizeTags(tags string) string {
	parts := strings.Split(tags, ",")
	seen := map[string]bool{}
	out := []string{}
	for _, t := range parts {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return strings.Join(out, ", ")
}

func (db *DB) SetChatMeta(chatJID, priority, tags string) (*ChatMeta, error) {
	m := &ChatMeta{
		ChatJID:  chatJID,
		Priority: normalizePriority(priority),
		Tags:     normalizeTags(tags),
	}
	_, err := db.conn.Exec(`
		INSERT INTO chat_meta (chat_jid, priority, tags, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(chat_jid) DO UPDATE SET priority = excluded.priority, tags = excluded.tags, updated_at = excluded.updated_at`,
		m.ChatJID, m.Priority, m.Tags, time.Now().Unix(),
	)
	return m, err
}

func (db *DB) GetAllChatMeta() (map[string]*ChatMeta, error) {
	rows, err := db.conn.Query("SELECT chat_jid, priority, tags FROM chat_meta")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*ChatMeta{}
	for rows.Next() {
		m := &ChatMeta{}
		if err := rows.Scan(&m.ChatJID, &m.Priority, &m.Tags); err != nil {
			return nil, err
		}
		out[m.ChatJID] = m
	}
	return out, rows.Err()
}

// Extraction toggle: background LLM commitment-mining is OPT-IN. Default off —
// the reply queues are free, and drafts run on-demand only.
func (db *DB) GetExtractionEnabled() bool {
	return db.GetSetting("extraction_enabled") == "1"
}

func (db *DB) SetExtractionEnabled(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return db.SetSetting("extraction_enabled", v)
}

// DistinctPersonChats lists every 1:1 chat JID (for name resolution).
func (db *DB) DistinctPersonChats() ([]string, error) {
	rows, err := db.conn.Query("SELECT DISTINCT chat_jid FROM messages WHERE is_group = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// AllChatJIDs lists every distinct chat (1:1 and group) for state sync.
func (db *DB) AllChatJIDs() ([]string, error) {
	rows, err := db.conn.Query("SELECT DISTINCT chat_jid FROM messages")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// UpdateChatName stamps a resolved display name across a chat's messages.
func (db *DB) UpdateChatName(chatJID, name string) error {
	if name == "" {
		return nil
	}
	_, err := db.conn.Exec("UPDATE messages SET chat_name = ? WHERE chat_jid = ?", name, chatJID)
	return err
}

// DeleteChat is the "forget this contact" action — wipes everything we hold
// about one chat. Right-to-delete for the SOC2-spirit local store.
func (db *DB) DeleteChat(chatJID string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		"DELETE FROM messages WHERE chat_jid = ?",
		"DELETE FROM commitments WHERE chat_jid = ?",
		"DELETE FROM chat_meta WHERE chat_jid = ?",
		"DELETE FROM favorite_chats WHERE chat_jid = ?",
		"DELETE FROM muted_chats WHERE chat_jid = ?",
	} {
		if _, err := tx.Exec(stmt, chatJID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceArchived swaps in the full set of chats WhatsApp says are archived.
func (db *DB) ReplaceArchived(jids []string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM archived_chats"); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT OR IGNORE INTO archived_chats (chat_jid) VALUES (?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, j := range jids {
		if _, err := stmt.Exec(j); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CountArchived is the number of archived chats (for the Archive tab badge).
func (db *DB) CountArchived() int {
	var n int
	db.conn.QueryRow("SELECT COUNT(*) FROM archived_chats").Scan(&n)
	return n
}

// CountChats is the number of distinct chats we've seen — the People count.
func (db *DB) CountChats() int {
	var n int
	db.conn.QueryRow("SELECT COUNT(DISTINCT chat_jid) FROM messages").Scan(&n)
	return n
}

// Person is one row of the People view — a contact-centric rollup of a chat:
// who it is, how much it matters (priority/tags), and where the reply debt sits.
type Person struct {
	ChatJID         string  `json:"chat_jid"`
	Name            string  `json:"name"`
	IsGroup         bool    `json:"is_group"`
	Priority        string  `json:"priority"`
	Tags            string  `json:"tags"`
	LastText        string  `json:"last_text"`
	LastFromMe      bool    `json:"last_from_me"`
	LastSender      string  `json:"last_sender"` // who sent the last msg (group previews)
	LastTime        int64   `json:"last_time"`
	WaitingHours    float64 `json:"waiting_hours"`
	Status          string  `json:"status"` // "needs_reply" | "awaiting" | "idle"
	Muted           bool    `json:"muted"`
	OpenCommitments int     `json:"open_commitments"`
}

// GetPeople returns the active contact directory (archived chats excluded).
func (db *DB) GetPeople() ([]*Person, error) { return db.GetPeopleFiltered(false) }

// GetPeopleFiltered returns chats as person-centric rows sorted by priority
// (P0 first), then reply debt, then recency. archivedOnly switches between the
// active directory and the Archive view (mirrors what you archived in WhatsApp).
func (db *DB) GetPeopleFiltered(archivedOnly bool) ([]*Person, error) {
	minAwaitHours := db.getIntSetting("awaiting_reply_min_hours", 12)

	archivedClause := "AND m.chat_jid NOT IN (SELECT chat_jid FROM archived_chats)"
	if archivedOnly {
		archivedClause = "AND m.chat_jid IN (SELECT chat_jid FROM archived_chats)"
	}
	now := time.Now()

	rows, err := db.conn.Query(`
		SELECT m.chat_jid, m.chat_name, m.sender_name, m.content, m.timestamp, m.is_from_me, m.is_group,
		       COALESCE(cm.priority, ''), COALESCE(cm.tags, ''),
		       EXISTS(SELECT 1 FROM muted_chats mu WHERE mu.chat_jid = m.chat_jid AND (mu.muted_until = 0 OR mu.muted_until > ?)),
		       (SELECT COUNT(*) FROM commitments c WHERE c.chat_jid = m.chat_jid AND c.status = 'open')
		FROM (
			SELECT chat_jid, chat_name, sender_name, content, timestamp, is_from_me, is_group,
			       ROW_NUMBER() OVER (PARTITION BY chat_jid ORDER BY timestamp DESC, id DESC) AS rn
			FROM messages
			WHERE is_reaction = 0
		) m
		LEFT JOIN chat_meta cm ON cm.chat_jid = m.chat_jid
		WHERE m.rn = 1 `+archivedClause+`
		ORDER BY m.timestamp DESC`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	people := []*Person{}
	for rows.Next() {
		var p Person
		var senderName string
		var fromMe, group, muted int
		if err := rows.Scan(&p.ChatJID, &p.Name, &senderName, &p.LastText, &p.LastTime, &fromMe, &group,
			&p.Priority, &p.Tags, &muted, &p.OpenCommitments); err != nil {
			return nil, err
		}
		p.LastFromMe = fromMe == 1
		p.IsGroup = group == 1
		p.Muted = muted == 1
		p.LastSender = senderName
		if isMasked(p.Name) {
			p.Name = ""
		}
		if p.Name == "" {
			if !p.LastFromMe && senderName != "" && !isMasked(senderName) {
				p.Name = senderName
			} else {
				p.Name = displayPhone(p.ChatJID)
			}
		}
		p.LastText = snippet(p.LastText, 120)
		p.WaitingHours = now.Sub(time.Unix(p.LastTime, 0)).Hours()
		if p.LastFromMe {
			if p.WaitingHours >= float64(minAwaitHours) {
				p.Status = "awaiting"
			} else {
				p.Status = "idle"
			}
		} else {
			p.Status = "needs_reply"
		}
		people = append(people, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	prioRank := map[string]int{"P0": 0, "P1": 1, "P2": 2, "": 3}
	statusRank := map[string]int{"needs_reply": 0, "awaiting": 1, "idle": 2}
	sort.SliceStable(people, func(i, j int) bool {
		if prioRank[people[i].Priority] != prioRank[people[j].Priority] {
			return prioRank[people[i].Priority] < prioRank[people[j].Priority]
		}
		if statusRank[people[i].Status] != statusRank[people[j].Status] {
			return statusRank[people[i].Status] < statusRank[people[j].Status]
		}
		return people[i].LastTime > people[j].LastTime
	})
	return people, nil
}
