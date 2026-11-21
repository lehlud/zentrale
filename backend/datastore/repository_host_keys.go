package datastore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type RepositoryHostKey struct {
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
}

// Keep the latest confirmation at the top level for existing records and for
// the confirmation guard. An endpoint can have pins for several algorithms.
type RepositoryHostTrust struct {
	RepositoryHostKey
	OtherKeys []RepositoryHostKey `json:"otherKeys,omitempty"`
}

// Replace the pin for the same algorithm, while retaining confirmed keys of
// other algorithms. A stale prompt cannot overwrite a newer decision.
func (i *Instance) ConfirmRepositoryHostKey(endpoint, previousFingerprint string, key RepositoryHostKey) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	var current RepositoryHostTrust
	if err := i.Load("repository_host_key", endpoint, &current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	stale := errors.New("repository host key trust changed; retry the connection before confirming")
	if current.Fingerprint != previousFingerprint {
		return stale
	}

	next := RepositoryHostTrust{RepositoryHostKey: key}
	keyType, _, _ := strings.Cut(key.PublicKey, " ")
	for _, old := range append(current.OtherKeys, current.RepositoryHostKey) {
		oldType, _, _ := strings.Cut(old.PublicKey, " ")
		if old.PublicKey != "" && oldType != keyType {
			next.OtherKeys = append(next.OtherKeys, old)
		}
	}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := i.db.Exec(`
		INSERT INTO resources(kind,id,parent_id,data,created_at)
		SELECT 'repository_host_key', ?, '', ?, ?
		WHERE ? = COALESCE((SELECT json_extract(data, '$.fingerprint') FROM resources WHERE kind='repository_host_key' AND id=?), '')
		ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`,
		endpoint, string(data), resourceCreatedAt(), previousFingerprint, endpoint,
	)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count == 0 {
		return stale
	}
	return nil
}
