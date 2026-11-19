package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

var cloneProgress = regexp.MustCompile(`^ZENTRALE_CLONE_PROGRESS (Connecting to repository|Waiting for checkout|Enumerating objects|Counting objects|Compressing objects|Receiving objects|Resolving deltas|Updating files|Checking out files|Checking out selected directories|Writing checkout data) (-1|[0-9]{1,3})(?:\t([0-9/]*)\t([0-9.A-Za-z /|]*))?$`)

var errRepositoryStalled = errors.New("Git stopped reporting progress while preparing the repository")

// Git uses both CR and LF for its status, but the helper emits bounded LF lines.
// Consume only new bytes so a long clone never reparses earlier output.
func repositoryProgressReader(active func(), update func(step string, percent int, detail string)) func([]byte) {
	var pending, previous string
	return func(chunk []byte) {
		remaining := pending + string(chunk)
		for {
			line, rest, complete := strings.Cut(remaining, "\n")
			if !complete {
				pending = line
				if len(pending) > 256 {
					pending = " " // This long, incomplete line cannot be a marker.
				}
				return
			}
			remaining = rest
			line = strings.TrimSuffix(line, "\r")
			if line == "ZENTRALE_CLONE_ACTIVE" {
				active()
				continue
			}
			if line == previous || len(line) > 256 {
				continue
			}
			if match := cloneProgress.FindStringSubmatch(line); match != nil {
				percent, _ := strconv.Atoi(match[2])
				if percent <= 100 {
					previous = line
					detail := match[4]
					if detail == "" {
						detail = match[3]
					}
					active()
					update(match[1], percent, detail)
				}
			}
		}
	}
}

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

	if (project.RepositoryID != "" || project.RepositorySSHURL != "") && !rootFound {
		return errors.New("project repository connection is missing; reconnect the repository in project settings")
	}

	// The root must be cloned before additional sources create subdirectories.
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].ProjectRoot != sources[j].ProjectRoot {
			return sources[i].ProjectRoot
		}

		return sources[i].Name < sources[j].Name
	})

	// A successful check is valid for this live SSH connection and exact source
	// configuration. Model rounds reuse it; explicit checks, reconnects and
	// changed sources inspect the working tree before proceeding. Editors may
	// bypass this function only after validating the complete readiness key and
	// live native VM generation, never from this source fingerprint alone.
	key, err := engine.sourceReadinessKey(project, sources)
	if err != nil {
		return err
	}
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
	var preparing domain.Agent
	reportProgress := engine.Store.Load("agent", scope, &preparing) == nil
	if reportProgress {
		engine.setRunState(scope, "repository", nil)
	}

	checkouts := make([]map[string]any, 0, len(sources))
	var replacedOrigins []string
	var attachedNames []string
	if rootFound {
		replacedOrigins, err = engine.Store.PreviousCheckoutOrigins(project.ID)
		if err != nil {
			return err
		}
		for _, source := range sources {
			if !source.ProjectRoot {
				attachedNames = append(attachedNames, source.Name)
			}
		}
	}
	for _, source := range sources {
		directory, err := sourceDirectory(project, source)
		if err != nil {
			return err
		}

		httpURLs, movedFrom, err := engine.GitForge.RepositoryCheckoutURLs(source.BindingID, source.URL)
		if err != nil {
			return err
		}

		checkout := map[string]any{
			"id":        source.ID,
			"url":       source.URL,
			"directory": directory,
			"httpUrls":  httpURLs,
			"movedFrom": movedFrom,
		}
		if source.ProjectRoot {
			checkout["replaceFrom"] = replacedOrigins
			checkout["attachedNames"] = attachedNames
		}
		checkouts = append(checkouts, checkout)
	}

	// Separate 9p guests cannot coordinate using directory flock locks. Hold
	// this workspace's semaphore across inspection and cloning in the harness.
	// A waiting agent inherits cancellation from its caller and does not time out
	// a separate agent's active checkout.
	value, _ := engine.sourcePreparations.LoadOrStore(project.Directory, make(chan struct{}, 1))
	lock := value.(chan struct{})
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return fmt.Errorf("wait for another agent to prepare project repositories: %w", ctx.Err())
	}

	temporary, err := engine.temporaryDirectory(scope)
	if err != nil {
		return err
	}
	workspaceContext, cancelWorkspace := context.WithTimeout(ctx, time.Minute)
	err = prepareRemoteWorkspace(workspaceContext, connection, project.Directory, temporary)
	cancelWorkspace()
	if err != nil {
		return fmt.Errorf("prepare repository workspace: %w", err)
	}

	run := func(configuration map[string]any, network bool, timeout time.Duration) (string, error) {
		// Staging can prepare a guest, so give it a separate cold-boot budget.
		// Start the command timeout only once its runtime is ready.
		stageContext, cancelStage := context.WithTimeout(ctx, remote.ManagedVMSetupTimeout)
		command, level, err := engine.sandbox(stageContext, connection, machine, remote.Execution{
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

		var output string
		if configuration["operation"] == "prepare" {
			if reportProgress {
				engine.setRunState(scope, "repository", nil)
			}
			configuration["reportProgress"] = true // Also used by the idle watchdog.
			configuration["managedVM"] = level == remote.SandboxMicroVM && machine.MicroVMCommand == ""
			runContext, cancelRun := context.WithCancelCause(ctx)
			idle := time.AfterFunc(timeout, func() { cancelRun(errRepositoryStalled) })
			defer func() {
				cancelRun(nil)
				idle.Stop()
			}()
			onChunk := repositoryProgressReader(func() {
				if runContext.Err() == nil {
					idle.Reset(timeout)
				}
			}, func(step string, percent int, detail string) {
				if reportProgress && runContext.Err() == nil {
					engine.publishRunState(domain.AgentRunState{
						AgentID: scope, Phase: "repository", RepositoryStep: step, RepositoryPercent: percent, RepositoryDetail: detail,
					})
				}
			})
			output, err = remote.RunStreamingInput(runContext, connection, command, strings.NewReader(JSON(configuration)), false, nil, onChunk)
			if errors.Is(context.Cause(runContext), errRepositoryStalled) {
				return output, errRepositoryStalled
			}
		} else {
			runContext, cancelRun := context.WithTimeout(ctx, timeout)
			defer cancelRun()
			output, err = remote.RunPipe(runContext, connection, command, strings.NewReader(JSON(configuration)))
		}
		if level == remote.SandboxMicroVM && machine.MicroVMCommand == "" {
			if interrupted := remote.ParseVMInvocationFailure(output, err); interrupted != nil {
				return output, interrupted
			}
		}
		return output, err
	}
	rememberPrepared := func() {
		if entry != nil && ctx.Err() == nil {
			entry.mutex.Lock()
			if entry.connection != nil && entry.connection.Client == connection.Client {
				entry.sourcesKey = key
			}
			entry.mutex.Unlock()
		}
	}

	// Remove previously issued credentials before consulting current source/key
	// state. A removed source or failed refresh must never retain an old key.
	if _, err := run(map[string]any{
		"operation": "clear-project-git", "project": ".",
		"storageRoot": project.Directory,
	}, false, time.Minute); err != nil {
		return fmt.Errorf("clear project Git credentials: %w", err)
	}
	if len(sources) == 0 {
		rememberPrepared()
		return nil
	}

	// Recognize existing checkouts and apply confirmed address changes without
	// contacting the Git server or touching any files in the worktree.
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
		configuration, err := engine.sourceGitConfiguration(configurationContext, source, directory)
		cancelConfiguration()
		if err == nil {
			configuration["operation"] = "prepare"
			if source.ProjectRoot {
				configuration["replaceFrom"] = replacedOrigins
				configuration["attachedNames"] = attachedNames
			}
			if source.ProjectRoot && len(project.RepositoryCheckoutPaths) > 0 {
				configuration["checkoutPaths"] = project.RepositoryCheckoutPaths
			}
			var output string
			output, err = run(configuration, true, 15*time.Minute)
			err = repositoryHostKeyFailure(output, err, configuration)
		}

		if err != nil {
			return fmt.Errorf("prepare repository %q: %w", source.Name, err)
		}
	}

	// Repository-local configuration is shared by VS Code's Git extension,
	// integrated terminals and agent commands, without touching ~/.gitconfig or
	// forwarding the host's SSH agent. Refresh on every preparation, not only clone.
	identity, err := engine.ProjectCommitIdentity(project)
	if err != nil {
		return err
	}
	configurations := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		directory, _ := sourceDirectory(project, source)
		configurationContext, cancelConfiguration := context.WithTimeout(ctx, time.Minute)
		configuration, err := engine.sourceGitConfiguration(configurationContext, source, directory)
		cancelConfiguration()
		if err != nil {
			return fmt.Errorf("configure repository %q: %w", source.Name, err)
		}
		configuration["identity"] = map[string]string{"name": identity.Name, "email": identity.Email}
		configurations = append(configurations, configuration)
	}
	if _, err := run(map[string]any{
		"operation": "configure-project-git", "project": ".",
		"sources": configurations,
	}, false, time.Minute); err != nil {
		return fmt.Errorf("configure project Git: %w", err)
	}

	rememberPrepared()

	return nil
}
