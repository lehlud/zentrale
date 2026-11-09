package remote

import (
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"gopkg.in/yaml.v3"
)

// ParsePool accepts exactly one YAML document. Unknown fields are errors so a
// misspelled limit or sandbox setting cannot silently weaken the configuration.
func ParsePool(configuration string) (domain.Pool, error) {
	pool, err := decodePoolConfiguration(configuration)
	if err != nil {
		return domain.Pool{}, err
	}
	pool = WithPoolDefaults(pool)
	if err := validatePoolSettings(pool); err != nil {
		return domain.Pool{}, err
	}
	if _, err := parsePoolKnownHosts(pool.KnownHosts); err != nil {
		return domain.Pool{}, err
	}
	if err := normalizePoolMachines(&pool); err != nil {
		return domain.Pool{}, err
	}
	return pool, nil
}

// A pool uses one shared root on every member, including projects created later.
func WithPoolDefaults(pool domain.Pool) domain.Pool {
	if pool.WorkspaceRoot == "" && pool.SharedHome {
		pool.WorkspaceRoot = DefaultWorkspaceRoot
	}
	return pool
}

func decodePoolConfiguration(configuration string) (domain.Pool, error) {
	var pool domain.Pool
	decoder := yaml.NewDecoder(strings.NewReader(configuration))
	decoder.KnownFields(true)
	if err := decoder.Decode(&pool); err != nil {
		if strings.Contains(err.Error(), "field key_id not found") || strings.Contains(err.Error(), "field keyId not found") {
			return domain.Pool{}, errors.New("pool SSH keys are managed automatically; remove key_id from the YAML")
		}
		return domain.Pool{}, fmt.Errorf("decode pool configuration: %w", err)
	}
	var additionalDocument any
	if err := decoder.Decode(&additionalDocument); err != io.EOF {
		return domain.Pool{}, errors.New("one YAML document expected")
	}
	return pool, nil
}

func validatePoolSettings(pool domain.Pool) error {
	if err := validateStorageConfiguration(pool.Storage, pool.QuotaCommand); err != nil {
		return err
	}
	if pool.VMType != "" && pool.VMType != VMTypeMicroVM && pool.VMType != VMTypeQ35 {
		return errors.New("pool vm_type must be microvm or q35")
	}
	if !ValidID(pool.ID) {
		return errors.New("pool id is invalid")
	}
	if strings.TrimSpace(pool.Name) == "" {
		return errors.New("pool name is required")
	}
	if len(pool.Machines) == 0 {
		return errors.New("pool must contain at least one machine")
	}
	if pool.MaxMachines < 1 || pool.MaxMachines > len(pool.Machines) {
		return errors.New("max_machines must be between one and the number of pool machines")
	}
	if pool.Sandbox != "" && pool.Sandbox != SandboxAutomatic && pool.Sandbox != SandboxMicroVM && pool.Sandbox != SandboxBubblewrap && pool.Sandbox != SandboxDisabled {
		return errors.New("pool sandbox must be auto, microvm, bwrap or none")
	}
	if err := ValidateWorkspaceRoot(pool.WorkspaceRoot); err != nil {
		return fmt.Errorf("pool workspace_root: %w", err)
	}
	if !pool.SharedHome && !path.IsAbs(pool.WorkspaceRoot) {
		return errors.New("a pool without shared_home requires an absolute workspace_root on shared persistent storage")
	}
	return nil
}

func normalizePoolMachines(pool *domain.Pool) error {
	usedIDs := make(map[string]bool, len(pool.Machines))
	for index, machine := range pool.Machines {
		if machine.WorkspaceRoot != "" && machine.WorkspaceRoot != pool.WorkspaceRoot {
			return fmt.Errorf("pool machine %q must use the pool workspace_root", machine.ID)
		}
		machine.WorkspaceRoot = pool.WorkspaceRoot
		if len(machine.Storage) == 0 {
			machine.Storage = pool.Storage
		}
		if len(machine.QuotaCommand) == 0 {
			machine.QuotaCommand = pool.QuotaCommand
		}
		if machine.Sandbox == "" {
			machine.Sandbox = pool.Sandbox
		}
		if machine.VMType == "" {
			machine.VMType = pool.VMType
		}
		machine.SingleUser = machine.SingleUser || pool.SingleUser
		machine = WithMachineDefaults(machine)
		if err := validateMachine(machine, false); err != nil {
			return fmt.Errorf("pool machine %q: %w", machine.ID, err)
		}
		if usedIDs[machine.ID] {
			return fmt.Errorf("duplicate pool machine id %q", machine.ID)
		}
		usedIDs[machine.ID] = true
		pool.Machines[index] = machine
	}
	return nil
}
