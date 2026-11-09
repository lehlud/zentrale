// Package remote manages SSH connections and execution on remote machines.
package remote

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

const (
	SandboxAutomatic  = "auto"
	SandboxMicroVM    = "microvm"
	SandboxBubblewrap = "bwrap"
	SandboxDisabled   = "none"
	VMTypeMicroVM     = "microvm"
	VMTypeQ35         = "q35"

	defaultSSHPort       = 22
	maximumTCPPort       = 65535
	DefaultWorkspaceRoot = "~/.local/share/zentrale/projects"
)

var resourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)

func ValidID(identifier string) bool {
	return resourceIDPattern.MatchString(identifier)
}

// WithMachineDefaults returns a normalized copy. Validation itself does not
// mutate configuration, so callers can safely validate an existing resource.
func WithMachineDefaults(machine domain.Machine) domain.Machine {
	if machine.WorkspaceRoot == "" {
		machine.WorkspaceRoot = DefaultWorkspaceRoot
	}
	if machine.Port == 0 {
		machine.Port = defaultSSHPort
	}
	if machine.Sandbox == "" {
		machine.Sandbox = SandboxAutomatic
	}
	if machine.VMType == "" {
		machine.VMType = VMTypeMicroVM
		if len(machine.GPUDevices) > 0 {
			machine.VMType = VMTypeQ35
		}
	}
	machine.GPUDevices = append([]string{}, machine.GPUDevices...)
	machine.Storage = append([]domain.StorageVolume{}, machine.Storage...)
	machine.QuotaCommand = append([]string{}, machine.QuotaCommand...)
	for index, device := range machine.GPUDevices {
		machine.GPUDevices[index] = strings.ToLower(strings.TrimSpace(device))
	}
	return machine
}

func ValidateMachine(machine domain.Machine) error {
	return validateMachine(machine, true)
}

func validateMachine(machine domain.Machine, requireKey bool) error {
	if machine.WorkspaceRoot != "" {
		if err := ValidateWorkspaceRoot(machine.WorkspaceRoot); err != nil {
			return err
		}
	}
	if err := validateMachineIdentity(machine, requireKey); err != nil {
		return err
	}
	if err := validateMachinePorts(machine); err != nil {
		return err
	}
	return validateSandboxConfiguration(machine)
}

// Home-relative roots are resolved by the SSH user's Python runtime, never by
// the Zentrale host. Shell substitutions and other relative paths are rejected.
func ValidateWorkspaceRoot(root string) error {
	if strings.ContainsAny(root, "\x00\r\n") {
		return fmt.Errorf("workspace root contains invalid characters")
	}
	if strings.HasPrefix(root, "~/") {
		cleaned := path.Clean(strings.TrimPrefix(root, "~/"))
		if cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../") {
			return nil
		}
	}
	if path.IsAbs(root) && path.Clean(root) != "/" {
		return nil
	}
	return fmt.Errorf("workspace root must be an absolute directory other than / or a directory below ~/")
}

func validateMachineIdentity(machine domain.Machine, requireKey bool) error {
	if !ValidID(machine.ID) {
		return fmt.Errorf("machine id must contain 1–80 letters, digits, dots, underscores or hyphens")
	}
	requiredFields := []struct {
		name  string
		value string
	}{
		{name: "name", value: machine.Name},
		{name: "host", value: machine.Host},
		{name: "user", value: machine.User},
	}
	for _, field := range requiredFields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("machine %s is required", field.name)
		}
	}
	if requireKey && machine.KeyID <= 0 {
		return fmt.Errorf("machine SSH key is required")
	}
	for _, addressPart := range []string{machine.Host, machine.User} {
		if strings.ContainsAny(addressPart, "\x00\r\n") {
			return fmt.Errorf("SSH host and user must not contain NUL bytes or line breaks")
		}
	}
	return nil
}

func validTCPPort(port int) bool {
	return port >= 1 && port <= maximumTCPPort
}

func validateMachinePorts(machine domain.Machine) error {
	if !validTCPPort(machine.Port) {
		return fmt.Errorf("SSH port must be between 1 and %d", maximumTCPPort)
	}
	return nil
}

func validateSandboxConfiguration(machine domain.Machine) error {
	if err := validateStorageConfiguration(machine.Storage, machine.QuotaCommand); err != nil {
		return err
	}
	if len(machine.Storage) > 0 && (machine.MicroVMCommand != "" || (machine.Sandbox != SandboxAutomatic && machine.Sandbox != SandboxMicroVM)) {
		return fmt.Errorf("storage requires the built-in managed VM with sandbox auto or microvm")
	}
	if machine.MicroVMCommand != "" && !path.IsAbs(machine.MicroVMCommand) {
		return fmt.Errorf("microvm_command must be an absolute path when supplied")
	}
	if err := validateVMConfiguration(machine); err != nil {
		return err
	}
	switch machine.Sandbox {
	case SandboxAutomatic, SandboxBubblewrap, SandboxDisabled:
		return nil
	case SandboxMicroVM:
		return nil
	default:
		return fmt.Errorf("sandbox must be auto, microvm, bwrap or none")
	}
}

var pciAddressPattern = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[01][0-9a-f]\.[0-7]$`)

func validateVMConfiguration(machine domain.Machine) error {
	if machine.VMType != "" && machine.VMType != VMTypeMicroVM && machine.VMType != VMTypeQ35 {
		return fmt.Errorf("vm_type must be microvm or q35")
	}
	if len(machine.GPUDevices) > 16 {
		return fmt.Errorf("gpu_devices accepts at most 16 PCI functions")
	}
	seen := map[string]bool{}
	for _, device := range machine.GPUDevices {
		if !pciAddressPattern.MatchString(device) || seen[device] {
			return fmt.Errorf("gpu_devices must contain unique PCI addresses such as 0000:01:00.0")
		}
		seen[device] = true
	}
	if len(machine.GPUDevices) > 0 && machine.VMType != "" && machine.VMType != VMTypeQ35 {
		return fmt.Errorf("gpu_devices requires vm_type q35")
	}
	if machine.VMType == VMTypeQ35 || len(machine.GPUDevices) > 0 {
		if machine.Sandbox != SandboxAutomatic && machine.Sandbox != SandboxMicroVM {
			return fmt.Errorf("q35 and gpu_devices require sandbox auto or microvm")
		}
		if machine.MicroVMCommand != "" {
			return fmt.Errorf("q35 and gpu_devices require the built-in VM runner")
		}
	}
	return nil
}
