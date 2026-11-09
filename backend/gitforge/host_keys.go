package gitforge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHKnownHosts obtains GitHub's keys from its HTTPS metadata endpoint, scoped
// to the exact SSH host. Other providers keep using explicitly trusted keys.
func (client *Client) SSHKnownHosts(ctx context.Context, repositoryURL string) (string, error) {
	address := repositoryURL
	if !strings.HasPrefix(address, "ssh://") {
		_, hostPath, ok := strings.Cut(address, "@")
		if !ok || !strings.HasPrefix(strings.ToLower(hostPath), "github.com:") {
			return "", nil
		}

		userHost, path, ok := strings.Cut(address, ":")
		if !ok {
			return "", nil
		}
		address = "ssh://" + userHost + "/" + path
	}

	parsed, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("parse repository SSH address: %w", err)
	}

	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if parsed.Scheme != "ssh" || !((host == "github.com" && (port == "" || port == "22")) ||
		(host == "ssh.github.com" && port == "443")) {
		return "", nil
	}

	keys, err := client.githubHostKeys(ctx)
	if err != nil {
		return "", err
	}

	if port != "" && port != "22" {
		host = "[" + host + "]:" + port
	}

	var known strings.Builder
	for _, key := range keys {
		known.WriteString(host + " ")
		known.Write(ssh.MarshalAuthorizedKey(key))
	}

	return known.String(), nil
}

func (client *Client) githubHostKeys(ctx context.Context) ([]ssh.PublicKey, error) {
	client.hostKeyMu.Lock()
	keys, checked := client.hostKeys, client.hostKeyAt
	client.hostKeyMu.Unlock()
	if len(keys) > 0 && time.Since(checked) < time.Hour {
		return keys, nil
	}

	var metadata struct {
		Keys []string `json:"ssh_keys"`
	}
	endpoint := strings.TrimRight(client.GitHubAPIURL, "/") + "/meta"
	if err := client.request(ctx, http.MethodGet, endpoint, "", nil, false, &metadata); err != nil {
		return nil, fmt.Errorf("load GitHub SSH host keys: %w", err)
	}
	if len(metadata.Keys) == 0 {
		return nil, errors.New("GitHub metadata contains no SSH host keys")
	}

	keys = make([]ssh.PublicKey, 0, len(metadata.Keys))
	for _, encoded := range metadata.Keys {
		key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(encoded))
		if err != nil || len(options) > 0 || len(strings.TrimSpace(string(rest))) > 0 {
			return nil, errors.New("GitHub metadata contains an invalid SSH host key")
		}
		keys = append(keys, key)
	}

	client.hostKeyMu.Lock()
	client.hostKeys, client.hostKeyAt = keys, time.Now()
	client.hostKeyMu.Unlock()

	return keys, nil
}
