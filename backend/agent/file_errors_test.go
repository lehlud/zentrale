package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDecodeFileResultOnlyHandlesPrivateErrorProtocol(t *testing.T) {
	for _, input := range []string{
		`{"_toolError":{"code":"ENOENT","message":"path not found: missing.txt"}}`,
		`{"_toolError":null}`, `{"_toolError":{}}`,
		`{"_toolError":{"code":"ENOENT","message":"missing"},"written":true}`,
		`null`, "Traceback: unexpected internal bug",
	} {
		if result, err := decodeFileResult(input); err == nil || result != nil {
			t.Fatalf("failure became success: %q: %v, %v", input, result, err)
		}
	}
	result, err := decodeFileResult(`{"content":"Traceback: this is file content","written":true}`)
	if err != nil || result["content"] != "Traceback: this is file content" {
		t.Fatal("ordinary contents were filtered", result, err)
	}
}

func TestEmbeddedFileHelperErrorsBecomeConciseToolFailures(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python is required for embedded helper subprocess tests")
	}
	for _, test := range []struct{ action, path, input, message string }{
		{"read_file", "missing.txt", "", "path not found"},
		{"read_file", "fifo", "", "regular file"},
		{"write_file", "fifo", "bmV3", "regular file"},
		{"write_file", "directory", "bmV3", "directory"},
		{"apply_patch", "", "*** Begin Patch\n*** Update File: one.txt\n@@\n-one\n+changed\n*** Update File: two.txt\n@@\n-outdated\n+new\n*** End Patch\n", "two.txt: patch context did not match"},
	} {
		t.Run(test.action, func(t *testing.T) {
			root := t.TempDir()
			if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string]string{"one.txt": "one\n", "two.txt": "two\n"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(filepath.Join(root, "directory"), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, python, "-I", "-c", fileOperationScript(test.action), root, filepath.Join(root, "tmp"), test.path, test.action, "-")
			command.Stdin = strings.NewReader(test.input)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("helper transport failed instead of returning an operation error: %v: %s", err, output)
			}
			result, err := decodeFileResult(string(output))
			if result != nil || err == nil || !strings.Contains(err.Error(), test.message) || strings.Contains(err.Error(), "Traceback") || strings.Contains(err.Error(), "Process exited") {
				t.Fatalf("unexpected tool result: %v, %v", result, err)
			}
			for name, want := range map[string]string{"one.txt": "one\n", "two.txt": "two\n"} {
				data, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(data) != want {
					t.Fatalf("partial write in %s: %q, %v", name, data, err)
				}
			}
		})
	}
}
