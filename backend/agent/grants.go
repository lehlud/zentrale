package agent

import (
	"context"
	_ "embed"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

//go:embed scripts/watch_file.py
var watchFileScript string

// A digest check protects the next read even before the remote watch is ready.
// The watch additionally revokes grants immediately after external changes.
func (engine *Engine) rememberReadGrant(agentRecord domain.Agent, filename, digest, machineID string) {
	ctx, cancel := context.WithCancel(engine.ctx)
	permission := grant{
		Path:      filename,
		Hash:      digest,
		MachineID: machineID,

		watchID: NewID(),
		cancel:  cancel,
	}

	engine.mu.Lock()

	if previous, exists := engine.grants[agentRecord.ID][filename]; exists && previous.cancel != nil {
		previous.cancel()
	}

	if engine.grants[agentRecord.ID] == nil {
		engine.grants[agentRecord.ID] = make(map[string]grant)
	}

	engine.grants[agentRecord.ID][filename] = permission
	engine.workers.Add(1)
	engine.mu.Unlock()
	go engine.watchReadGrant(ctx, agentRecord.ID, permission)
}

func (engine *Engine) watchReadGrant(ctx context.Context, agentID string, permission grant) {
	defer engine.workers.Done()
	defer permission.cancel()
	defer engine.revokeReadGrant(agentID, permission)

	connection, err := engine.Remote.Connector.Dial(ctx, permission.MachineID)
	if err != nil {
		return
	}
	defer connection.Close()

	command := strings.Join([]string{
		"python3 -I -u -c", remote.Quote(watchFileScript), remote.Quote(permission.Path),
	}, " ")

	_, _ = remote.Run(ctx, connection.Client, command)
}

func (engine *Engine) revokeReadGrant(agentID string, permission grant) {
	engine.mu.Lock()
	current, exists := engine.grants[agentID][permission.Path]
	if exists && current.watchID == permission.watchID {
		delete(engine.grants[agentID], permission.Path)
	}

	engine.mu.Unlock()

	if exists {
		engine.Emit("grant_revoked", agentID, map[string]string{"path": permission.Path})
	}
}

// User instructions can narrow permissions even when a file is unchanged.
func (engine *Engine) invalidateProjectGrants(projectID string) {
	engine.mu.Lock()
	engine.instructionRevisions[projectID]++
	engine.mu.Unlock()
	agents, err := engine.listAll("agent", &projectID)
	if err != nil {
		return
	}

	for _, agent := range agents {
		engine.invalidate(stringArg(agent, "id"))
	}
}

func (engine *Engine) instructionRevision(projectID string) uint64 {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	return engine.instructionRevisions[projectID]
}
