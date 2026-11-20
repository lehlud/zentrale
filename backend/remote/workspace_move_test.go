package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceMoveLargerThanDaemonOutputLimit(t *testing.T) {
	connector, _ := daemonTestConnector(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := connector.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	const project, token, move = "large-project", "workspace-token", "archive-test"
	makeWorkspace := func() string {
		root := t.TempDir()
		workspace := filepath.Join(root, "project-"+project)
		metadata := filepath.Join(root, ".zentrale-workspaces")
		for _, directory := range []string{workspace, metadata} {
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(metadata, "project-"+project+".json"), []byte(`{"token":"`+token+`"}`), 0600); err != nil {
			t.Fatal(err)
		}
		return workspace
	}
	source, destination := makeWorkspace(), makeWorkspace()
	content := bytes.Repeat([]byte{0, 255, 13, 10}, 256*1024)
	hash := sha256.New()
	file, err := os.Create(filepath.Join(source, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	writer := io.MultiWriter(file, hash)
	// Regular, non-sparse bytes exercise the archive path past the daemon's
	// 64 MiB per-resource ceiling.
	for range 65 {
		if _, err := writer.Write(content); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if err := TransferWorkspace(ctx, connection, connection, project, source, destination, token, move); err != nil {
		t.Fatal(err)
	}
	copied, err := os.Open(filepath.Join(destination, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	digest := sha256.New()
	if count, err := io.Copy(digest, copied); err != nil || count != 65*1024*1024 || !bytes.Equal(digest.Sum(nil), hash.Sum(nil)) {
		t.Fatalf("archive bytes changed in transit: size %d, %v", count, err)
	}
}

func TestWorkspaceMoveChecksArchiveProcessExit(t *testing.T) {
	connector, _ := daemonTestConnector(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := connector.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	err = transferMoveStream(ctx, connection, connection, "printf 'partial archive'; exit 23", "cat >/dev/null")
	var failure *DaemonExitError
	if !errors.As(err, &failure) || failure.Code != 23 {
		t.Fatalf("sender failure was lost: %v", err)
	}
}

func TestWorkspaceMoveDiscardsOnlyMatchingLegacyVMExports(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "project-demo")
	backups := filepath.Join(workspace, ".zentrale-vm-backups")
	metadata := filepath.Join(root, ".zentrale-workspaces")
	for _, directory := range []string{backups, metadata} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}

	removed := []string{
		filepath.Join(backups, "current-0123456789abcdef.pending"),
		filepath.Join(backups, "current-abcdef0123456789.qcow2"),
		filepath.Join(metadata, "project-demo-snapshot-ready-current-abcdef0123456789"),
	}
	preserved := []string{
		filepath.Join(backups, "older-0123456789abcdef.qcow2"),
		filepath.Join(backups, "notes.txt"),
		filepath.Join(workspace, "local-changes.txt"),
	}
	files := map[string]string{
		filepath.Join(backups, ".owner"):             `"zentrale-vm-backups-v1"`,
		filepath.Join(metadata, "project-demo.json"): `{"token":"transfer-token"}`,
	}
	for _, file := range append(removed, preserved...) {
		files[file] = "fixture data"
	}
	for file, content := range files {
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		command := exec.Command("python3", "-I", "-c", workspaceMoveScript, "discard-legacy-snapshots", "project-demo", workspace, "transfer-token", "current")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("discard legacy snapshots: %s: %v", output, err)
		}
	}
	for _, file := range removed {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("old temporary file %s remains: %v", file, err)
		}
	}
	for _, file := range preserved {
		if content, err := os.ReadFile(file); err != nil || string(content) != "fixture data" {
			t.Errorf("unrelated file %s was changed: %v", file, err)
		}
	}
}
