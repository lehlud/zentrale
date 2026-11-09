package remote

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestManagedMicroVMReleaseHasPinnedVerifiedArtifacts(t *testing.T) {
	type artifact struct {
		URL     string `json:"url"`
		SHA256  string `json:"sha256"`
		Source  string `json:"source"`
		License string `json:"license"`
	}
	var release struct {
		Version string   `json:"version"`
		Runtime artifact `json:"runtime"`
		Busybox artifact `json:"busybox"`
		Kernel  artifact `json:"kernel"`
		Fedora  artifact `json:"fedora"`
	}
	if err := json.Unmarshal(managedMicroVMRelease, &release); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(release.Version) || release.Version == "." || release.Version == ".." {
		t.Fatalf("release version is unsafe as a cache directory: %q", release.Version)
	}
	for _, entry := range []artifact{release.Runtime, release.Busybox, release.Kernel, release.Fedora} {
		parsed, err := url.Parse(entry.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" {
			t.Fatalf("artifact needs a fixed HTTPS URL: %+v", entry)
		}
		digest, err := hex.DecodeString(entry.SHA256)
		if err != nil || len(digest) != 32 || entry.Source == "" || entry.License == "" {
			t.Fatalf("missing artifact checksum or provenance: %+v", entry)
		}
	}
	if len(SandboxImplementationID()) != 64 {
		t.Fatal("sandbox observation identity is missing")
	}
}

func TestGuestAdministrationNeverBecomesHostRoot(t *testing.T) {
	for _, machine := range []domain.Machine{
		{Sandbox: SandboxDisabled},
		{Sandbox: "bwrap"},
		{Sandbox: SandboxMicroVM, MicroVMCommand: "/custom/runner"},
	} {
		if _, _, err := Sandbox(context.Background(), nil, machine, Execution{Workspace: "/project", GuestRoot: true}); err == nil {
			t.Fatalf("guestRoot accepted without the managed runner: %+v", machine)
		}
	}
	if _, _, err := Sandbox(context.Background(), nil, domain.Machine{}, Execution{Workspace: "/project", GuestRoot: true, OutsideSandbox: true}); err == nil {
		t.Fatal("guestRoot accepted with host execution")
	}
}
