package agent

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

func validateTeamTasks(tasks []domain.TeamTask, pattern string) error {
	if len(tasks) < 1 || len(tasks) > 32 {
		return errors.New("teamwork requires 1..32 tasks")
	}

	known := map[string]bool{}

	for index := range tasks {
		task := &tasks[index]
		if task.ID == "" || len(task.ID) > 64 || known[task.ID] {
			return errors.New("task IDs must be unique and contain 1..64 bytes")
		}

		known[task.ID] = true

		validTitle := strings.TrimSpace(task.Title) != "" && len(task.Title) <= 160
		validObjective := strings.TrimSpace(task.Objective) != "" && len(task.Objective) <= 4000
		validAcceptance := strings.TrimSpace(task.Acceptance) != "" && len(task.Acceptance) <= 4000
		if !validTitle || !validObjective || !validAcceptance {
			return errors.New("each task needs title (1..160), objective and acceptance (1..4000 bytes each)")
		}

		if task.Kind == "" {
			task.Kind = "work"
		}

		if task.Kind != "work" && task.Kind != "verify" {
			return errors.New("task kind must be work or verify")
		}

		if len(task.WritePaths) > 16 || len(task.DependsOn) > 32 {
			return errors.New("task has too many write paths or dependencies")
		}

		for _, scope := range task.WritePaths {
			if err := validateTeamWritePath(scope); err != nil {
				return err
			}
		}

		task.OwnerID, task.Status, task.Result, task.Evidence = "", "pending", "", ""
	}

	if err := validateTeamDependencies(tasks); err != nil {
		return err
	}

	if pattern == "solve_verify" {
		for _, task := range tasks {
			if task.Kind == "work" && !hasTeamVerifier(tasks, task.ID) {
				return errors.New("solve_verify requires a dependent verification task for every work task")
			}
		}
	}

	return nil
}

func validateTeamWritePath(scope string) error {
	// Inspect original components first: a/../b must not become an accepted b.
	for _, component := range strings.Split(scope, "/") {
		if component == ".." {
			return errors.New("write paths cannot contain traversal components")
		}
	}

	normalized := scope != "" && path.Clean(scope) == scope && !path.IsAbs(scope)
	if !normalized || len(scope) > 240 || strings.ContainsAny(scope, "\\\x00*?[]:") {
		return errors.New("write paths must be literal normalized project-relative paths; use . for the whole project")
	}

	return nil
}

func validateTeamDependencies(tasks []domain.TeamTask) error {
	byID := map[string]domain.TeamTask{}

	for _, task := range tasks {
		byID[task.ID] = task
	}

	visited, active := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		task, exists := byID[id]
		if !exists || active[id] {
			return errors.New("dependencies must exist and form an acyclic graph")
		}

		if visited[id] {
			return nil
		}

		active[id] = true

		for _, dependency := range task.DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}

		delete(active, id)
		visited[id] = true

		return nil
	}

	for _, task := range tasks {
		if task.Kind == "verify" && len(task.DependsOn) == 0 {
			return errors.New("verification tasks must depend on the work they check")
		}

		if err := visit(task.ID); err != nil {
			return err
		}
	}

	return nil
}

func hasTeamVerifier(tasks []domain.TeamTask, id string) bool {
	for _, candidate := range tasks {
		if candidate.Kind != "verify" {
			continue
		}

		for _, dependency := range candidate.DependsOn {
			if dependency == id {
				return true
			}
		}
	}

	return false
}

func claimTeamTask(board *domain.Teamwork, task *domain.TeamTask, ownerID string) error {
	if task.Status != "pending" && (task.Status != "blocked" || task.OwnerID != ownerID) {
		return errors.New("task is already owned or finished; release blocked work before reassignment")
	}

	workers := map[string]bool{}

	for _, other := range board.Tasks {
		for _, dependency := range task.DependsOn {
			if other.ID != dependency {
				continue
			}

			if other.Status != "done" {
				return errors.New("dependencies are not finished")
			}

			if task.Kind == "verify" && other.OwnerID == ownerID {
				return errors.New("verification must be owned by a different agent than its dependency")
			}
		}

		if other.Status != "claimed" {
			continue
		}

		if other.OwnerID != board.AgentID {
			workers[other.OwnerID] = true
		}

		for _, left := range task.WritePaths {
			for _, right := range other.WritePaths {
				if teamPathsOverlap(left, right) {
					return fmt.Errorf("declared write paths overlap task %s; serialize or use separate outputs", other.ID)
				}
			}
		}
	}

	if ownerID != board.AgentID {
		workers[ownerID] = true
	}

	if len(workers) > board.MaxWorkers {
		return errors.New("teamwork worker budget is full")
	}

	return nil
}

func teamPathsOverlap(left, right string) bool {
	return left == "." || right == "." || left == right ||
		strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
