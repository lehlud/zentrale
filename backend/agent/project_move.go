package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

const (
	projectMoveKind     = "project_move"
	movePreflightFailed = "preflight_failed"
)

func pendingProjectMove(move projectMoveRecord) bool {
	return move.Phase == "copying" || move.Phase == "cleanup"
}

type projectMoveDirectory struct {
	Source      remote.MoveDirectory
	Destination string
	Shared      bool
	Retired     bool
}

type projectMoveRecord struct {
	domain.ProjectMove
	From, To    domain.Project
	Host        string
	Workspace   string
	Directories []projectMoveDirectory
	VMs         map[string]microVMRecord
	Scratch     []string
}

func (engine *Engine) ProjectMove(id string) (*domain.ProjectMove, error) {
	var move projectMoveRecord
	if err := engine.Store.Load(projectMoveKind, id, &move); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &move.ProjectMove, nil
}

func (engine *Engine) StartProjectMove(id, machineID, poolID string) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	if err := engine.ctx.Err(); err != nil {
		return err
	}
	var move projectMoveRecord
	err := engine.Store.Load(projectMoveKind, id, &move)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if pendingProjectMove(move) {
		if move.MachineID != machineID || move.PoolID != poolID {
			return errors.New("retry the pending move to its original destination")
		}
		if move.Error == "" {
			return nil
		}
		move.Error = ""
		if err := engine.saveProjectMove(move); err != nil {
			return err
		}
		engine.runProjectMove(move)
		return nil
	}

	if err := engine.projectAvailable(id); err != nil {
		return err
	}
	var from domain.Project
	if err := engine.Store.Load("project", id, &from); err != nil {
		return err
	}
	if !from.ManagedWorkspace || len(from.MachineIDs)+len(from.PoolIDs) != 1 {
		return errors.New("moving requires a managed project workspace with a configured source machine or pool")
	}
	if (machineID == "") == (poolID == "") {
		return errors.New("select exactly one remote machine or exactly one pool")
	}
	if machineID != "" {
		if strings.Contains(machineID, "/") {
			return errors.New("select a standalone machine or an entire pool")
		}
		if err := engine.Remote.Connector.RequireWorkTarget(machineID); err != nil {
			return err
		}
	} else {
		var pool domain.Pool
		if err := engine.Store.Load("pool", poolID, &pool); err != nil {
			return err
		}
	}
	if (machineID != "" && slices.Contains(from.MachineIDs, machineID)) || (poolID != "" && slices.Contains(from.PoolIDs, poolID)) {
		return errors.New("choose a different machine or pool")
	}
	family, err := engine.agentFamily(domain.Agent{ID: remote.EditorScopeID(id, ""), ProjectID: id})
	if err != nil {
		return err
	}
	terminal, err := engine.Store.HasTerminalAttachment(id, "")
	if err != nil {
		return err
	}
	if terminal || engine.agentsAreBusy(family) || engine.Editors.HasActive(family, "") {
		return errors.New("finish project work and close its editors and terminals before moving")
	}
	to := from
	to.MachineIDs, to.PoolIDs = nil, nil
	if machineID != "" {
		to.MachineIDs = []string{machineID}
	} else {
		to.PoolIDs = []string{poolID}
	}
	move = projectMoveRecord{
		ProjectMove: domain.ProjectMove{ProjectID: id, MachineID: machineID, PoolID: poolID, Phase: "copying", ID: NewID()},
		From:        from, To: to,
	}
	if err := engine.saveProjectMove(move); err != nil {
		return err
	}
	engine.setProjectMoving(id, true)
	engine.runProjectMove(move)
	return nil
}

func (engine *Engine) saveProjectMove(move projectMoveRecord) error {
	err := engine.Store.Save(projectMoveKind, move.ProjectID, move.ProjectID, move)
	if err == nil {
		engine.Emit("resources", "", nil)
	}
	return err
}

func (engine *Engine) setProjectMoving(projectID string, moving bool) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if moving {
		engine.movingProjects[projectID] = true
	} else {
		delete(engine.movingProjects, projectID)
	}
}

func (engine *Engine) recoverProjectMoves() error {
	moves, err := loadResources[projectMoveRecord](engine, projectMoveKind)
	if err != nil {
		return err
	}
	for _, move := range moves {
		if pendingProjectMove(move) {
			engine.setProjectMoving(move.ProjectID, true)
			if move.Error == "" {
				engine.runProjectMove(move)
			}
		}
	}
	return nil
}

