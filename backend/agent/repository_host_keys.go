package agent

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/gitforge"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) gitKnownHosts(ctx context.Context, source domain.Source) (string, domain.SSHHostKey, error) {
	// GitHub publishes host keys over authenticated HTTPS. Retain that authority.
	known, err := engine.GitForge.Client.SSHKnownHosts(ctx, source.URL)
	if err != nil || known != "" {
		return known, domain.SSHHostKey{}, err
	}
	endpoint, err := domain.RepositorySSHAddress(source.URL)
	if err != nil {
		return "", domain.SSHHostKey{}, err
	}
	host, port, _ := net.SplitHostPort(endpoint)
	name := host
	if port != "22" {
		name = "[" + host + "]:" + port
	}
	challenge := domain.SSHHostKey{Host: endpoint, RepositorySourceID: source.ID}
	var trust datastore.RepositoryHostTrust
	if err := engine.Store.Load("repository_host_key", endpoint, &trust); err == nil {
		challenge.PreviousFingerprint = trust.Fingerprint
		var hosts strings.Builder
		for _, pin := range append([]datastore.RepositoryHostKey{trust.RepositoryHostKey}, trust.OtherKeys...) {
			hosts.WriteString(name + " " + pin.PublicKey + "\n")
		}
		return hosts.String(), challenge, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", domain.SSHHostKey{}, err
	}

	// Machine trust has no hostname association. Including those keys also
	// changes OpenSSH's algorithm preference based on unrelated servers.
	return "", challenge, nil
}

func repositoryHostKeyFailure(output string, failure error, configuration map[string]any) error {
	var exit interface{ ExitStatus() int }
	if !errors.As(failure, &exit) || exit.ExitStatus() != 79 {
		return failure
	}
	const marker = "ZENTRALE_REPOSITORY_HOST_KEY "
	// Only the helper's final line is authoritative. Earlier diagnostics may
	// contain text supplied by the remote Git service.
	output = strings.TrimSpace(output)
	line := output[strings.LastIndexByte(output, '\n')+1:]
	value, ok := strings.CutPrefix(line, marker)
	if !ok {
		return failure
	}
	if sources, ok := configuration["sources"].([]map[string]any); ok {
		index, encoded := splitRepositoryHostKeySource(value, len(sources))
		if index < 0 || sources[index]["verifyHostKey"] != true {
			return failure
		}
		value, configuration = encoded, sources[index]
	}
	challenge, _ := configuration["repositoryHostKey"].(domain.SSHHostKey)
	if challenge.RepositorySourceID == "" {
		return failure
	}
	key, err := repositoryPublicKey(value)
	if err != nil {
		return failure
	}
	return &remote.HostKeyError{
		Host: challenge.Host, Fingerprint: key.Fingerprint, PublicKey: key.PublicKey,
		RepositorySourceID: challenge.RepositorySourceID, PreviousFingerprint: challenge.PreviousFingerprint,
	}
}

func splitRepositoryHostKeySource(value string, count int) (int, string) {
	rawIndex, key, found := strings.Cut(value, " ")
	index, err := strconv.Atoi(rawIndex)
	if !found || err != nil || index < 0 || index >= count {
		return -1, ""
	}
	return index, key
}

func repositoryPublicKey(value string) (datastore.RepositoryHostKey, error) {
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(value))
	if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return datastore.RepositoryHostKey{}, errors.New("one SSH host public key expected")
	}
	return datastore.RepositoryHostKey{
		PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), Fingerprint: ssh.FingerprintSHA256(key),
	}, nil
}

func (engine *Engine) ConfirmRepositoryHostKey(sourceID, host, publicKey, previousFingerprint string) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	var source domain.Source
	if err := engine.Store.Load("source", sourceID, &source); err != nil {
		return err
	}
	if err := engine.projectAvailable(source.ProjectID); err != nil {
		return err
	}
	endpoint, err := domain.RepositorySSHAddress(source.URL)
	if err != nil || endpoint != host || gitforge.UsesPublishedSSHHostKeys(endpoint) {
		return errors.New("repository SSH destination changed or uses published host keys; retry the connection")
	}
	key, err := repositoryPublicKey(publicKey)
	if err != nil {
		return err
	}
	return engine.Store.ConfirmRepositoryHostKey(endpoint, previousFingerprint, key)
}
