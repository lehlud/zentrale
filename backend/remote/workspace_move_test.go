package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
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
