package remote

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/lehlud/zentrale/domain"
)

type Manager struct {
	Connector *Connector
	mutex     sync.Mutex
	// Pool -> owner -> machine. Jobs hold separate leases so a background process
	// keeps its slot after its parent agent becomes idle or changes machines.
	leases         map[string]map[string]string
	gpuLeases      map[string]string
	workspaceLocks sync.Map
}

type machineCandidate struct {
	id         string
	pool       *domain.Pool
	configured *domain.Machine
}

func NewManager(connector *Connector) *Manager {
	return &Manager{
		Connector: connector,
		leases:    make(map[string]map[string]string),
		gpuLeases: make(map[string]string),
	}
}

func (manager *Manager) Release(ownerID string) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	for _, poolLeases := range manager.leases {
		delete(poolLeases, ownerID)
	}
	delete(manager.gpuLeases, ownerID)
}

func (manager *Manager) reserve(pool domain.Pool, ownerID, machineID string) bool {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	return manager.reserveLocked(pool, ownerID, machineID)
}

func (manager *Manager) reserveLocked(pool domain.Pool, ownerID, machineID string) bool {
	poolLeases := manager.leases[pool.ID]
	if poolLeases == nil {
		poolLeases = make(map[string]string)
		manager.leases[pool.ID] = poolLeases
	}
	activeMachines := make(map[string]bool)
	for otherOwner, leasedMachine := range poolLeases {
		if otherOwner != ownerID {
			activeMachines[leasedMachine] = true
		}
	}
	needsAnotherSlot := !activeMachines[machineID]
	if needsAnotherSlot && len(activeMachines) >= pool.MaxMachines {
		return false
	}
	poolLeases[ownerID] = machineID
	return true
}

func (manager *Manager) Hold(ownerID, jobID string) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if machineID := manager.gpuLeases[ownerID]; machineID != "" {
		manager.gpuLeases["job:"+jobID] = machineID
	}
	for _, poolLeases := range manager.leases {
		if machineID, exists := poolLeases[ownerID]; exists {
			poolLeases["job:"+jobID] = machineID
		}
	}
}

func (manager *Manager) Connect(
	ctx context.Context,
	project domain.Project,
	ownerID string,
	preferredMachine string,
	excludedMachines map[string]bool,
) (*Connection, domain.Machine, error) {
	candidates, err := manager.projectCandidates(project)
	if err != nil {
		return nil, domain.Machine{}, err
	}
	preferCandidate(candidates, preferredMachine)
	manager.Release(ownerID)
	failures := []string{}
	for _, candidate := range candidates {
		if excludedMachines[candidate.id] {
			continue
		}
		if candidate.pool != nil && !manager.reserve(*candidate.pool, ownerID, candidate.id) {
			continue
		}
		connection, machine, err := manager.connectCandidate(ctx, candidate)
		if err == nil {
			return connection, machine, nil
		}
		manager.Release(ownerID)
		var identityError *HostKeyError
		var poolIdentityError *PoolHostKeyError
		if errors.As(err, &identityError) || errors.As(err, &poolIdentityError) {
			// An unknown identity needs an explicit trust decision, not a silent retry
			// against another host that could hide a changed or intercepted connection.
			return nil, domain.Machine{}, err
		}
		failures = append(failures, candidate.id+": "+err.Error())
	}
	return nil, domain.Machine{}, fmt.Errorf("no available machine: %s", strings.Join(failures, "; "))
}

func (manager *Manager) projectCandidates(project domain.Project) ([]machineCandidate, error) {
	candidates := make([]machineCandidate, 0, len(project.MachineIDs))
	for _, machineID := range project.MachineIDs {
		candidates = append(candidates, machineCandidate{id: machineID})
	}
	for _, poolID := range project.PoolIDs {
		var pool domain.Pool
		if err := manager.Connector.Store.Load("pool", poolID, &pool); err != nil {
			return nil, err
		}
		if err := validateStableWorkspace(project, pool); err != nil {
			return nil, err
		}
		for _, machine := range pool.Machines {
			member := machine
			candidates = append(candidates, machineCandidate{
				id:         pool.ID + "/" + machine.ID,
				pool:       &pool,
				configured: &member,
			})
		}
	}
	return candidates, nil
}

func validateStableWorkspace(project domain.Project, pool domain.Pool) error {
	if project.ManagedWorkspace || project.Directory == "" {
		return nil
	}
	if pool.SharedHome || pool.WorkspaceRoot == "" {
		return nil
	}
	stableDirectory := path.Clean(pool.WorkspaceRoot)
	if project.Directory == stableDirectory {
		return nil
	}
	if !strings.HasPrefix(project.Directory, stableDirectory+"/") {
		return errors.New("project must be inside pool workspace_root")
	}
	return nil
}

func preferCandidate(candidates []machineCandidate, preferredID string) {
	for index, candidate := range candidates {
		if candidate.id == preferredID {
			candidates[0], candidates[index] = candidates[index], candidates[0]
			return
		}
	}
}

func (manager *Manager) connectCandidate(ctx context.Context, candidate machineCandidate) (*Connection, domain.Machine, error) {
	machine, err := manager.Connector.Machine(candidate.id)
	if err != nil {
		return nil, machine, err
	}
	connection, err := manager.Connector.Dial(ctx, candidate.id)
	if err != nil {
		return nil, machine, err
	}
	probeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := CheckAvailability(probeContext, connection, machine); err != nil {
		connection.Close()
		return nil, machine, err
	}
	return connection, machine, nil
}

func CheckAvailability(ctx context.Context, connection *Connection, machine domain.Machine) error {
	if !machine.SingleUser {
		return nil
	}
	output, err := Run(ctx, connection.Client, "who | awk '{print $1}' | sort -u")
	if err != nil {
		return err
	}
	for _, username := range strings.Fields(output) {
		if username != machine.User {
			return fmt.Errorf("machine in use by %s", username)
		}
	}
	return nil
}
