package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path"
	"strconv"
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

// RestoreUsage accounts for existing remote work before any new placement.
// Existing work must be counted even if it already fills the pool's limit.
func (manager *Manager) RestoreUsage(project domain.Project, ownerID, machineID string, gpu bool) error {
	candidates, err := manager.projectCandidates(project)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if candidate.id != machineID {
			continue
		}
		manager.mutex.Lock()
		defer manager.mutex.Unlock()
		if candidate.pool != nil {
			if manager.leases[candidate.pool.ID] == nil {
				manager.leases[candidate.pool.ID] = make(map[string]string)
			}
			manager.leases[candidate.pool.ID][ownerID] = machineID
		}
		if gpu {
			manager.gpuLeases[ownerID] = machineID
		}
		return nil
	}
	return errors.New("original machine is no longer assigned to the project")
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
	failures := []error{}
	// Give the preferred connection its usual chance first. Then screen small
	// batches of direct pool routes so blackholed TCP ports do not serially
	// consume the entire connection budget before a reachable host is tried.
	const probeBatchSize = 8
	probeEnd := 1
	var unreachable map[string]error
	for index, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}

		if excludedMachines[candidate.id] {
			continue
		}
		if candidate.pool != nil && index >= probeEnd {
			probeEnd = min(index+probeBatchSize, len(candidates))
			unreachable = probeDirectPoolRoutes(ctx, candidates[index:probeEnd], excludedMachines)
		}
		if err := unreachable[candidate.id]; err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", candidate.id, err))
			continue
		}

		if candidate.pool != nil && !manager.reserve(*candidate.pool, ownerID, candidate.id) {
			continue
		}

		attempt := ctx
		cancel := func() {}
		if candidate.pool != nil {
			// The SSH handshake, daemon attach and availability check belong
			// to this candidate's budget, even when its TCP port responds.
			attempt, cancel = context.WithTimeout(ctx, 35*time.Second)
		}
		connection, machine, err := manager.connectCandidate(attempt, candidate)
		cancel()
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

		failures = append(failures, fmt.Errorf("%s: %w", candidate.id, err))
	}

	if len(failures) == 0 {
		return nil, domain.Machine{}, errors.New("no available machine: all assigned machines are excluded or reserved")
	}
	return nil, domain.Machine{}, fmt.Errorf("no available machine: %w", errors.Join(failures...))
}

// Only screen TCP reachability. SSH authentication, host keys, leases and
// availability are still checked in preference order on the normal path.
func probeDirectPoolRoutes(ctx context.Context, candidates []machineCandidate, excluded map[string]bool) map[string]error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	dialer := net.Dialer{}
	var workers sync.WaitGroup
	var mutex sync.Mutex
	failed := make(map[string]error)
	for _, candidate := range candidates {
		if excluded[candidate.id] || candidate.configured == nil || len(candidate.configured.Jumps) != 0 {
			continue
		}
		workers.Go(func() {
			machine := WithMachineDefaults(*candidate.configured)
			address := net.JoinHostPort(machine.Host, strconv.Itoa(machine.Port))
			connection, err := dialer.DialContext(ctx, "tcp", address)
			if err == nil {
				_ = connection.Close()
				return
			}
			mutex.Lock()
			failed[candidate.id] = err
			mutex.Unlock()
		})
	}
	workers.Wait()
	return failed
}

func (manager *Manager) projectCandidates(project domain.Project) ([]machineCandidate, error) {
	if len(project.MachineIDs)+len(project.PoolIDs) != 1 {
		return nil, errors.New("select exactly one remote machine or exactly one pool for the project")
	}
	candidates := make([]machineCandidate, 0, len(project.MachineIDs))

	for _, machineID := range project.MachineIDs {
		if err := manager.Connector.RequireWorkTarget(machineID); err != nil {
			return nil, err
		}
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
			if machine.JumpHostOnly {
				continue
			}
			member := machine
			candidates = append(candidates, machineCandidate{
				id:         pool.ID + "/" + machine.ID,
				pool:       &pool,
				configured: &member,
			})
		}
	}

	if project.RequiresTenantIsolation() {
		return manager.isolatedProjectCandidates(project, candidates)
	}

	return candidates, nil
}

func (manager *Manager) isolatedProjectCandidates(project domain.Project, candidates []machineCandidate) ([]machineCandidate, error) {
	if !project.ManagedWorkspace {
		return nil, errors.New("remote execution for a shared or personal project needs a managed workspace")
	}

	eligible := candidates[:0]
	for _, candidate := range candidates {
		machine, err := manager.Connector.Machine(candidate.id)
		if err != nil {
			return nil, err
		}

		// The legacy bubblewrap mode can read the host filesystem.
		if machine.MicroVMCommand == "" &&
			(machine.Sandbox == SandboxAutomatic || machine.Sandbox == SandboxMicroVM) {
			eligible = append(eligible, candidate)
		}
	}

	if len(eligible) == 0 {
		return nil, errors.New("no managed MicroVM target is configured for this project")
	}

	return eligible, nil
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

	output, err := Run(ctx, connection, "who | awk '{print $1}' | sort -u")
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
