package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) createVenv(
	ctx context.Context,
	agentRecord domain.Agent,
	args map[string]any,
	network bool,
) (any, error) {
	packages, err := stringsArg(args, "packages")
	if err != nil {
		return nil, err
	}

	if len(packages) > 0 && !network {
		return nil, errors.New("package installation requires network=true")
	}

	id := NewID()
	temporary, err := engine.temporaryDirectory(agentRecord.ID)
	if err != nil {
		return nil, err
	}
	dir := temporary + "/venv-" + id
	command := "python3 -m venv " + remote.Quote(dir)

	if len(packages) > 0 {
		command += " && " + remote.Quote(dir+"/bin/pip") + " install --"

		for _, pkg := range packages {
			command += " " + remote.Quote(pkg)
		}
	}

	// Save ownership first so partial creation is cleaned up on errors as well.
	conn, m, _, err := engine.connect(ctx, agentRecord)
	if err != nil {
		return nil, err
	}

	conn.Close()
	record := map[string]any{"id": id, "agentId": agentRecord.ID, "path": dir, "machineId": m.ID}
	if err := engine.Store.Save("venv", id, agentRecord.ID, record); err != nil {
		return nil, err
	}

	result, err := engine.execute(ctx, agentRecord, command, network, false)
	if err != nil {
		return nil, err
	}

	return map[string]any{"id": id, "venv": dir, "execution": result}, nil
}

func (engine *Engine) cleanupVenv(agentRecord domain.Agent, id string) (any, error) {
	var record map[string]any
	if err := engine.Store.Load("venv", id, &record); err != nil {
		return nil, err
	}

	if record["agentId"] != agentRecord.ID {
		return nil, errors.New("venv belongs to another agent")
	}

	dir := stringArg(record, "path")
	temporary, err := engine.temporaryDirectory(agentRecord.ID)
	if err != nil {
		return nil, err
	}
	// Old worker-owned environments can still be removed by their recorded owner.
	legacy := agentTemporaryDirectory(agentRecord.ID)
	valid := false
	for _, root := range []string{temporary, legacy} {
		valid = valid || (strings.HasPrefix(dir, root+"/venv-") && !strings.Contains(strings.TrimPrefix(dir, root+"/"), "/"))
	}
	if !valid || strings.Contains(dir, "..") {
		return nil, errors.New("invalid managed venv path")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := engine.Remote.Connector.Dial(ctx, stringArg(record, "machineId"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	_, err = remote.Run(ctx, conn.Client, "rm -rf -- "+remote.Quote(dir))
	if err != nil {
		return nil, err
	}

	return true, engine.Store.Remove("venv", id)
}

func (engine *Engine) cleanupVenvs(agentID string) {
	var agentRecord domain.Agent
	if engine.Store.Load("agent", agentID, &agentRecord) != nil {
		return
	}

	rows, _ := engine.listAll("venv", &agentID)

	for _, row := range rows {
		_, err := engine.cleanupVenv(agentRecord, stringArg(row, "id"))
		if err != nil {
			engine.Emit("cleanup_error", agentID, err.Error())
		}
	}
}
