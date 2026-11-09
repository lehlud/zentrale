package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"golang.org/x/crypto/ssh"
)

// Opt in with a disposable database copy. Every remote write belongs to a new
// private test directory or to an explicitly owned test-agent MicroVM scope.
func TestManagedMicroVMLive(t *testing.T) {
	snapshot := os.Getenv("ZENTRALE_MICROVM_TEST_SNAPSHOT")
	machineID := os.Getenv("ZENTRALE_MICROVM_TEST_MACHINE")
	if snapshot == "" || machineID == "" {
		t.Skip("set ZENTRALE_MICROVM_TEST_SNAPSHOT and ZENTRALE_MICROVM_TEST_MACHINE for the real KVM/SSH test")
	}
	store, err := datastore.Open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	connector := &Connector{Store: store}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	connection, err := connector.Dial(ctx, machineID)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	machine, err := connector.Machine(machineID)
	if err != nil {
		t.Fatal(err)
	}
	machine.Sandbox, machine.MicroVMCommand = SandboxAutomatic, ""
	vmType := os.Getenv("ZENTRALE_MICROVM_TEST_VM_TYPE")
	storageRoot := os.Getenv("ZENTRALE_MICROVM_TEST_STORAGE_ROOT")
	if vmType != "" {
		machine.VMType = vmType
	}
	if storageRoot != "" {
		machine.Storage = []domain.StorageVolume{{ID: "live-storage", Path: storageRoot, Shared: true}}
	}
	if command := os.Getenv("ZENTRALE_MICROVM_TEST_QUOTA_COMMAND"); command != "" {
		machine.QuotaCommand = []string{command}
	}
	if vmType != "" || storageRoot != "" {
		// Editor sessions resolve the machine through the connector again. Keep
		// their configuration in this disposable snapshot consistent with ours.
		if parts := strings.SplitN(machineID, "/", 2); len(parts) == 2 {
			var pool domain.Pool
			if err := store.Load("pool", parts[0], &pool); err != nil {
				t.Fatal(err)
			}
			for index := range pool.Machines {
				if pool.Machines[index].ID == parts[1] {
					pool.Machines[index].VMType = machine.VMType
					pool.Machines[index].GPUDevices = nil
					pool.Machines[index].Storage = machine.Storage
					pool.Machines[index].QuotaCommand = machine.QuotaCommand
				}
			}
			if err := store.Save("pool", pool.ID, "", pool); err != nil {
				t.Fatal(err)
			}
		} else if err := store.Patch("machine", machineID, map[string]any{"vmType": machine.VMType, "gpuDevices": []string{}, "storage": machine.Storage, "quotaCommand": machine.QuotaCommand}); err != nil {
			t.Fatal(err)
		}
	}
	machine.GPUDevices = nil // The standard live test never claims a physical GPU.
	if err := CheckAvailability(ctx, connection, machine); err != nil {
		t.Fatal(err)
	}
	base, err := Run(ctx, connection.Client, "mktemp -d /tmp/zentrale-microvm-smoke-XXXXXXXX")
	if err != nil {
		t.Fatal(err)
	}
	base = strings.TrimSpace(base)
	if !strings.HasPrefix(base, "/tmp/zentrale-microvm-smoke-") || strings.ContainsAny(base, "\r\n") {
		t.Fatalf("invalid test directory: %q", base)
	}
	first := Execution{AgentID: "live-agent-a", ProjectID: strings.TrimPrefix(base, "/tmp/"), Workspace: base + "/project 'quoted' = directory", TemporaryDirectory: base + "/temporary-a"}
	second := first
	second.AgentID, second.TemporaryDirectory = "live-agent-b", base+"/temporary-b"
	t.Cleanup(func() {
		cleanupContext, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		cleanupConnection, err := connector.Dial(cleanupContext, machineID)
		if err != nil {
			t.Error(err)
			return
		}
		defer cleanupConnection.Close()
		for _, execution := range []Execution{first, second} {
			if _, err := ManageMicroVM(cleanupContext, cleanupConnection, machine, "remove", execution); err != nil {
				t.Error(err)
			}
		}
		if _, err := Run(cleanupContext, cleanupConnection.Client, "rm -rf -- "+Quote(base)); err != nil {
			t.Error(err)
		}
		for _, mount := range machine.StorageMounts {
			if !strings.HasSuffix(mount.HostPath, "/project-"+first.ProjectID) {
				t.Error("refusing to clean an unexpected storage path")
				continue
			}
			marker := strings.TrimSuffix(mount.HostPath, "/project-"+first.ProjectID) + "/.zentrale-workspaces/project-" + first.ProjectID + ".json"
			if _, err := Run(cleanupContext, cleanupConnection.Client, "rm -rf -- "+Quote(mount.HostPath)+" && rm -f -- "+Quote(marker)); err != nil {
				t.Error(err)
			}
		}
	})
	manager := NewManager(connector)
	machine.StorageMounts, err = manager.PrepareStorage(ctx, connection, machine, domain.Project{ID: first.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(ctx, connection.Client, "mkdir -p -- "+Quote(first.Workspace)+" "+Quote(first.TemporaryDirectory)+" "+Quote(second.TemporaryDirectory)+" && printf 'host sentinel' > "+Quote(base+"/outside.txt"))
	if err != nil {
		t.Fatal(err)
	}
	status := InspectSandbox(ctx, connection, machine)
	if status.Error != "" || status.Level != SandboxMicroVM {
		t.Fatalf("auto selection: %+v", status)
	}
	t.Logf("auto selected %s on %s", status.Level, machine.Name)
	wrap := func(execution Execution, command string, network, guestRoot, tooling bool) string {
		t.Helper()
		execution.Command, execution.Network, execution.GuestRoot, execution.Tooling = command, network, guestRoot, tooling
		wrapped, level, err := Sandbox(ctx, connection, machine, execution)
		if err != nil || level != SandboxMicroVM {
			t.Fatalf("prepare MicroVM: level=%q error=%v", level, err)
		}
		return wrapped
	}
	run := func(execution Execution, command string, network, root bool) string {
		t.Helper()
		output, err := Run(ctx, connection.Client, wrap(execution, command, network, root, false))
		if err != nil {
			t.Fatalf("guest command: %v", err)
		}
		return strings.TrimSpace(output)
	}
	python := func(script string) string { return "python3 -c " + Quote(script) }
	boot := func(execution Execution) string {
		return run(execution, "cat /proc/sys/kernel/random/boot_id", false, false)
	}
	firstBoot := boot(first)
	if firstBoot == "" || !strings.Contains(run(first, "cat /etc/os-release", false, false), `VERSION_ID=45`) {
		t.Fatal("not a Fedora 45 guest")
	}
	info, err := ManageMicroVM(ctx, connection, machine, "info", first)
	if err != nil || info["vmType"] != WithMachineDefaults(machine).VMType {
		t.Fatalf("guest booted the wrong machine type: %v %v", info, err)
	}
	t.Run("additional project storage and host quota", func(t *testing.T) {
		if len(machine.StorageMounts) == 0 {
			t.Skip("set ZENTRALE_MICROVM_TEST_STORAGE_ROOT to verify an additional real filesystem")
		}
		mount := machine.StorageMounts[0]
		if run(first, "printf volume-proof > "+Quote(mount.GuestPath+"/proof.txt")+"; test ! -e "+Quote(storageRoot), false, false) != "" {
			t.Fatal("unexpected volume output")
		}
		output, err := Run(ctx, connection.Client, "cat -- "+Quote(mount.HostPath+"/proof.txt"))
		if err != nil || output != "volume-proof" {
			t.Fatal("volume writes did not reach project storage", output, err)
		}
		if len(machine.QuotaCommand) > 0 {
			quota, err := ReadStorageQuota(ctx, connection, machine)
			if err != nil || quota["output"] == "" {
				t.Fatal("host quota failed", quota, err)
			}
			t.Logf("host quota returned status %v without running a command in the guest", quota["exitCode"])
		}
	})
	t.Run("standard tools are available before user package installations", func(t *testing.T) {
		output := run(first, "for tool in git rg python3 pip3 curl ssh scp jq less file find xargs diff cmp patch tar gzip bzip2 xz unzip zip which ps pgrep flock script make fish; do command -v \"$tool\" >/dev/null || exit 1; done; git --version; rg --version; python3 -m pip --version", false, false)
		if !strings.Contains(output, "git version") || !strings.Contains(output, "ripgrep") || !strings.Contains(output, "pip ") {
			t.Fatalf("default tools missing: %s", output)
		}
	})
	run(first, "printf checkpoint > project.txt; printf guest-state > /home/zentrale/state", false, false)
	if boot(first) != firstBoot || run(first, "cat /home/zentrale/state", false, false) != "guest-state" {
		t.Fatal("VM or guest filesystem changed between calls")
	}

	t.Run("real Fedora package installation", func(t *testing.T) {
		output := run(first, "microdnf -y install tree", true, true)
		t.Logf("Fedora package transaction completed (%d bytes of logs)", len(output))
		if run(first, "python3 -c 'print(6*7)'", false, false) != "42" || !strings.HasPrefix(run(first, "tree --version", false, false), "tree ") {
			t.Fatal("installed packages unavailable to later unprivileged actions")
		}
		if boot(first) != firstBoot {
			t.Fatal("install rebooted the VM")
		}
		run(first, "printf installed > /var/lib/zentrale-live-package-marker", false, true)
	})
	// The rest of this test needs the package installed above; fail clearly if it failed.
	if t.Failed() {
		return
	}
	t.Run("interactive terminal is inside the agent guest", func(t *testing.T) {
		execution := first
		execution.TTY, execution.ProtocolIO = true, true
		command := python("import sys; print('terminal:'+str(sys.stdin.isatty()),flush=True); print(input(),flush=True)")
		wrapped := wrap(execution, command, false, false, false)
		output, err := RunStreamingInput(ctx, connection.Client, wrapped, strings.NewReader("guest-input\n"), true, nil, nil)
		if err != nil || !strings.Contains(output, "terminal:True") || !strings.Contains(output, "guest-input") {
			t.Fatalf("guest terminal: %q %v", output, err)
		}
		if boot(first) != firstBoot {
			t.Fatal("terminal changed the persistent VM")
		}
	})
	t.Run("VS Code server and terminal devices share the agent guest", func(t *testing.T) {
		editors := NewEditors(NewManager(connector))
		defer editors.Close()
		project := domain.Project{ID: first.ProjectID, Directory: first.Workspace, MachineIDs: []string{machineID}}
		session, err := editors.StartFor(ctx, project, EditorTarget{
			AgentID: first.AgentID, MachineID: machineID, TemporaryDirectory: first.TemporaryDirectory,
			CommitIdentity: domain.CommitIdentity{Name: "Live Editor", Email: "live-editor@example.test"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if session.Sandbox != SandboxMicroVM || session.AgentID != first.AgentID {
			t.Fatalf("editor environment: %+v", session)
		}
		transport, release, err := editors.Transport(project, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		defer transport.CloseIdleConnections()
		location, err := url.Parse(session.URL)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
		readGuest := func(filename string) string {
			t.Helper()
			response, err := client.Get("http://vscode" + location.Path + "vscode-remote-resource?path=" + url.QueryEscape(filename))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			content, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("guest resource %s: status=%d error=%v body=%s", filename, response.StatusCode, err, content)
			}
			return string(content)
		}
		if readGuest("/home/zentrale/state") != "guest-state" || !strings.Contains(readGuest("/etc/os-release"), `VERSION_ID=45`) {
			t.Fatal("VS Code filesystem is outside the agent's Fedora guest")
		}
		run(first, python(`import os,pty
master,slave=pty.openpty()
assert os.ttyname(slave).startswith('/dev/pts/')
os.close(master);os.close(slave)
print('guest terminal devices work')`), false, false)
		if boot(first) != firstBoot {
			t.Fatal("starting VS Code replaced the agent's VM")
		}
		if err := editors.Stop(project.ID, session.ID); err != nil {
			t.Fatal(err)
		}
		if boot(first) != firstBoot {
			t.Fatal("stopping VS Code terminated the persistent guest")
		}
	})
	t.Run("per-action network including root", func(t *testing.T) {
		run(first, python(`import urllib.request
with urllib.request.urlopen('https://example.com',timeout=20) as response: assert response.status==200
print('approved HTTPS')`), true, false)
		for _, root := range []bool{false, true} {
			run(first, python(`import os,socket
assert os.listdir('/sys/class/net')==['lo'],os.listdir('/sys/class/net')
s=socket.socket();s.settimeout(2)
try: s.connect(('1.1.1.1',443))
except OSError: print('network denied')
else: raise AssertionError('network inherited a previous approval')`), false, root)
		}
	})
	t.Run("guest root cannot reach control or host", func(t *testing.T) {
		run(first, python(`import os
from pathlib import Path
assert not Path('/run/zentrale-control').exists()
assert not Path('/dev/vda').exists()
try: os.chroot('/')
except PermissionError: pass
else: raise AssertionError('task retained CAP_SYS_CHROOT')
Path('/root/guest-only').write_text('guest root is writable')
print('guest administration stays inside Fedora')`), false, true)
	})
	t.Run("stdin large commands and file harness", func(t *testing.T) {
		output, err := RunPipe(ctx, connection.Client, wrap(first, "cat; printf 'direct stdout\\n' > /dev/stdout", false, false, false), strings.NewReader("input over SSH\n"))
		if err != nil || !strings.Contains(output, "input over SSH") || !strings.Contains(output, "direct stdout") {
			t.Fatalf("stdin/stdout: %q %v", output, err)
		}
		_, err = Run(ctx, connection.Client, wrap(first, "printf 'specific failure' >&2; exit 7", false, false, false))
		var exit *ssh.ExitError
		if !errors.As(err, &exit) || exit.ExitStatus() != 7 || !strings.Contains(err.Error(), "specific failure") {
			t.Fatalf("exit status: %v", err)
		}
		if run(first, "#"+strings.Repeat("x", 150000)+"\nprintf 'large command ran'", false, false) != "large command ran" {
			t.Fatal("large command failed")
		}
		script, err := os.ReadFile("../agent/scripts/files.py")
		if err != nil {
			t.Fatal(err)
		}
		run(first, `printf '%s' "raise RuntimeError('project module executed')" > json.py`, false, false)
		probe := "python3 -I -c " + Quote(`import os
from pathlib import Path
assert not Path('/var/tmp/zentrale-microvm-'+str(os.getuid())).exists()
assert not Path('/run/zentrale-control').exists()
print('tool filesystem is isolated')`)
		if output, err := Run(ctx, connection.Client, wrap(first, probe, false, false, true)); err != nil || !strings.Contains(output, "tool filesystem is isolated") {
			t.Fatalf("tool isolation: %q %v", output, err)
		}
		content := strings.Repeat("checkpoint line\n", 20000)
		command := "python3 -I -c " + Quote(string(script)) + " " + Quote(first.Workspace) + " " + Quote(first.TemporaryDirectory) + " large.txt write_file '-'"
		output, err = RunPipe(ctx, connection.Client, wrap(first, command, false, false, true), strings.NewReader(Encode(content)))
		var result map[string]any
		if err != nil || json.Unmarshal([]byte(output), &result) != nil || result["written"] != true || result["_change"] == nil {
			t.Fatalf("large file write: %q %v", output, err)
		}
		if run(first, "wc -l < large.txt", false, false) != "20000" {
			t.Fatal("file harness and Fedora see different projects")
		}
	})

	secondBoot := boot(second)
	// The default shell may pull Python into each fresh guest. Check a private
	// disk marker rather than assuming a particular dependency set for fish.
	if secondBoot == firstBoot || run(second, "test ! -e /home/zentrale/state && test ! -e /var/lib/zentrale-live-package-marker && cat project.txt", false, false) != "checkpoint" {
		t.Fatal("agent environments are not separate")
	}
	t.Run("running job and cancellation preserve VM", func(t *testing.T) {
		jobContext, stop := context.WithTimeout(ctx, 30*time.Second)
		ready, done := make(chan struct{}, 1), make(chan error, 1)
		command := wrap(first, "printf 'JOB_RUNNING\\n'; sleep 600", false, false, false)
		go func() {
			_, err := RunStreaming(jobContext, connection.Client, command, func(output string) {
				if strings.Contains(output, "JOB_RUNNING") {
					select {
					case ready <- struct{}{}:
					default:
					}
				}
			})
			done <- err
		}()
		select {
		case <-ready:
		case err := <-done:
			t.Fatalf("job did not start: %v", err)
		case <-jobContext.Done():
			t.Fatal(jobContext.Err())
		}
		if boot(first) != firstBoot {
			t.Fatal("concurrent action got a different VM")
		}
		if _, err := ManageMicroVM(ctx, connection, machine, "reset", first); err == nil || !strings.Contains(err.Error(), "stop running jobs") {
			t.Fatalf("reset of active job: %v", err)
		}
		stop()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("stop: %v", err)
		}
		// Cancellation is delivered through the mailbox after the SSH job dies.
		time.Sleep(500 * time.Millisecond)
		if boot(first) != firstBoot {
			t.Fatal("cancelling a job destroyed its VM")
		}
	})
	t.Run("SSH disconnect cancels only its job", func(t *testing.T) {
		peer, err := connector.Dial(ctx, machineID)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		command := wrap(first, python(`import os,time
from pathlib import Path
os.setsid()
print('SSH_JOB_RUNNING',flush=True)
while True:
    with Path('heartbeat').open('a') as output: output.write('tick\n')
    time.sleep(0.1)`), false, false, false)
		ready, done := make(chan struct{}, 1), make(chan error, 1)
		go func() {
			_, err := RunStreaming(ctx, peer.Client, command, func(output string) {
				if strings.Contains(output, "SSH_JOB_RUNNING") {
					select {
					case ready <- struct{}{}:
					default:
					}
				}
			})
			done <- err
		}()
		select {
		case <-ready:
		case err := <-done:
			t.Fatalf("SSH job did not start: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		peer.Client.Close()
		if err := <-done; err == nil {
			t.Fatal("SSH loss was not reported")
		}
		time.Sleep(2 * time.Second)
		before := run(first, "wc -l < heartbeat", false, false)
		time.Sleep(500 * time.Millisecond)
		if after := run(first, "wc -l < heartbeat", false, false); before != after {
			t.Fatal("job survived SSH loss")
		}
		if boot(first) != firstBoot {
			t.Fatal("SSH loss destroyed the stable VM")
		}
	})
	t.Run("reset is scoped and preserves project", func(t *testing.T) {
		if _, err := ManageMicroVM(ctx, connection, machine, "reset", first); err != nil {
			t.Fatal(err)
		}
		if boot(first) == firstBoot {
			t.Fatal("reset did not replace VM")
		}
		if run(first, "test ! -e /home/zentrale/state && test ! -e /root/guest-only && test ! -e /var/lib/zentrale-live-package-marker && cat project.txt", false, false) != "checkpoint" {
			t.Fatal("reset retained guest changes or lost project files")
		}
		script, err := os.ReadFile("../agent/scripts/files.py")
		if err != nil {
			t.Fatal(err)
		}
		command := "python3 -I -c " + Quote(string(script)) + " " + Quote(first.Workspace) + " " + Quote(first.TemporaryDirectory) + " project.txt read_file ''"
		output, err := Run(ctx, connection.Client, wrap(first, command, false, false, true))
		var result map[string]any
		if err != nil || json.Unmarshal([]byte(output), &result) != nil || result["content"] != "checkpoint" {
			t.Fatalf("file tools need Fedora Python after reset: %q %v", output, err)
		}
		if boot(second) != secondBoot {
			t.Fatal("reset affected another agent")
		}
		if len(machine.StorageMounts) > 0 && run(first, "cat "+Quote(machine.StorageMounts[0].GuestPath+"/proof.txt"), false, false) != "volume-proof" {
			t.Fatal("reset lost additional storage")
		}
	})
	t.Run("stop and reopen retain Fedora disk", func(t *testing.T) {
		run(second, "printf durable > /home/zentrale/durable", false, false)
		// A persistent disk created before a standard tool was introduced must
		// be upgraded at boot without losing its existing files.
		run(second, "microdnf -y remove ripgrep", false, true)
		if run(second, "if command -v rg >/dev/null; then exit 1; fi; printf missing", false, false) != "missing" {
			t.Fatal("migration fixture still contains ripgrep")
		}
		if _, err := ManageMicroVM(ctx, connection, machine, "stop", second); err != nil {
			t.Fatal(err)
		}
		if boot(second) == secondBoot || run(second, "cat /home/zentrale/durable", false, false) != "durable" {
			t.Fatal("reopen lost persistent guest disk")
		}
		if !strings.HasPrefix(run(second, "rg --version", false, false), "ripgrep ") {
			t.Fatal("reopen did not add the missing standard tool to the existing disk")
		}
	})
	outside, err := Run(ctx, connection.Client, "cat -- "+Quote(base+"/outside.txt"))
	if err != nil || outside != "host sentinel" {
		t.Fatalf("host sentinel changed: %q %v", outside, err)
	}
	fmt.Fprintln(os.Stdout, "Fedora persistence, packages, per-action network, ownership and reset verified")
}
