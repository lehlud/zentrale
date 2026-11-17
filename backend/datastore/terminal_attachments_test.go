package datastore

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestExpiredTerminalLeaseReleasesOnlyItsPrivateReceipt(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for id, expiry := range map[string]time.Time{"expired": time.Now().Add(-time.Minute), "active": time.Now().Add(time.Minute)} {
		if err := store.SaveCredentialResource("terminal-runtime/"+id, []byte("private input"), "terminal_attachment", id, "project", map[string]any{"machineId": id, "expiresAt": expiry}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PruneTerminalAttachments(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Credential("terminal-runtime/expired"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired private receipt retained: %v", err)
	}
	if _, err := store.Credential("terminal-runtime/active"); err != nil {
		t.Fatal(err)
	}
	if active, err := store.HasTerminalAttachment("project", "active"); err != nil || !active {
		t.Fatalf("active lease lost: %v %v", active, err)
	}
	if active, err := store.HasTerminalAttachment("project", "expired"); err != nil || active {
		t.Fatalf("expired lease retained: %v %v", active, err)
	}
}
