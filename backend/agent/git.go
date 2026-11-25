package agent

import (
	"context"
	"encoding/pem"
	"errors"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) cloneSource(
	ctx context.Context,
	agentRecord domain.Agent,
	sourceID string,
) (any, error) {
	return engine.runSourceGit(ctx, agentRecord, sourceID, nil, true)
}

func (engine *Engine) runSourceGit(
	ctx context.Context,
	agentRecord domain.Agent,
	sourceID string,
	arguments []string,
	network bool,
) (any, error) {
	var source domain.Source
	if err := engine.Store.Load("source", sourceID, &source); err != nil {
		return nil, err
	}

	if source.ProjectID != agentRecord.ProjectID {
		return nil, errors.New("source is outside project")
	}

	if len(arguments) > 0 && source.ReadOnly && arguments[0] == "push" {
		return nil, errors.New("read-only sources cannot be pushed")
	}

	project, err := engine.project(agentRecord)
	if err != nil {
		return nil, err
	}

	directory, err := sourceDirectory(project, source)
	if err != nil {
		return nil, err
	}

	configuration, err := engine.sourceGitConfiguration(ctx, source, directory)
	if err != nil {
		return nil, err
	}

	configuration["operation"] = "clone"
	if source.ProjectRoot && len(project.RepositoryCheckoutPaths) > 0 {
		configuration["checkoutPaths"] = project.RepositoryCheckoutPaths
	}
	displayCommand := "git clone " + source.URL

	if len(arguments) > 0 {
		configuration["operation"] = "command"
		displayCommand = "git -C " + directory + " " + strings.Join(arguments, " ")
	}

	configuration["arguments"] = arguments

	// Credentials travel on SSH stdin. Only the descriptive command enters the
	// persistent job log; the remote helper removes key files even on Git errors.
	return engine.executeRemote(ctx, agentRecord, executionRequest{
		Command:        "python3 -I -c " + remote.Quote(gitScript),
		DisplayCommand: displayCommand,
		Input:          strings.NewReader(JSON(configuration)),
		Network:        network,
	})
}

func sourceDirectory(project domain.Project, source domain.Source) (string, error) {
	if source.ProjectRoot {
		return remote.WorkspaceRelative(project.Directory, "")
	}

	return remote.WorkspaceRelative(project.Directory, source.Name)
}

func (engine *Engine) sourceGitConfiguration(
	ctx context.Context,
	source domain.Source,
	directory string,
) (map[string]any, error) {
	var httpURLs []string
	if source.BindingID != "" {
		var project domain.Project
		if err := engine.Store.Load("project", source.ProjectID, &project); err != nil {
			return nil, err
		}
		service := engine.GitFor(project.Owner())
		if err := service.RefreshRepositoryAuthorization(ctx, source.BindingID, source.URL); err != nil {
			return nil, err
		}
		var err error
		httpURLs, err = service.RepositoryHTTPURLs(source.BindingID, source.URL)
		if err != nil {
			return nil, err
		}
	}

	privateKey, err := engine.Store.PrivateKey(source.KeyID)
	if err != nil {
		return nil, err
	}

	knownHosts, hostKey, err := engine.gitKnownHosts(ctx, source)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"key": string(pem.EncodeToMemory(&pem.Block{
			Type:  "EC PRIVATE KEY",
			Bytes: privateKey,
		})),
		"known":             knownHosts,
		"repositoryHostKey": hostKey,
		"url":               source.URL,
		"httpUrls":          httpURLs,
		"directory":         directory,
		"readOnly":          source.ReadOnly,
	}, nil
}
