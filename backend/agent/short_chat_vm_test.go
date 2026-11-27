package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestShortChatVMLazyAndOwnerScoped(t *testing.T) {
	var calls atomic.Int32
	var lock sync.Mutex
	var staged []map[string]any
	const guestCommand = "printf 'from guest'"

	var plainTurns atomic.Int32
	plainProvider := providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		plainTurns.Add(1)
		return providers.Result{Text: "A plain answer", UsageKnown: true}, nil
	})
	engine, projectChat, project := vmExecutionTestEngine(t, plainProvider, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		calls.Add(1)
		command = strings.ReplaceAll(command, `'"'"'`, `'`)
		if strings.Contains(command, guestCommand) {
			t.Error("user command appeared in a host command; it belongs only in the guest stdin payload")
			return 1
		}

		switch {
		case strings.Contains(command, "short-chat workspace is not a private directory"):
		case strings.Contains(command, " 'ensure'"):
		case strings.Contains(command, " 'stage'"):
			var payload struct {
				Execution map[string]any `json:"execution"`
			}
			if err := json.NewDecoder(input).Decode(&payload); err != nil {
				t.Error(err)
				return 1
			}

			lock.Lock()
			staged = append(staged, payload.Execution)
			lock.Unlock()
			fmt.Fprint(stdout, "/tmp/test-vm/requests/0123456789abcdef0123456789abcdef")
		case strings.Contains(command, " 'run'"):
			fmt.Fprint(stdout, "from guest")
		default:
			t.Error("unexpected host operation")
			return 1
		}
		return 0
	})

	alice := addTestChatUser(t, engine, "alice-short-chat")
	bob := addTestChatUser(t, engine, "bob-short-chat")
	first := createTestShortChat(t, engine, alice)
	second := createTestShortChat(t, engine, alice)
	third := createTestShortChat(t, engine, bob)
	machineID := project.MachineIDs[0]
	ctx := context.Background()

	if err := engine.Prompt(first.ID, "Answer without tools"); err != nil {
		t.Fatal(err)
	}
	if record := waitForAgent(t, engine, first.ID); record.Status == "error" || plainTurns.Load() == 0 {
		t.Fatalf("plain chat did not reach inference: status=%q turns=%d", record.Status, plainTurns.Load())
	}
	if goal, err := engine.CurrentGoal(first.ID); err != nil || goal != nil || plainTurns.Load() != 1 {
		t.Fatalf("plain chat must finish after one inference and without a goal: goal=%+v turns=%d error=%v", goal, plainTurns.Load(), err)
	}

	checkShortChatShareWithoutVM(t, engine, first)
	forged := first
	forged.ChatOwnerID = bob
	for _, tc := range []struct {
		chat            domain.Agent
		machineID, pool string
	}{
		{first, "", ""},
		{first, machineID, "both"},
		{projectChat, machineID, ""},
		{forged, machineID, ""},
	} {
		if _, err := engine.RunShortChatCommand(ctx, tc.chat, tc.machineID, tc.pool, guestCommand); err == nil {
			t.Fatal("invalid short-chat command was accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("plain chat, share read, or rejected command started remote work: %d calls", calls.Load())
	}
	if _, pinned, err := engine.loadShortChatPlacement(alice, machineID, ""); err != nil || pinned {
		t.Fatalf("plain chat unexpectedly acquired VM placement: pinned=%t err=%v", pinned, err)
	}

	for _, chat := range []domain.Agent{first, second, third} {
		output, err := engine.RunShortChatCommand(ctx, chat, machineID, "", guestCommand)
		if err != nil || !strings.Contains(output, "from guest") {
			t.Fatalf("short-chat command output=%q err=%v", output, err)
		}
	}
	placement, pinned, err := engine.loadShortChatPlacement(alice, machineID, "")
	if err != nil || !pinned || placement.MemberID != machineID {
		t.Fatalf("placement=%+v pinned=%t err=%v", placement, pinned, err)
	}
	remoteBeforeChange := calls.Load()
	if _, err := engine.RunShortChatCommand(ctx, first, "other-machine", "", guestCommand); err == nil || calls.Load() != remoteBeforeChange {
		t.Fatal("changing the target must not silently place an existing user on another machine")
	}

	lock.Lock()
	defer lock.Unlock()
	if len(staged) != 3 {
		t.Fatalf("staged requests: %d", len(staged))
	}
	for i, execution := range staged {
		chat := []domain.Agent{first, second, third}[i]
		scope, ok := execution["projectId"].(string)
		if !ok || !strings.HasPrefix(scope, "shortchat-") || len("project-"+scope) > 80 || scope == project.ID ||
			execution["agentId"] != "project-"+scope || execution["network"] != false || execution["guestRoot"] != false ||
			execution["tooling"] != false || execution["storage"] != nil || execution["workspace"] == project.Directory {
			t.Errorf("unexpected execution payload: %+v", execution)
		}
		guestScript, _ := execution["command"].(string)
		if !strings.Contains(guestScript, "/home/zentrale/project/chat-"+shortChatDigest(scope, chat.ID)) ||
			!strings.Contains(guestScript, "from guest") {
			t.Errorf("command uses wrong working directory: %s", guestScript)
		}
	}
	for _, key := range []string{"projectId", "agentId", "workspace", "temporaryDirectory"} {
		if staged[0][key] != staged[1][key] || staged[0][key] == staged[2][key] {
			t.Errorf("short-chat VM isolation mismatch for %q", key)
		}
	}
}

func addTestChatUser(t *testing.T, engine *Engine, name string) string {
	t.Helper()
	user := domain.User{ID: NewID(), Username: name, Name: name, Role: domain.RoleUser}
	if err := engine.Store.InsertAccount(datastore.Account{User: user, PasswordHash: "test-only-hash"}); err != nil {
		t.Fatal(err)
	}
	return user.ID
}

func createTestShortChat(t *testing.T, engine *Engine, ownerID string) domain.Agent {
	t.Helper()
	chat, err := engine.CreateShortChat(ownerID, "Test short chat")
	if err != nil {
		t.Fatal(err)
	}
	return chat
}

func checkShortChatShareWithoutVM(t *testing.T, engine *Engine, chat domain.Agent) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PROPFIND":
			w.WriteHeader(http.StatusMultiStatus)
			kind := ""
			if r.URL.Path == "/" {
				kind = "<d:collection/>"
			}
			fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype>%s</d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`, r.URL.Path, kind)
		case r.Method == "GET" && r.URL.Path == "/note.txt":
			fmt.Fprint(w, "share-only data")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	share, err := engine.SaveFileShare(FileShareConfig{Protocol: "webdav", Endpoint: server.URL, Root: "/", Name: "Test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := engine.shortChatTool(context.Background(), chat, providers.Call{
		Name: "share_read", Arguments: map[string]any{"shareId": share.ID, "path": "note.txt"},
	})
	if err != nil || got != (shareExcerpt{Text: "share-only data"}) {
		t.Fatalf("share read without a VM: got=%v err=%v", got, err)
	}

	got, err = engine.shortChatTool(t.Context(), chat, providers.Call{
		Name: "share_read", Arguments: map[string]any{"shareId": share.ID, "path": "note.txt", "offset": 6, "limit": 4},
	})
	excerpt, ok := got.(shareExcerpt)
	if err != nil || !ok || excerpt.Text != "only" || excerpt.Offset != 6 || excerpt.NextOffset == nil || *excerpt.NextOffset != 10 {
		t.Fatalf("share excerpt without a VM: got=%+v err=%v", got, err)
	}
}
