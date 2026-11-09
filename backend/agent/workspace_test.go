package agent

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func managedWorkspaceEngine(t *testing.T, handlers ...testssh.CommandHandler) (*Engine, domain.Agent, domain.Project, domain.Machine) {
	t.Helper()
	provider := providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{Text: `{"allowed":true,"reason":"","blanket":false}`, UsageKnown: true}, nil
	})

	handler := shellPeerHandler("/bin/sh", os.Environ())

	if len(handlers) > 0 {
		handler = handlers[0]
	}

	engine, record, project := executionTestEngine(t, provider, handler)
	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	machine.Sandbox = remote.SandboxDisabled
	machine.WorkspaceRoot = filepath.Join(t.TempDir(), "persistent 'quoted' $directory")

	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}

	project.Directory, project.ManagedWorkspace = "", true

	if err := engine.Store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}

	return engine, record, project, machine
}

func TestManagedEditorUsesTheSameResolvedDirectoryAsAgentTools(t *testing.T) {
	shell := shellPeerHandler("/bin/sh", os.Environ())
	handler := func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "ZENTRALE_EDITOR_READY ") {
			_, _ = fmt.Fprintln(stdout, `ZENTRALE_EDITOR_READY {"socketPath":"/tmp/ze-workspace-test/vscode.sock"}`)
			_, _ = io.Copy(io.Discard, stdin)

			return 0
		}

		return shell(command, stdin, stdout, stderr)
	}

	engine, record, project, machine := managedWorkspaceEngine(t, handler)
	ctx := context.Background()
	session, err := engine.StartEditor(ctx, project.ID, record.ID)
	if err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(machine.WorkspaceRoot, "project-"+project.ID)
	link, _ := url.Parse(session.URL)
	if session.Directory != expected || link.Query().Get("folder") != expected {
		t.Fatal("editor opened a different workspace", session)
	}

	if current, err := engine.CurrentEditor(project.ID, record.ID); err != nil || current.ID != session.ID {
		t.Fatal("persisted project no longer matched its active editor", current, err)
	}

	if info, err := engine.Editors.Info(project, session.ID); err != nil || info.Directory != expected {
		t.Fatal(info, err)
	}

	transport, release, err := engine.Editors.Transport(project, session.ID)
	if err != nil {
		t.Fatal(err)
	}

	transport.CloseIdleConnections()
	release()
	connection, _, resolved, err := engine.connect(ctx, record)
	if err != nil {
		t.Fatal(err)
	}

	connection.Close()

	if resolved.Directory != session.Directory {
		t.Fatal("agent tools and VS Code diverged", resolved.Directory, session.Directory)
	}

	if err := engine.Editors.Stop(project.ID, session.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticWorkspaceIsStableAcrossAgentsRenamesAndDefaultRootChanges(t *testing.T) {
	engine, record, project, machine := managedWorkspaceEngine(t)
	ctx := context.Background()
	connection, _, first, err := engine.connect(ctx, record)
	if err != nil {
		t.Fatal(err)
	}

	connection.Close()
	expected := filepath.Join(machine.WorkspaceRoot, "project-"+project.ID)
	if first.Directory != expected {
		t.Fatal(first.Directory, expected)
	}

	if _, err := engine.file(
		ctx,
		record,
		"write_file",
		map[string]any{
			"path":    "checkpoint.txt",
			"content": "saved",
		},
		false,
		false,
	); err != nil {
		t.Fatal(err)
	}

	project.Name = "Renamed project"
	renamed, err := engine.SaveProject(project)
	if err != nil || !renamed.ManagedWorkspace || renamed.Directory != "" {
		t.Fatal(renamed, err)
	}

	machine.WorkspaceRoot = filepath.Join(t.TempDir(), "new default")

	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}

	connection, _, after, err := engine.connect(ctx, record)
	if err != nil {
		t.Fatal(err)
	}

	connection.Close()

	if after.Directory != expected {
		t.Fatal("changing the default moved an existing workspace", after.Directory)
	}

	data, err := os.ReadFile(filepath.Join(after.Directory, "checkpoint.txt"))
	if err != nil || string(data) != "saved" {
		t.Fatal(string(data), err)
	}

	copy := record
	copy.ID = "another-agent"

	if err := engine.Store.Save("agent", copy.ID, project.ID, copy); err != nil {
		t.Fatal(err)
	}

	connection, _, shared, err := engine.connect(ctx, copy)
	if err != nil {
		t.Fatal(err)
	}

	connection.Close()

	if shared.Directory != expected {
		t.Fatal("agents in one project received different workspaces", shared.Directory)
	}

	alias := machine
	alias.ID = "same-host-alias"
	alias.WorkspaceRoot = filepath.Dir(expected)
	connection, err = engine.Remote.Connector.Dial(ctx, machine.ID)
	if err != nil {
		t.Fatal(err)
	}

	aliased, err := engine.Remote.ResolveProject(ctx, connection, alias, project)
	connection.Close()

	if err != nil || aliased.Directory != expected {
		t.Fatal("a machine alias could not reuse the same project on shared storage", aliased, err)
	}

	other, err := engine.SaveProject(domain.Project{
		ID:         "other-project",
		Name:       project.Name,
		MachineIDs: project.MachineIDs,
	})
	if err != nil || !other.ManagedWorkspace {
		t.Fatal(other, err)
	}

	connection, err = engine.Remote.Connector.Dial(ctx, machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	newWorkspace, err := engine.Remote.ResolveProject(ctx, connection, machine, other)
	if err != nil || newWorkspace.Directory != filepath.Join(machine.WorkspaceRoot, "project-"+other.ID) {
		t.Fatal(newWorkspace, err)
	}
}

func TestPoolWorkspaceReceiptIsSharedAndSurvivesManagerRestart(t *testing.T) {
	engine, _, project, machine := managedWorkspaceEngine(t)
	ctx := context.Background()
	connection, err := engine.Remote.Connector.Dial(ctx, machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	first := machine
	first.ID = "pool/first"
	second := machine
	second.ID = "pool/second"
	var group sync.WaitGroup

	for _, member := range []domain.Machine{first, second} {
		group.Go(func() {
			resolved, err := engine.Remote.ResolveProject(ctx, connection, member, project)
			if err != nil || !strings.HasSuffix(resolved.Directory, "/project-"+project.ID) {
				t.Error(resolved, err)
			}
		})
	}

	group.Wait()
	manager := remote.NewManager(engine.Remote.Connector)
	resolved, err := manager.ResolveProject(ctx, connection, second, project)
	if err != nil {
		t.Fatal(err)
	}

	// A pool mount which disappears must fail; it must never create a fresh,
	// empty replacement and let the agent pretend its checkpoints survived.
	if err := os.Rename(resolved.Directory, resolved.Directory+"-unmounted"); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.ResolveProject(ctx, connection, first, project); err == nil ||
		!strings.Contains(err.Error(), "same persistent storage") {
		t.Fatal("missing persistent workspace was silently replaced", err)
	}

	if _, err := os.Stat(resolved.Directory); !os.IsNotExist(err) {
		t.Fatal("missing pool workspace was recreated", err)
	}
}

func TestWorkspacePreparationFailsBeforeInference(t *testing.T) {
	engine, record, _, machine := managedWorkspaceEngine(t)
	var calls atomic.Int32
	engine.Provider = providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		calls.Add(1)
		return providers.Result{Text: "Should never run"}, nil
	})

	blocker := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(blocker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}

	machine.WorkspaceRoot = blocker + "/projects"
	_ = engine.Store.Save("machine", machine.ID, "", machine)

	if err := engine.PromptContext(context.Background(), record.ID, "Create a project"); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, record.ID); final.Status != "error" {
		t.Fatal(final)
	}

	if calls.Load() != 0 {
		t.Fatal("inference started without a usable workspace")
	}

	machine.WorkspaceRoot = filepath.Join(t.TempDir(), "corrected root")
	_ = engine.Store.Save("machine", machine.ID, "", machine)

	if result := engine.CheckAgentConnection(context.Background(), record.ID); !result.Connected {
		t.Fatal("an unsuccessful preparation pinned the invalid root", result)
	}

	if calls.Load() != 0 {
		t.Fatal("connection recovery started inference")
	}
}

func TestLegacyProjectPathIsPreservedWhenTheNewUIOmitsDirectory(t *testing.T) {
	engine, record := testEngine(t, nil)
	var previous domain.Project
	_ = engine.Store.Load("project", record.ProjectID, &previous)
	request := previous
	request.Directory = ""
	request.Name = "Edited in the new UI"
	saved, err := engine.SaveProject(request)
	if err != nil || saved.ManagedWorkspace || saved.Directory != previous.Directory {
		t.Fatal(saved, err)
	}
}
