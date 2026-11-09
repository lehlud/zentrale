package agent

import (
	"context"
	"errors"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) quotaMachine(record domain.Agent, machineID string) (domain.Machine, error) {
	if err := engine.Store.Load("agent", record.ID, &record); err != nil {
		return domain.Machine{}, err
	}
	var project domain.Project
	if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
		return domain.Machine{}, err
	}
	if machineID == "" {
		machineID = record.MachineID
	}
	if !projectAllowsMachine(project, machineID) {
		return domain.Machine{}, errors.New("quota machine must be assigned to this project")
	}
	return engine.Remote.Connector.Machine(machineID)
}

func (engine *Engine) storageQuota(ctx context.Context, record domain.Agent, arguments map[string]any) (any, error) {
	for key := range arguments {
		if key != "machineId" {
			return nil, errors.New("storage_quota accepts only machineId; the host command is fixed by configuration")
		}
	}
	machine, err := engine.quotaMachine(record, stringArg(arguments, "machineId"))
	if err != nil {
		return nil, err
	}
	// No sandbox detection, VM provisioning, user-supplied command, or cleanup
	// executes here. The host query is fixed by trusted pool configuration.
	connection, err := engine.Remote.Connector.Dial(ctx, machine.ID)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	return remote.ReadStorageQuota(ctx, connection, machine)
}

func (engine *Engine) storageList(ctx context.Context, record domain.Agent) (any, error) {
	connection, machine, _, err := engine.connect(ctx, record)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	return map[string]any{"machineId": machine.ID, "volumes": machine.StorageMounts, "quotaAvailable": len(machine.QuotaCommand) > 0, "scope": "only this project's managed directories are exposed; quota covers the whole user account"}, nil
}
