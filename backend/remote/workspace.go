package remote

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lehlud/zentrale/domain"
)

//go:embed scripts/workspace.py
var workspaceScript string

type workspaceBinding struct {
	ID          string            `json:"id"`
	ProjectID   string            `json:"projectId"`
	Root        string            `json:"root"`
	Token       string            `json:"token"`
	Initialized bool              `json:"initialized"`
	Directories map[string]string `json:"directories"`
}

func workspaceBindingID(projectID, machineID string) string {
	scope := "machine:" + machineID

	if poolID, _, pooled := strings.Cut(machineID, "/"); pooled {
		scope = "pool:" + poolID
	}

	return fmt.Sprintf("%x", sha256.Sum256([]byte(projectID+"\x00"+scope)))
}

// KnownProjectDirectory reads an already prepared location without contacting
// SSH. Configuration, approvals and runtime context all use the same binding.
func (manager *Manager) KnownProjectDirectory(project domain.Project, machineID string) (string, error) {
	if !project.ManagedWorkspace || machineID == "" {
		return project.Directory, nil
	}

	var binding workspaceBinding
	err := manager.Connector.Store.Load("project_workspace", workspaceBindingID(project.ID, machineID), &binding)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	return binding.Directories[machineID], nil
}

// ResolveProject pins the configured root on first use. Renaming a project or
// editing a machine's default affects no existing workspace. Pool members share
// one receipt; failover must find it instead of silently creating an empty copy.
func (manager *Manager) ResolveProject(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	project domain.Project,
) (domain.Project, error) {
	if !project.ManagedWorkspace {
		_, err := Workspace(project.Directory, "")
		return project, err
	}

	if !ValidID(project.ID) {
		return project, errors.New("invalid project id")
	}

	id := workspaceBindingID(project.ID, machine.ID)
	directory, err := manager.resolveManagedDirectory(
		ctx,
		connection,
		machine,
		project,
		WithMachineDefaults(machine).WorkspaceRoot,
		id,
		"project_workspace",
	)

	project.Directory = directory

	return project, err
}

func (manager *Manager) resolveManagedDirectory(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	project domain.Project,
	root, id, kind string,
) (string, error) {
	if !ValidID(project.ID) {
		return "", errors.New("invalid project id")
	}

	lock, _ := manager.workspaceLocks.LoadOrStore(id, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	if err := ctx.Err(); err != nil {
		return "", err
	}

	var binding workspaceBinding
	err := manager.Connector.Store.Load(kind, id, &binding)
	if errors.Is(err, sql.ErrNoRows) {
		token, err := manager.Connector.Store.ProjectWorkspaceToken(project.ID)
		if err != nil {
			return "", err
		}

		binding = workspaceBinding{
			ID:        id,
			ProjectID: project.ID,
			Root:      root,

			Token:       token,
			Directories: map[string]string{},
		}
		// Retain the receipt even when SSH is interrupted after creating it.
		if err := manager.Connector.Store.Save(kind, id, project.ID, binding); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}

	if configuredRoot := root; !binding.Initialized && binding.Root != configuredRoot {
		// A failed first preparation must not pin a typo forever. No agent or
		// editor has used this workspace until initialization has succeeded.
		binding.Root = configuredRoot

		if err := manager.Connector.Store.Save(kind, id, project.ID, binding); err != nil {
			return "", err
		}
	}

	if err := ValidateWorkspaceRoot(binding.Root); err != nil {
		return "", err
	}

	configuration, err := json.Marshal(map[string]any{
		"projectId": project.ID,
		"root":      binding.Root,
		"token":     binding.Token,

		"initialized": binding.Initialized,
		"directory":   binding.Directories[machine.ID],
	})
	if err != nil {
		return "", err
	}

	prepare, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	output, err := RunPipe(
		prepare,
		connection.Client,
		"python3 -I -c "+Quote(workspaceScript),
		strings.NewReader(string(configuration)),
	)
	if err != nil {
		return "", fmt.Errorf("prepare persistent project workspace: %w", err)
	}

	var receipt struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal([]byte(output), &receipt); err != nil {
		return "", errors.New("remote workspace did not return a valid receipt")
	}

	if _, err := Workspace(receipt.Directory, ""); err != nil {
		return "", err
	}

	if binding.Directories == nil {
		binding.Directories = map[string]string{}
	}

	if !binding.Initialized || binding.Directories[machine.ID] != receipt.Directory {
		binding.Initialized = true
		binding.Directories[machine.ID] = receipt.Directory

		if err := manager.Connector.Store.Save(kind, id, project.ID, binding); err != nil {
			return "", err
		}
	}

	return receipt.Directory, nil
}
