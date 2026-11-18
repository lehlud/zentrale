package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestAutomaticClonePromptsForRepositoryHostKey(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "unknown"
		if changed {
			name = "changed"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newRepositoryFixture(t, nil)
			engine, source := fixture.engine, fixture.source
			endpoint, err := domain.RepositorySSHAddress(source.URL)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := remote.DecodePublicKey(fixture.server.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			prior := ""
			if changed {
				old, err := engine.Store.GenerateSSHKey()
				if err != nil {
					t.Fatal(err)
				}
				oldPublic, err := remote.PublicKey(old.X509Data)
				if err != nil {
					t.Fatal(err)
				}
				if err := engine.ConfirmRepositoryHostKey(source.ID, endpoint, oldPublic, ""); err != nil {
					t.Fatal(err)
				}
				encoded, err := remote.DecodePublicKey(oldPublic)
				if err != nil {
					t.Fatal(err)
				}
				prior = remote.KeyFingerprint(encoded)
			} else if err := engine.Store.DistrustSSHKey(actual); err != nil {
				t.Fatal(err)
			}
			check := func() domain.MachineConnectionCheck {
				return engine.CheckAgentConnection(context.Background(), fixture.record.ID)
			}
			result := check()
			challenge := result.HostKey
			if result.Connected || challenge.RepositorySourceID != source.ID || challenge.Host != endpoint ||
				challenge.PublicKey != fixture.server.PublicKey || challenge.Fingerprint != remote.KeyFingerprint(actual) ||
				challenge.PreviousFingerprint != prior || fixture.clones.Load() != 0 {
				t.Fatalf("clone must stop for the actual host key challenge: %+v", result)
			}
			if err := engine.ConfirmRepositoryHostKey(source.ID, "another.example:22", challenge.PublicKey, prior); err == nil {
				t.Fatal("accepted a key for a different repository endpoint")
			}
			if err := engine.ConfirmRepositoryHostKey(source.ID, endpoint, challenge.PublicKey, prior); err != nil {
				t.Fatal(err)
			}
			if err := engine.ConfirmRepositoryHostKey(source.ID, endpoint, challenge.PublicKey, prior); err == nil {
				t.Fatal("accepted a stale host key confirmation")
			}

			fixture.server.RejectAuthentication(true)
			if result := check(); result.Connected || result.HostKey.PublicKey != "" || !strings.Contains(result.Error, "Permission denied") {
				t.Fatalf("an invalid deploy key must not become a host key prompt: %+v", result)
			}
			fixture.server.RejectAuthentication(false)
			if result := check(); !result.Connected || fixture.clones.Load() != 1 {
				t.Fatalf("accepted host key did not allow clone: %+v", result)
			}
			if !changed {
				if trusted, err := engine.Store.IsTrustedSSHKey(actual); err != nil || trusted {
					t.Fatal("confirming Git trust changed global SSH trust", err)
				}
			}
		})
	}
}
