package remote

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestExecutionPathsMatchTheSelectedEnvironment(t *testing.T) {
	for _, test := range []struct {
		name, level, runner string
		guest               bool
	}{
		{"managed VM", SandboxMicroVM, "", true},
		{"custom VM", SandboxMicroVM, "/usr/bin/custom-runner", false},
		{"bubblewrap", "bwrap", "", false},
		{"host or approved outsideSandbox", SandboxDisabled, "", false},
		{"unobserved automatic selection", SandboxAutomatic, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := PathsForExecution(domain.Machine{MicroVMCommand: test.runner}, test.level, "/host/project", "/host/scratch")
			expected := ExecutionPaths{Workspace: "/host/project", TemporaryDirectory: "/host/scratch"}
			if test.guest {
				expected = ExecutionPaths{Workspace: ManagedGuestWorkspace, TemporaryDirectory: ManagedGuestTemporaryDirectory}
			}

			if paths != expected {
				t.Fatalf("paths = %+v, want %+v", paths, expected)
			}
		})
	}
}

func TestHostExecutionUsesItsOwnProjectAndScratch(t *testing.T) {
	workspace := t.TempDir()
	scratch := filepath.Join(t.TempDir(), "scratch 'quoted' $literal")
	for _, outside := range []bool{false, true} {
		command, level, err := Sandbox(context.Background(), nil, domain.Machine{Sandbox: SandboxDisabled}, Execution{
			Workspace: workspace, TemporaryDirectory: scratch,
			Command: `printf '%s\n%s' "$PWD" "$TMPDIR"`, OutsideSandbox: outside,
		})
		if err != nil || level != SandboxDisabled {
			t.Fatal(level, err)
		}

		output, err := exec.Command("/bin/sh", "-c", command).CombinedOutput()
		if err != nil || string(output) != workspace+"\n"+scratch {
			t.Fatalf("host environment = %q, %v", output, err)
		}
	}

	wrapped := bubblewrapCommand(Execution{Workspace: workspace, TemporaryDirectory: scratch, Command: "true"})
	if !strings.Contains(wrapped, "--setenv TMPDIR "+Quote(scratch)) {
		t.Fatal("bubblewrap did not receive its managed scratch directory")
	}
}

func TestWorkspaceRelativeDoesNotDependOnTheMountPoint(t *testing.T) {
	for _, requested := range []string{"backend/file.go", "/host/project/backend/file.go", ManagedGuestWorkspace + "/backend/file.go"} {
		relative, err := WorkspaceRelative("/host/project", requested)
		if err != nil || relative != "./backend/file.go" {
			t.Fatal(relative, err)
		}
	}

	for _, requested := range []string{"../secret", "/host/project-other/file", ManagedGuestWorkspace + "/../secret", "file\x00"} {
		if _, err := WorkspaceRelative("/host/project", requested); err == nil {
			t.Fatalf("escaped project: %q", requested)
		}
	}
}
