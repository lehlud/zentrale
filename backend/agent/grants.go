package agent

import "github.com/lehlud/zentrale/domain"

// Reuse is checked against the bytes' SHA-256 and instruction revision on every
// read. Keeping a remote watcher per grant added processes without strengthening
// that boundary; no file bytes reach an agent before the digest check.
func (engine *Engine) rememberReadGrant(agentRecord domain.Agent, filename, digest, machineID string, revision uint64) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.ctx.Err() != nil || engine.instructionRevisions[agentRecord.ProjectID] != revision {
		return
	}
	if engine.grants[agentRecord.ID] == nil {
		engine.grants[agentRecord.ID] = make(map[string]grant)
	}
	engine.grants[agentRecord.ID][filename] = grant{Path: filename, Hash: digest, MachineID: machineID, Revision: revision}
}

func (engine *Engine) revokeReadGrant(agentID string, permission grant) {
	engine.mu.Lock()
	current, exists := engine.grants[agentID][permission.Path]
	revoked := exists && current == permission
	if revoked {
		delete(engine.grants[agentID], permission.Path)
	}
	engine.mu.Unlock()
	if revoked {
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
