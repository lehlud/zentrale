package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

type repositoryFixture struct {
	engine        *Engine
	record        domain.Agent
	project       domain.Project
	source        domain.Source
	directory     string
	server        *testssh.Server
	clones        atomic.Int32
	inspections   atomic.Int32
	beforeInspect func()
}

func newRepositoryFixture(t *testing.T, beforeClone func()) *repositoryFixture {
	t.Helper()
	fixture := &repositoryFixture{}
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, record, project, machine := managedWorkspaceEngine(t, func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "def checkout_exists(") {
			input, err := io.ReadAll(stdin)
			if err != nil {
				return 1
			}
			var configuration struct {
				Operation string `json:"operation"`
			}
			if err := json.Unmarshal(input, &configuration); err != nil {
				return 1
			}
			if configuration.Operation == "inspect" {
				fixture.inspections.Add(1)
				if fixture.beforeInspect != nil {
					fixture.beforeInspect()
				}
			}
			stdin = bytes.NewReader(input)
		}
		if strings.Contains(command, "ZENTRALE_EDITOR_READY ") {
			if _, err := os.Stat(filepath.Join(fixture.directory, "README.md")); err != nil {
				t.Error("editor started before its repository was available", err)
			}
			_, _ = fmt.Fprintln(stdout, `ZENTRALE_EDITOR_READY {"socketPath":"/tmp/ze-repository-test/vscode.sock"}`)
			_, _ = io.Copy(io.Discard, stdin)
			return 0
		}
		return shell(command, stdin, stdout, stderr)
	})
	fixture.engine, fixture.record, fixture.project = engine, record, project
	fixture.directory = filepath.Join(machine.WorkspaceRoot, "project-"+project.ID)
	t.Cleanup(func() { _ = os.RemoveAll(agentTemporaryDirectory(record.ID)) })

	seed := filepath.Join(t.TempDir(), "seed")
	bare := filepath.Join(t.TempDir(), "repository.git")
	git := func(arguments ...string) {
		t.Helper()
		command := exec.Command("git", arguments...)
		command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("fixture Git: %v\n%s", err, output)
		}
	}
	git("init", "--", seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("Repository content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("-C", seed, "add", "README.md")
	git(
		"-C", seed,
		"-c", "user.name=Fixture",
		"-c", "user.email=fixture@example.test",
		"-c", "commit.gpgsign=false",
		"commit", "-m", "Initial content",
	)
	git("clone", "--bare", "--", seed, bare)

	server, err := testssh.StartWithCommandHandler(func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		if strings.HasPrefix(command, "git-upload-pack ") {
			fixture.clones.Add(1)
			if beforeClone != nil {
				beforeClone()
			}
		}
		return shell(command, stdin, stdout, stderr)
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.server = server
	t.Cleanup(server.Close)
	public, err := remote.DecodePublicKey(server.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}

	address := &url.URL{
		Scheme: "ssh", User: url.User("git"),
		Host: net.JoinHostPort(server.Host, strconv.Itoa(server.Port)), Path: bare,
	}
	fixture.source = domain.Source{
		ID: "root-repository", ProjectID: project.ID, Name: "repository",
		URL: address.String(), KeyID: machine.KeyID, ProjectRoot: true,
	}
	if err := engine.Store.Save("source", fixture.source.ID, project.ID, fixture.source); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestModelRoundsReusePreparedSourcesUntilReconnectOrConfigurationChanges(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	engine, record := fixture.engine, fixture.record
	var calls atomic.Int32
	engine.Provider = withTestGoalReviewer(providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.Config.ID == "approver-provider" {
			return providers.Result{Text: `{"allowed":true,"reason":"","blanket":false}`, UsageKnown: true}, nil
		}
		round := calls.Add(1)
		if round < 4 {
			return providers.Result{
				Calls:      []providers.Call{{ID: NewID(), Name: "read_file", Arguments: map[string]any{"path": "README.md"}}},
				UsageKnown: true,
			}, nil
		}
		return providers.Result{Text: "Repository inspected.", UsageKnown: true}, nil
	}))
	if err := engine.Prompt(record.ID, "Read the repository in several steps"); err != nil {
		t.Fatal(err)
	}
	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
		t.Fatal(final.Error)
	}
	if calls.Load() != 4 || fixture.inspections.Load() != 1 || fixture.clones.Load() != 1 {
		t.Fatal("model rounds repeated repository preparation", calls.Load(), fixture.inspections.Load(), fixture.clones.Load())
	}

	// The lifecycle releases the SSH connection when a task finishes. Its next
	// task must inspect again, even though it keeps the existing working tree.
	if err := engine.requireConnection(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if fixture.inspections.Load() != 2 {
		t.Fatal("a new connection reused stale readiness", fixture.inspections.Load())
	}
	if err := engine.requireConnection(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if fixture.inspections.Load() != 2 {
		t.Fatal("unchanged sources were re-inspected", fixture.inspections.Load())
	}

	// A subagent preparing the same workspace must not make this agent wait
	// for a repository check it has already completed on its live connection.
	value, _ := engine.sourcePreparations.Load(fixture.directory)
	lock := value.(chan struct{})
	lock <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := engine.requireConnection(ctx, record.ID)
	cancel()
	<-lock
	if err != nil {
		t.Fatal("a prepared agent waited behind another agent", err)
	}

	additional := fixture.source
	additional.ID, additional.Name, additional.ProjectRoot = NewID(), "library", false
	if err := engine.Store.Save("source", additional.ID, record.ProjectID, additional); err != nil {
		t.Fatal(err)
	}
	if err := engine.requireConnection(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if fixture.inspections.Load() != 3 || fixture.clones.Load() != 2 {
		t.Fatal("changed sources were not prepared", fixture.inspections.Load(), fixture.clones.Load())
	}
	if _, err := os.Stat(filepath.Join(fixture.directory, "library", "README.md")); err != nil {
		t.Fatal("new source missing", err)
	}
}

func TestRepositoryWaitCanBeCancelledWithoutCachingSuccess(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	engine, record := fixture.engine, fixture.record
	connection, machine, project, err := engine.connect(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	engine.sourcePreparations.Store(fixture.directory, lock)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err = engine.prepareProjectSourcesForConnection(ctx, connection, machine, project, record.ID, true)
	if err == nil || !strings.Contains(err.Error(), "wait for another agent") || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatal("waiting did not report its own cancellable stage", err)
	}
	if fixture.inspections.Load() != 0 {
		t.Fatal("inspected a workspace while another agent was cloning")
	}
	<-lock
	if err := engine.requireConnection(context.Background(), record.ID); err != nil {
		t.Fatal("cancelled preparation was not retried", err)
	}
	if fixture.inspections.Load() != 1 || fixture.clones.Load() != 1 {
		t.Fatal("cancelled preparation was cached", fixture.inspections.Load(), fixture.clones.Load())
	}
}

func TestCancelledRepositoryInspectionInvalidatesEarlierSuccess(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	engine, record := fixture.engine, fixture.record
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	var block atomic.Bool
	fixture.beforeInspect = func() {
		if block.CompareAndSwap(true, false) {
			close(started)
			<-release
		}
	}
	if err := engine.requireConnection(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}

	block.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan domain.MachineConnectionCheck, 1)
	go func() { results <- engine.CheckAgentConnection(ctx, record.ID) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("explicit check did not inspect the repository again")
	}
	cancel()
	select {
	case result := <-results:
		if result.Connected || !strings.Contains(result.Error, "context canceled") {
			t.Fatal("inspection ignored cancellation", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop inspection")
	}
	unblock()
	if err := engine.requireConnection(context.Background(), record.ID); err != nil {
		t.Fatal("cancelled inspection could not recover", err)
	}
	if fixture.inspections.Load() != 3 || fixture.clones.Load() != 1 {
		t.Fatal("cancelled inspection reused earlier success or recloned local work", fixture.inspections.Load(), fixture.clones.Load())
	}
}

func TestRepositoriesAreClonedBeforeInferenceAndReusedWithoutNetwork(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	engine, record := fixture.engine, fixture.record
	additional := fixture.source
	additional.ID, additional.Name = "additional", "library"
	additional.ProjectRoot, additional.ReadOnly = false, true
	if err := engine.Store.Save("source", additional.ID, record.ProjectID, additional); err != nil {
		t.Fatal(err)
	}

	content := "Repository content\n"
	var calls atomic.Int32
	engine.Provider = withTestGoalReviewer(providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		calls.Add(1)
		for _, directory := range []string{fixture.directory, filepath.Join(fixture.directory, "library")} {
			actual, err := os.ReadFile(filepath.Join(directory, "README.md"))
			expected := "Repository content\n"
			if directory == fixture.directory {
				expected = content
			}
			if err != nil || string(actual) != expected {
				t.Errorf("model saw an unprepared repository: %q, %v", actual, err)
			}
		}
		return providers.Result{Text: "Repository inspected.", UsageKnown: true}, nil
	}))
	if err := engine.Prompt(record.ID, "Explain the project"); err != nil {
		t.Fatal(err)
	}
	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
		t.Fatal(final.Error)
	}
	if fixture.clones.Load() != 2 || calls.Load() != 1 {
		t.Fatal("expected two checkouts before the first model call", fixture.clones.Load(), calls.Load())
	}
	history, err := engine.loadHistory(record.ID)
	if err != nil || len(history) != 2 || history[0].Role != "user" || history[1].Role != "assistant" {
		t.Fatal("initial checkout leaked into model tool history", history, err)
	}
	pushURL := exec.Command(
		"git", "-C", filepath.Join(fixture.directory, "library"),
		"remote", "get-url", "--push", "origin",
	)
	if output, err := pushURL.CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "DISABLED_READ_ONLY" {
		t.Fatal("additional read-only source became writable", string(output), err)
	}

	content = "Local work must survive\n"
	for name, body := range map[string]string{"README.md": content, "untracked.txt": "keep"} {
		if err := os.WriteFile(filepath.Join(fixture.directory, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixture.server.RejectAuthentication(true)
	engine.releaseConnection(record.ID)
	if err := engine.Prompt(record.ID, "Continue inspecting"); err != nil {
		t.Fatal(err)
	}
	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
		t.Fatal(final.Error)
	}
	if fixture.clones.Load() != 2 || calls.Load() != 2 {
		t.Fatal("reconnect fetched or recloned an existing repository", fixture.clones.Load(), calls.Load())
	}
	if actual, err := os.ReadFile(filepath.Join(fixture.directory, "untracked.txt")); err != nil || string(actual) != "keep" {
		t.Fatal("preparation removed local work", string(actual), err)
	}
}

func TestRepositoryCloneFailureStopsInferenceAndRetainsThePromptForRetry(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	engine, record := fixture.engine, fixture.record
	public, _ := remote.DecodePublicKey(fixture.server.PublicKey)
	if err := engine.Store.DistrustSSHKey(public); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	engine.Provider = withTestGoalReviewer(providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		calls.Add(1)
		return providers.Result{Text: "Repository inspected.", UsageKnown: true}, nil
	}))
	if err := engine.Prompt(record.ID, "Explain this repository"); err != nil {
		t.Fatal(err)
	}
	if final := waitForAgent(t, engine, record.ID); final.Status != "error" ||
		!strings.Contains(final.Error, "Host key verification failed") {
		t.Fatal("untrusted repository host did not stop preparation", final)
	}
	history, err := engine.loadHistory(record.ID)
	if err != nil || len(history) != 1 || history[0].Content != "Explain this repository" || calls.Load() != 0 {
		t.Fatal("failed checkout consumed the task or reached inference", history, err, calls.Load())
	}
	if err := engine.Store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}
	if err := engine.ResumeInputs(record.ID); err != nil {
		t.Fatal(err)
	}
	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
		t.Fatal("checkout could not recover after trust was corrected", final.Error)
	}
	history, err = engine.loadHistory(record.ID)
	if err != nil || len(history) != 2 || calls.Load() != 1 || fixture.clones.Load() != 1 {
		t.Fatal("retry duplicated or lost work", history, err, calls.Load(), fixture.clones.Load())
	}
}

