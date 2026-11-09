package agent

import (
	"context"
	"encoding/pem"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"

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

	configuration, err := engine.sourceGitConfiguration(ctx, source, directory, agentTemporaryDirectory(agentRecord.ID))
	if err != nil {
		return nil, err
	}

	configuration["operation"] = "clone"
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
		return remote.Workspace(project.Directory, "")
	}

	return remote.Workspace(project.Directory, source.Name)
}

func (engine *Engine) sourceGitConfiguration(
	ctx context.Context,
	source domain.Source,
	directory, temporary string,
) (map[string]any, error) {
	privateKey, err := engine.Store.PrivateKey(source.KeyID)
	if err != nil {
		return nil, err
	}

	knownHosts, err := engine.gitKnownHosts(ctx, source.URL)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"key": string(pem.EncodeToMemory(&pem.Block{
			Type:  "EC PRIVATE KEY",
			Bytes: privateKey,
		})),
		"known":              knownHosts,
		"url":                source.URL,
		"directory":          directory,
		"readOnly":           source.ReadOnly,
		"temporaryDirectory": temporary,
	}, nil
}

func (engine *Engine) gitKnownHosts(ctx context.Context, repositoryURL string) (string, error) {
	// GitHub publishes its host keys over authenticated HTTPS. Machine host keys
	// must not shadow them, or an unrelated ECDSA key looks like a changed host.
	known, err := engine.GitForge.Client.SSHKnownHosts(ctx, repositoryURL)
	if err != nil || known != "" {
		return known, err
	}

	trustedKeys, err := engine.Store.TrustedKeys()
	if err != nil {
		return "", err
	}

	var knownHosts strings.Builder

	for _, encodedKey := range trustedKeys {
		publicKey, err := ssh.ParsePublicKey(encodedKey)
		if err != nil {
			return "", err
		}

		knownHosts.WriteString("* ")
		knownHosts.Write(ssh.MarshalAuthorizedKey(publicKey))
	}

	return knownHosts.String(), nil
}
