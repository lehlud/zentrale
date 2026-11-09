package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type recordedSandboxStatus struct {
	domain.SandboxStatus
	Configuration  string `json:"configuration"`
	Implementation string `json:"implementation"`
}

func (engine *Engine) rememberSandboxStatus(machine domain.Machine, status domain.SandboxStatus) error {
	machine = remote.WithMachineDefaults(machine)
	// Lifecycle cleanup can hold configMu while closing a connection. Observation
	// writes must stay independent of that lock, including during preflight.
	engine.sandboxMu.Lock()
	defer engine.sandboxMu.Unlock()
	configured, err := engine.Remote.Connector.Machine(machine.ID)
	if err != nil {
		return err
	}
	configuration := JSON(machine)
	if configuration != JSON(configured) {
		// A probe started before a configuration edit cannot describe the new host.
		return nil
	}
	var previous recordedSandboxStatus
	_ = engine.Store.Load("sandboxStatus", machine.ID, &previous)
	record := recordedSandboxStatus{
		SandboxStatus:  status,
		Configuration:  configuration,
		Implementation: remote.SandboxImplementationID(),
	}
	if err := engine.Store.Save("sandboxStatus", machine.ID, "", record); err != nil {
		return fmt.Errorf("save sandbox selection: %w", err)
	}
	if previous.Configuration != configuration || previous.Implementation != record.Implementation || previous.Level != status.Level || previous.Error != status.Error {
		engine.Emit("sandbox", "", status)
	}
	return nil
}

func (engine *Engine) rememberExecutionSandbox(machine domain.Machine, level string, outsideSandbox bool) error {
	if outsideSandbox {
		// An approved exception must not overwrite the result of automatic selection.
		return nil
	}
	return engine.rememberSandboxStatus(machine, domain.SandboxStatus{
		MachineID: machine.ID, Mode: machine.Sandbox, Level: level, CheckedAt: time.Now().UTC(),
	})
}

func (engine *Engine) CheckMachineConnection(ctx context.Context, machineID string) domain.MachineConnectionCheck {
	machine, err := engine.Remote.Connector.Machine(machineID)
	if err != nil {
		return domain.MachineConnectionCheck{Error: err.Error()}
	}
	result := engine.Remote.Connector.Check(ctx, machineID)
	if !result.Connected {
		return result
	}
	err = engine.rememberSandboxStatus(machine, result.Sandbox)
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

// SandboxStatuses reads configuration in batches and does no SSH work. Changed
// or removed machines have no current observation until their next real check.
func (engine *Engine) SandboxStatuses() ([]domain.SandboxStatus, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	machines, err := loadResources[domain.Machine](engine, "machine")
	if err != nil {
		return nil, err
	}
	pools, err := loadResources[domain.Pool](engine, "pool")
	if err != nil {
		return nil, err
	}
	configurations := make(map[string]string, len(machines))
	for _, machine := range machines {
		configurations[machine.ID] = JSON(remote.WithMachineDefaults(machine))
	}
	for _, pool := range pools {
		pool = remote.WithPoolDefaults(pool)
		for _, machine := range pool.Machines {
			machine.ID = pool.ID + "/" + machine.ID
			machine.WorkspaceRoot = pool.WorkspaceRoot
			if len(machine.Storage) == 0 {
				machine.Storage = pool.Storage
			}
			if len(machine.QuotaCommand) == 0 {
				machine.QuotaCommand = pool.QuotaCommand
			}
			if machine.VMType == "" {
				machine.VMType = pool.VMType
			}
			configurations[machine.ID] = JSON(remote.WithMachineDefaults(machine))
		}
	}
	records, err := loadResources[recordedSandboxStatus](engine, "sandboxStatus")
	if err != nil {
		return nil, err
	}
	statuses := make([]domain.SandboxStatus, 0, len(records))
	implementation := remote.SandboxImplementationID()
	for _, record := range records {
		if configurations[record.MachineID] == record.Configuration && record.Implementation == implementation {
			statuses = append(statuses, record.SandboxStatus)
		}
	}
	return statuses, nil
}

func loadResources[T any](engine *Engine, kind string) ([]T, error) {
	rows, err := engine.listAll(kind, nil)
	if err != nil {
		return nil, err
	}
	var resources []T
	err = json.Unmarshal([]byte(JSON(rows)), &resources)
	return resources, err
}