func (engine *Engine) runProjectMove(move projectMoveRecord) {
	engine.workers.Go(func() {
		err := engine.executeProjectMove(engine.ctx, &move)
		engine.configMu.Lock()
		defer engine.configMu.Unlock()
		if engine.ctx.Err() != nil {
			return
		}
		if err != nil {
			move.Error = err.Error()
			if move.Host == "" {
				move.Phase = movePreflightFailed
			}
		} else {
			move.Phase = "done"
		}
		if err := engine.saveProjectMove(move); err == nil && !pendingProjectMove(move) {
			engine.setProjectMoving(move.ProjectID, false)
		}
	})
}

func (engine *Engine) executeProjectMove(ctx context.Context, move *projectMoveRecord) error {
	token, err := engine.Store.ProjectWorkspaceToken(move.ProjectID)
	if err != nil {
		return err
	}
	owner := "move:" + move.ID
	if move.Host == "" {
		connection, machine, err := engine.Remote.Connect(ctx, move.To, owner, "", nil)
		if err != nil {
			return fmt.Errorf("connect to move destination: %w", err)
		}
		defer engine.Remote.Release(owner)
		defer connection.Close()
		if err := engine.planProjectMove(ctx, move, connection, machine); err != nil {
			return err
		}
	}
	target, err := engine.dialMoveHost(ctx, move.Host)
	if err != nil {
		return fmt.Errorf("connect to move destination: %w", err)
	}
	defer target.Close()
	if move.Phase == "copying" {
		for index := range move.Directories {
			directory := &move.Directories[index]
			if err := engine.copyMoveDirectory(ctx, move, directory, target, token, index); err != nil {
				return fmt.Errorf("copy project storage: %w", err)
			}
		}
		for _, key := range slices.Sorted(maps.Keys(move.VMs)) {
			vm := move.VMs[key]
			sum := sha256.Sum256([]byte(key))
			snapshot := fmt.Sprintf("%s-%x", move.ID, sum[:8])
			if err := engine.archiveMovedVM(ctx, vm, target, move.Workspace, token, snapshot); err != nil {
				return fmt.Errorf("save old VM on %s: %w", vm.MachineID, err)
			}
		}
		if err := engine.cutOverProjectMove(move); err != nil {
			return err
		}
	}
	for index, directory := range move.Directories {
		if directory.Retired {
			continue
		}
		id := fmt.Sprintf("%s-%d", move.ID, index)
		state, err := remote.MoveDirectoryState(ctx, target, "check", move.ProjectID, directory.Destination, token, id)
		if err != nil {
			return fmt.Errorf("verify destination before cleanup: %w", err)
		}
		if (directory.Shared && state != "shared") || (!directory.Shared && state != "published") {
			return errors.New("cannot verify the destination before cleanup")
		}
	}
	for _, id := range slices.Sorted(maps.Keys(move.VMs)) {
		vm := move.VMs[id]
		if err := engine.removeMovedVM(ctx, vm); err != nil {
			return fmt.Errorf("remove old VM on %s: %w", vm.MachineID, err)
		}
		// Detach the receipt without scheduling another deletion, which
		// could race a future move back to this host.
		delete(move.VMs, id)
		if err := engine.Store.Remove("microvm", id); err != nil {
			move.VMs[id] = vm
			return err
		}
		if err := engine.saveProjectMove(*move); err != nil {
			return err
		}
	}
	for _, host := range move.Scratch {
		connection, err := engine.dialMoveHost(ctx, host)
		if err != nil {
			return fmt.Errorf("connect to remove old scratch on %s: %w", host, err)
		}
		err = remote.RemoveProjectMoveScratch(ctx, connection, move.ProjectID)
		connection.Close()
		if err != nil {
			return fmt.Errorf("remove old scratch on %s: %w", host, err)
		}
		move.Scratch = move.Scratch[1:]
		if err := engine.saveProjectMove(*move); err != nil {
			return err
		}
	}
	for index := range move.Directories {
		directory := &move.Directories[index]
		if directory.Retired {
			continue
		}
		id := fmt.Sprintf("%s-%d", move.ID, index)
		if !directory.Shared {
			connection, err := engine.dialMoveHost(ctx, directory.Source.MachineID)
			if err != nil {
				return fmt.Errorf("connect to old workspace for cleanup: %w", err)
			}
			_, err = remote.MoveDirectoryState(ctx, connection, "remove", move.ProjectID, directory.Source.Path, token, id)
			connection.Close()
			if err != nil {
				return fmt.Errorf("remove old storage: %w", err)
			}
		}
		directory.Retired = true
		if err := engine.Store.ChangeResources(
			[]datastore.ResourceWrite{{Kind: projectMoveKind, ID: move.ProjectID, Parent: move.ProjectID, Value: *move}},
			[]datastore.ResourceDelete{{Kind: directory.Source.Kind, ID: directory.Source.BindingID}},
		); err != nil {
			directory.Retired = false
			return err
		}
	}
	for index, directory := range move.Directories {
		id := fmt.Sprintf("%s-%d", move.ID, index)
		if _, err := remote.MoveDirectoryState(ctx, target, "finish", move.ProjectID, directory.Destination, token, id); err != nil {
			return fmt.Errorf("finish destination storage: %w", err)
		}
	}
	return nil
}

