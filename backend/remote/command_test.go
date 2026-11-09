package remote

import (
	"os/exec"
	"testing"
)

func TestSSHCommandPreservesPayloadAcrossLoginShells(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python 3 is required for remote command tests")
	}
	compiler := "import sys; compile(sys.argv[1], '<ssh-editor>', 'exec'); sys.stdout.buffer.write(sys.argv[1].encode())"
	tests := []struct {
		name, command, expected string
	}{
		{
			name:     "embedded editor supervisor compiles unchanged",
			command:  Quote(python) + " -c " + Quote(compiler) + " " + Quote(editorSupervisor),
			expected: editorSupervisor,
		},
		{
			name:     "embedded MicroVM runner compiles unchanged",
			command:  Quote(python) + " -c " + Quote(compiler) + " " + Quote(managedMicroVMRunner),
			expected: managedMicroVMRunner,
		},
		{
			name:     "literal shell metacharacters and backslashes",
			command:  "printf '%s' " + Quote("single' double\" \\ \\\\ \\' \\n \\t $HOME $(exit 17) `exit 18`\nGrüße\t"),
			expected: "single' double\" \\ \\\\ \\' \\n \\t $HOME $(exit 17) `exit 18`\nGrüße\t",
		},
	}
	for _, shell := range []string{"sh", "bash", "fish"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is not installed", shell)
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					arguments := []string{"-c", shellCommand(test.command)}
					if shell == "fish" {
						arguments = append([]string{"--no-config"}, arguments...)
					}
					output, err := exec.Command(binary, arguments...).CombinedOutput()
					if err != nil {
						t.Fatalf("SSH login shell changed the command: %v\n%s", err, output)
					}
					if string(output) != test.expected {
						t.Fatal("SSH login shell changed the payload bytes")
					}
				})
			}
		})
	}
}
