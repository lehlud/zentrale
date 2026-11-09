package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

// ConnectForJob reserves independently of the agent's default connection. A
// requested machine is exact; GPU scheduling never falls back to a CPU host.
func (manager *Manager) ConnectForJob(
	ctx context.Context,
	project domain.Project,
	ownerID, machineID string,
	gpu bool,
	excluded map[string]bool,
) (*Connection, domain.Machine, error) {
	candidates, err := manager.projectCandidates(project)
	if err != nil {
		return nil, domain.Machine{}, err
	}

	failures := []string{}
	eligible := false

	for _, candidate := range candidates {
		if excluded[candidate.id] {
			continue
		}

		if machineID != "" && candidate.id != machineID {
			continue
		}

		configured, err := manager.candidateConfiguration(candidate)
		if err != nil {
			return nil, domain.Machine{}, err
		}

		if gpu && (configured.VMType != VMTypeQ35 || len(configured.GPUDevices) == 0) {
			continue
		}

		eligible = true

		if !manager.reserveJobCandidate(candidate, ownerID, gpu) {
			continue
		}

		connection, machine, err := manager.connectCandidate(ctx, candidate)
		if err == nil {
			return connection, machine, nil
		}

		manager.Release(ownerID)
		var hostKey *HostKeyError
		var poolKey *PoolHostKeyError
		if errors.As(err, &hostKey) || errors.As(err, &poolKey) {
			return nil, domain.Machine{}, err
		}

		failures = append(failures, candidate.id+": "+err.Error())
	}

	if !eligible {
		return nil, domain.Machine{}, errors.New("no assigned machine matches the requested machineId/GPU configuration; GPU tags alone do not enable VM passthrough")
	}

	return nil, domain.Machine{}, fmt.Errorf(
		"no free matching job machine (respecting GPU reservations and pool limits); wait for running jobs or select another configured machine: %s",
		strings.Join(failures, "; "),
	)
}

func (manager *Manager) reserveJobCandidate(candidate machineCandidate, ownerID string, gpu bool) bool {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	if gpu {
		for owner, machine := range manager.gpuLeases {
			if owner != ownerID && machine == candidate.id {
				return false
			}
		}
	}

	if candidate.pool != nil && !manager.reserveLocked(*candidate.pool, ownerID, candidate.id) {
		return false
	}

	if gpu {
		manager.gpuLeases[ownerID] = candidate.id
	}

	return true
}

// ResourceState is a local observation, not a claim that a remote GPU driver
// or host is ready. VFIO and single-user checks still happen before execution.
func (manager *Manager) ResourceState(project domain.Project) (map[string]any, error) {
	candidates, err := manager.projectCandidates(project)
	if err != nil {
		return nil, err
	}

	manager.mutex.Lock()
	busyGPU := map[string]bool{}
	for _, machine := range manager.gpuLeases {
		busyGPU[machine] = true
	}

	active := map[string]map[string]bool{}
	for pool, leases := range manager.leases {
		active[pool] = map[string]bool{}

		for _, machine := range leases {
			active[pool][machine] = true
		}
	}

	manager.mutex.Unlock()
	machines := []any{}
	pools := map[string]any{}
	for _, candidate := range candidates {
		machine, err := manager.candidateConfiguration(candidate)
		if err != nil {
			return nil, err
		}

		if candidate.pool != nil {
			pools[candidate.pool.ID] = map[string]any{
				"maxMachines":    candidate.pool.MaxMachines,
				"activeMachines": len(active[candidate.pool.ID]),
			}
		}

		if len(machine.GPUDevices) > 0 {
			machines = append(
				machines,
				map[string]any{
					"id":             candidate.id,
					"name":           machine.Name,
					"properties":     machine.Properties,
					"vmType":         machine.VMType,
					"gpuDevices":     machine.GPUDevices,
					"gpuJobReserved": busyGPU[candidate.id],
				},
			)
		}
	}

	return map[string]any{
		"pools":              pools,
		"configuredGPUHosts": machines,
		"hostReadiness":      "not probed; GPU properties alone do not imply passthrough",
	}, nil
}

func (manager *Manager) candidateConfiguration(candidate machineCandidate) (domain.Machine, error) {
	if candidate.pool != nil && candidate.configured != nil {
		return poolMachine(*candidate.pool, candidate.id, *candidate.configured), nil
	}

	return manager.Connector.Machine(candidate.id)
}
