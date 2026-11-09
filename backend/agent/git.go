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

	directory, err := remote.Workspace(project.Directory, source.Name)

	if source.ProjectRoot {
		directory, err = remote.Workspace(project.Directory, "")
	}

	if err != nil {
		return nil, err
	}

	privateKey, err := engine.Store.PrivateKey(source.KeyID)
	if err != nil {
		return nil, err
	}

	knownHosts, err := engine.gitKnownHosts()
	if err != nil {
		return nil, err
	}

	operation := "clone"
	displayCommand := "git clone " + source.URL

	if len(arguments) > 0 {
		operation = "command"
		displayCommand = "git -C " + directory + " " + strings.Join(arguments, " ")
	}

	configuration := map[string]any{
		"key": string(pem.EncodeToMemory(&pem.Block{
			Type:  "EC PRIVATE KEY",
			Bytes: privateKey,
		})),
		"known":              knownHosts,
		"url":                source.URL,
		"directory":          directory,
		"readOnly":           source.ReadOnly,
		"temporaryDirectory": agentTemporaryDirectory(agentRecord.ID),
		"operation":          operation,
		"arguments":          arguments,
	}

	// Credentials travel on SSH stdin. Only the descriptive command enters the
	// persistent job log; the remote helper removes key files even on Git errors.
	return engine.executeRemote(ctx, agentRecord, executionRequest{
		Command:        "python3 -I -c " + remote.Quote(gitScript),
		DisplayCommand: displayCommand,
		Input:          strings.NewReader(JSON(configuration)),
		Network:        network,
	})
}

func (engine *Engine) gitKnownHosts() (string, error) {
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
