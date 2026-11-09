package remote

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

//go:embed scripts/storage_quota.py
var quotaScript string

func ReadStorageQuota(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
) (map[string]any, error) {
	if err := validateStorageConfiguration(machine.Storage, machine.QuotaCommand); err != nil {
		return nil, err
	}

	if len(machine.QuotaCommand) == 0 {
		return nil, errors.New("no quota_command is configured for this machine or pool")
	}

	payload, _ := json.Marshal(map[string]any{"command": machine.QuotaCommand})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	output, err := RunPipe(ctx, connection.Client, "python3 -I -c "+Quote(quotaScript), strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("read host storage quota: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, errors.New("invalid host quota result")
	}

	result["machineId"], result["command"], result["checkedAt"] = machine.ID, machine.QuotaCommand, time.Now().UTC()

	return result, nil
}

func validateStorageConfiguration(volumes []domain.StorageVolume, quotaCommand []string) error {
	if len(volumes) > 8 || len(quotaCommand) > 16 {
		return errors.New("storage permits at most 8 volumes and quota_command at most 16 arguments")
	}

	seen := map[string]bool{}
	for _, volume := range volumes {
		if len(volume.Description) > 2000 || strings.ContainsRune(volume.Description, '\x00') {
			return errors.New("storage description must be at most 2000 bytes without NUL")
		}

		if !ValidID(volume.ID) || seen[volume.ID] {
			return errors.New("storage requires unique valid volume ids")
		}

		seen[volume.ID] = true

		if err := ValidateWorkspaceRoot(volume.Path); err != nil {
			return fmt.Errorf("storage %s path: %w", volume.ID, err)
		}

		clean := path.Clean(volume.Path)

		for _, root := range []string{"/proc", "/sys", "/dev", "/run", "/etc", "/usr", "/boot"} {
			if clean == root || strings.HasPrefix(clean, root+"/") {
				return fmt.Errorf("storage %s cannot use system filesystems", volume.ID)
			}
		}
	}

	for index, argument := range quotaCommand {
		if argument == "" || len(argument) > 4096 || strings.ContainsAny(argument, "\x00\r\n") {
			return errors.New("invalid quota_command argument")
		}

		if index == 0 && !path.IsAbs(argument) {
			return errors.New("quota_command must start with an absolute executable path")
		}
	}

	return nil
}

// PrepareStorage shares the workspace receipt rules: pin successful roots and
// refuse an empty replacement when another pool member loses shared storage.
func (manager *Manager) PrepareStorage(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	project domain.Project,
) ([]domain.StorageMount, error) {
	mounts := []domain.StorageMount{}

	if err := validateStorageConfiguration(machine.Storage, machine.QuotaCommand); err != nil {
		return nil, err
	}

	for _, volume := range machine.Storage {
		scope := "machine:" + machine.ID

		if poolID, _, pooled := strings.Cut(machine.ID, "/"); pooled && volume.Shared {
			scope = "pool:" + poolID
		}

		id := fmt.Sprintf("%x", sha256.Sum256([]byte(project.ID+"\x00"+scope+"\x00storage:"+volume.ID)))
		directory, err := manager.resolveManagedDirectory(ctx, connection, machine, project, volume.Path, id, "project_storage")
		if err != nil {
			return nil, fmt.Errorf("prepare storage %s: %w", volume.ID, err)
		}

		mounts = append(
			mounts,
			domain.StorageMount{
				ID:        volume.ID,
				HostPath:  directory,
				GuestPath: "/mnt/zentrale-storage/" + volume.ID,
			},
		)
	}

	return mounts, nil
}
