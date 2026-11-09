package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) listProjectMachines(agentRecord domain.Agent) ([]any, error) {
	project, err := engine.project(agentRecord)
	if err != nil {
		return nil, err
	}

	result := []any{}
	for _, machineID := range project.MachineIDs {
		machine, err := engine.Remote.Connector.Machine(machineID)
		if err != nil {
			return nil, err
		}

		result = append(result, machine)
	}

	for _, poolID := range project.PoolIDs {
		var pool domain.Pool
		if err := engine.Store.Load("pool", poolID, &pool); err != nil {
			return nil, err
		}

		result = append(result, pool)
	}

	return result, nil
}

func projectAllowsMachine(project domain.Project, machineID string) bool {
	for _, assignedID := range project.MachineIDs {
		if assignedID == machineID {
			return true
		}
	}

	for _, poolID := range project.PoolIDs {
		if strings.HasPrefix(machineID, poolID+"/") {
			return true
		}
	}

	return false
}

func (engine *Engine) selectProjectMachine(
	ctx context.Context,
	agentRecord domain.Agent,
	machineID string,
) (domain.Machine, error) {
	project, err := engine.project(agentRecord)
	if err != nil {
		return domain.Machine{}, err
	}

	if !projectAllowsMachine(project, machineID) {
		return domain.Machine{}, errors.New("machine is not assigned to this project")
	}

	connection, machine, err := engine.Remote.Connect(ctx, project, agentRecord.ID, machineID, nil)
	if err != nil {
		return domain.Machine{}, err
	}
	defer connection.Close()

	if machine.ID != machineID {
		return domain.Machine{}, errors.New("requested machine unavailable; failover selected " + machine.ID)
	}

	if _, err := engine.Remote.ResolveProject(ctx, connection, machine, project); err != nil {
		return domain.Machine{}, err
	}

	agentRecord.MachineID = machineID

	return machine, engine.saveAgent(agentRecord)
}
