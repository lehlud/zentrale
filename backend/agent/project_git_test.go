package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectGitPreparationConfiguresPlainGitAndClearsDetachedCredentials(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	engine, record := fixture.engine, fixture.record
	project := fixture.project
	project.CommitName, project.CommitEmail = "Project Author", "project@example.test"
	if err := engine.Store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}
	if result := engine.CheckAgentConnection(context.Background(), record.ID); !result.Connected {
		t.Fatal(result)
	}
	git := func(arguments ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", fixture.directory}, arguments...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "SSH_AUTH_SOCK=/unrelated-agent")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("plain Git %v failed: %s: %v", arguments, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	if git("config", "user.name") != project.CommitName || git("config", "user.email") != project.CommitEmail {
		t.Fatal("repository identity was not persisted for plain Git")
	}
	// This goes through the installed wrapper, not the harness Git tool, proving
	// the same persistent configuration works for an editor or terminal process.
	if !strings.Contains(git("ls-remote", "origin", "HEAD"), "HEAD") {
		t.Fatal("plain Git did not authenticate with the repository key")
	}
	key := filepath.Join(fixture.directory, ".git", "zentrale-git", "key")
	if info, err := os.Stat(key); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("repository key missing or not private", info, err)
	}
	project.RepositoryID, project.GitConnectionID, project.RepositoryName = "", "", ""
	if err := engine.Store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Remove("source", fixture.source.ID); err != nil {
		t.Fatal(err)
	}
	if result := engine.CheckAgentConnection(context.Background(), record.ID); !result.Connected {
		t.Fatal(result)
	}
	if _, err := os.Stat(key); !os.IsNotExist(err) {
		t.Fatal("detached source retained a repository key", err)
	}
	if git("config", "core.sshCommand") != "false" {
		t.Fatal("detached source could fall back to unrelated SSH credentials")
	}
}
