package remote

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
