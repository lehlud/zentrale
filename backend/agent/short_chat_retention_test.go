package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func retentionTestEngine(t *testing.T) (*Engine, func(string, string, time.Time)) {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "retention.sqlite")
	store, err := datastore.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db, err := sql.Open("sqlite3", filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	engine := &Engine{
		Store: store, Remote: remote.NewManager(nil), installationID: strings.Repeat("b", 32), preparing: make(map[string]int),
	}
	t.Cleanup(engine.workers.Wait)
	return engine, func(kind, id string, when time.Time) {
		t.Helper()
		if _, err := db.Exec(`UPDATE resources SET created_at=? WHERE kind=? AND id=?`,
			when.UTC().Format("2006-01-02 15:04:05.000+00:00"), kind, id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestShortChatRetentionRestartsOnMessageAndKeepsAtLeastHalfAfterUnpin(t *testing.T) {
	engine, backdate := retentionTestEngine(t)
	now := time.Now().UTC()
	settings, err := engine.ShortChatSettings()
	if err != nil || settings.RetentionDays != 45 || settings.VMLifetimeDays != 7 {
		t.Fatalf("unexpected defaults: %+v, %v", settings, err)
	}

	const owner, project = "chat-owner", "untouched-project"
	for _, chat := range []domain.Agent{
		{ID: "expired", ChatOwnerID: owner, Role: "main"},
		{ID: "pinned", ChatOwnerID: owner, Role: "main", ChatPinned: true},
		{ID: "older-unpinned", ChatOwnerID: owner, Role: "main", ChatPinned: true},
		{ID: "younger-unpinned", ChatOwnerID: owner, Role: "main", ChatPinned: true},
		{ID: "reused", ChatOwnerID: owner, Role: "main"},
		{ID: "queued", ChatOwnerID: owner, Role: "main"},
		{ID: "busy", ChatOwnerID: owner, Role: "main"},
		{ID: project, ProjectID: project, Role: "main"},
	} {
		if err := engine.Store.Save("agent", chat.ID, chat.ProjectID, chat); err != nil {
			t.Fatal(err)
		}
		age := 60 * shortChatDay
		if chat.ID == "younger-unpinned" {
			age = 10 * shortChatDay
		}
		backdate("agent", chat.ID, now.Add(-age))
	}

	for _, id := range []string{"older-unpinned", "younger-unpinned"} {
		if err := engine.SetShortChatPinned(owner, id, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.Store.Save("message", "new-message", "reused", domain.Message{Role: "user", Content: "back again"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Save("agent_input", "new-input", "queued", domain.AgentInput{AgentID: "queued", Status: "queued"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Save("message", "expired-message", "expired", domain.Message{Role: "user"}); err != nil {
		t.Fatal(err)
	}
	backdate("message", "expired-message", now.Add(-59*shortChatDay))
	if err := engine.Store.SaveAttachment(domain.Attachment{AgentID: "expired", ID: "file"}, []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Save(shortChatVMKind, owner, "", shortChatVMPlacement{MemberID: "old-host"}); err != nil {
		t.Fatal(err)
	}
	engine.preparing["busy"] = 1

	exists := func(kind, id string, want bool) {
		t.Helper()
		var row map[string]any
		err := engine.Store.Load(kind, id, &row)
		if (want && err != nil) || (!want && !errors.Is(err, sql.ErrNoRows)) {
			t.Fatalf("%s %s exists=%t: %v", kind, id, want, err)
		}
	}

	engine.pruneShortChats(context.Background(), now.Add(22*shortChatDay))
	exists("agent", "expired", false)
	exists("message", "expired-message", false)
	if _, _, err := engine.Store.Attachment("expired", "file"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired attachment was not removed: %v", err)
	}
	rows, err := engine.Store.PendingShortChatCleanup()
	if err != nil || len(rows) != 1 || stringArg(rows[0], "memberId") != "old-host" {
		t.Fatalf("deletion did not retain its cleanup target: %v, %v", rows, err)
	}
	for _, id := range []string{"older-unpinned", "younger-unpinned", "reused", "queued", "busy", "pinned", project} {
		exists("agent", id, true)
	}

	engine.pruneShortChats(context.Background(), now.Add(22*shortChatDay+13*time.Hour))
	exists("agent", "older-unpinned", false)
	exists("agent", "younger-unpinned", true)

	engine.pruneShortChats(context.Background(), now.Add(36*shortChatDay))
	exists("agent", "younger-unpinned", false)
	exists("agent", "reused", true)
	exists("agent", "queued", true)

	engine.pruneShortChats(context.Background(), now.Add(46*shortChatDay))
	exists("agent", "reused", false)
	exists("agent", "queued", false)
	for _, id := range []string{"busy", "pinned", project} {
		exists("agent", id, true)
	}
}

func TestShortChatLocalVMExpiryRemovesDiskAndWorkspaceWithoutBoot(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("local VM lifecycle requires Python")
	}
	root := t.TempDir()
	t.Setenv("ZENTRALE_SHORT_CHAT_VM_DIR", root)
	engine, backdate := retentionTestEngine(t)
	chat := domain.Agent{ID: "chat", ChatOwnerID: "owner"}
	scope, directory := engine.shortChatWorkspace(chat)
	local := remote.LocalShortChatMachineID
	placement := shortChatVMPlacement{TargetMachineID: local, MemberID: local, Stopped: true, UsedAt: time.Now()}
	if err := engine.Store.Save(shortChatVMKind, chat.ChatOwnerID, "", placement); err != nil {
		t.Fatal(err)
	}

	installation := filepath.Join(root, engine.installationID)
	key := sha256.Sum256([]byte(engine.installationID + "\x00" + scope))
	workspace := filepath.Join(installation, "workspaces", scope)
	answer := filepath.Join(workspace, directory, "answer.txt")
	disk := filepath.Join(installation, "runtime", "projects", fmt.Sprintf("%x", key), "root.qcow2")
	for _, filename := range []string{answer, disk} {
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte("demo"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	if err := streamLocalChatFile(workspace, directory+"/answer.txt", func(size int64, reader io.Reader) error {
		data, err := io.ReadAll(reader)
		if err != nil || size != 4 || string(data) != "demo" {
			t.Fatalf("unexpected local file: %d, %q, %v", size, data, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(installation, filepath.Join(workspace, directory, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := streamLocalChatFile(workspace, directory+"/alias/irrelevant.txt", func(int64, io.Reader) error {
		t.Fatal("local reader followed a symlink")
		return nil
	}); !errors.Is(err, ErrWorkspaceFileNotFound) {
		t.Fatalf("unexpected symlink response: %v", err)
	}

	removedChat := domain.Agent{ID: "removed-chat", ChatOwnerID: chat.ChatOwnerID, Role: "main"}
	_, removedDirectory := engine.shortChatWorkspace(removedChat)
	removedWorkspace := filepath.Join(workspace, removedDirectory)
	if err := os.Mkdir(removedWorkspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(answer, filepath.Join(removedWorkspace, "other-chat-file")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Save("agent", removedChat.ID, "", removedChat); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.RemoveAgents([]string{removedChat.ID}); err != nil {
		t.Fatal(err)
	}
	engine.cleanupShortChatDirectories(context.Background())
	if _, err := os.Stat(removedWorkspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed chat still has a workspace: %v", err)
	}
	if _, err := os.Stat(answer); err != nil {
		t.Fatalf("other chat's files were touched: %v", err)
	}

	backdate(shortChatVMKind, chat.ChatOwnerID, time.Now().Add(-8*shortChatDay))
	engine.reconcileShortChatVMs(context.Background())
	if err := engine.Store.Load(shortChatVMKind, chat.ChatOwnerID, &placement); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired VM receipt was retained: %v", err)
	}
	for _, path := range []string{workspace, disk} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired VM file %q survived: %v", path, err)
		}
	}
}
