package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestProjectStoragePreservesOtherDataAndPinsSharedRoots(t *testing.T) {
	engine, _, project, machine := managedWorkspaceEngine(t)
	root := t.TempDir()
	unrelated := filepath.Join(root, "existing-user-data")
	if err := os.WriteFile(unrelated, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	machine.Storage = []domain.StorageVolume{{ID: "large", Path: root, Shared: true}}
	machine.ID = "pool/one"
	connection, err := engine.Remote.Connector.Dial(context.Background(), "test-worker")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	first, err := engine.Remote.PrepareStorage(context.Background(), connection, machine, project)
	if err != nil || len(first) != 1 || first[0].HostPath != filepath.Join(root, "project-"+project.ID) || first[0].GuestPath != "/mnt/zentrale-storage/large" {
		t.Fatal(first, err)
	}
	machine.ID = "pool/two"
	shared, err := engine.Remote.PrepareStorage(context.Background(), connection, machine, project)
	if err != nil || shared[0] != first[0] {
		t.Fatal("shared members diverged", shared, err)
	}
	machine.Storage[0].Path = t.TempDir()
	pinned, err := engine.Remote.PrepareStorage(context.Background(), connection, machine, project)
	if err != nil || pinned[0] != first[0] {
		t.Fatal("changing defaults moved existing storage", pinned, err)
	}
	if err := os.Remove(first[0].HostPath); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Remote.PrepareStorage(context.Background(), connection, machine, project); err == nil || !strings.Contains(err.Error(), "missing on this machine") {
		t.Fatal("missing shared storage became an empty replacement", err)
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "preserve" {
		t.Fatal("unrelated storage data changed", err)
	}
}

func TestHostQuotaUsesOnlyConfiguredArgvAndWorksWithoutVM(t *testing.T) {
	var queries atomic.Int32
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, record, _, machine := managedWorkspaceEngine(t, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "administrator-configured") {
			queries.Add(1)
		}
		return shell(command, input, stdout, stderr)
	})
	directory := t.TempDir()
	script := filepath.Join(directory, "quota.py")
	forbidden := filepath.Join(directory, "must-not-exist")
	if err := os.WriteFile(script, []byte("import sys\nprint('\\x1b[31mquota 101%\\x1b[0m')\nprint(sys.argv[1])\nsys.exit(1)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	machine.QuotaCommand = []string{"/usr/bin/python3", script, "; touch " + forbidden}
	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	result, err := engine.storageQuota(context.Background(), record, map[string]any{"machineId": machine.ID})
	if err != nil {
		t.Fatal(err)
	}
	quota := result.(map[string]any)
	if quota["exitCode"] != float64(1) || !strings.Contains(quota["output"].(string), "quota 101%") || strings.Contains(quota["output"].(string), "\x1b[") {
		t.Fatal(quota)
	}
	if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
		t.Fatal("quota arguments executed through a shell")
	}
	if _, err := engine.storageQuota(context.Background(), record, map[string]any{"machineId": machine.ID, "command": "arbitrary"}); err == nil {
		t.Fatal("accepted a model-supplied quota command")
	}
	if _, err := engine.storageQuota(context.Background(), record, map[string]any{"machineId": "outside-project"}); err == nil {
		t.Fatal("queried quota outside project")
	}
	if records, err := engine.listAll("microvm", &record.ID); err != nil || len(records) != 0 {
		t.Fatal("quota query booted a VM", records, err)
	}
	engine.Provider = providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{Text: `{"allowed":false,"reason":"test denial","blanket":false}`, UsageKnown: true}, nil
	})
	if _, err := engine.dispatch(context.Background(), record, providers.Call{Name: "harness", Arguments: map[string]any{"action": "storage_quota", "arguments": JSON(map[string]any{"machineId": machine.ID})}}); err == nil || queries.Load() != 1 {
		t.Fatal("quota query bypassed independent approval", queries.Load(), err)
	}
}

func TestReadOnlyInspectionCanFollowAJobWithoutSwitchingTheDefaultMachine(t *testing.T) {
	engine, record, project, machine := managedWorkspaceEngine(t)
	ctx := context.Background()
	connection, _, first, err := engine.connect(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	if err := os.WriteFile(filepath.Join(first.Directory, "script.py"), []byte("default-machine"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := machine
	other.ID = "other-worker"
	other.WorkspaceRoot = t.TempDir()
	if err := engine.Store.Save("machine", other.ID, "", other); err != nil {
		t.Fatal(err)
	}
	project.MachineIDs = append(project.MachineIDs, other.ID)
	if err := engine.Store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}
	connection, _, second, release, err := engine.connectForJob(ctx, record, &executionRequest{MachineID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	release()
	if err := os.WriteFile(filepath.Join(second.Directory, "script.py"), []byte("target-machine"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Inspect(ctx, record, "read_file", map[string]any{"path": "script.py", "machineId": other.ID})
	if err != nil || result.(map[string]any)["content"] != "target-machine" {
		t.Fatal("approver inspected the wrong job filesystem", result, err)
	}
	var current domain.Agent
	if err := engine.Store.Load("agent", record.ID, &current); err != nil || current.MachineID != machine.ID {
		t.Fatal("inspection changed default machine", current, err)
	}
}