func TestRepositoryPreparationPreservesNonemptyDirectoriesAndDifferentOrigins(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	engine, record := fixture.engine, fixture.record
	connection, _, _, err := engine.connect(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	readme := filepath.Join(fixture.directory, "README.md")
	if err := os.WriteFile(readme, []byte("existing user data"), 0600); err != nil {
		t.Fatal(err)
	}
	result := engine.CheckAgentConnection(context.Background(), record.ID)
	if result.Connected || !strings.Contains(result.Error, "Existing files were preserved") {
		t.Fatal("nonempty directory was not protected", result)
	}
	if actual, err := os.ReadFile(readme); err != nil || string(actual) != "existing user data" || fixture.clones.Load() != 0 {
		t.Fatal(string(actual), err, fixture.clones.Load())
	}
	if err := os.Remove(readme); err != nil {
		t.Fatal(err)
	}
	if result := engine.CheckAgentConnection(context.Background(), record.ID); !result.Connected {
		t.Fatal(result)
	}
	changeOrigin := exec.Command(
		"git", "-C", fixture.directory, "remote", "set-url", "origin", "git@example.test:other.git",
	)
	if output, err := changeOrigin.CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	result = engine.CheckAgentConnection(context.Background(), record.ID)
	if result.Connected || !strings.Contains(result.Error, "origin does not match") || fixture.clones.Load() != 1 {
		t.Fatal("different repository was silently reused or replaced", result)
	}
}

func TestConcurrentRepositoryPreparationSharesOneCheckout(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var blocked atomic.Bool
	fixture := newRepositoryFixture(t, func() {
		if blocked.CompareAndSwap(false, true) {
			close(started)
			<-release
		}
	})
	engine, record := fixture.engine, fixture.record
	second := record
	second.ID = NewID()
	t.Cleanup(func() { _ = os.RemoveAll(agentTemporaryDirectory(second.ID)) })
	if err := engine.Store.Save("agent", second.ID, second.ProjectID, second); err != nil {
		t.Fatal(err)
	}
	results := make(chan domain.MachineConnectionCheck, 2)
	go func() { results <- engine.CheckAgentConnection(context.Background(), record.ID) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("initial checkout did not start")
	}
	go func() { results <- engine.CheckAgentConnection(context.Background(), second.ID) }()
	close(release)
	for range 2 {
		select {
		case result := <-results:
			if !result.Connected {
				t.Error(result)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent checkout did not finish")
		}
	}
	if fixture.clones.Load() != 1 {
		t.Fatal("two conversations cloned the same checkout", fixture.clones.Load())
	}
}

func TestEditorPreparesRepositoryBeforeLaunching(t *testing.T) {
	fixture := newRepositoryFixture(t, nil)
	session, err := fixture.engine.StartEditor(context.Background(), fixture.project.ID, fixture.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Editors.Stop(fixture.project.ID, session.ID)
	if session.Directory != fixture.directory || fixture.clones.Load() != 1 {
		t.Fatal("editor did not open the prepared repository", session, fixture.clones.Load())
	}
}
