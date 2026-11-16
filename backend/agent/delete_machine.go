package agent

import (
	"errors"
	"strings"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

// Called under configMu. Remove dependent connection configurations, detach
// projects, and preserve conversations, history, files and private keys.
func (engine *Engine) deleteMachineCascade(kind, id string) error {
	var target map[string]any
	if err := engine.Store.Load(kind, id, &target); err != nil {
		return err
	}
	machines, err := loadResources[domain.Machine](engine, "machine")
	if err != nil {
		return err
	}
	pools, err := loadResources[domain.Pool](engine, "pool")
	if err != nil {
		return err
	}
	removedMachines, removedPools := map[string]bool{}, map[string]bool{}
	if kind == "machine" {
		removedMachines[id] = true
	} else {
		removedPools[id] = true
	}
	removed := func(reference string) bool {
		if removedMachines[reference] {
			return true
		}
		for pool := range removedPools {
			if reference == pool || strings.HasPrefix(reference, pool+"/") {
				return true
			}
		}
		return false
	}
	// Jump dependencies may include pool members. Compute their transitive
	// closure without changing routes or attempting a direct-host fallback.
	for changed := true; changed; {
		changed = false
		for _, machine := range machines {
			if removedMachines[machine.ID] {
				continue
			}
			for _, jump := range machine.Jumps {
				if removed(jump) {
					removedMachines[machine.ID] = true
					changed = true
					break
				}
			}
		}
		for _, pool := range pools {
			if removedPools[pool.ID] {
				continue
			}
			remaining := 0
			for _, machine := range pool.Machines {
				member := pool.ID + "/" + machine.ID
				if !removedMachines[member] {
					for _, jump := range machine.Jumps {
						if removed(jump) {
							removedMachines[member] = true
							changed = true
							break
						}
					}
				}
				if !removedMachines[member] {
					remaining++
				}
			}
			if remaining == 0 {
				removedPools[pool.ID] = true
				changed = true
			}
		}
	}
	projects, err := loadResources[domain.Project](engine, "project")
	if err != nil {
		return err
	}
	affectedProjects := map[string]bool{}
	writes := []datastore.ResourceWrite{}
	for _, project := range projects {
		keptMachines, keptPools := []string{}, []string{}
		for _, reference := range project.MachineIDs {
			if removed(reference) {
				affectedProjects[project.ID] = true
			} else {
				keptMachines = append(keptMachines, reference)
			}
		}
		for _, reference := range project.PoolIDs {
			if removedPools[reference] {
				affectedProjects[project.ID] = true
			} else {
				keptPools = append(keptPools, reference)
			}
		}
		if affectedProjects[project.ID] {
			if err := engine.projectAvailable(project.ID); err != nil {
				return err
			}
			project.MachineIDs, project.PoolIDs = keptMachines, keptPools
			writes = append(writes, datastore.ResourceWrite{Kind: "project", ID: project.ID, Value: project})
		}
	}
	for _, project := range engine.projectOperations {
		for _, reference := range append(append([]string{}, project.MachineIDs...), project.PoolIDs...) {
			if removed(reference) {
				return errors.New("a repository operation using this machine is still in progress")
			}
		}
	}
	agents, err := loadResources[domain.Agent](engine, "agent")
	if err != nil {
		return err
	}
	affectedAgents := map[string]bool{}
	for _, record := range agents {
		if removed(record.MachineID) || (record.MachineID == "" && affectedProjects[record.ProjectID]) {
			affectedAgents[record.ID] = true
			if removed(record.MachineID) {
				record.MachineID = ""
				writes = append(writes, datastore.ResourceWrite{Kind: "agent", ID: record.ID, Parent: record.ProjectID, Value: record})
			}
		}
	}
	if engine.agentsAreBusy(affectedAgents) {
		return errors.New("stop the affected agents and jobs before removing their machine")
	}
	engine.mu.Lock()
	starting := false
	for project := range affectedProjects {
		starting = starting || engine.editorPreparing[project] > 0
	}
	engine.mu.Unlock()
	if starting {
		return errors.New("an affected editor is still starting; retry after it finishes")
	}
	removals := []datastore.ResourceDelete{}
	for _, machine := range machines {
		if removedMachines[machine.ID] {
			removals = append(removals, datastore.ResourceDelete{Kind: "machine", ID: machine.ID})
		}
	}
	for _, pool := range pools {
		if removedPools[pool.ID] {
			removals = append(removals, datastore.ResourceDelete{Kind: "pool", ID: pool.ID})
			continue
		}
		kept := make([]domain.Machine, 0, len(pool.Machines))
		for _, member := range pool.Machines {
			if !removedMachines[pool.ID+"/"+member.ID] {
				kept = append(kept, member)
			}
		}
		if len(kept) != len(pool.Machines) {
			pool.Machines = kept
			writes = append(writes, datastore.ResourceWrite{Kind: "pool", ID: pool.ID, Value: pool})
		}
	}
	for _, collection := range []string{"microvm", "sandboxStatus"} {
		rows, err := engine.listAll(collection, nil)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if removed(stringArg(row, "machineId")) {
				resourceID := stringArg(row, "id")
				if collection == "microvm" {
					resourceID = stringArg(row, "agentId") + ":" + stringArg(row, "machineId")
				} else {
					resourceID = stringArg(row, "machineId")
				}
				removals = append(removals, datastore.ResourceDelete{Kind: collection, ID: resourceID})
			}
		}
	}
	if err := engine.Store.ChangeResources(writes, removals); err != nil {
		return err
	}
	engine.scheduleRemovalCleanup(affectedAgents, "")
	engine.workers.Add(1)
	go func() { defer engine.workers.Done(); _ = engine.Editors.StopMachines(removedMachines, removedPools) }()
	engine.Emit("resources", "", nil)
	return nil
}
