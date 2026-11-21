package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestExistingRepositoryRepairsHostTrustWithoutRecloning(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	engine, source := fixture.engine, fixture.source
	ctx := context.Background()
	check := func() domain.MachineConnectionCheck {
		return engine.CheckAgentConnection(ctx, fixture.record.ID)
	}
	if result := check(); !result.Connected {
		t.Fatal(result)
	}
	endpoint, err := domain.RepositorySSHAddress(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a checkout prepared under the legacy global trust fallback.
	if err := engine.Store.Remove("repository_host_key", endpoint); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(fixture.directory, "README.md")
	const unsaved = "Local changes waiting to be committed"
	if err := os.WriteFile(readme, []byte(unsaved), 0600); err != nil {
		t.Fatal(err)
	}
	engine.setRunState(fixture.record.ID, "tools", nil)
	result := check()
	challenge := result.HostKey
	if result.Connected || challenge.RepositorySourceID != source.ID ||
		challenge.Host != endpoint || challenge.PublicKey != fixture.server.PublicKey {
		t.Fatalf("reused checkout did not report its Git host: %+v", result)
	}
	if phase := engine.RunState(fixture.record.ID).Phase; phase != "tools" {
		t.Fatalf("manual preparation replaced the running agent's phase: %s", phase)
	}
	if err := engine.ConfirmRepositoryHostKey(source.ID, endpoint, challenge.PublicKey, challenge.PreviousFingerprint); err != nil {
		t.Fatal(err)
	}
	if result := check(); !result.Connected || fixture.clones.Load() != 1 {
		t.Fatalf("could not refresh Git trust in the existing checkout: %+v", result)
	}
	if content, err := os.ReadFile(readme); err != nil || string(content) != unsaved {
		t.Fatalf("recovery changed worktree data: %q, %v", content, err)
	}
	git := func(arguments ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", fixture.directory}, arguments...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("plain Git %s: %s: %v", arguments[0], output, err)
		}
	}
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-am", "Preserve local work")
	git("push", "origin", "HEAD:refs/heads/repaired")
	if result := check(); !result.Connected || fixture.clones.Load() != 1 {
		t.Fatalf("repreparation lost the confirmed key: %+v", result)
	}
	git("push", "origin", "HEAD:refs/heads/repaired-again")
}

func TestRepositoryTrustRetainsOtherAlgorithmsAndReplacesRotatedKey(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	engine, source := fixture.engine, fixture.source
	endpoint, err := domain.RepositorySSHAddress(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	ed, err := repositoryPublicKey(fixture.server.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	// The on-disk representation before support for multiple algorithms.
	if err := engine.Store.Save("repository_host_key", endpoint, "", datastore.RepositoryHostKey{PublicKey: ed.PublicKey, Fingerprint: ed.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	newECDSA := func() datastore.RepositoryHostKey {
		t.Helper()
		key, err := engine.Store.GenerateSSHKey()
		if err != nil {
			t.Fatal(err)
		}
		public, err := remote.PublicKey(key.X509Data)
		if err != nil {
			t.Fatal(err)
		}
		pin, err := repositoryPublicKey(public)
		if err != nil {
			t.Fatal(err)
		}
		return pin
	}
	ecdsa, rotated := newECDSA(), newECDSA()
	if err := engine.ConfirmRepositoryHostKey(source.ID, endpoint, ecdsa.PublicKey, ed.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := engine.ConfirmRepositoryHostKey(source.ID, endpoint, rotated.PublicKey, ed.Fingerprint); err == nil {
		t.Fatal("outdated confirmation replaced the current set of keys")
	}
	if err := engine.ConfirmRepositoryHostKey(source.ID, endpoint, rotated.PublicKey, ecdsa.Fingerprint); err != nil {
		t.Fatal(err)
	}
	known, challenge, err := engine.gitKnownHosts(context.Background(), source)
	if err != nil || challenge.PreviousFingerprint != rotated.Fingerprint ||
		!strings.Contains(known, ed.PublicKey) || !strings.Contains(known, rotated.PublicKey) ||
		strings.Contains(known, ecdsa.PublicKey) {
		t.Fatalf("the stored endpoint pins did not survive rotation: %q, %+v, %v", known, challenge, err)
	}
}

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
			if err := engine.Store.Remove("repository_host_key", endpoint); err != nil {
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
			} else if err := engine.Store.TrustSSHKey(actual); err != nil {
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
				if trusted, err := engine.Store.IsTrustedSSHKey(actual); err != nil || !trusted {
					t.Fatal("confirming Git trust changed global SSH trust", err)
				}
			}
		})
	}
}
