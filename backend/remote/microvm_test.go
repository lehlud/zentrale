package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/ulikunitz/xz"
)

func TestManagedVMSSHCommandFitsShellArgumentAndPreservesConfiguration(t *testing.T) {
	machine := domain.Machine{
		GPUDevices: []string{"0000:65:00.0"},
		Storage:    []domain.StorageVolume{{ID: "quoted", Path: "/srv/project's files"}},
	}
	command := microVMCommand("run", machine)
	// Measure the quoted SSH argument and leave half of Linux's 128 KiB
	// limit available for machine configuration and the invocation path.
	if size := len(shellCommand(command)); size >= 64*1024 {
		t.Fatalf("runner command is too large for nested SSH shell quoting: %d bytes", size)
	}
	match := regexp.MustCompile(`base64.b85decode\('([^']+)'\)`).FindStringSubmatch(strings.ReplaceAll(command, `'"'"'`, `'`))
	if len(match) != 2 {
		t.Fatal("missing compressed runner bundle")
	}
	decode := "python3 -I -c " + Quote("import base64,sys; sys.stdout.buffer.write(base64.b85decode(sys.argv[1]))") + " " + Quote(match[1])
	compressed, err := exec.Command("/bin/sh", "-c", shellCommand(decode)).Output()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := xz.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Runner        string `json:"runner"`
		Memory        string `json:"memory"`
		Configuration struct {
			NVIDIA string `json:"nvidia"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(payload, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Runner != managedMicroVMRunner || bundle.Memory != managedMicroVMMemory || bundle.Configuration.NVIDIA != managedNVIDIA {
		t.Fatal("compression changed trusted runner or machine configuration")
	}
	// Run the actual bootstrap and inspect the runtime configuration before
	// executing the runner. The per-machine options must survive shell quoting.
	runner := Quote(`exec(compile(bundle['runner'],'<zentrale-microvm>','exec'))`)
	inspect := strings.Replace(command, runner[1:len(runner)-1], "print(sys.argv[2])", 1)
	if inspect == command {
		t.Fatal("runner bootstrap was not found")
	}
	sent, err := exec.Command("/bin/sh", "-c", shellCommand(inspect)).Output()
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(sent, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(microVMConfiguration(machine)), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("runner bootstrap changed per-machine configuration")
	}
}

func TestPythonBase85HandlesZeroGroupsAndPartialBlocks(t *testing.T) {
	for size := 0; size < 12; size++ {
		for _, fill := range []byte{0, 1, 255} {
			data := bytes.Repeat([]byte{fill}, size)
			decoded, err := exec.Command("python3", "-I", "-c", "import base64,sys; sys.stdout.buffer.write(base64.b85decode(sys.argv[1]))", pythonBase85(data)).Output()
			if err != nil || !bytes.Equal(decoded, data) {
				t.Fatalf("Python Base85 roundtrip failed for size=%d fill=%d: %v", size, fill, err)
			}
		}
	}
}

func TestManagedMicroVMReleaseHasVerifiedArtifacts(t *testing.T) {
	type artifact struct {
		URL     string `json:"url"`
		SHA256  string `json:"sha256"`
		Source  string `json:"source"`
		License string `json:"license"`
		Package string `json:"package"`
	}

	var release struct {
		Version string   `json:"version"`
		Runtime artifact `json:"runtime"`
		Busybox artifact `json:"busybox"`
		Kernel  artifact `json:"kernel"`
		Fedora  artifact `json:"fedora"`
		Alpine  struct {
			Repository string `json:"repository"`
			Keys       map[string]struct {
				N string `json:"n"`
				E int    `json:"e"`
			} `json:"keys"`
		} `json:"alpine"`
		NVIDIA struct {
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
		release.Runtime, release.Fedora, release.NVIDIA.artifact,
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
	if release.Alpine.Repository != "https://dl-cdn.alpinelinux.org/alpine/v3.22/main/x86_64" || len(release.Alpine.Keys) == 0 {
		t.Fatal("Alpine packages require the official versioned repository and pinned signing keys")
	}
	for name, key := range release.Alpine.Keys {
		modulus, err := hex.DecodeString(key.N)
		if err != nil || len(modulus) != 512 || key.E != 65537 || !strings.HasSuffix(name, ".rsa.pub") {
			t.Fatal("invalid pinned Alpine repository signing key")
		}
	}
	for name, entry := range map[string]artifact{
		"busybox-static": release.Busybox, "linux-virt": release.Kernel,
		"linux-virt-dev": release.NVIDIA.Headers, "musl": release.NVIDIA.Musl,
	} {
		if entry.Package != name || entry.URL != "" || entry.SHA256 != "" || entry.Source == "" || entry.License == "" {
			t.Fatalf("Alpine package must resolve from the signed repository index: %+v", entry)
		}
	}

	if len(SandboxImplementationID()) != 64 {
		t.Fatal("sandbox observation identity is missing")
	}
}

// Keep the byte order of the original concatenation when refactoring the hash input.
func TestMicroVMRunnerHashPreservesConcatenation(t *testing.T) {
	machine := WithMachineDefaults(domain.Machine{})
	options, err := json.Marshal(map[string]any{
		"gpu":        GPUEnabled(machine),
		"gpuDevices": machine.GPUDevices,
		"storage":    machine.Storage,
	})
	if err != nil {
		t.Fatal(err)
	}

	originalInput := managedMicroVMRunner +
		projectScratchScript +
		managedMicroVMMemory +
		managedMicroVMGuest +
		managedMicroVMInit +
		managedMicroVMStreams +
		managedVFIO +
		managedNVIDIA +
		terminalRelay +
		string(managedMicroVMRelease) +
		string(options)

	digest := sha256.Sum256([]byte(originalInput))
	expectedHash := hex.EncodeToString(digest[:])

	var configuration struct {
		RunnerHash string `json:"runnerHash"`
	}
	if err := json.Unmarshal([]byte(microVMConfiguration()), &configuration); err != nil {
		t.Fatal(err)
	}

	if configuration.RunnerHash != expectedHash {
		t.Fatalf("runner hash changed: got %q, want %q", configuration.RunnerHash, expectedHash)
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
