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

func TestPlainGitPushesWorkflowChangesWithRepositoryKeyDespiteHTTPSConfiguration(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	engine, record := fixture.engine, fixture.record
	if result := engine.CheckAgentConnection(context.Background(), record.ID); !result.Connected {
		t.Fatal(result)
	}

	fixture.source.BindingID = "ssh-binding"
	if err := engine.Store.Save("repository_binding", fixture.source.BindingID, "", map[string]any{
		"sshUrl": fixture.source.URL, "baseUrl": "https://forge.example", "fullName": "team/demo",
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Save("source", fixture.source.ID, fixture.project.ID, fixture.source); err != nil {
		t.Fatal(err)
	}

	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[url \"https://oauth.example/\"]\n\tinsteadOf = ssh://git@\n\tpushInsteadOf = ssh://git@\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git := func(arguments ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", fixture.directory}, arguments...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1", "SSH_AUTH_SOCK=/unrelated-agent")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("Git %v failed: %s: %v", arguments, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	git("config", "--local", "remote.origin.url", "https://old-oauth-token@forge.example/team/demo.git")
	git("config", "--local", "--add", "remote.origin.pushurl", "https://forge.example/team/demo.git")
	git("config", "--local", "--add", "remote.origin.pushurl", "https://obsolete.example/other.git")
	if result := engine.CheckAgentConnection(context.Background(), record.ID); !result.Connected {
		t.Fatal(result)
	}
	if got := git("remote", "get-url", "--push", "--all", "origin"); got != fixture.source.URL {
		t.Fatalf("push still uses another credential: %q", got)
	}
	if got := git("remote", "get-url", "origin"); got != fixture.source.URL {
		t.Fatal(got)
	}

	workflow := filepath.Join(fixture.directory, ".github", "workflows", "tests.yml")
	if err := os.MkdirAll(filepath.Dir(workflow), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflow, []byte("name: Tests\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo OK\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".github/workflows/tests.yml")
	git("-c", "user.name=Test", "-c", "user.email=test@example.test", "-c", "commit.gpgsign=false", "commit", "-m", "Add workflow")
	git("push", "origin", "HEAD:refs/heads/workflow-test")
	git("push", "https://forge.example/team/demo.git", "HEAD:refs/heads/workflow-test-https")
	if !strings.Contains(git("ls-remote", "origin", "refs/heads/workflow-test*"), "workflow-test-https") {
		t.Fatal("workflow commit did not reach SSH repository")
	}

	source := fixture.source
	source.ReadOnly = true
	if err := engine.Store.Save("source", source.ID, fixture.project.ID, source); err != nil {
		t.Fatal(err)
	}
	if result := engine.CheckAgentConnection(context.Background(), record.ID); !result.Connected {
		t.Fatal(result)
	}
	command := exec.Command("git", "-C", fixture.directory, "push", "https://forge.example/team/demo.git", "HEAD:refs/heads/forbidden")
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1")
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "Unexpected repository SSH command") {
		t.Fatalf("read-only push was not rejected by the repository SSH wrapper: %s: %v", output, err)
	}
}
