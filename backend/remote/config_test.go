package remote

import (
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

const validPool = `id: gpu
name: GPU pool
max_machines: 1
shared_home: false
workspace_root: /srv/projects
sandbox: bwrap
single_user: true
machines:
  - id: worker
    name: Worker
    host: example.org
    user: developer
    properties: [GPU, RTX3070]
`

func TestBuiltInMicroVMDoesNotRequireAnExternalRunner(t *testing.T) {
	machine := domain.Machine{
		ID:      "machine",
		Name:    "Machine",
		Host:    "example.org",
		Port:    22,
		User:    "developer",
		KeyID:   1,
		Sandbox: SandboxMicroVM,
	}
	if err := ValidateMachine(machine); err != nil {
		t.Fatalf("built-in MicroVM runner rejected: %v", err)
	}

	for _, mode := range []string{SandboxMicroVM, SandboxAutomatic} {
		machine.Sandbox, machine.MicroVMCommand = mode, "relative/runner"

		if err := ValidateMachine(machine); err == nil {
			t.Fatalf("relative custom runner accepted for %s", mode)
		}
	}

	machine.MicroVMCommand = "/opt/custom-microvm"

	if err := ValidateMachine(machine); err != nil {
		t.Fatalf("custom runner override rejected: %v", err)
	}
}

func TestPoolConfigurationInheritanceAndStrictValidation(t *testing.T) {
	pool, err := ParsePool(validPool)
	if err != nil {
		t.Fatal(err)
	}

	machine := pool.Machines[0]
	if machine.Port != 22 ||
		machine.Sandbox != "bwrap" ||
		!machine.SingleUser ||
		len(machine.Properties) != 2 {
		t.Fatalf("defaults and inheritance: %+v", machine)
	}

	invalid := []string{
		strings.Replace(validPool, "workspace_root: /srv/projects", "stable_directory: /srv/projects", 1),
		strings.Replace(validPool, "    user: developer", "    user: developer\n    ide_port: 8080", 1),
		validPool + "unexpected: true\n",
		strings.Replace(validPool, "max_machines: 1", "max_machines: 0", 1),
		strings.Replace(validPool, "workspace_root: /srv/projects", "workspace_root: relative", 1),
		validPool + "---\nid: another\n",
		validPool + "key_id: 1\n",
		strings.Replace(validPool, "    user: developer", "    key_id: 1\n    user: developer", 1),
		strings.Replace(validPool, "    user: developer", "    key_id: 0\n    user: developer", 1),
	}

	for _, configuration := range invalid {
		if _, err := ParsePool(configuration); err == nil {
			t.Fatal("invalid pool configuration accepted")
		}
	}
}

func TestPoolLimitIsSharedAcrossAgentsAndBackgroundJobs(t *testing.T) {
	manager := NewManager(nil)
	pool := domain.Pool{ID: "pool", MaxMachines: 1}
	if !manager.reserve(pool, "main", "worker-one") {
		t.Fatal("first lease failed")
	}

	if !manager.reserve(pool, "subagent", "worker-one") {
		t.Fatal("sharing the same machine consumed another slot")
	}

	if manager.reserve(pool, "other-agent", "worker-two") {
		t.Fatal("pool limit was enforced per agent instead of globally")
	}

	manager.Hold("main", "job")
	manager.Release("main")
	manager.Release("subagent")

	if manager.reserve(pool, "other-agent", "worker-two") {
		t.Fatal("background job did not retain its pool slot")
	}

	manager.Release("job:job")

	if !manager.reserve(pool, "other-agent", "worker-two") {
		t.Fatal("completed work did not release its pool slot")
	}
}

func TestWorkspaceBoundaryAndSandboxNetworkIsolation(t *testing.T) {
	for _, filename := range []string{"../../etc/passwd", "/etc/passwd", "file\x00name"} {
		if _, err := Workspace("/srv/project", filename); err == nil {
			t.Fatalf("path escaped workspace: %q", filename)
		}
	}

	if _, err := Workspace("/", "file"); err == nil {
		t.Fatal("filesystem root accepted as project")
	}

	execution := Execution{
		Workspace:          "/srv/project",
		TemporaryDirectory: "/tmp/zentrale-agent",
		Command:            "printf 'hello'",
	}

	command := bubblewrapCommand(execution)
	if !strings.Contains(command, "--unshare-all") || strings.Contains(command, "--share-net") {
		t.Fatalf("network allowed by default: %s", command)
	}

	if !strings.Contains(command, "--bind '/tmp/zentrale-agent' '/tmp/zentrale-agent'") {
		t.Fatal("managed temporary directory not preserved")
	}

	execution.Network = true

	if !strings.Contains(bubblewrapCommand(execution), "--share-net") {
		t.Fatal("individually approved network unavailable")
	}
}
