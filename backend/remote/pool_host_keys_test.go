package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func hostTrustSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}

	return signer
}

func hostCertificate(t *testing.T, authority, host ssh.Signer, principal string) *ssh.Certificate {
	t.Helper()
	cert := &ssh.Certificate{
		Key:      host.PublicKey(),
		CertType: ssh.HostCert,
		KeyId:    "host-test",

		ValidPrincipals: []string{principal},

		ValidAfter:  uint64(time.Now().Add(-time.Minute).Unix()),
		ValidBefore: uint64(time.Now().Add(time.Hour).Unix()),
	}
	if err := cert.SignCert(rand.Reader, authority); err != nil {
		t.Fatal(err)
	}

	return cert
}

func publicHostKey(key ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func TestPoolKnownHostsCertificateValidation(t *testing.T) {
	authority, host := hostTrustSigner(t), hostTrustSigner(t)
	caLine := "@cert-authority *.cip.example.org,!excluded.cip.example.org " + publicHostKey(authority.PublicKey()) + "\n"
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}

	for _, name := range []string{
		"valid",
		"wrong-host",
		"excluded-host",
		"wrong-principal",
		"expired",
		"future",
		"wrong-ca",
		"invalid-signature",
		"user-certificate",
		"revoked-ca",
		"revoked-host",
	} {
		t.Run(name, func(t *testing.T) {
			cert := hostCertificate(t, authority, host, "worker.cip.example.org")
			contents, address := caLine, "worker.cip.example.org:22"

			switch name {
			case "wrong-host":
				address = "worker.other.example.org:22"
			case "excluded-host":
				address = "excluded.cip.example.org:22"
			case "wrong-principal":
				cert.ValidPrincipals = []string{"other.cip.example.org"}
			case "expired":
				cert.ValidBefore = uint64(time.Now().Add(-time.Second).Unix())
			case "future":
				cert.ValidAfter = uint64(time.Now().Add(time.Minute).Unix())
			case "wrong-ca":
				cert = hostCertificate(t, hostTrustSigner(t), host, "worker.cip.example.org")
			case "invalid-signature":
				cert.Signature.Blob[0] ^= 1
			case "user-certificate":
				cert.CertType = ssh.UserCert
			case "revoked-ca":
				contents += "@revoked * " + publicHostKey(authority.PublicKey()) + "\n"
			case "revoked-host":
				contents += "@revoked * " + publicHostKey(host.PublicKey()) + "\n"
			}

			// Re-sign altered certificate fields so the relevant validation,
			// rather than merely a stale signature, rejects the certificate.
			if name == "wrong-principal" ||
				name == "expired" ||
				name == "future" ||
				name == "user-certificate" {
				if err := cert.SignCert(rand.Reader, authority); err != nil {
					t.Fatal(err)
				}
			}

			policy, err := parsePoolKnownHosts(contents)
			if err != nil {
				t.Fatal(err)
			}

			err = policy.verify(address, remote, cert)
			if (err == nil) != (name == "valid") {
				t.Fatalf("certificate verdict for %s: %v", name, err)
			}
		})
	}
}

func TestPoolKnownHostsRawKeysPortsHashesAndMalformedImports(t *testing.T) {
	first, second := hostTrustSigner(t), hostTrustSigner(t)
	address := "worker.example.org:2222"
	contents := knownhosts.Line([]string{address}, first.PublicKey()) + "\n" +
		knownhosts.Line([]string{
			knownhosts.HashHostname(knownhosts.Normalize(address)),
		}, second.PublicKey()) + "\n"

	policy, err := parsePoolKnownHosts(contents)
	if err != nil {
		t.Fatal(err)
	}

	remote := &net.TCPAddr{}

	for _, key := range []ssh.PublicKey{first.PublicKey(), second.PublicKey()} {
		if err := policy.verify(address, remote, key); err != nil {
			t.Fatal("raw pinned host was rejected", err)
		}

		if err := policy.verify("worker.example.org:22", remote, key); err == nil {
			t.Fatal("host pin escaped its port")
		}
	}

	for _, value := range []string{
		"invalid",
		"@unknown host ssh-ed25519 x",
		"@cert-authority *.example.org ssh-ed25519 invalid",
		strings.Repeat("x", (1<<20)+1),
	} {
		configuration := validPool + "known_hosts: |\n  " + value + "\n"
		if _, err := ParsePool(configuration); err == nil {
			t.Fatal("malformed known_hosts was accepted")
		}
	}
}

