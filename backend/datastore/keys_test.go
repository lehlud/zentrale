package datastore

import (
	"bytes"
	"crypto/elliptic"
	"crypto/x509"
	"errors"
	"path/filepath"
	"testing"
)

func newKeyTestInstance(t *testing.T) *Instance {
	t.Helper()
	i, err := Open("")
	if err != nil {
		t.Fatal(err)
	}

	i.db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := i.Close(); err != nil {
			t.Error(err)
		}
	})

	return i
}

func TestSSHKeys(t *testing.T) {
	i := newKeyTestInstance(t)
	empty, err := i.ListPrivSSHKeys()
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty key list: keys=%#v err=%v", empty, err)
	}

	generated := make(map[int][]byte)

	for id := 1; id <= 2; id++ {
		key, err := i.GenerateSSHKey()
		if err != nil {
			t.Fatal(err)
		}

		if key == nil || key.ID != id || len(key.X509Data) == 0 {
			t.Fatalf("missing generated identifier or encoded key")
		}

		privateKey, err := x509.ParseECPrivateKey(key.X509Data)
		if err != nil || privateKey.Curve != elliptic.P521() {
			t.Fatalf("expected a DER-encoded P-521 private key: err=%v", err)
		}

		var stored []byte
		if err := i.db.QueryRow("SELECT x509_data FROM private_ssh_keys WHERE id = ?", id).Scan(&stored); err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(stored, key.X509Data) {
			t.Fatal("returned key data differs from the stored key")
		}

		generated[key.ID] = key.X509Data
	}

	keys, err := i.ListPrivSSHKeys()
	if err != nil || len(keys) != len(generated) {
		t.Fatalf("key list: count=%d err=%v", len(keys), err)
	}

	for _, key := range keys {
		if expected, ok := generated[key.ID]; !ok || !bytes.Equal(key.X509Data, expected) {
			t.Fatalf("listed key %d differs from its generated result", key.ID)
		}

		delete(generated, key.ID)
	}
}

func TestSSHKeyErrors(t *testing.T) {
	closed := newQueryTestInstance(t)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}

	for _, i := range []*Instance{nil, {}, closed} {
		key, err := i.GenerateSSHKey()
		if !errors.Is(err, errClosed) || key != nil {
			t.Fatalf("expected a closed-datastore generation error: err=%v", err)
		}

		keys, err := i.ListPrivSSHKeys()
		if !errors.Is(err, errClosed) || keys != nil {
			t.Fatalf("expected a closed-datastore listing error: err=%v", err)
		}

		if err := i.DropPrivSSHKey(1); !errors.Is(err, errClosed) {
			t.Fatalf("expected a closed-datastore deletion error: %v", err)
		}

		trusted, err := i.IsTrustedSSHKey([]byte{1})
		if !errors.Is(err, errClosed) || trusted {
			t.Fatalf("expected a closed-datastore lookup error: trusted=%v err=%v", trusted, err)
		}

		if err := i.TrustSSHKey([]byte{1}); !errors.Is(err, errClosed) {
			t.Fatalf("expected a closed-datastore trust error: %v", err)
		}

		if err := i.DistrustSSHKey([]byte{1}); !errors.Is(err, errClosed) {
			t.Fatalf("expected a closed-datastore distrust error: %v", err)
		}
	}

	// This instance is open but has no private_ssh_keys table.
	i := newQueryTestInstance(t)
	key, err := i.GenerateSSHKey()
	if err == nil || key != nil {
		t.Fatalf("expected an insertion error: err=%v", err)
	}

	keys, err := i.ListPrivSSHKeys()
	if err == nil || keys != nil {
		t.Fatalf("expected a listing error: err=%v", err)
	}

	if err := i.DropPrivSSHKey(1); err == nil {
		t.Fatal("expected a deletion error for a missing table")
	}

	trusted, err := i.IsTrustedSSHKey([]byte{1})
	if err == nil || trusted {
		t.Fatalf("expected a lookup error for a missing table: trusted=%v err=%v", trusted, err)
	}

	if err := i.TrustSSHKey([]byte{1}); err == nil {
		t.Fatal("expected a trust error for a missing table")
	}

	if err := i.DistrustSSHKey([]byte{1}); err == nil {
		t.Fatal("expected a distrust error for a missing table")
	}
}

func TestDropPrivSSHKey(t *testing.T) {
	i := newKeyTestInstance(t)
	first, err := i.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}

	second, err := i.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := i.DropPrivSSHKey(first.ID); err != nil {
			t.Fatal(err)
		}
	}

	keys, err := i.ListPrivSSHKeys()
	if err != nil || len(keys) != 1 || keys[0].ID != second.ID {
		t.Fatalf("private-key deletion changed the wrong rows: count=%d err=%v", len(keys), err)
	}
}

func TestSSHKeyTrust(t *testing.T) {
	i := newKeyTestInstance(t)
	first := append([]byte{0, 255}, []byte("'); DELETE FROM known_ssh_keys; --")...)
	second := []byte{1, 2, 3}
	trusted, err := i.IsTrustedSSHKey(first)
	if err != nil || trusted {
		t.Fatalf("unknown key: trusted=%v err=%v", trusted, err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := i.TrustSSHKey(first); err != nil {
			t.Fatal(err)
		}
	}

	var count int
	if err := i.db.QueryRow("SELECT count(*) FROM known_ssh_keys").Scan(&count); err != nil || count != 1 {
		t.Fatalf("trusting twice created duplicates: count=%d err=%v", count, err)
	}

	if err := i.TrustSSHKey(second); err != nil {
		t.Fatal(err)
	}

	for _, data := range [][]byte{first, second} {
		trusted, err := i.IsTrustedSSHKey(data)
		if err != nil || !trusted {
			t.Fatalf("stored key: trusted=%v err=%v", trusted, err)
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := i.DistrustSSHKey(first); err != nil {
			t.Fatal(err)
		}
	}

	trusted, err = i.IsTrustedSSHKey(first)
	if err != nil || trusted {
		t.Fatalf("revoked key: trusted=%v err=%v", trusted, err)
	}

	trusted, err = i.IsTrustedSSHKey(second)
	if err != nil || !trusted {
		t.Fatalf("revoking one key affected another: trusted=%v err=%v", trusted, err)
	}
}

func TestKeysPersistAcrossOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "datastore.duckdb")
	i, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if !i.Closed() {
			if err := i.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	key, err := i.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}

	data := []byte{1, 2, 255}
	if err := i.TrustSSHKey(data); err != nil {
		t.Fatal(err)
	}

	if err := i.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	i = reopened
	keys, err := i.ListPrivSSHKeys()
	if err != nil || len(keys) != 1 || keys[0].ID != key.ID || !bytes.Equal(keys[0].X509Data, key.X509Data) {
		t.Fatalf("private keys did not survive reopening: count=%d err=%v", len(keys), err)
	}

	trusted, err := i.IsTrustedSSHKey(data)
	if err != nil || !trusted {
		t.Fatalf("trust did not survive reopening: trusted=%v err=%v", trusted, err)
	}

	next, err := i.GenerateSSHKey()
	if err != nil || next.ID != key.ID+1 {
		t.Fatalf("generated identifiers did not survive reopening: err=%v", err)
	}
}
