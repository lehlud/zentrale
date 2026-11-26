package remote

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveRetirementRemovesOnlyReceiptOwnedWorkspace(t *testing.T) {
	connector, _ := daemonTestConnector(t)
	connection, err := connector.Dial(t.Context(), "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	const project, token = "archive-fixture", "fixture-token"
	root := t.TempDir()
	workspace := filepath.Join(root, "project-"+project)
	metadata := filepath.Join(root, ".zentrale-workspaces")
	for _, directory := range []string{workspace, metadata} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}

	marker := filepath.Join(metadata, "project-"+project+".json")
	neighbor := filepath.Join(root, "independent-data")
	file := filepath.Join(workspace, "checkout")
	for path, content := range map[string]string{marker: `{"token":"fixture-token"}`, neighbor: "retain", file: "fixture"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	run := func(operation, receipt string) error {
		_, err := MoveDirectoryState(t.Context(), connection, operation, project, workspace, receipt, "archive")
		return err
	}
	if err := run("check-owned", token); err != nil {
		t.Fatalf("owned workspace failed preflight: %v", err)
	}
	if err := run("archive-remove", "wrong-receipt"); err == nil {
		t.Fatal("retired a workspace without its ownership receipt")
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "fixture" {
		t.Fatalf("preflight or invalid cleanup changed the workspace: %q (%v)", data, err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := run("archive-remove", token); err != nil {
			t.Fatalf("cleanup attempt %d: %v", attempt, err)
		}
	}
	for _, path := range []string{workspace, marker} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("workspace or receipt remained: %s (%v)", path, err)
		}
	}
	if data, err := os.ReadFile(neighbor); err != nil || string(data) != "retain" {
		t.Fatalf("cleanup changed independent data: %q (%v)", data, err)
	}
}

func TestArchiveGitCommandRequiresCleanPublishedTrees(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "nested")
	if err := os.Mkdir(repository, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "git", args...)
		command.Dir = repository
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, output, err)
		}
	}
	check := func(want string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "sh", "-c", ArchiveGitCommand())
		command.Dir = root
		output, err := command.CombinedOutput()
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(string(output), want)) {
			t.Fatalf("archive check got %q (%v), wanted %q", output, err, want)
		}
	}

	check("")
	git("init", "--initial-branch=main")
	git("-c", "user.name=Test", "-c", "user.email=test@example.org", "commit", "--allow-empty", "-m", "Initial")
	check("Push local commits")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	check("")

	if err := os.WriteFile(filepath.Join(repository, "untracked.txt"), []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	check("Commit or discard local changes")
}
