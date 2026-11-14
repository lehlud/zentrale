package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type commandLifetimeKey struct{}

func withCommandLifetime(ctx context.Context, request interaction) (context.Context, error) {
	if !requiresCommandTimeout(request.Action) {
		return ctx, nil
	}
	lifetime, err := commandTimeout(request.Arguments)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, commandLifetimeKey{}, lifetime), nil
}

// This is a command lifetime, not the time to wait before returning a job ID.
// Omission is rejected for new agent calls; internal/legacy execution requests
// retain the finite runtime fallback in startJob.
func commandTimeout(arguments map[string]any) (time.Duration, error) {
	if _, present := arguments["timeout_ms"]; !present {
		return 0, errors.New("timeout_ms is required; choose a finite lifetime appropriate for this command")
	}
	value, err := outputInteger(arguments, "timeout_ms", 0, 1, 604800000)
	if err != nil {
		return 0, err
	}
	return time.Duration(value) * time.Millisecond, nil
}

func stringsArg(arguments map[string]any, key string) ([]string, error) {
	values, valid := arguments[key].([]any)
	if !valid {
		return nil, errors.New("expected string array")
	}

	result := make([]string, 0, len(values))

	for _, item := range values {
		value, valid := item.(string)
		if !valid || !noSecrets(value) {
			return nil, errors.New("expected string array")
		}

		result = append(result, value)
	}

	return result, nil
}

func (engine *Engine) runPython(
	ctx context.Context,
	agentRecord domain.Agent,
	arguments map[string]any,
	network bool,
) (any, error) {
	environment := stringArg(arguments, "venv")
	executable := "python3"

	if environment != "" {
		project, err := engine.project(agentRecord)
		if err != nil {
			return nil, err
		}

		temporary, err := engine.temporaryDirectory(agentRecord.ID)
		if err != nil {
			return nil, err
		}
		managedPrefix := temporary + "/venv-"
		if !strings.HasPrefix(environment, managedPrefix) {
			environment, err = remote.Workspace(project.Directory, environment)
			if err != nil {
				return nil, err
			}
		}

		executable = remote.Quote(environment + "/bin/python")
	}

	command := executable + " -c " + remote.Quote(stringArg(arguments, "code"))

	return engine.execute(ctx, agentRecord, command, network, false)
}

func (engine *Engine) runContainer(
	ctx context.Context,
	agentRecord domain.Agent,
	request interaction,
) (any, error) {
	runtime := stringArg(request.Arguments, "runtime")
	if runtime != "docker" && runtime != "podman" {
		return nil, errors.New("runtime must be docker or podman")
	}

	arguments, err := stringsArg(request.Arguments, "arguments")
	if err != nil || len(arguments) == 0 {
		return nil, errors.New("container arguments required")
	}

	removesContainer := false

	for _, argument := range arguments {
		removesContainer = removesContainer || argument == "--rm" || argument == "--rm=true"
	}

	if arguments[0] == "run" && !removesContainer && request.Justification == "" {
		return nil, errors.New("omitting --rm requires justification")
	}

	commandParts := []string{runtime}

	for _, argument := range arguments {
		commandParts = append(commandParts, remote.Quote(argument))
	}

	return engine.executeRemote(ctx, agentRecord, executionRequest{
		Command:           strings.Join(commandParts, " "),
		Network:           request.Network,
		OutsideSandbox:    true,
		RequireContainers: true,
	})
}

func (engine *Engine) sourceGitTool(
	ctx context.Context,
	agentRecord domain.Agent,
	request interaction,
) (any, error) {
	arguments, err := stringsArg(request.Arguments, "arguments")
	if err != nil || len(arguments) == 0 {
		return nil, errors.New("Git arguments required")
	}

	switch arguments[0] {
	case "fetch", "pull", "push":
		if !request.Network {
			return nil, errors.New("request network=true for this Git interaction")
		}
	}

	sourceID := stringArg(request.Arguments, "sourceId")

	return engine.runSourceGit(ctx, agentRecord, sourceID, arguments, request.Network)
}