func (engine *Engine) dialMoveHost(ctx context.Context, id string) (*remote.Connection, error) {
	attempt, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	return engine.Remote.Connector.Dial(attempt, id)
}

func (engine *Engine) planProjectMove(ctx context.Context, move *projectMoveRecord, target *remote.Connection, machine domain.Machine) error {
	sources, err := engine.Remote.MoveDirectories(move.From)
	if err != nil {
		return err
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Minute)
	_, err = remote.InspectSandboxWithError(probe, target, machine)
	cancel()
	if err != nil {
		return fmt.Errorf("check destination sandbox: %w", err)
	}
	prepared, err := engine.Remote.ResolveProject(ctx, target, machine, move.To)
	if err != nil {
		return err
	}
	mounts, err := engine.Remote.PrepareStorage(ctx, target, machine, prepared)
	if err != nil {
		return err
	}
	destinations := map[string]string{"": prepared.Directory}
	for _, mount := range mounts {
		destinations[mount.ID] = mount.HostPath
	}
	directories := []projectMoveDirectory{}
	seen := map[string]bool{}
	for _, volume := range slices.Sorted(maps.Keys(sources)) {
		source := sources[volume]
		path := destinations[volume]
		if path == "" {
			return fmt.Errorf("the destination requires a storage volume with ID %q", volume)
		}
		if volume != "" && move.PoolID != "" {
			index := slices.IndexFunc(machine.Storage, func(candidate domain.StorageVolume) bool { return candidate.ID == volume })
			if index < 0 || !machine.Storage[index].Shared {
				return fmt.Errorf("storage volume %q must be shared across the destination pool", volume)
			}
		}
		if seen[path] || (volume != "" && path == prepared.Directory) {
			return errors.New("destination storage directories overlap")
		}
		seen[path] = true
		directories = append(directories, projectMoveDirectory{Source: source, Destination: path})
	}
	vms, err := engine.Store.ProjectMicroVMs(move.ProjectID)
	if err != nil {
		return fmt.Errorf("read the project's VM receipts: %w", err)
	}
	records := make(map[string]microVMRecord, len(vms))
	for id, data := range vms {
		var vm microVMRecord
		if err := json.Unmarshal(data, &vm); err != nil {
			return fmt.Errorf("decode VM receipt %q: %w", id, err)
		}
		records[id] = vm
	}
	scratch := map[string]bool{}
	for _, directory := range directories {
		for host := range directory.Source.Paths {
			scratch[host] = true
		}
	}
	for _, vm := range records {
		scratch[vm.MachineID] = true
	}
	move.Scratch = slices.Sorted(maps.Keys(scratch))
	move.Directories, move.VMs, move.Host, move.Workspace = directories, records, machine.ID, prepared.Directory
	return engine.saveProjectMove(*move)
}

