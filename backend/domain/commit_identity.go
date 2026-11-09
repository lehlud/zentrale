package domain

import (
	"errors"
	"net/mail"
	"strings"
)

// CommitIdentity is the author and committer identity for agent-created commits.
type CommitIdentity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func NormalizeCommitIdentity(identity CommitIdentity) (CommitIdentity, error) {
	identity.Name = strings.TrimSpace(identity.Name)
	identity.Email = strings.TrimSpace(identity.Email)

	if identity.Name == "" || len(identity.Name) > 200 || strings.ContainsAny(identity.Name, "<>\x00\r\n") {
		return identity, errors.New("commit name must contain 1–200 characters without line breaks or angle brackets")
	}

	address, err := mail.ParseAddress(identity.Email)
	if err != nil ||
		address.Address != identity.Email ||
		address.Name != "" ||
		len(identity.Email) > 254 ||
		strings.ContainsAny(identity.Email, "<>\x00\r\n") {
		return identity, errors.New("commit email must be a valid email address")
	}

	return identity, nil
}
