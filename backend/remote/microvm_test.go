package remote

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestManagedVMSSHCommandFitsShellArgumentAndPreservesConfiguration(t *testing.T) {
	command := microVMCommand("run", domain.Machine{})
	if len(command) >= 48*1024 {
		t.Fatalf("runner command is too large for nested SSH shell quoting: %d bytes", len(command))
	}
	match := regexp.MustCompile(`base64.b64decode\('([A-Za-z0-9+/=]+)'\)`).FindStringSubmatch(strings.ReplaceAll(command, `'"'"'`, `'`))
	if len(match) != 2 {
		t.Fatal("missing compressed runner bundle")
	}
	compressed, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Runner        string `json:"runner"`
		Configuration struct {
			NVIDIA string `json:"nvidia"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(payload, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Runner != managedMicroVMRunner || bundle.Configuration.NVIDIA != managedNVIDIA {
		t.Fatal("compression changed trusted runner or machine configuration")
	}
}

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
		NVIDIA  struct {
			artifact
			Headers artifact `json:"headers"`
			Musl    artifact `json:"musl"`
		} `json:"nvidia"`
	}
	if err := json.Unmarshal(managedMicroVMRelease, &release); err != nil {
		t.Fatal(err)
	}

	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(release.Version) ||
		release.Version == "." ||
		release.Version == ".." {
		t.Fatalf("release version is unsafe as a cache directory: %q", release.Version)
	}

	for _, entry := range []artifact{
		release.Runtime, release.Busybox, release.Kernel, release.Fedora,
		release.NVIDIA.artifact, release.NVIDIA.Headers, release.NVIDIA.Musl,
	} {
		parsed, err := url.Parse(entry.URL)
		if err != nil ||
			parsed.Scheme != "https" ||
			parsed.Host == "" ||
			parsed.User != nil ||
			parsed.RawQuery != "" {
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
		if _, _, err := Sandbox(context.Background(), nil, machine, Execution{
			Workspace: "/project",
			GuestRoot: true,
		}); err == nil {
			t.Fatalf("guestRoot accepted without the managed runner: %+v", machine)
		}
	}

	if _, _, err := Sandbox(
		context.Background(),
		nil,
		domain.Machine{},
		Execution{
			Workspace:      "/project",
			GuestRoot:      true,
			OutsideSandbox: true,
		},
	); err == nil {
		t.Fatal("guestRoot accepted with host execution")
	}
}