func TestPoolHostTrustIsScopedStrictAndRecheckedForCachedConnections(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })
	login, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}

	authority, host := hostTrustSigner(t), hostTrustSigner(t)
	cert := hostCertificate(t, authority, host, "127.0.0.1")
	signer, err := ssh.NewCertSigner(cert, host)
	if err != nil {
		t.Fatal(err)
	}

	server := newSSHFixtureWithSigners(t, "developer\n", signer, host)
	machine := server.machine("member", login.ID)
	address := net.JoinHostPort(machine.Host, formatPort(machine.Port))
	caLine := "@cert-authority " + knownhosts.Normalize(address) + " " + publicHostKey(authority.PublicKey()) + "\n"
	pool := domain.Pool{ID: "pool", MaxMachines: 1, Machines: []domain.Machine{machine}, KnownHosts: caLine}
	if err := store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}

	connector := &Connector{Store: store}
	connection, err := connector.Dial(context.Background(), "pool/member")
	if err != nil {
		t.Fatal("trusted host certificate did not connect", err)
	}
	defer connection.Close()

	if _, certified := connection.shared.identities[0].key.(*ssh.Certificate); !certified {
		t.Fatal("CA connection did not negotiate a host certificate")
	}

	if err := connector.VerifyTrust(connection); err != nil {
		t.Fatal(err)
	}

	trusted, _ := store.TrustedKeys()
	if len(trusted) != 0 {
		t.Fatal("pool trust leaked into global trust")
	}

	outside := machine
	outside.ID = "outside"

	if err := store.Save("machine", outside.ID, "", outside); err != nil {
		t.Fatal(err)
	}

	if other, err := connector.Dial(context.Background(), outside.ID); err == nil {
		other.Close()
		t.Fatal("pool CA trusted an unrelated standalone machine")
	}

	// A matching raw-key pin must work even when this server also offers a
	// certificate and another unrelated host in the same pool uses a CA.
	pool.KnownHosts = knownhosts.Line([]string{address}, host.PublicKey()) + "\n" +
		"@cert-authority other.example.org " + publicHostKey(authority.PublicKey()) + "\n"

	if err := store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}

	pinned, err := connector.Dial(context.Background(), "pool/member")
	if err != nil {
		t.Fatal("pinned raw key did not connect", err)
	}
	defer pinned.Close()

	if _, certified := pinned.shared.identities[0].key.(*ssh.Certificate); certified {
		t.Fatal("raw-key pin incorrectly negotiated a certificate")
	}

	// Global trust must not bypass explicit pool restrictions.
	if err := store.TrustSSHKey(cert.Marshal()); err != nil {
		t.Fatal(err)
	}

	if err := store.TrustSSHKey(host.PublicKey().Marshal()); err != nil {
		t.Fatal(err)
	}

	pool.KnownHosts = caLine + "@revoked * " + publicHostKey(authority.PublicKey()) + "\n"

	if err := store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}

	var rejected *PoolHostKeyError
	if err := connector.VerifyTrust(connection); !errors.As(err, &rejected) {
		t.Fatal("cached connection ignored a revoked CA", err)
	}

	if current, err := connector.Dial(context.Background(), "pool/member"); !errors.As(err, &rejected) {
		if current != nil {
			current.Close()
		}

		t.Fatal("global trust bypassed pool CA revocation", err)
	}

	fallback := newSSHFixture(t, "developer\n")
	other := fallback.machine("fallback", login.ID)
	pool.Machines = append(pool.Machines, other)
	pool.KnownHosts += knownhosts.Line([]string{
		net.JoinHostPort(other.Host, formatPort(other.Port)),
	}, fallback.hostKey) + "\n"

	if err := store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(connector)
	if current, _, err := manager.Connect(context.Background(), domain.Project{
		PoolIDs: []string{
			pool.ID,
		},
	}, "owner", "", nil); !errors.As(err, &rejected) {
		if current != nil {
			current.Close()
		}

		t.Fatal("failover hid a revoked identity by selecting another host", err)
	}
}
