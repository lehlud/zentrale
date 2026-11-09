package remote

import "github.com/lehlud/zentrale/domain"

// Scope defaults to the command tree rather than changing the SSH user's Git
// configuration or repositories that may also be used outside Zentrale.
func WithCommitIdentity(command string, identity domain.CommitIdentity) string {
	if identity.Name == "" && identity.Email == "" {
		return command
	}

	return "export GIT_AUTHOR_NAME=" + Quote(identity.Name) +
		" GIT_AUTHOR_EMAIL=" + Quote(identity.Email) +
		" GIT_COMMITTER_NAME=" + Quote(identity.Name) +
		" GIT_COMMITTER_EMAIL=" + Quote(identity.Email) + "\n" + command
}
