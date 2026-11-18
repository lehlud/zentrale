package datastore

import (
	"encoding/json"
	"errors"
)

type RepositoryHostKey struct {
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
}

// ConfirmRepositoryHostKey stores a pin for a single SSH endpoint. A prompt
// opened before another confirmation cannot overwrite the newer decision.
func (i *Instance) ConfirmRepositoryHostKey(endpoint, previousFingerprint string, key RepositoryHostKey) error {
	if i == nil || i.Closed() {
		return errClosed
	}
	data, err := json.Marshal(key)
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
		return errors.New("repository host key trust changed; retry the connection before confirming")
	}
	return nil
}
