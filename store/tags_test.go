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

func TestArchiveExclusionAndGroupSender(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	now := time.Now()
	saveMsg := func(id, chat, sender, chatName string, fromMe, group bool, ago time.Duration) {
		if err := db.SaveMessage(&Message{
			ID: id, ChatJID: chat, SenderJID: sender, SenderName: sender,
			ChatName: chatName, Content: "msg " + id, Timestamp: now.Add(-ago),
			IsFromMe: fromMe, IsGroup: group,
		}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	saveMsg("a1", "A@s.whatsapp.net", "Alice", "Alice", false, false, 1*time.Hour) // needs reply, 1:1
	saveMsg("g1", "G@g.us", "Celestine", "My Group", false, true, 2*time.Hour)      // group, sender Celestine

	q, err := db.GetReplyQueues()
	if err != nil {
		t.Fatalf("queues: %v", err)
	}
	if len(q.NeedsReply) != 2 {
		t.Fatalf("needs_reply = %d, want 2", len(q.NeedsReply))
	}
	// group row carries the sender for "Sender: message" previews
	if !hasChat(q.NeedsReply, "G@g.us") {
		t.Fatal("group chat missing")
	}
	for _, it := range q.NeedsReply {
		if it.ChatJID == "G@g.us" && it.LastSender != "Celestine" {
			t.Errorf("group last_sender = %q, want Celestine", it.LastSender)
		}
	}

	// Archive the group → it drops out of queues and People, shows in archived.
	if err := db.ReplaceArchived([]string{"G@g.us"}); err != nil {
		t.Fatalf("ReplaceArchived: %v", err)
	}
	q2, _ := db.GetReplyQueues()
	if hasChat(q2.NeedsReply, "G@g.us") {
		t.Error("archived group should be excluded from needs_reply")
	}
	people, _ := db.GetPeopleFiltered(false)
	for _, p := range people {
		if p.ChatJID == "G@g.us" {
			t.Error("archived group should be excluded from People")
		}
	}
	arch, _ := db.GetPeopleFiltered(true)
	if len(arch) != 1 || arch[0].ChatJID != "G@g.us" {
		t.Errorf("archived view = %+v, want just the group", arch)
	}
	if db.CountArchived() != 1 {
		t.Errorf("CountArchived = %d, want 1", db.CountArchived())
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
