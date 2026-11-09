package agent

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestAgentCommitsUseGlobalIdentityProjectOverrideAndInheritance(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is needed for commit integration tests")
	}
	engine, record, project := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Error("Git tool test must not call a model")
		return providers.Result{}, nil
	}), shellPeerHandler("/bin/sh", append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")))
	global := domain.CommitIdentity{Name: "Team O'Connor $(false)", Email: "team@example.org"}
	if err := engine.SaveCommitIdentity(global); err != nil {
		t.Fatal(err)
	}
	run := func(command string) string {
		t.Helper()
		result, err := engine.execute(context.Background(), record, command, false, true)
		if err != nil {
			t.Fatal(err)
		}
		job := result.(map[string]any)["job"].(domain.Job)
		if job.Status != "completed" {
			t.Fatalf("Git command failed: %s %s", job.Error, job.Output)
		}
		return job.Output
	}
	run("git init -q; git -c user.name='Wrong local author' -c user.email=wrong@example.org commit --allow-empty -qm first")
	// Git environment defaults apply to both author and committer, even when a
	// remote home or repository already carries an unrelated user identity.
	expectIdentity := func(expected domain.CommitIdentity) {
		t.Helper()
		got := run("git log -1 --format='%an|%ae|%cn|%ce'")
		want := expected.Name + "|" + expected.Email + "|" + expected.Name + "|" + expected.Email + "\n"
		if got != want {
			t.Fatalf("commit identity: got %q, want %q", got, want)
		}
	}
	expectIdentity(global)
	project.CommitName, project.CommitEmail = "Project Author", "project@example.org"
	if _, err := engine.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	run("git commit --allow-empty -qm second")
	expectIdentity(domain.CommitIdentity{Name: project.CommitName, Email: project.CommitEmail})
	project.CommitName, project.CommitEmail = "", ""
	if _, err := engine.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	run("git commit --allow-empty -qm third")
	expectIdentity(global)
	project.CommitName = "Incomplete"
	if _, err := engine.SaveProject(project); err == nil {
		t.Fatal("partial project override accepted")
	}
}
