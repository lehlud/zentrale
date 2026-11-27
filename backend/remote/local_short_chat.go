package remote

import (
	"context"
	"errors"
	"fmt"
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

// CheckLocalShortChat runs only the prerequisites probe. It never provisions
// a workspace, downloads an image, or starts a guest.
func CheckLocalShortChat(ctx context.Context) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return errors.New("the managed MicroVM runner requires Linux x86-64")
	}

	info, err := os.Stat("/dev/kvm")
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("/dev/kvm is not present; pass the device through to the container")
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return errors.New("/dev/kvm is not a character device")
	}

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	// Unlike "select", "ensure" reports the reason when a prerequisite fails.
	command := exec.CommandContext(ctx, "python3", "-I", "-c", microVMBootstrap(), "ensure")
	var output, diagnostics limitedOutput
	command.Stdout, command.Stderr = &output, &diagnostics
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("local MicroVM check: %w", ctx.Err())
		}
		if detail := strings.TrimSpace(diagnostics.String()); detail != "" {
			return errors.New(strings.SplitN(detail, "\n", 2)[0])
		}
		return fmt.Errorf("could not run the local MicroVM check: %w", err)
	}

	if strings.TrimSpace(output.String()) != SandboxMicroVM {
		return errors.New("unexpected result from the local MicroVM check")
	}
	return nil
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

// PrewarmLocalShortChatBase prepares the shared Fedora tool image without a
// user workspace or any chat data. A running user VM does not depend on it.
func PrewarmLocalShortChatBase(ctx context.Context, installationID string) error {
	connection, machine, err := LocalShortChatConnection(installationID)
	if err != nil {
		return err
	}
	defer connection.Close()
	_, err = runLocalMicroVM(ctx, connection, machine, "warm-local-base", nil, "")
	return err
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
