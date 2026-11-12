package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestReadinessWarmTurnsSkipPreparationButExplicitChecksDoNot(t *testing.T) {
	var prepares, stages, probes atomic.Int32
	var generation atomic.Value
	generation.Store(strings.Repeat("a", 64))
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, record, project := executionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		originalCommand := command
		command = strings.ReplaceAll(command, `'"'"'`, `'`)
		if strings.Contains(command, "guest dispatcher is unresponsive") {
			probes.Add(1)
			_, _ = io.Copy(io.Discard, input)
			fmt.Fprintln(stdout, generation.Load().(string))
			return 0
		}
		// The native runner is now compressed; match its stable bootstrap and
		// final action argument instead of plaintext inside the embedded runner.
		if strings.Contains(command, "<zentrale-microvm>") {
			if strings.Contains(command, " 'ensure'") || strings.Contains(command, " 'select'") {
				fmt.Fprintln(stdout, "microvm")
				return 0
			}
			_, _ = io.Copy(io.Discard, input)
			if strings.Contains(command, " 'prepare'") {
				prepares.Add(1)
				fmt.Fprintln(stdout, `{"os":"fedora","version":"45","bootId":"11111111-2222-3333-4444-555555555555"}`)
				return 0
			}
			if strings.Contains(command, " 'stage'") {
				stages.Add(1)
				fmt.Fprintln(stdout, "/tmp/test-vm/session/requests/0123456789abcdef0123456789abcdef")
				return 0
			}
			return 0
		}
		return shell(originalCommand, input, stdout, stderr)
	})
	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	machine.Sandbox = remote.SandboxMicroVM
	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := engine.requireConnection(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	initialPrepare, initialStage, initialProbe := prepares.Load(), stages.Load(), probes.Load()
	if initialPrepare != 1 || initialProbe != 1 {
		t.Fatal("cold path did not establish readiness", initialPrepare, initialProbe)
	}
	for i := 0; i < 3; i++ {
		if err := engine.requireConnection(ctx, record.ID); err != nil {
			t.Fatal(err)
		}
	}
	if prepares.Load() != initialPrepare || stages.Load() != initialStage || probes.Load() != initialProbe+3 || engine.RunState(record.ID).Phase != "ready" {
		t.Fatal("warm turns repeated setup", prepares.Load(), stages.Load(), probes.Load(), engine.RunState(record.ID))
	}
	openWorkspace := func(agentID string) error {
		_, err := engine.withEditorEnvironment(ctx, project.ID, agentID, func(ctx context.Context, connection *remote.Connection, machine domain.Machine, resolved domain.Project, target remote.EditorTarget) (domain.EditorSession, error) {
			if target.AgentID != record.ID || target.TemporaryDirectory != agentTemporaryDirectory(record.ID) {
				t.Errorf("editor/terminal did not reuse owner scope: %+v", target)
			}
			return domain.EditorSession{}, nil
		})
		return err
	}
	child := domain.Agent{ID: "warm-editor-child", ProjectID: project.ID, ParentID: record.ID, Role: "child"}
	if err := engine.Store.Save("agent", child.ID, project.ID, child); err != nil {
		t.Fatal(err)
	}
	beforeProbes := probes.Load()
	for _, selection := range []string{record.ID, "", child.ID} {
		if err := openWorkspace(selection); err != nil {
			t.Fatal(err)
		}
	}
	if prepares.Load() != initialPrepare || stages.Load() != initialStage || probes.Load() != beforeProbes+3 {
		t.Fatal("warm editor/terminal repeated VM or source preparation", prepares.Load(), stages.Load(), probes.Load())
	}
	if result := engine.CheckAgentConnection(ctx, record.ID); !result.Connected || prepares.Load() != initialPrepare+1 {
		t.Fatal("explicit check skipped setup", result, prepares.Load())
	}
	// Reset/recovery creates a new native boot generation even on the same SSH.
	generation.Store(strings.Repeat("b", 64))
	before := prepares.Load()
	if err := openWorkspace(record.ID); err != nil || prepares.Load() != before+1 {
		t.Fatal("editor/terminal generation change reused readiness", err, prepares.Load())
	}
	// The cold editor path itself must establish readiness for the next open.
	before = prepares.Load()
	beforeStages := stages.Load()
	if err := openWorkspace(record.ID); err != nil || prepares.Load() != before || stages.Load() != beforeStages {
		t.Fatal("editor did not retain validated readiness", err, prepares.Load(), stages.Load())
	}
	operation := engine.vmOperation(record.ID, machine.ID)
	if err := operation.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	operation.stopped = true
	operation.Unlock()
	before = prepares.Load()
	if err := engine.requireConnection(ctx, record.ID); err != nil || prepares.Load() != before+1 {
		t.Fatal("stopped scope reused readiness", err, prepares.Load())
	}
	engine.releaseConnection(record.ID)
	before = prepares.Load()
	if err := engine.requireConnection(ctx, record.ID); err != nil || prepares.Load() != before+1 {
		t.Fatal("disconnected scope reused readiness", err, prepares.Load())
	}
	// A different assigned machine drops the per-transport readiness proof.
	alternate := machine
	alternate.ID = "failover-worker"
	if err := engine.Store.Save("machine", alternate.ID, "", alternate); err != nil {
		t.Fatal(err)
	}
	project.MachineIDs = append(project.MachineIDs, alternate.ID)
	if err := engine.Store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Load("agent", record.ID, &record); err != nil {
		t.Fatal(err)
	}
	record.MachineID = alternate.ID
	if err := engine.Store.Save("agent", record.ID, record.ProjectID, record); err != nil {
		t.Fatal(err)
	}
	before = prepares.Load()
	if err := engine.requireConnection(ctx, record.ID); err != nil || prepares.Load() != before+1 {
		t.Fatal("failover reused old readiness", err, prepares.Load())
	}
	connection, machine, resolved, err := engine.connect(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	baseline, err := engine.connectionReadinessKey(record, machine, resolved)
	if err != nil {
		t.Fatal(err)
	}
	changedMounts := machine
	changedMounts.StorageMounts = append(append([]domain.StorageMount(nil), machine.StorageMounts...), domain.StorageMount{})
	if key, err := engine.connectionReadinessKey(record, changedMounts, resolved); err == nil && key == baseline {
		t.Fatal("storage changed without invalidation")
	}
	machine.Name += " changed"
	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	if key, err := engine.connectionReadinessKey(record, machine, resolved); err == nil && key == baseline {
		t.Fatal("machine config changed without invalidation")
	}
	changed := resolved
	changed.Directory += "-other"
	if _, ok := engine.reuseReadyConnection(ctx, record, connection, machine, changed); ok {
		t.Fatal("scope change reused readiness")
	}
	entry := engine.connectionEntry(record.ID)
	entry.mutex.Lock()
	invalidated := entry.ready == nil && entry.sourcesKey == ""
	entry.mutex.Unlock()
	if !invalidated {
		t.Fatal("scope mismatch did not invalidate sources")
	}
	trusted, err := engine.Store.TrustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range trusted {
		if err := engine.Store.DistrustSSHKey(key); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.Remote.Connector.VerifyTrust(connection); err == nil {
		t.Fatal("existing transport ignored trust revocation")
	}
	engine.releaseConnection(record.ID)
	before = probes.Load()
	if err := engine.requireConnection(ctx, record.ID); err == nil || probes.Load() != before {
		t.Fatal("new handshake ignored trust revocation", err, probes.Load())
	}
}

func TestReadinessSourceCredentialTrustAndConfigurationFingerprints(t *testing.T) {
	engine, record := testEngine(t, nil)
	var project domain.Project
	if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
		t.Fatal(err)
	}
	sources := []domain.Source{{ID: "source", ProjectID: project.ID, BindingID: "binding", URL: "ssh://example/repository"}}
	binding := map[string]any{"id": "binding", "connectionId": "github", "keyId": 1}
	if err := engine.Store.Save("repository_binding", "binding", "github", binding); err != nil {
		t.Fatal(err)
	}
	key := func() string {
		t.Helper()
		value, err := engine.sourceReadinessKey(project, sources)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := key()
	if first != key() {
		t.Fatal("unstable fingerprint")
	}
	for _, token := range []string{"first-token", "rotated-token"} {
		if err := engine.Store.Save("git_account", "github", "", map[string]any{"secret": token}); err != nil {
			t.Fatal(err)
		}
		current := key()
		if current == first {
			t.Fatal("credential change not detected")
		}
		first = current
	}
	if err := engine.Store.TrustSSHKey([]byte("new-repository-host")); err != nil {
		t.Fatal(err)
	}
	if current := key(); current == first {
		t.Fatal("trust change not detected")
	} else {
		first = current
	}
	sources[0].ReadOnly = true
	if current := key(); current == first {
		t.Fatal("source change not detected")
	} else {
		first = current
	}
	project.CommitName = "New identity"
	if key() == first {
		t.Fatal("identity change not detected")
	}
}
