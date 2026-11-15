package agent

import (
	"os"
	"os/exec"
	"testing"
)

func TestFilePathArgumentsUseTheExecutionEnvironment(t *testing.T) {
	for _, test := range []struct{ requested, expected string }{
		{"file 'quotes' $literal", "./file 'quotes' $literal"},
		{"/host/project/backend/file.go", "./backend/file.go"},
		{"/home/zentrale/project/backend/file.go", "./backend/file.go"},
		{"/tmp/zentrale-owner/venv-id/bin/python", "/runtime/scratch/venv-id/bin/python"},
		{"/tmp/zentrale/attachments/id/file 'quoted'", "/runtime/scratch/attachments/id/file 'quoted'"},
		{"/tmp/zentrale", "/runtime/scratch"},
	} {
		argument, err := filePathArgument("/host/project", "/tmp/zentrale-owner", test.requested)
		if err != nil {
			t.Fatal(err)
		}

		command := exec.Command("/bin/sh", "-c", "printf '%s' "+argument)
		command.Env = append(os.Environ(), "TMPDIR=/runtime/scratch")
		output, err := command.CombinedOutput()
		if err != nil || string(output) != test.expected {
			t.Fatalf("%q = %q, want %q (%v)", test.requested, output, test.expected, err)
		}
	}

	for _, requested := range []string{
		"/etc/passwd", "/tmp/zentrale-other/file", "/tmp/zentrale-owner/../../secret",
		"/tmp/zentrale/../secret", "/home/zentrale/project/../secret", "file\x00",
	} {
		if _, err := filePathArgument("/host/project", "/tmp/zentrale-owner", requested); err == nil {
			t.Fatalf("path escaped project/scratch: %q", requested)
		}
	}

	if _, err := filePathArgument("/host/project", "", "/etc/passwd"); err == nil {
		t.Fatal("empty temporary root allowed arbitrary absolute paths")
	}
}
