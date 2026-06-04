package whatsapp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/msfoundry/commit/store"
	"go.mau.fi/whatsmeow/types"
)

// The defining safety property of this fork: the client is read-only by
// default and SendMessage never reaches WhatsApp.
func TestReadOnlyBlocksSend(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	c := New(db, t.TempDir(), nil, context.Background())
	if !c.IsReadOnly() {
		t.Fatal("New client should be read-only by default")
	}

	jid := types.NewJID("15551234567", types.DefaultUserServer)
	err = c.SendMessage(context.Background(), jid, "this must never be sent")
	if !errors.Is(err, ErrReadOnly) {
		t.Errorf("SendMessage in read-only returned %v, want ErrReadOnly", err)
	}
}