func (engine *Engine) copyMoveDirectory(ctx context.Context, move *projectMoveRecord, directory *projectMoveDirectory, target *remote.Connection, token string, index int) error {
	id := fmt.Sprintf("%s-%d", move.ID, index)
	var connection *remote.Connection
	var failures []error
	for _, machine := range slices.Sorted(maps.Keys(directory.Source.Paths)) {
		source, err := engine.dialMoveHost(ctx, machine)
		if err == nil {
			_, err = remote.MoveDirectoryState(ctx, source, "mark", move.ProjectID, directory.Source.Paths[machine], token, id)
			if err == nil {
				connection = source
				directory.Source.MachineID, directory.Source.Path = machine, directory.Source.Paths[machine]
				break
			}
			source.Close()
		}
		failures = append(failures, err)
	}
	if connection == nil {
		return fmt.Errorf("old project storage is unavailable: %w", errors.Join(failures...))
	}
	defer connection.Close()
	state, err := remote.MoveDirectoryState(ctx, target, "check", move.ProjectID, directory.Destination, token, id)
	if err != nil {
		return err
	}
	directory.Shared = state == "shared"
	switch state {
	case "shared", "published":
		return nil
	case "empty":
		return remote.TransferWorkspace(ctx, connection, target, move.ProjectID, directory.Source.Path, directory.Destination, token, id)
	default:
		return errors.New("invalid destination storage receipt")
	}
}

func (engine *Engine) cutOverProjectMove(move *projectMoveRecord) error {
	// Serialize the short DB cutover with agent settings changes. Releasing
	// cached SSH connections stays outside the configuration lock.
	engine.configMu.Lock()
	agents, err := engine.listAll("agent", &move.ProjectID)
	if err != nil {
		engine.configMu.Unlock()
		return err
	}
	writes := []datastore.ResourceWrite{{Kind: "project", ID: move.ProjectID, Value: move.To}}
	for _, row := range agents {
		if stringArg(row, "machineId") != "" {
			row["machineId"] = ""
			writes = append(writes, datastore.ResourceWrite{Kind: "agent", ID: stringArg(row, "id"), Parent: move.ProjectID, Value: row})
		}
	}
	updated := *move
	updated.Phase = "cleanup"
	writes = append(writes, datastore.ResourceWrite{Kind: projectMoveKind, ID: move.ProjectID, Parent: move.ProjectID, Value: updated})
	err = engine.Store.SaveResources(writes)
	engine.configMu.Unlock()
	if err != nil {
		return err
	}
	*move = updated
	for _, row := range agents {
		id := stringArg(row, "id")
		engine.invalidate(id)
		engine.closeMCPServers(id)
		engine.releaseConnection(id)
	}
	engine.Emit("resources", "", nil)
	return nil
}

func (engine *Engine) removeMovedVM(ctx context.Context, vm microVMRecord) error {
	machine, err := engine.Remote.Connector.Machine(vm.MachineID)
	if err != nil {
		return err
	}
	operation := engine.vmOperation(vm.AgentID, vm.MachineID)
	if err := operation.Lock(ctx); err != nil {
		return err
	}
	defer operation.Unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	connection, err := engine.Remote.Connector.Dial(ctx, vm.MachineID)
	if err != nil {
		return err
	}
	defer connection.Close()
	execution := remote.Execution{
		AgentID: vm.AgentID, ProjectID: vm.ProjectID, Workspace: vm.Workspace,
		TemporaryDirectory: engine.environmentTemporaryDirectory(vm.AgentID),
	}
	if _, err := remote.ManageMicroVM(ctx, connection, machine, "remove", execution); err != nil {
		return err
	}
	operation.stopped = true
	return remote.RetireMovedVM(ctx, connection, execution)
}

func (engine *Engine) archiveMovedVM(ctx context.Context, vm microVMRecord, target *remote.Connection, workspace, token, id string) error {
	machine, err := engine.Remote.Connector.Machine(vm.MachineID)
	if err != nil {
		return err
	}
	if machine.MicroVMCommand != "" {
		return errors.New("restore the built-in VM runner on the source machine before moving")
	}
	operation := engine.vmOperation(vm.AgentID, vm.MachineID)
	if err := operation.Lock(ctx); err != nil {
		return err
	}
	defer operation.Unlock()
	connection, err := engine.dialMoveHost(ctx, vm.MachineID)
	if err != nil {
		return err
	}
	defer connection.Close()
	execution := remote.Execution{
		AgentID: vm.AgentID, ProjectID: vm.ProjectID, Workspace: vm.Workspace,
		TemporaryDirectory: engine.environmentTemporaryDirectory(vm.AgentID),
	}
	stop, cancel := context.WithTimeout(ctx, time.Minute)
	_, err = remote.ManageMicroVM(stop, connection, machine, "stop", execution)
	cancel()
	if err != nil {
		return err
	}
	operation.stopped = true
	return remote.SnapshotMovedVM(ctx, connection, target, execution, workspace, token, id)
}
