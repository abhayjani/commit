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

	if len(q.NeedsReply) != 1 || q.NeedsReply[0].ChatJID != "A@s.whatsapp.net" {
		t.Errorf("needs_reply = %d items, want 1 (Alice): %+v", len(q.NeedsReply), q.NeedsReply)
	}
	if len(q.AwaitingReply) != 1 || q.AwaitingReply[0].ChatJID != "B@s.whatsapp.net" {
		t.Errorf("awaiting_reply = %d items, want 1 (Bob): %+v", len(q.AwaitingReply), q.AwaitingReply)
	}
	if len(q.NeedsReply) == 1 && q.NeedsReply[0].WaitingHours < 0.5 {
		t.Errorf("Alice waiting_hours = %v, want ~1", q.NeedsReply[0].WaitingHours)
	}
}
