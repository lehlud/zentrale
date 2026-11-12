package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestVMPreflightRetriesOnlyRecognizedStartupFailures(t *testing.T) {
	for _, test := range []struct {
		reason string
		status int
		retry  bool
	}{
		{"package_install_timeout", 76, true},
		{"package_install_failed", 76, true},
		{"startup_timeout", 76, true},
		{"unknown_failure", 76, false},
		{"package_install_timeout", 1, false},
	} {
		t.Run(fmt.Sprintf("%s/exit%d", test.reason, test.status), func(t *testing.T) {
			var prepares atomic.Int32
			shell := shellPeerHandler("/bin/sh", os.Environ())
			engine, main, _ := executionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
				if !strings.Contains(command, "<zentrale-microvm>") {
					return shell(command, input, stdout, stderr)
				}
				command = strings.ReplaceAll(command, `'"'"'`, `'`)
				if strings.Contains(command, " 'select'") || strings.Contains(command, " 'ensure'") {
					fmt.Fprintln(stdout, "microvm")
					return 0
				}
				io.Copy(io.Discard, input)
				if strings.Contains(command, " 'stage'") {
					fmt.Fprintln(stdout, "/tmp/test-vm/session/requests/0123456789abcdef0123456789abcdef")
					return 0
				}
				if strings.Contains(command, " 'prepare'") && prepares.Add(1) == 1 {
					fmt.Fprintln(stderr, "Installing 129/130")
					fmt.Fprintf(stdout, "ZENTRALE_VM_STARTUP_FAILED {\"reason\":%q}\n", test.reason)
					return test.status
				}
				fmt.Fprint(stdout, `{"os":"fedora","version":"45"}`)
				return 0
			})
			check := engine.CheckAgentConnection(context.Background(), main.ID)
			expected := int32(1)
			if test.retry {
				expected = 2
			}
			if check.Connected != test.retry || prepares.Load() != expected {
				t.Fatalf("connected=%v prepares=%d error=%s", check.Connected, prepares.Load(), check.Error)
			}
		})
	}
}

func TestVMPreflightRecoversAutomaticallyAndAccountsForSharedJobs(t *testing.T) {
	var prepares, stops atomic.Int32
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, main, project := executionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "<zentrale-microvm>") {
			command = strings.ReplaceAll(command, `'"'"'`, `'`)
			if strings.Contains(command, " 'select'") || strings.Contains(command, " 'ensure'") {
				fmt.Fprintln(stdout, "microvm")
				return 0
			}
			io.Copy(io.Discard, input)
			if strings.Contains(command, " 'stage'") {
				fmt.Fprintln(stdout, "/tmp/test-vm/session/requests/0123456789abcdef0123456789abcdef")
				return 0
			}
			if strings.Contains(command, " 'prepare'") && prepares.Add(1) == 1 {
				fmt.Fprintln(stdout, `ZENTRALE_VM_RECOVERY_REQUIRED {"reason":"unresponsive"}`)
				return 75
			}
			if strings.Contains(command, " 'stop'") {
				stops.Add(1)
			}
			fmt.Fprint(stdout, `{"os":"fedora","version":"45"}`)
			return 0
		}
		return shell(command, input, stdout, stderr)
	})
	child, err := engine.Create(project.ID, "Worker", main.ProviderID, "approver-provider", main.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cancelled atomic.Bool
	affected := &runningJob{sandbox: remote.SandboxMicroVM, job: domain.Job{AgentID: child.ID, MachineID: project.MachineIDs[0]}, cancel: func() { cancelled.Store(true) }}
	unrelated := &runningJob{sandbox: remote.SandboxMicroVM, job: domain.Job{AgentID: child.ID, MachineID: "other-host"}, cancel: func() { t.Error("another host's job was cancelled") }}
	otherConversation := &runningJob{sandbox: remote.SandboxMicroVM, job: domain.Job{AgentID: "other-conversation", MachineID: project.MachineIDs[0]}, cancel: func() { t.Error("another conversation's job was cancelled") }}
	engine.mu.Lock()
	engine.jobs["affected"], engine.jobs["unrelated"] = affected, unrelated
	engine.jobs["other-conversation"] = otherConversation
	engine.mu.Unlock()
	defer func() {
		engine.mu.Lock()
		delete(engine.jobs, "affected")
		delete(engine.jobs, "unrelated")
		delete(engine.jobs, "other-conversation")
		engine.mu.Unlock()
	}()
	check := engine.CheckAgentConnection(context.Background(), main.ID)
	if !check.Connected {
		t.Fatalf("recovery reached the user as an error: %s", check.Error)
	}
	if prepares.Load() != 2 || stops.Load() != 1 || !cancelled.Load() {
		t.Fatalf("prepare=%d stop=%d cancelled=%v", prepares.Load(), stops.Load(), cancelled.Load())
	}
	affected.mu.Lock()
	reason := affected.terminationReason
	affected.mu.Unlock()
	if !strings.Contains(reason, "automatically recovered") {
		t.Fatalf("missing interruption reason: %s", reason)
	}
}

func TestStagingRecoversBeforeStartingTheRequestedCommand(t *testing.T) {
	var stages, stops atomic.Int32
	engine, main, project := executionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		command = strings.ReplaceAll(command, `'"'"'`, `'`)
		if strings.Contains(command, " 'ensure'") {
			fmt.Fprintln(stdout, "microvm")
			return 0
		}
		io.Copy(io.Discard, input)
		if strings.Contains(command, " 'stage'") {
			if stages.Add(1) == 1 {
				fmt.Fprintln(stdout, `ZENTRALE_VM_RECOVERY_REQUIRED {"reason":"unresponsive"}`)
				return 75
			}
			fmt.Fprintln(stdout, "/tmp/test-vm/session/requests/0123456789abcdef0123456789abcdef")
		} else if strings.Contains(command, " 'stop'") {
			stops.Add(1)
			fmt.Fprint(stdout, `{}`)
		} else {
			t.Error("unexpected command action")
			return 1
		}
		return 0
	})
	release := engine.useEnvironment(main.ID)
	defer release()
	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	machine.Sandbox = remote.SandboxMicroVM
	connection, err := engine.Remote.Connector.Dial(context.Background(), machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	command, level, err := engine.sandbox(context.Background(), connection, machine, remote.Execution{AgentID: main.ID, ProjectID: project.ID, Workspace: project.Directory, Command: "printf ready"})
	if err != nil || level != remote.SandboxMicroVM || !strings.HasPrefix(command, "exec ") || stages.Load() != 2 || stops.Load() != 1 {
		t.Fatalf("stage recovery: stages=%d stops=%d level=%s error=%v", stages.Load(), stops.Load(), level, err)
	}
}
