package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestProjectMoveTransfersWorkspaceBeforeRemovingSource(t *testing.T) {
	engine, record, project, old := managedWorkspaceEngine(t)
	ctx := context.Background()
	connection, _, resolved, err := engine.connect(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()

	target := old
	target.ID = "move-destination"
	target.WorkspaceRoot = t.TempDir()
	if err := engine.Store.Save("machine", target.ID, "", target); err != nil {
		t.Fatal(err)
	}
	work := resolved.Directory
	if err := os.Mkdir(filepath.Join(work, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".git/local-state", "uncommitted"} {
		if err := os.WriteFile(filepath.Join(work, name), []byte(name), 0750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("uncommitted", filepath.Join(work, "link")); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(target.WorkspaceRoot, "project-"+project.ID)
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(destination, "existing")
	if err := os.WriteFile(foreign, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.StartProjectMove(project.ID, target.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitForProjectMove(t, engine, project.ID, func(move *domain.ProjectMove) bool {
		return move.Error != ""
	})
	var saved domain.Project
	if err := engine.Store.Load("project", project.ID, &saved); err != nil || saved.MachineIDs[0] != old.ID {
		t.Fatal("failed transfer changed the project assignment", err)
	}
	for _, file := range []string{filepath.Join(work, "uncommitted"), foreign} {
		if _, err := os.Stat(file); err != nil {
			t.Fatal("a failed move removed data", err)
		}
	}

	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if err := engine.StartProjectMove(project.ID, target.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitForProjectMove(t, engine, project.ID, func(move *domain.ProjectMove) bool {
		if move.Error != "" {
			t.Fatalf("move failed: %s", move.Error)
		}
		return move.Phase == "done"
	})
	if err := engine.Store.Load("project", project.ID, &saved); err != nil || saved.MachineIDs[0] != target.ID {
		t.Fatal("project did not switch to the verified target", err)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatal("previous workspace remains", err)
	}
	for _, name := range []string{".git/local-state", "uncommitted"} {
		path := filepath.Join(destination, name)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != name {
			t.Fatal("missing project file", name, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0750 {
			t.Fatal("file mode was changed", err)
		}
	}
	if link, err := os.Readlink(filepath.Join(destination, "link")); err != nil || link != "uncommitted" {
		t.Fatal("symlink was not preserved", err)
	}
}

func TestProjectMoveAcrossSharedStorageKeepsFiles(t *testing.T) {
	engine, record, project, old := managedWorkspaceEngine(t)
	connection, _, resolved, err := engine.connect(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()

	member := old
	member.ID = "shared-target"
	pool := domain.Pool{
		ID: "destination-pool", Name: "Destination pool", SharedHome: true,
		WorkspaceRoot: old.WorkspaceRoot, MaxMachines: 2, KeyID: old.KeyID,
		Machines: []domain.Machine{member},
	}
	if err := engine.Store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(resolved.Directory, "keep")
	if err := os.WriteFile(file, []byte("shared"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.StartProjectMove(project.ID, "", pool.ID); err != nil {
		t.Fatal(err)
	}
	waitForProjectMove(t, engine, project.ID, func(move *domain.ProjectMove) bool {
		if move.Error != "" {
			t.Fatalf("move failed: %s", move.Error)
		}
		return move.Phase == "done"
	})
	if data, err := os.ReadFile(file); err != nil || string(data) != "shared" {
		t.Fatal("moving between two routes to the same storage removed the files", err)
	}
}

func TestProjectMoveUsesStoredVMReceiptKeys(t *testing.T) {
	shell := shellPeerHandler("/bin/sh", os.Environ())
	var mu sync.Mutex
	actions := map[string]int{}
	engine, record, project, old := managedWorkspaceEngine(t, func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "<zentrale-microvm>") {
			for _, action := range []string{"stop", "remove"} {
				if strings.Contains(strings.ReplaceAll(command, `'"'"'`, "'"), "'"+action+"'") {
					mu.Lock()
					actions[action]++
					mu.Unlock()
					fmt.Fprintln(stdout, "{}")
					return 0
				}
			}
		}
		return shell(command, stdin, stdout, stderr)
	})
	connection, _, resolved, err := engine.connect(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	target := old
	target.ID, target.WorkspaceRoot = "vm-move-destination", t.TempDir()
	if err := engine.Store.Save("machine", target.ID, "", target); err != nil {
		t.Fatal(err)
	}
	vm := microVMRecord{
		AgentID: remote.EditorScopeID(project.ID, ""), ProjectID: project.ID,
		MachineID: old.ID, Workspace: resolved.Directory,
	}
	currentKey := vm.AgentID + ":" + old.ID
	if err := engine.Store.Save("microvm", currentKey, project.ID, vm); err != nil {
		t.Fatal(err)
	}
	// Older receipts had an agent parent. The actual storage key is always
	// authoritative, including for imported receipts.
	const legacyKey = "imported-vm-receipt"
	vm.AgentID = NewID()
	if err := engine.Store.Save("microvm", legacyKey, vm.AgentID, vm); err != nil {
		t.Fatal(err)
	}
	if err := engine.StartProjectMove(project.ID, target.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitForProjectMove(t, engine, project.ID, func(move *domain.ProjectMove) bool {
		if move.Error != "" {
			t.Fatalf("move failed with existing VM receipts: %s", move.Error)
		}
		return move.Phase == "done"
	})
	for _, id := range []string{currentKey, legacyKey} {
		if err := engine.Store.Load("microvm", id, &vm); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("old VM receipt %q was not removed: %v", id, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if actions["stop"] != 2 || actions["remove"] != 2 {
		t.Fatalf("expected both VM receipts to be processed: %v", actions)
	}
}

func TestProjectMoveTransfersProjectVolume(t *testing.T) {
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, record, project, old := managedWorkspaceEngine(t, func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "<zentrale-microvm>") && strings.Contains(command, "ensure") {
			fmt.Fprintln(stdout, "microvm")
			return 0
		}
		return shell(command, stdin, stdout, stderr)
	})
	old.Sandbox = remote.SandboxMicroVM
	old.Storage = []domain.StorageVolume{{ID: "data", Path: t.TempDir()}}
	if err := engine.Store.Save("machine", old.ID, "", old); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	connection, _, resolved, err := engine.connect(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	mounts, err := engine.Remote.PrepareStorage(ctx, connection, old, resolved)
	connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mounts[0].HostPath, "checkpoint"), []byte("saved"), 0600); err != nil {
		t.Fatal(err)
	}
	target := old
	target.ID = "volume-target"
	target.WorkspaceRoot = t.TempDir()
	target.Storage = []domain.StorageVolume{{ID: "data", Path: t.TempDir()}}
	if err := engine.Store.Save("machine", target.ID, "", target); err != nil {
		t.Fatal(err)
	}
	if err := engine.StartProjectMove(project.ID, target.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitForProjectMove(t, engine, project.ID, func(move *domain.ProjectMove) bool {
		if move.Error != "" {
			t.Fatalf("move failed: %s", move.Error)
		}
		return move.Phase == "done"
	})
	if _, err := os.Stat(mounts[0].HostPath); !os.IsNotExist(err) {
		t.Fatal("old storage directory remains", err)
	}
	data, err := os.ReadFile(filepath.Join(target.Storage[0].Path, "project-"+project.ID, "checkpoint"))
	if err != nil || string(data) != "saved" {
		t.Fatal("managed storage did not move", err)
	}
}

func waitForProjectMove(t *testing.T, engine *Engine, id string, ready func(*domain.ProjectMove) bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		move, err := engine.ProjectMove(id)
		if err != nil {
			t.Fatal(err)
		}
		if move != nil && ready(move) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the project move")
}
