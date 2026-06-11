package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestChatMetaPriorityTagsAndPeople(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	now := time.Now()
	save := func(id, chat, sender string, fromMe bool, ago time.Duration) {
		if err := db.SaveMessage(&Message{
			ID: id, ChatJID: chat, SenderJID: sender, SenderName: sender,
			ChatName: sender, Content: "msg " + id, Timestamp: now.Add(-ago),
			IsFromMe: fromMe,
		}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	save("a1", "A@s.whatsapp.net", "Alice", false, 1*time.Hour)  // needs reply
	save("b1", "B@s.whatsapp.net", "Bob", true, 20*time.Hour)    // awaiting
	save("c1", "C@s.whatsapp.net", "Cara", true, 1*time.Hour)    // idle

	// Tag Alice P0/TPF; normalization dedupes + trims.
	m, err := db.SetChatMeta("A@s.whatsapp.net", "p0", " TPF , tpf, Client ")
	if err != nil {
		t.Fatalf("SetChatMeta: %v", err)
	}
	if m.Priority != "P0" || m.Tags != "TPF, Client" {
		t.Errorf("normalized meta = %q/%q, want P0/\"TPF, Client\"", m.Priority, m.Tags)
	}

	// Reply queues carry the meta.
	q, err := db.GetReplyQueues()
	if err != nil {
		t.Fatalf("GetReplyQueues: %v", err)
	}
	if len(q.NeedsReply) != 1 || q.NeedsReply[0].Priority != "P0" || q.NeedsReply[0].Tags != "TPF, Client" {
		t.Errorf("needs_reply meta missing: %+v", q.NeedsReply)
	}

	// People: 3 rows, P0 first, statuses correct.
	people, err := db.GetPeople()
	if err != nil {
		t.Fatalf("GetPeople: %v", err)
	}
	if len(people) != 3 {
		t.Fatalf("people = %d, want 3", len(people))
	}
	if people[0].ChatJID != "A@s.whatsapp.net" || people[0].Status != "needs_reply" || people[0].Priority != "P0" {
		t.Errorf("people[0] should be Alice P0 needs_reply: %+v", people[0])
	}
	statuses := map[string]string{}
	for _, p := range people {
		statuses[p.ChatJID] = p.Status
	}
	if statuses["B@s.whatsapp.net"] != "awaiting" || statuses["C@s.whatsapp.net"] != "idle" {
		t.Errorf("statuses wrong: %v", statuses)
	}

	if db.CountChats() != 3 {
		t.Errorf("CountChats = %d, want 3", db.CountChats())
	}
}

func TestExtractionToggleDefaultOff(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if db.GetExtractionEnabled() {
		t.Error("extraction should be OFF by default (opt-in)")
	}
	db.SetExtractionEnabled(true)
	if !db.GetExtractionEnabled() {
		t.Error("extraction should be on after enabling")
	}
}
