package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lehlud/zentrale/providers"
)

func TestProjectImageToolStreamsReviewedSnapshotIntoNextModelRequest(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python is required for the workspace helper")
	}

	var pngFile bytes.Buffer
	if err := png.Encode(&pngFile, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	payload := pngFile.Bytes()
	encoded := base64.StdEncoding.EncodeToString(payload)
	reviewed := fmt.Sprintf("%x", sha256.Sum256(payload))
	var mainCalls, reviewCalls int
	provider := providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.System == approverInstructions {
			reviewCalls++
			text := JSON(request.Messages)
			if !strings.Contains(text, reviewed) || strings.Contains(text, encoded) {
				t.Errorf("review must receive a digest, not image bytes: %s", text)
			}

			allowed := !strings.Contains(request.Messages[len(request.Messages)-1].Content, "deny the fixture")
			return providers.Result{Text: fmt.Sprintf(`{"allowed":%t,"reason":"fixture"}`, allowed), UsageKnown: true}, nil
		}

		mainCalls++
		messages := request.Messages
		if len(messages) != 3 || messages[0].CallID != "image" || messages[1].CallID != "sibling" ||
			messages[2].Role != "user" || !strings.Contains(messages[2].Content, "scan.png") ||
			len(messages[2].Images) != 1 || messages[2].Images[0].MimeType != "image/png" ||
			!bytes.Equal(messages[2].Images[0].Data, payload) {
			t.Errorf("expected reviewed image after sibling tool results: %+v", messages)
		}
		return providers.Result{Text: "seen", UsageKnown: true}, nil
	})
	engine, record, project := executionTestEngine(t, provider, shellPeerHandler("/bin/sh", os.Environ()))
	ctx, cancel := context.WithTimeout(withTurnImages(context.Background()), 30*time.Second)
	defer cancel()
	if err := engine.requireConnection(ctx, record.ID); err != nil {
		t.Fatal(err)
	}

	scan := filepath.Join(project.Directory, "scan.png")
	if err := os.WriteFile(scan, payload, 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project.Directory, "linked.png")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(project.Directory, "pipe.png"), 0600); err != nil {
		t.Fatal(err)
	}

	nativeCall := func(id, path, reason string) providers.Call {
		return providers.Call{ID: id, Name: "view_image", Arguments: map[string]any{"path": path, "justification": reason}}
	}
	calls := []providers.Call{
		nativeCall("image", "scan.png", "inspect the example"),
		{ID: "sibling", Name: "get_goal"},
	}
	if err := engine.executeCalls(ctx, record, calls); err != nil {
		t.Fatal(err)
	}
	history, err := engine.loadHistory(record.ID)
	if err != nil || len(history) != 2 || strings.Contains(JSON(history), encoded) || len(history[0].Images) != 0 {
		t.Fatalf("expected metadata only in persisted history: %+v, %v", history, err)
	}
	if err := os.Remove(scan); err != nil {
		t.Fatal(err)
	}
	if _, _, err := engine.completeVisible(ctx, record.ID, providers.Request{Messages: history}); err != nil {
		t.Fatal(err)
	}
	if len(attachProjectImages(withTurnImages(ctx), record.ID, history)) != 2 ||
		len(attachProjectImages(ctx, "another-agent", history)) != 2 {
		t.Fatal("snapshot escaped its own agent and turn")
	}
	if err := os.WriteFile(scan, payload, 0600); err != nil {
		t.Fatal(err)
	}

	if err := engine.executeCalls(ctx, record, []providers.Call{
		nativeCall("escape", outside, "try an absolute non-project path"),
		nativeCall("link", "linked.png", "try a symlink"),
		nativeCall("fifo", "pipe.png", "try a special file"),
		{ID: "denied", Name: "harness", Arguments: map[string]any{
			"action": "view_image", "arguments": `{"path":"scan.png"}`, "justification": "deny the fixture",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	history, err = engine.loadHistory(record.ID)
	if err != nil || len(history) != 6 {
		t.Fatalf("missing tool receipts: %+v, %v", history, err)
	}
	for _, message := range history[2:] {
		if !toolOutputHasFailure(message.Content) {
			t.Errorf("%s unexpectedly succeeded: %s", message.CallID, message.Content)
		}
	}
	if len(attachProjectImages(ctx, record.ID, history)) != len(history)+1 || mainCalls != 1 || reviewCalls != 2 {
		t.Fatalf("unexpected image delivery or reviews: main=%d reviews=%d", mainCalls, reviewCalls)
	}
}

func TestReadProjectImageRejectsOversizedTruncatedOrNonRasterData(t *testing.T) {
	for _, test := range []struct {
		name string
		size int64
		data string
	}{
		{"oversized", maxProjectImageBytes + 1, ""},
		{"truncated", 3, "xx"},
		{"text", 4, "text"},
		{"bad header", 8, "\x89PNG\r\n\x1a\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, err := readProjectImage(test.size, strings.NewReader(test.data)); err == nil || len(got.Data) != 0 {
				t.Fatalf("invalid file accepted: %+v, %v", got, err)
			}
		})
	}
}
