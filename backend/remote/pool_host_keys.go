package remote

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type poolHostTrust struct {
	verify         ssh.HostKeyCallback
	keys           []ssh.PublicKey
	hasAuthorities bool
}

type PoolHostKeyError struct {
	Host  string
	Cause error
}

func (err *PoolHostKeyError) Error() string {
	return fmt.Sprintf("pool known_hosts verification failed for %s: %v", err.Host, err.Cause)
}

func (err *PoolHostKeyError) Unwrap() error {
	return err.Cause
}

// Use the standard OpenSSH verifier, including hostname patterns, certificate
// principals, validity, signatures, hashed hosts and @revoked entries. The SDK
// accepts file paths; its returned callback owns parsed data, not an open file.
func parsePoolKnownHosts(contents string) (*poolHostTrust, error) {
	if len(contents) > 1<<20 {
		return nil, errors.New("pool known_hosts exceeds 1 MiB")
	}

	if strings.TrimSpace(contents) == "" {
		return nil, nil
	}

	file, err := os.CreateTemp("", "zentrale-known-hosts-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())

	if _, err := file.WriteString(contents); err != nil {
		_ = file.Close()
		return nil, err
	}

	if err := file.Close(); err != nil {
		return nil, err
	}

	verify, err := knownhosts.New(file.Name())
	if err != nil {
		return nil, fmt.Errorf("pool known_hosts: %s", strings.ReplaceAll(err.Error(), file.Name(), "YAML"))
	}

	policy := &poolHostTrust{verify: verify}
	scanner := bufio.NewScanner(strings.NewReader(contents))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		marker, _, key, _, _, err := ssh.ParseKnownHosts([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("pool known_hosts: %w", err)
		}

		if marker == "cert-authority" {
			policy.hasAuthorities = true
		} else if marker == "" {
			policy.keys = append(policy.keys, key)
		}
	}

	return policy, scanner.Err()
}

// Offer algorithms for keys actually pinned to this address. A server may also
// offer a certificate whose CA is unknown; a matching raw-key pin still works.
func (policy *poolHostTrust) algorithms(address string) []string {
	allowed := map[string]bool{}
	remote := &net.TCPAddr{}

	for _, key := range policy.keys {
		if policy.verify(address, remote, key) == nil {
			allowed[key.Type()] = true
		}
	}

	algorithms := []string{}
	for _, algorithm := range ssh.SupportedAlgorithms().HostKeys {
		if allowed[algorithm] ||
			allowed[ssh.KeyAlgoRSA] &&
				(algorithm == ssh.KeyAlgoRSASHA256 ||
					algorithm == ssh.KeyAlgoRSASHA512) {
			algorithms = append(algorithms, algorithm)
		}
	}

	if policy.hasAuthorities {
		for _, algorithm := range ssh.SupportedAlgorithms().HostKeys {
			if strings.Contains(algorithm, "-cert-") {
				algorithms = append(algorithms, algorithm)
			}
		}
	}

	if len(algorithms) == 0 {
		// Unknown and revoked hosts must reach the verifier and fail closed.
		return ssh.SupportedAlgorithms().HostKeys
	}

	return algorithms
}

func (connector *Connector) hostPolicy(machineID string) (*poolHostTrust, error) {
	poolID, _, pooled := strings.Cut(machineID, "/")
	if !pooled {
		return nil, nil
	}

	var pool domain.Pool
	if err := connector.Store.Load("pool", poolID, &pool); err != nil {
		return nil, err
	}

	return parsePoolKnownHosts(pool.KnownHosts)
}

func (connector *Connector) verifyHostIdentity(identity verifiedHostIdentity) error {
	machine, err := connector.Machine(identity.machineID)
	if err != nil {
		return err
	}

	if net.JoinHostPort(machine.Host, fmt.Sprint(machine.Port)) != identity.address {
		return errors.New("SSH host configuration changed")
	}

	policy, err := connector.hostPolicy(identity.machineID)
	if err != nil {
		return err
	}

	if policy != nil {
		if err := policy.verify(identity.address, identity.remote, identity.key); err != nil {
			return &PoolHostKeyError{Host: identity.address, Cause: err}
		}

		return nil
	}

	trusted, err := connector.Store.IsTrustedSSHKey(identity.key.Marshal())
	if err != nil {
		return err
	}

	if !trusted {
		return &HostKeyError{
			identity.address,
			ssh.FingerprintSHA256(identity.key),
			strings.TrimSpace(string(ssh.MarshalAuthorizedKey(identity.key))),
		}
	}

	return nil
}
