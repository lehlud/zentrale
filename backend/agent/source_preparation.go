package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

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

	// Separate 9p guests cannot coordinate using directory flock locks. Hold
	// this workspace's semaphore across inspection and cloning in the harness.
	value, _ := engine.sourcePreparations.LoadOrStore(project.Directory, make(chan struct{}, 1))
	lock := value.(chan struct{})
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// The root must be cloned before additional sources create subdirectories.
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].ProjectRoot != sources[j].ProjectRoot {
			return sources[i].ProjectRoot
		}

		return sources[i].Name < sources[j].Name
	})

	temporary := agentTemporaryDirectory(scope)
	if err := prepareRemoteWorkspace(ctx, connection, project.Directory, temporary); err != nil {
		return err
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

	run := func(configuration map[string]any, network bool) (string, error) {
		command, _, err := remote.Sandbox(ctx, connection, machine, remote.Execution{
			AgentID:            scope,
			ProjectID:          project.ID,
			Workspace:          project.Directory,
			TemporaryDirectory: temporary,
			Command:            "python3 -I -c " + remote.Quote(gitScript),
			Network:            network,
		})
		if err != nil {
			return "", err
		}

		return remote.RunPipe(ctx, connection.Client, command, strings.NewReader(JSON(configuration)))
	}

	// Existing work needs no network request, private key or fetch. Check it on
	// each preparation, including a reconnect, failover or service restart.
	output, err := run(map[string]any{"operation": "inspect", "sources": checkouts}, false)
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
		configuration, err := engine.sourceGitConfiguration(ctx, source, directory, temporary)
		if err == nil {
			configuration["operation"] = "prepare"
			_, err = run(configuration, true)
		}

		if err != nil {
			return fmt.Errorf("prepare repository %q: %w", source.Name, err)
		}
	}

	return nil
}
