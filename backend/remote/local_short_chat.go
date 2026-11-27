package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

// LocalShortChatMachineID never refers to an SSH machine or a project target.
const LocalShortChatMachineID = "@zentrale"

func LocalShortChatAvailable() bool {
	info, err := os.Stat("/dev/kvm")
	if err != nil || info.Mode()&os.ModeCharDevice == 0 || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	// The existing runner checks KVM permissions, API version and QEMU.
	// Selecting never provisions, downloads or boots a guest.
	output, err := exec.CommandContext(ctx, "python3", "-I", "-c", microVMBootstrap(), "select").Output()
	return err == nil && strings.TrimSpace(string(output)) == SandboxMicroVM
}

func LocalShortChatConnection(installationID string) (*Connection, domain.Machine, error) {
	if !shortChatInstallationPattern.MatchString(installationID) {
		return nil, domain.Machine{}, errors.New("invalid local short-chat installation identity")
	}
	root := os.Getenv("ZENTRALE_SHORT_CHAT_VM_DIR")
	if root == "" {
		root = filepath.Join(os.TempDir(), "zentrale-local-short-chats")
	}
	if !filepath.IsAbs(root) {
		return nil, domain.Machine{}, errors.New("the local short-chat VM directory must be absolute")
	}

	machine := domain.Machine{ID: LocalShortChatMachineID, Name: "Zentrale", Sandbox: SandboxMicroVM}
	connection := &Connection{machineID: machine.ID, installationID: installationID, localShortChatRoot: filepath.Join(root, installationID)}
	return connection, machine, nil
}

func localVMEnvironment(connection *Connection) []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + connection.localShortChatRoot,
		"ZENTRALE_LOCAL_VM_ROOT=" + connection.localShortChatRoot,
	}
}

// No local shell transport: the host only runs the embedded trusted VM runner.
// In particular, model commands are serialized in the stage payload for the guest.
func runLocalMicroVM(ctx context.Context, connection *Connection, machine domain.Machine, action string, input io.Reader, invocation string) (string, error) {
	if (action == "run") != (invocation != "") {
		return "", errors.New("a VM run requires a staged invocation")
	}
	args := []string{"-I", "-c", microVMBootstrap(machine), action}
	if invocation != "" {
		args = append(args, invocation)
	}

	command := exec.CommandContext(ctx, "python3", args...)
	command.Env = localVMEnvironment(connection)
	output := &limitedOutput{}
	command.Stdin, command.Stdout, command.Stderr = input, output, output
	command.WaitDelay = 2 * time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return output.String(), ctx.Err()
		}
		return output.String(), commandFailure(err, output.String())
	}
	return output.String(), nil
}
