package gitforge

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestGitHubHostKeysAreValidatedScopedAndCached(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(ssh.MarshalAuthorizedKey(key))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/meta" || request.Header.Get("Authorization") != "" {
			t.Error("host-key discovery used the wrong endpoint or sent an OAuth token", request)
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"ssh_keys": []string{encoded}})
	}))
	defer server.Close()
	client := NewClient()
	client.GitHubAPIURL = server.URL
	for _, test := range []struct{ repository, host string }{
		{"git@github.com:owner/repo.git", "github.com"},
		{"ssh://git@github.com:22/owner/repo.git", "github.com"},
		{"ssh://git@ssh.github.com:443/owner/repo.git", "[ssh.github.com]:443"},
	} {
		known, err := client.SSHKnownHosts(context.Background(), test.repository)
		if err != nil || known != test.host+" "+encoded {
			t.Fatal(test.repository, known, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("host keys were fetched on every Git operation", calls.Load())
	}
	for _, repository := range []string{
		"git@gitlab.com:owner/repo.git", "git@github.com.evil.test:owner/repo.git",
		"git@example.test:owner/repo.git", "ssh://git@github.com:2222/owner/repo.git",
		"git@[::1]:owner/repo.git", "ssh://git@[::1]:2222/owner/repo.git",
		"https://github.com/owner/repo.git",
	} {
		known, err := client.SSHKnownHosts(context.Background(), repository)
		if err != nil || known != "" || calls.Load() != 1 {
			t.Fatal("GitHub host keys were applied to another host or port", repository, known, err)
		}
	}
	client.hostKeyAt = time.Now().Add(-2 * time.Hour)
	server.Close()
	if known, err := client.SSHKnownHosts(context.Background(), "git@github.com:owner/repo.git"); err == nil || known != "" {
		t.Fatal("expired host keys silently survived a failed HTTPS refresh", known, err)
	}
}

func TestGitHubHostKeysFailClosedForInvalidMetadata(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"ssh_keys":[]}`, `{"ssh_keys":["invalid"]}`,
		`{"ssh_keys":["ssh-ed25519 AAAA"]}`, `not JSON`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = response.Write([]byte(body))
			}))
			defer server.Close()
			client := NewClient()
			client.GitHubAPIURL = server.URL
			known, err := client.SSHKnownHosts(context.Background(), "git@github.com:owner/repo.git")
			if err == nil || known != "" || !strings.Contains(err.Error(), "Git") {
				t.Fatal(known, err)
			}
		})
	}
}
