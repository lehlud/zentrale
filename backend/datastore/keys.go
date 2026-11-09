package datastore

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"fmt"
)

// SSHPrivKey is a stored EC private key with its database-generated identifier and
// DER-encoded key data. A zero ID is omitted on insert so the database assigns it.
type SSHPrivKey struct {
	ID        int    `db:"id,omitempty"`
	X509Data  []byte `db:"x509_data"`
	Purpose   string `db:"purpose"`
	PurposeID string `db:"purpose_id"`
}

// GenerateSSHKey generates and stores a P-521 EC private key, returning its
// database identifier and encoded data. Generation, encoding, insertion, or
// decoding failures return a nil key and a wrapped error.
func (i *Instance) GenerateSSHKey() (*SSHPrivKey, error) {
	data, err := generateSSHKeyData()
	if err != nil {
		return nil, err
	}
	key, err := i.insertOne[*SSHPrivKey]("private_ssh_keys", SSHPrivKey{X509Data: data})
	if err != nil {
		return nil, fmt.Errorf("GenerateSSHKey(): db insertion failed: %w", err)
	}
	return key, nil
}

func generateSSHKeyData() ([]byte, error) {
	privkey, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("GenerateSSHKey(): generation failed: %w", err)
	}

	encPrivkey, err := x509.MarshalECPrivateKey(privkey)
	if err != nil {
		return nil, fmt.Errorf("GenerateSSHKey(): marshalling failed: %w", err)
	}

	return encPrivkey, nil
}

// ListPrivSSHKeys retrieves the stored private keys. An empty table returns a
// non-nil empty slice; query or decoding failures return nil and a wrapped error.
func (i *Instance) ListPrivSSHKeys() ([]SSHPrivKey, error) {
	keys, err := i.query[[]SSHPrivKey]("SELECT * FROM private_ssh_keys")
	if err != nil {
		return nil, fmt.Errorf("ListPrivSSHKeys(): db retrieval failed: %w", err)
	}

	return keys, nil
}

// DropPrivSSHKey deletes the private key with the given database identifier.
// An identifier that is already absent succeeds; database failures return a
// wrapped error, including errClosed for a nil, uninitialized, or closed instance.
func (i *Instance) DropPrivSSHKey(keyId int) error {
	if err := i.dropUnusedSSHKey(keyId); err != nil {
		return fmt.Errorf("DropPrivSSHKey(): db deletion failed: %w", err)
	}

	return nil
}

// IsTrustedSSHKey reports whether these exact encoded key bytes are trusted.
// An unknown key returns false and nil. A database failure returns false and a
// wrapped error, keeping an untrusted key distinct from a failed lookup.
func (i *Instance) IsTrustedSSHKey(data []byte) (bool, error) {
	result, err := i.query[struct {
		Trusted bool `db:"trusted"`
	}]("SELECT EXISTS (SELECT 1 FROM known_ssh_keys WHERE x509_data = ?) AS trusted", data)

	if err != nil {
		return false, fmt.Errorf("IsTrustedSSHKey(): db retrieval failed: %w", err)
	}

	return result.Trusted, nil
}

// TrustSSHKey stores these encoded key bytes as trusted. Repeated calls for the
// same key succeed without creating duplicates. Comparison and storage use the
// complete byte sequence; database failures return a wrapped error.
func (i *Instance) TrustSSHKey(data []byte) error {
	_, err := i.query[[]map[string]any](`
		INSERT INTO known_ssh_keys (x509_data) VALUES (?)
		ON CONFLICT DO NOTHING RETURNING x509_data
	`, data)

	if err != nil {
		return fmt.Errorf("TrustSSHKey(): db insertion failed: %w", err)
	}

	return nil
}

// DistrustSSHKey removes trust for these exact encoded key bytes. A key that is
// already absent succeeds; database failures return a wrapped error.
func (i *Instance) DistrustSSHKey(data []byte) error {
	if _, err := i.delete("known_ssh_keys", "x509_data = ?", data); err != nil {
		return fmt.Errorf("DistrustSSHKey(): db deletion failed: %w", err)
	}

	return nil
}
