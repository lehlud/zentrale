package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) projectMachines(agentRecord domain.Agent) ([]domain.Machine, error) {
	project, err := engine.project(agentRecord)
	if err != nil {
		return nil, err
	}

	result := []domain.Machine{}
	seen := map[string]bool{}
	appendMachine := func(machine domain.Machine) {
		if !seen[machine.ID] {
			seen[machine.ID] = true
			result = append(result, machine)
		}
	}
	for _, machineID := range project.MachineIDs {
		machine, err := engine.Remote.Connector.Machine(machineID)
		if err != nil {
			return nil, err
		}

		appendMachine(machine)
	}

	for _, poolID := range project.PoolIDs {
		var pool domain.Pool
		if err := engine.Store.Load("pool", poolID, &pool); err != nil {
			return nil, err
		}

		for _, member := range pool.Machines {
			machine, err := engine.Remote.Connector.Machine(pool.ID + "/" + member.ID)
			if err != nil {
				return nil, err
			}
			appendMachine(machine)
		}
	}

	return result, nil
}

type machinePage struct {
	Machines []domain.Machine `json:"machines"`
	Total    int              `json:"total"`
	Offset   int              `json:"offset"`
	HasMore  bool             `json:"hasMore"`
}

func machinePageArgument(arguments map[string]any, name string, fallback int) (int, error) {
	value, exists := arguments[name]
	if !exists {
		return fallback, nil
	}
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 1_000_000_000 || math.Trunc(number) != number {
		return 0, fmt.Errorf("%s must be a non-negative integer (at most 1000000000)", name)
	}
	return int(number), nil
}

func filterMachinePage(machines []domain.Machine, arguments map[string]any) (machinePage, error) {
	limit, err := machinePageArgument(arguments, "limit", 20)
	if err != nil {
		return machinePage{}, err
	}
	if limit < 1 || limit > 100 {
		return machinePage{}, errors.New("limit must be between 1 and 100")
	}
	offset, err := machinePageArgument(arguments, "offset", 0)
	if err != nil {
		return machinePage{}, err
	}
	query := ""
	if value, exists := arguments["search"]; exists {
		var ok bool
		query, ok = value.(string)
		if !ok {
			return machinePage{}, errors.New("search must be a string")
		}
	}
	terms := strings.Fields(strings.ToLower(query))
	matches := []domain.Machine{}
	for _, machine := range machines {
		text := strings.ToLower(strings.Join(append([]string{machine.ID, machine.Name, machine.Host}, machine.Properties...), " "))
		matched := true
		for _, term := range terms {
			if !strings.Contains(text, term) {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, machine)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	start := min(offset, len(matches))
	end := min(start+limit, len(matches))
	return machinePage{Machines: matches[start:end], Total: len(matches), Offset: offset, HasMore: end < len(matches)}, nil
}

func (engine *Engine) listProjectMachines(agentRecord domain.Agent, arguments map[string]any) (machinePage, error) {
	// Reject malformed pagination before accessing the machine inventory.
	if _, err := filterMachinePage(nil, arguments); err != nil {
		return machinePage{}, err
	}
	machines, err := engine.projectMachines(agentRecord)
	if err != nil {
		return machinePage{}, err
	}
	return filterMachinePage(machines, arguments)
}

type machinePropertyCount struct {
	Property string `json:"property"`
	Machines int    `json:"machines"`
}

func countMachineProperties(machines []domain.Machine) []machinePropertyCount {
	counts := map[string]int{}
	for _, machine := range machines {
		seen := map[string]bool{}
		for _, property := range machine.Properties {
			if property != "" && !seen[property] {
				counts[property]++
				seen[property] = true
			}
		}
	}
	result := []machinePropertyCount{}
	for property, count := range counts {
		result = append(result, machinePropertyCount{Property: property, Machines: count})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Property < result[j].Property })
	return result
}

func (engine *Engine) listMachineProperties(agentRecord domain.Agent) ([]machinePropertyCount, error) {
	machines, err := engine.projectMachines(agentRecord)
	if err != nil {
		return nil, err
	}
	return countMachineProperties(machines), nil
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
