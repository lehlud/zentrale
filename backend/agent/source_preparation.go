package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

// Repository selection authorizes its initial checkout. Preparation belongs to
// the harness, so inference and the editor never start with an empty repository.
func (engine *Engine) prepareProjectSources(
	ctx context.Context,
	connection *remote.Connection,
	machine domain.Machine,
	project domain.Project,
	scope string,
) error {
	return engine.prepareProjectSourcesForConnection(ctx, connection, machine, project, scope, false)
}

func (engine *Engine) prepareProjectSourcesForConnection(
	ctx context.Context,
	connection *remote.Connection,
	machine domain.Machine,
	project domain.Project,
	scope string,
	reuse bool,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	rows, err := engine.listAll("source", &project.ID)
	if err != nil {
		return err
	}

	var sources []domain.Source
	if err := decode(JSON(rows), &sources); err != nil {
		return err
	}

	rootFound := false
	for _, source := range sources {
		rootFound = rootFound || source.ProjectRoot
	}

	if project.RepositoryID != "" && !rootFound {
		return errors.New("project repository connection is missing; reconnect the repository in project settings")
	}

	if len(sources) == 0 {
		return nil
	}

	// The root must be cloned before additional sources create subdirectories.
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].ProjectRoot != sources[j].ProjectRoot {
			return sources[i].ProjectRoot
		}

		return sources[i].Name < sources[j].Name
	})

	// A successful check is valid for this live SSH connection and exact source
	// configuration. Model rounds reuse it; explicit checks, editors, reconnects
	// and changed sources still inspect the working tree before proceeding.
	key := JSON([]any{project.ID, project.Directory, sources})
	entry := engine.connectionEntry(scope)
	if entry != nil {
		entry.mutex.Lock()
		current := entry.connection != nil && entry.connection.Client == connection.Client
		prepared := current && entry.sourcesKey == key
		if current && (!reuse || !prepared) {
			entry.sourcesKey = ""
		}
		entry.mutex.Unlock()
		if reuse && prepared {
			return nil
		}
	}

	checkouts := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		directory, err := sourceDirectory(project, source)
		if err != nil {
			return err
		}

		checkouts = append(checkouts, map[string]any{
			"id":        source.ID,
			"url":       source.URL,
			"directory": directory,
		})
	}

	// Separate 9p guests cannot coordinate using directory flock locks. Hold
	// this workspace's semaphore across inspection and cloning in the harness.
	// Waiting behind another agent's clone must not use up an inspection's budget.
	value, _ := engine.sourcePreparations.LoadOrStore(project.Directory, make(chan struct{}, 1))
	lock := value.(chan struct{})
	waitContext, cancelWait := context.WithTimeout(ctx, 15*time.Minute)
	select {
	case lock <- struct{}{}:
		cancelWait()
		defer func() { <-lock }()
	case <-waitContext.Done():
		defer cancelWait()
		return fmt.Errorf("wait for another agent to prepare project repositories: %w", waitContext.Err())
	}

	temporary := agentTemporaryDirectory(scope)
	workspaceContext, cancelWorkspace := context.WithTimeout(ctx, time.Minute)
	err = prepareRemoteWorkspace(workspaceContext, connection, project.Directory, temporary)
	cancelWorkspace()
	if err != nil {
		return fmt.Errorf("prepare repository workspace: %w", err)
	}

	run := func(configuration map[string]any, network bool, timeout time.Duration) (string, error) {
		// Staging can prepare a guest, so give it a separate cold-boot budget.
		// Start the command timeout only once its runtime is ready.
		stageContext, cancelStage := context.WithTimeout(ctx, 10*time.Minute)
		command, _, err := remote.Sandbox(stageContext, connection, machine, remote.Execution{
			AgentID:            scope,
			ProjectID:          project.ID,
			Workspace:          project.Directory,
			TemporaryDirectory: temporary,
			Command:            "python3 -I -c " + remote.Quote(gitScript),
			Network:            network,
		})
		cancelStage()
		if err != nil {
			return "", fmt.Errorf("prepare repository sandbox: %w", err)
		}

		runContext, cancelRun := context.WithTimeout(ctx, timeout)
		defer cancelRun()
		return remote.RunPipe(runContext, connection.Client, command, strings.NewReader(JSON(configuration)))
	}

	// Existing work needs no network request, private key or fetch.
	output, err := run(map[string]any{"operation": "inspect", "sources": checkouts}, false, time.Minute)
	if err != nil {
		return fmt.Errorf("inspect project repositories: %w", err)
	}

	var missing []string
	if err := json.Unmarshal([]byte(output), &missing); err != nil {
		return fmt.Errorf("read repository preparation result: %w", err)
	}

	for _, source := range sources {
		if !slices.Contains(missing, source.ID) {
			continue
		}

		directory, _ := sourceDirectory(project, source)
		configurationContext, cancelConfiguration := context.WithTimeout(ctx, time.Minute)
		configuration, err := engine.sourceGitConfiguration(configurationContext, source, directory, temporary)
		cancelConfiguration()
		if err == nil {
			configuration["operation"] = "prepare"
			_, err = run(configuration, true, 15*time.Minute)
		}

		if err != nil {
			return fmt.Errorf("prepare repository %q: %w", source.Name, err)
		}
	}

	if entry != nil && ctx.Err() == nil {
		entry.mutex.Lock()
		if entry.connection != nil && entry.connection.Client == connection.Client {
			entry.sourcesKey = key
		}
		entry.mutex.Unlock()
	}

	return nil
}
