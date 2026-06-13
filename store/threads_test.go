package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGetReplyQueues(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	now := time.Now()
	save := func(id, chat, sender string, fromMe, group bool, ago time.Duration) {
		if err := db.SaveMessage(&Message{
			ID: id, ChatJID: chat, SenderJID: sender, SenderName: sender,
			ChatName: sender, Content: "msg " + id, Timestamp: now.Add(-ago),
			IsFromMe: fromMe, IsGroup: group,
		}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}

	// chatA: they messaged last, 1h ago  -> needs_reply
	save("a1", "A@s.whatsapp.net", "Alice", true, false, 3*time.Hour)
	save("a2", "A@s.whatsapp.net", "Alice", false, false, 1*time.Hour)
	// chatB: I messaged last, 20h ago    -> awaiting_reply (>=12h default)
	save("b1", "B@s.whatsapp.net", "Bob", false, false, 25*time.Hour)
	save("b2", "B@s.whatsapp.net", "Bob", true, false, 20*time.Hour)
	// chatC: I messaged last, 2h ago     -> neither (below 12h threshold)
	save("c1", "C@s.whatsapp.net", "Cara", true, false, 2*time.Hour)
	// group: they messaged last          -> excluded (groups off by default)
	save("g1", "G@g.us", "Group", false, true, 30*time.Minute)

	q, err := db.GetReplyQueues()
	if err != nil {
		t.Fatalf("GetReplyQueues: %v", err)
	}

	// Groups are tracked by default → Alice (1:1) + the group both need a reply.
	if len(q.NeedsReply) != 2 {
		t.Errorf("needs_reply = %d items, want 2 (Alice + group): %+v", len(q.NeedsReply), q.NeedsReply)
	}
	if !hasChat(q.NeedsReply, "A@s.whatsapp.net") || !hasChat(q.NeedsReply, "G@g.us") {
		t.Errorf("needs_reply should contain Alice and the group: %+v", q.NeedsReply)
	}
	if len(q.AwaitingReply) != 1 || q.AwaitingReply[0].ChatJID != "B@s.whatsapp.net" {
		t.Errorf("awaiting_reply = %d items, want 1 (Bob): %+v", len(q.AwaitingReply), q.AwaitingReply)
	}

	// With groups excluded, only the 1:1 (Alice) remains.
	db.SetSetting("replies_include_groups", "0")
	q2, err := db.GetReplyQueues()
	if err != nil {
		t.Fatalf("GetReplyQueues (groups off): %v", err)
	}
	if len(q2.NeedsReply) != 1 || q2.NeedsReply[0].ChatJID != "A@s.whatsapp.net" {
		t.Errorf("needs_reply (groups off) = %d, want 1 (Alice): %+v", len(q2.NeedsReply), q2.NeedsReply)
	}
}

func TestReplyScoring(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	now := time.Now()
	save := func(id, chat, sender, content string, group, mentions bool, ago time.Duration) {
		if err := db.SaveMessage(&Message{
			ID: id, ChatJID: chat, SenderJID: sender, SenderName: sender, ChatName: sender,
			Content: content, Timestamp: now.Add(-ago), IsFromMe: false, IsGroup: group, MentionsMe: mentions,
		}); err != nil {
			t.Fatal(err)
		}
	}
	save("m1", "M@g.us", "Riya", "can you check this @you", true, true, 1*time.Hour)            // tagged you
	save("i1", "I@s.whatsapp.net", "Suhas", "connecting you with Raj from Acme", false, false, 2*time.Hour) // intro
	save("p1", "P@g.us", "Rando", "lol", true, false, 30*time.Minute)                          // noisy group, no signal

	q, err := db.GetReplyQueues()
	if err != nil {
		t.Fatal(err)
	}
	if len(q.NeedsReply) != 3 {
		t.Fatalf("needs_reply = %d, want 3", len(q.NeedsReply))
	}
	by := map[string]*ReplyItem{}
	for _, it := range q.NeedsReply {
		by[it.ChatJID] = it
	}
	if by["M@g.us"].Score <= by["P@g.us"].Score {
		t.Errorf("tagged chat (%.1f) should outrank noisy group (%.1f)", by["M@g.us"].Score, by["P@g.us"].Score)
	}
	if !containsStr(by["M@g.us"].Reasons, "tagged you") {
		t.Errorf("M reasons missing 'tagged you': %v", by["M@g.us"].Reasons)
	}
	if !containsStr(by["I@s.whatsapp.net"].Reasons, "intro") {
		t.Errorf("I reasons missing 'intro': %v", by["I@s.whatsapp.net"].Reasons)
	}
	// sorted highest-first
	for i := 1; i < len(q.NeedsReply); i++ {
		if q.NeedsReply[i-1].Score < q.NeedsReply[i].Score {
			t.Errorf("not sorted by score desc at %d", i)
		}
	}
}

func containsStr(ss []string, x string) bool {
	for _, s := range ss {
		if s == x {
			return true
		}
	}
	return false
}

func hasChat(items []*ReplyItem, jid string) bool {
	for _, it := range items {
		if it.ChatJID == jid {
			return true
		}
	}
	return false
}
