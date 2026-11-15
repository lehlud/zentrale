package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestManagedVenvUsesOwnerScratchAndBoundedCleanup(t *testing.T) {
	shell := shellPeerHandler("/bin/sh", os.Environ())
	commands := make(chan string, 1)
	engine, parent, _ := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Error("venv helpers must not call a model")
		return providers.Result{}, nil
	}), func(command string, input io.Reader, output, failures io.Writer) int {
		if strings.Contains(command, "python3 -m venv ") {
			commands <- command
			// Exercise the real shell path expansion without bootstrapping pip.
			command = strings.Replace(command, "python3 -m venv ", "mkdir -p -- ", 1)
		}

		return shell(command, input, output, failures)
	})
	child := parent
	child.ID, child.ParentID, child.Role = NewID(), parent.ID, "worker"
	if err := engine.Store.Save("agent", child.ID, child.ProjectID, child); err != nil {
		t.Fatal(err)
	}

	// Real venv tools run within an active agent turn. Otherwise completed jobs
	// intentionally trigger idle cleanup before the next tool can use the venv.
	engine.mu.Lock()
	engine.running[child.ID] = func() {}
	engine.mu.Unlock()
	t.Cleanup(func() {
		engine.mu.Lock()
		delete(engine.running, child.ID)
		engine.mu.Unlock()
	})

	temporary, err := engine.temporaryDirectory(child.ID)
	if err != nil || temporary != agentTemporaryDirectory(parent.ID) {
		t.Fatal("worker did not share its main agent's scratch", temporary, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(temporary) })

	result, err := engine.createVenv(context.Background(), child, map[string]any{"packages": []any{}}, false)
	if err != nil {
		t.Fatal(err)
	}
	created := result.(map[string]any)
	directory := stringArg(created, "venv")
	id := stringArg(created, "id")
	if directory != filepath.Join(temporary, "venv-"+id) {
		t.Fatal("wrong host runtime venv", directory)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatal("TMPDIR did not resolve to the owner's actual scratch", err)
	}
	if !strings.Contains(<-commands, `python3 -m venv "$TMPDIR"/`) {
		t.Fatal("venv command embedded a host scratch path")
	}

	executable := filepath.Join(directory, "bin", "python")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf managed-python"), 0700); err != nil {
		t.Fatal(err)
	}

	// Guest-absolute paths are translated structurally to the current runtime.
	pythonResult, err := engine.runPython(context.Background(), child, map[string]any{
		"venv": "/tmp/zentrale/venv-" + id, "code": "pass",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	job := pythonResult.(map[string]any)["job"].(domain.Job)
	if job.Status != "completed" || !strings.Contains(job.Output, "managed-python") {
		t.Fatalf("managed interpreter failed: %+v", job)
	}

	if _, err := engine.cleanupVenv(parent, id); err == nil {
		t.Fatal("another agent removed the worker's venv")
	}
	if _, err := engine.cleanupVenv(child, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("owned venv was not cleaned up", err)
	}

	for _, invalid := range []string{
		agentTemporaryDirectory(child.ID) + "/venv-old-worker-scope",
		temporary + "/venv-x/../../outside",
		filepath.Join(t.TempDir(), "venv-outside"),
	} {
		record := map[string]any{"id": id, "agentId": child.ID, "path": invalid}
		if err := engine.Store.Save("venv", id, child.ID, record); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.cleanupVenv(child, id); err == nil {
			t.Fatalf("invalid cleanup path accepted: %q", invalid)
		}
		var retained map[string]any
		if err := engine.Store.Load("venv", id, &retained); err != nil {
			t.Fatal("failed cleanup discarded ownership", err)
		}
	}
}
