package datastore

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// ProjectWorkspaceToken identifies a project's storage within this installation,
// including shared storage reached through different machine aliases or pools.
func (instance *Instance) ProjectWorkspaceToken(projectID string) (string, error) {
	if instance == nil || instance.Closed() {
		return "", ErrClosed
	}
	_, err := instance.db.Exec(
		"INSERT INTO settings(name,value) VALUES ('workspace_identity',?) ON CONFLICT(name) DO NOTHING",
		rand.Text(),
	)
	if err != nil {
		return "", err
	}
	secret, err := instance.Setting("workspace_identity")
	if err != nil {
		return "", err
	}
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write([]byte(projectID))
	return hex.EncodeToString(signature.Sum(nil)), nil
}
