package harnesseval

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

// The engine has stopped and joined all work before verification. Clean this
// private evaluation scope directly; a closed engine cannot admit lifecycle
// operations or supervise the asynchronous cleanup used by the application.
func cleanupCase(
	ctx context.Context,
	store *datastore.Instance,
	connector *remote.Connector,
	machine domain.Machine,
	project domain.Project,
	record domain.Agent,
	sandbox string,
) (cleanupError error) {
	directory := path.Dir(project.Directory)
	if path.Dir(directory) != "/tmp" || !strings.HasPrefix(directory, "/tmp/zentrale-eval-") || !remote.ValidID(path.Base(directory)) {
		return errors.New("refuse cleanup outside the private evaluation directory")
	}

	var records []map[string]any
	if record.ID != "" {
		var err error
		records, err = store.List("microvm", &project.ID, 500, 0)
		if err != nil {
			return err
		}
		for _, vm := range records {
			if vm["agentId"] != remote.EditorScopeID(project.ID, "") || vm["projectId"] != project.ID || vm["machineId"] != machine.ID || vm["workspace"] != project.Directory {
				return errors.New("refuse cleanup of a VM outside this evaluation scope")
			}
		}
	}

	defer func() { cleanupError = errors.Join(cleanupError, store.RemoveProject(project.ID)) }()
	connection, err := connector.Dial(ctx, machine.ID)
	if err != nil {
		return err
	}
	defer connection.Close()

	if record.ID != "" && machine.MicroVMCommand == "" && (len(records) > 0 || sandbox == remote.SandboxMicroVM) {
		_, err = remote.ManageMicroVM(ctx, connection, machine, "remove", remote.Execution{
			AgentID: remote.EditorScopeID(project.ID, ""), ProjectID: project.ID, Workspace: project.Directory,
			TemporaryDirectory: connection.ProjectTemporaryDirectory(project.ID),
		})
		// Keep backing files intact if the guest could not stop. The logical
		// deletion below leaves a durable cleanup record for a failed removal.
		if err != nil {
			return err
		}
		if err := store.Remove("microvm", remote.EditorScopeID(project.ID, "")+":"+machine.ID); err != nil {
			return err
		}
	}

	command := "rm -rf -- " + remote.Quote(directory)
	if record.ID != "" {
		command += " " + remote.Quote("/tmp/zentrale-"+record.ID)
		command += " " + remote.Quote(connection.ProjectTemporaryDirectory(project.ID))
	}
	_, err = remote.Run(ctx, connection, command)
	return err
}
