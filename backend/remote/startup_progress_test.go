package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"golang.org/x/crypto/ssh"
)

func TestNativeTerminalStageSkipsDuplicateEnsureAndStreamsActualBoot(t *testing.T) {
	var stages, ensures atomic.Int32
	release := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	client, _ := commandLifecyclePeer(t, "", func(command string, channel ssh.Channel, _ <-chan *ssh.Request) {
		command = strings.ReplaceAll(command, `'"'"'`, `'`)
		if strings.Contains(command, " 'ensure'") {
			ensures.Add(1)
			fixtureCommandExit(channel, 42)
			return
		}
		if strings.Contains(command, " 'run'") {
			var geometry any
			_ = json.NewDecoder(channel).Decode(&geometry)
			fmt.Fprint(channel, "\x00ZENTRALE_TERMINAL_READY\n")
			fixtureCommandExit(channel, 0)
			return
		}
		if !strings.Contains(command, " 'stage'") {
			fixtureCommandExit(channel, 43)
			return
		}
		stages.Add(1)
		var request struct {
			Execution map[string]any `json:"execution"`
		}
		if err := json.NewDecoder(channel).Decode(&request); err != nil || request.Execution["startupProgress"] != true {
			t.Errorf("missing invocation-local phase request: %+v, %v", request, err)
		}
		// Deliberately split the marker across SSH writes and hold the stage
		// open: the observer must fire before the terminal command is returned.
		fmt.Fprint(channel.Stderr(), "ZENTRALE_STARTUP_PHASE vm_")
		fmt.Fprintln(channel.Stderr(), "starting")
		<-release
		fmt.Fprintln(channel, "/tmp/vm/session/requests/0123456789abcdef0123456789abcdef")
		fixtureCommandExit(channel, 0)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	phases := make(chan string, 1)
	ctx = WithStartupProgress(ctx, func(phase string) {
		if phase == "vm_starting" {
			phases <- phase
		}
	})
	completed := make(chan error, 1)
	go func() {
		terminal, err := StartTerminal(ctx, &Connection{Client: client}, domain.Machine{Sandbox: SandboxMicroVM}, domain.Project{ID: "project", Directory: "/tmp/project"}, EditorTarget{
			AgentID: "agent", TemporaryDirectory: "/tmp/agent", PreparedSandbox: SandboxMicroVM,
		}, 80, 24)
		if terminal != nil {
			terminal.Close()
		}
		completed <- err
	}()
	select {
	case phase := <-phases:
		if phase != "vm_starting" {
			t.Fatal(phase)
		}
	case <-ctx.Done():
		t.Fatal("actual boot phase was not streamed during staging")
	}
	select {
	case err := <-completed:
		t.Fatalf("stage returned before the SSH barrier was released: %v", err)
	default:
	}
	unblock.Do(func() { close(release) })
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("staging did not finish")
	}
	if stages.Load() != 1 || ensures.Load() != 0 {
		t.Fatal(stages.Load(), ensures.Load())
	}
}

func TestNativeStageFailureDoesNotFallbackOrSkipValidation(t *testing.T) {
	client, _ := commandLifecyclePeer(t, "", func(command string, channel ssh.Channel, _ <-chan *ssh.Request) {
		if !strings.Contains(strings.ReplaceAll(command, `'"'"'`, `'`), " 'stage'") {
			t.Error("unexpected redundant probe")
		}
		fmt.Fprintln(channel.Stderr(), `ZENTRALE_VM_STARTUP_FAILED {"Reason":"startup_timeout","Diagnostic":"guest did not boot"}`)
		fixtureCommandExit(channel, 76)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal, err := StartTerminal(ctx, &Connection{Client: client}, domain.Machine{Sandbox: SandboxMicroVM}, domain.Project{Directory: "/tmp/project"}, EditorTarget{PreparedSandbox: SandboxMicroVM}, 80, 24)
	var failure *VMStartupFailure
	if terminal != nil || !errors.As(err, &failure) || failure.Reason != "startup_timeout" {
		t.Fatal(terminal, err)
	}
}

func TestStartupProtocolOnlyStripsItsExactTrustedMarker(t *testing.T) {
	input := startupBootMarker + "\n/path\nunknown diagnostic\nZENTRALE_STARTUP_PHASE unknown\n"
	if got := stripStartupBootMarker(input); got != "/path\nunknown diagnostic\nZENTRALE_STARTUP_PHASE unknown\n" {
		t.Fatal(got)
	}
}

func TestValidatedAutoTerminalAvoidsRepeatedSelectionButStillStages(t *testing.T) {
	for _, prepared := range []string{"", SandboxMicroVM} {
		t.Run("prepared="+prepared, func(t *testing.T) {
			var probes, stages, runs atomic.Int32
			client, _ := commandLifecyclePeer(t, "", func(command string, channel ssh.Channel, _ <-chan *ssh.Request) {
				command = strings.ReplaceAll(command, `'"'"'`, `'`)
				switch {
				case strings.Contains(command, " 'stage'"):
					stages.Add(1)
					var request any
					_ = json.NewDecoder(channel).Decode(&request)
					fmt.Fprintln(channel, "/tmp/vm/session/requests/0123456789abcdef0123456789abcdef")
				case strings.Contains(command, " 'run'"):
					runs.Add(1)
					var geometry any
					_ = json.NewDecoder(channel).Decode(&geometry)
					fmt.Fprint(channel, "\x00ZENTRALE_TERMINAL_READY\n")
				default:
					probes.Add(1)
					fmt.Fprintln(channel, "microvm")
				}
				fixtureCommandExit(channel, 0)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			terminal, err := StartTerminal(ctx, &Connection{Client: client}, domain.Machine{Sandbox: SandboxAutomatic}, domain.Project{ID: "project", Directory: "/tmp/project"}, EditorTarget{
				AgentID: "owner", TemporaryDirectory: "/tmp/owner", PreparedSandbox: prepared,
			}, 80, 24)
			if err != nil {
				t.Fatal(err)
			}
			defer terminal.Close()
			wantProbes := int32(1)
			if prepared == SandboxMicroVM {
				wantProbes = 0
			}
			if stages.Load() != 1 || runs.Load() != 1 || probes.Load() != wantProbes {
				t.Fatal("unexpected auto terminal SSH sequence", probes.Load(), stages.Load(), runs.Load())
			}
		})
	}
}

func TestPreparedTerminalStillValidatesWorkspaceAndGPUConfiguration(t *testing.T) {
	for _, test := range []struct {
		directory string
		machine   domain.Machine
	}{
		{"relative", domain.Machine{Sandbox: SandboxAutomatic}},
		{"/tmp/project", domain.Machine{Sandbox: SandboxAutomatic, GPUDevices: []string{"invalid-pci-address"}}},
	} {
		// A nil SSH client intentionally proves that validation fails before any
		// remote invocation, including on the prepared Auto/native fast path.
		if terminal, err := StartTerminal(context.Background(), &Connection{}, test.machine, domain.Project{Directory: test.directory}, EditorTarget{PreparedSandbox: SandboxMicroVM}, 80, 24); err == nil || terminal != nil {
			t.Fatal("prepared terminal skipped validation", test, err)
		}
	}
}
