package webapi

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/remote"
)

// Include many unrelated agents, model wire blobs and old job output: a tiny
// response alone is not evidence that server-side loading is bounded.
func seedBootstrapHistory(t testing.TB, f *apiFixture, agents, messages, jobs int) {
	t.Helper()
	saveBootstrapResource(t, f, "project", "history-project", "", map[string]any{"name": "History"})
	wire := strings.Repeat("private-provider-wire", 2048)
	writes := []datastore.ResourceWrite{}
	flush := func() {
		if err := f.engine.Store.SaveResources(writes); err != nil {
			t.Fatal(err)
		}
		writes = nil
	}
	add := func(kind, id, parent string, value map[string]any) {
		value["id"] = id
		writes = append(writes, datastore.ResourceWrite{Kind: kind, ID: id, Parent: parent, Value: value})
		if len(writes) == 100 {
			flush()
		}
	}
	for n := range agents {
		id, role := fmt.Sprintf("history-agent-%04d", n), "child"
		if n == 0 {
			role = "main"
		}
		add("agent", id, "history-project", map[string]any{"projectId": "history-project", "role": role, "summary": wire})
	}
	for n := range messages {
		parent := "history-agent-0000"
		if n%2 != 0 {
			parent = fmt.Sprintf("history-agent-%04d", 1+n%(agents-1))
		}
		id := fmt.Sprintf("history-message-%05d", n)
		add("message", id, parent, map[string]any{
			"agentId": parent, "role": "assistant", "content": "visible transcript", "wire": wire,
			"attachments": []map[string]any{{"id": "attachment-" + id, "name": "trace.txt", "mimeType": "text/plain", "size": 42, "path": "uploads/trace.txt", "machineId": "worker"}}})
		add("tool_activity", id, parent, map[string]any{"agentId": parent, "activities": []map[string]any{
			{"name": "exec", "arguments": `{"cmd":"build"}`, "content": "build output", "network": false, "justification": "test build"}}})
		add("file_change", id, parent, map[string]any{
			"agentId": parent, "messageId": id, "path": "example.go", "kind": "modified", "patch": "-old\n+new\n", "added": 1, "removed": 1})
	}
	for n := range jobs {
		status := "completed"
		if n < 3 {
			status = "running"
		}
		add("job", fmt.Sprintf("history-job-%05d", n), "history-agent-0000", map[string]any{
			"agentId": "history-agent-0000", "command": "build", "status": status, "output": wire})
	}
	flush()
}

// Mirror the complete production selection in frontend/src/conversation-bootstrap.ts
// (including agent-transcript.ts messageFields). In particular wire and full
// tool/diff data remain required by the UI; a wire-free query is not a faithful
// benchmark of the reported initial-conversation request.
const historyBootstrapQuery = `query{conversationBootstrap(projectId:"history-project",agentId:""){
 project { id name directory managedWorkspace machineIds poolIds commitName commitEmail gitConnectionId repositoryId repositoryName }
 agents { id name role status parentId approverId projectId providerId modelId machineId error contextStart }
 agent { id name role status parentId approverId projectId providerId modelId machineId error contextStart }
 messages { id agentId role content wire callId toolName createdAt
   attachments { id name mimeType size path machineId }
   activities { name arguments content network justification }
   change { id path kind patch added removed truncated notice }
   changes { id path kind patch added removed truncated notice } }
 goal { id agentId objective origin status requestedStatus requestReason sourceInputIds revision continuations blockedAttempts blocker reason evidence createdAt updatedAt }
 agentInputs { id agentId projectId text mode status createdAt deliveredAt attachments { id name mimeType size path machineId } }
 agentRunState { agentId phase error hostKey { host fingerprint publicKey } }
 questions { id title options status answer agentId }
 jobs { id command status error startedAt machineId agentId timeoutMs }
 modelDefaults { main { providerId modelId reasoningLevel } approver { providerId modelId reasoningLevel } }
 machineName sandboxMode
 sandboxStatus { machineId mode level checkedAt error }
 }}`

func TestConversationBootstrapLargeHistoryDefaultAgent(t *testing.T) {
	f := bootstrapFixture(t)
	seedBootstrapHistory(t, f, 64, 256, 256)
	data := requireData(t, f.query(t, historyBootstrapQuery, nil))["conversationBootstrap"].(map[string]any)
	if data["agent"].(map[string]any)["id"] != "history-agent-0000" || len(data["agents"].([]any)) != 64 ||
		len(data["messages"].([]any)) != 100 || len(data["jobs"].([]any)) != 3 {
		t.Fatal("history was not scoped/bounded to the default main agent")
	}
	for _, entry := range data["messages"].([]any) {
		message := entry.(map[string]any)
		activity := message["activities"].([]any)[0].(map[string]any)
		changes := message["changes"].([]any)
		if message["wire"] != strings.Repeat("private-provider-wire", 2048) ||
			activity["arguments"] != `{"cmd":"build"}` || activity["content"] != "build output" ||
			message["change"].(map[string]any)["patch"] != "-old\n+new\n" || len(changes) != 1 ||
			changes[0].(map[string]any)["patch"] != "-old\n+new\n" || len(message["attachments"].([]any)) != 1 {
			t.Fatal("production wire/activity/patch/attachment selection lost data")
		}
	}
}

func BenchmarkConversationBootstrapLargeHistory(b *testing.B) {
	f := bootstrapFixture(b)
	seedBootstrapHistory(b, f, 300, 3000, 1000)
	// Excludes DB creation, authentication and seeding; includes HTTP, session
	// validation, resolver/SQL work, JSON encoding and client decoding.
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		data := requireData(b, f.query(b, historyBootstrapQuery, nil))["conversationBootstrap"].(map[string]any)
		if len(data["messages"].([]any)) != 100 || len(data["jobs"].([]any)) != 3 {
			b.Fatal("unexpected bounded history")
		}
	}
}

func BenchmarkConversationSmallListsLargeHistory(b *testing.B) {
	f := bootstrapFixture(b)
	seedBootstrapHistory(b, f, 300, 3000, 1000)
	for _, item := range []struct{ name, query string }{
		{"projects", `query { projects(limit:500) { id name } }`},
		{"agents", `query { agents(projectId:"history-project",limit:500) { id projectId parentId approverId name role status } }`},
	} {
		b.Run(item.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				data := requireData(b, f.query(b, item.query, nil))
				if len(data[item.name].([]any)) == 0 {
					b.Fatal("empty collection")
				}
			}
		})
	}
}

func TestConversationBootstrapIncludesSavedSandboxObservation(t *testing.T) {
	f := bootstrapFixture(t)
	saveBootstrapResource(t, f, "project", "sandbox-project", "", map[string]any{"name": "Sandbox"})
	saveBootstrapResource(t, f, "machine", "sandbox-machine", "", map[string]any{"name": "Worker", "host": "unused.invalid", "sandbox": "auto"})
	saveBootstrapResource(t, f, "agent", "sandbox-agent", "sandbox-project", map[string]any{"projectId": "sandbox-project", "role": "main", "machineId": "sandbox-machine"})
	machine, err := f.engine.Remote.Connector.Machine("sandbox-machine")
	if err != nil {
		t.Fatal(err)
	}
	saveBootstrapResource(t, f, "sandboxStatus", machine.ID, "", map[string]any{
		"machineId": machine.ID, "mode": "auto", "level": "bwrap", "checkedAt": "2026-01-01T00:00:00Z",
		"configuration": agent.JSON(remote.WithMachineDefaults(machine)), "implementation": remote.SandboxImplementationID(),
	})
	response := f.query(t, `query{conversationBootstrap(projectId:"sandbox-project"){sandboxMode sandboxStatus{machineId mode level checkedAt error}}}`, nil)
	data := requireData(t, response)["conversationBootstrap"].(map[string]any)
	status, ok := data["sandboxStatus"].(map[string]any)
	if !ok || status["machineId"] != machine.ID || status["level"] != "bwrap" || status["mode"] != "auto" {
		t.Fatalf("saved observation lost during bootstrap: %v", response)
	}
}

func TestConversationBootstrapDoesNotTransferStoredDetailMegabytes(t *testing.T) {
	f := bootstrapFixture(t)
	saveBootstrapResource(t, f, "project", "payload-project", "", map[string]any{"name": "Payload regression"})
	saveBootstrapResource(t, f, "agent", "payload-agent", "payload-project", map[string]any{
		"projectId": "payload-project", "role": "main", "summary": strings.Repeat("summary", 16384),
	})
	for index := 0; index < 32; index++ {
		saveBootstrapResource(t, f, "job", fmt.Sprintf("payload-job-%02d", index), "payload-agent", map[string]any{
			"agentId": "payload-agent", "status": "completed", "command": "build", "output": strings.Repeat("output", 10923),
		})
	}
	saveBootstrapResource(t, f, "message", "payload-message", "payload-agent", map[string]any{"agentId": "payload-agent", "role": "assistant", "content": "A short visible reply"})
	started := time.Now()
	compact := f.query(t, `query{conversationBootstrap(projectId:"payload-project"){project{id name} agents{id name} agent{id name} messages{id role content} jobs{id command status} questions{id title status} agentInputs{id text status}}}`, nil)
	requireData(t, compact)
	compactDuration := time.Since(started)
	legacy := f.query(t, `query{agent(id:"payload-agent"){id summary} messages:recentMessages(agentId:"payload-agent"){id content wire} jobs(agentId:"payload-agent",limit:500){id command status output}}`, nil)
	requireData(t, legacy)
	compactJSON, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyJSON) < 1024*1024 {
		t.Fatal("fixture no longer represents megabyte-sized legacy detail queries")
	}
	if len(compactJSON) > 16*1024 || len(compactJSON)*100 > len(legacyJSON) {
		t.Fatalf("bootstrap transferred detail blobs: compact=%d legacy=%d", len(compactJSON), len(legacyJSON))
	}
	t.Logf("fixture response bytes: bootstrap=%d legacy=%d; bootstrap HTTP+resolver duration=%s (not a production latency benchmark)", len(compactJSON), len(legacyJSON), compactDuration)
}

const bootstrapTestQuery = `query($projectId:String!, $agentId:String) {
 conversationBootstrap(projectId:$projectId,agentId:$agentId) {
  project{id name} agents{id role summary} agent{id role projectId parentId summary}
  messages{id role content attachments{id name path} activities{name} changes{id path}}
  questions{id status title} agentInputs{id status}
 }
}`

func bootstrapFixture(t testing.TB) *apiFixture {
	t.Helper()
	f := newAPIFixture(t)
	requireData(t, f.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.test"})}`, nil))
	return f
}

func saveBootstrapResource(t testing.TB, f *apiFixture, kind, id, parent string, value map[string]any) {
	t.Helper()
	value["id"] = id
	if err := f.engine.Store.Save(kind, id, parent, value); err != nil {
		t.Fatal(err)
	}
}

func TestConversationBootstrapAuthenticationAndProjectScope(t *testing.T) {
	f := newAPIFixture(t)
	vars := map[string]any{"projectId": "project-a"}
	if response := f.query(t, bootstrapTestQuery, vars); response["errors"] == nil || !strings.Contains(fmt.Sprint(response["errors"]), "authentication required") {
		t.Fatalf("unauthenticated bootstrap allowed: %v", response)
	}
	requireData(t, f.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.test"})}`, nil))
	if response := f.query(t, bootstrapTestQuery, vars); response["errors"] == nil {
		t.Fatalf("unknown project accepted: %v", response)
	}
	saveBootstrapResource(t, f, "project", "project-a", "", map[string]any{"name": "A"})
	saveBootstrapResource(t, f, "project", "project-b", "", map[string]any{"name": "B"})
	saveBootstrapResource(t, f, "agent", "foreign", "project-b", map[string]any{"projectId": "project-b", "role": "main"})
	saveBootstrapResource(t, f, "message", "secret", "foreign", map[string]any{"agentId": "foreign", "role": "user", "content": "private foreign transcript"})
	vars["agentId"] = "foreign"
	response := f.query(t, bootstrapTestQuery, vars)
	if response["errors"] == nil || !strings.Contains(fmt.Sprint(response["errors"]), "agent does not belong to project") {
		t.Fatalf("cross-project selection accepted: %v", response)
	}
	if strings.Contains(fmt.Sprint(response), "private foreign transcript") {
		t.Fatal("cross-project transcript leaked")
	}
	vars["agentId"] = "missing"
	if response := f.query(t, bootstrapTestQuery, vars); response["errors"] == nil {
		t.Fatalf("unknown explicit agent silently fell back: %v", response)
	}
}

func TestConversationBootstrapDefaultExplicitChildAndApprover(t *testing.T) {
	f := bootstrapFixture(t)
	saveBootstrapResource(t, f, "project", "project", "", map[string]any{"name": "Project"})
	vars := map[string]any{"projectId": "project"}
	empty := requireData(t, f.query(t, bootstrapTestQuery, vars))["conversationBootstrap"].(map[string]any)
	if empty["agent"] != nil || len(empty["messages"].([]any)) != 0 || len(empty["agentInputs"].([]any)) != 0 {
		t.Fatalf("invalid empty project bootstrap: %v", empty)
	}
	for _, item := range []struct{ id, role, parent string }{
		{"main", "main", ""}, {"child", "child", "main"}, {"approver", "approver", "main"},
	} {
		saveBootstrapResource(t, f, "agent", item.id, "project", map[string]any{
			"projectId": "project", "role": item.role, "parentId": item.parent, "summary": "not for agent list",
		})
		saveBootstrapResource(t, f, "message", "message-"+item.id, item.id, map[string]any{
			"agentId": item.id, "role": "user", "content": item.id,
		})
	}
	for _, id := range []string{"", "main", "child", "approver"} {
		t.Run("selection-"+id, func(t *testing.T) {
			vars["agentId"] = id
			data := requireData(t, f.query(t, bootstrapTestQuery, vars))["conversationBootstrap"].(map[string]any)
			want := id
			if want == "" {
				want = "main"
			}
			selected := data["agent"].(map[string]any)
			if selected["id"] != want || selected["projectId"] != "project" {
				t.Fatalf("wrong selected agent: %v", selected)
			}
			if got := data["messages"].([]any); len(got) != 1 || got[0].(map[string]any)["content"] != want {
				t.Fatalf("wrong transcript: %v", got)
			}
			if want == "approver" && selected["role"] != "approver" {
				t.Fatal("bootstrap changed readonly approver role")
			}
			if selected["summary"] != "" {
				t.Fatal("bootstrap decoded selected agent's detail-only summary")
			}
			for _, row := range data["agents"].([]any) {
				if row.(map[string]any)["summary"] != "" {
					t.Fatal("agent list loaded large summaries")
				}
			}
		})
	}
}

func TestConversationBootstrapRecentMessagesMatchPaginationAndKeepPendingQuestions(t *testing.T) {
	f := bootstrapFixture(t)
	saveBootstrapResource(t, f, "project", "project", "", map[string]any{"name": "Project"})
	saveBootstrapResource(t, f, "agent", "main", "project", map[string]any{"projectId": "project", "role": "main"})
	for index := 0; index < 105; index++ {
		id := fmt.Sprintf("message-%03d", index)
		saveBootstrapResource(t, f, "message", id, "main", map[string]any{
			"agentId": "main", "role": "user", "content": id,
			"attachments": []map[string]any{{"id": "attachment-" + id, "name": "image.png", "path": "uploads/image.png"}},
		})
		status := "answered"
		if index == 104 {
			status = "open"
		}
		saveBootstrapResource(t, f, "question", fmt.Sprintf("question-%03d", index), "main", map[string]any{
			"agentId": "main", "title": id, "status": status,
		})
	}
	data := requireData(t, f.query(t, bootstrapTestQuery, map[string]any{"projectId": "project"}))["conversationBootstrap"].(map[string]any)
	messages := data["messages"].([]any)
	if len(messages) != 100 {
		t.Fatalf("bootstrap history unbounded: %d", len(messages))
	}
	plain := requireData(t, f.query(t, `query{recentMessages(agentId:"main"){id role content attachments{id name path} activities{name} changes{id path}}}`, nil))["recentMessages"]
	if !reflect.DeepEqual(data["messages"], plain) {
		t.Fatal("bootstrap changed transcript ordering, attachments or file change projection")
	}
	beforeID := messages[len(messages)-1].(map[string]any)["id"]
	older := requireData(t, f.query(t, `query($before:String!){recentMessages(agentId:"main",beforeId:$before){id}}`, map[string]any{"before": beforeID}))["recentMessages"].([]any)
	if len(older) != 5 {
		t.Fatalf("pagination cursor lost messages: %v", older)
	}
	questions := data["questions"].([]any)
	if len(questions) != 1 || questions[0].(map[string]any)["status"] != "open" {
		t.Fatalf("pending question lost beyond first page: %v", questions)
	}
	for field, selection := range map[string]string{"approvals": "id", "notes": "id", "usageSummary": "requestCount"} {
		response := f.query(t, `query{conversationBootstrap(projectId:"project"){`+field+`{`+selection+`}}}`, nil)
		if response["errors"] == nil {
			t.Fatalf("heavy detail collection exposed in bootstrap: %s", field)
		}
	}
}

func TestConversationBootstrapOnlyBoundedActiveJobMetadataAndAllOpenQuestions(t *testing.T) {
	f := bootstrapFixture(t)
	saveBootstrapResource(t, f, "project", "project", "", map[string]any{"name": "Project"})
	saveBootstrapResource(t, f, "agent", "main", "project", map[string]any{"projectId": "project", "role": "main"})
	for index := 0; index < 105; index++ {
		id := fmt.Sprintf("job-%03d", index)
		saveBootstrapResource(t, f, "job", id, "main", map[string]any{
			"agentId": "main", "command": "test command", "status": "running",
			"timeoutMs": 5000, "output": "PRIVATE OUTPUT MUST NOT BE LOADED",
		})
		saveBootstrapResource(t, f, "question", fmt.Sprintf("open-%03d", index), "main", map[string]any{
			"agentId": "main", "status": "open", "title": "Pending question",
		})
	}
	for _, item := range []struct{ id, parent, status string }{
		{"completed", "main", "completed"}, {"foreign", "other", "running"},
	} {
		saveBootstrapResource(t, f, "job", item.id, item.parent, map[string]any{
			"agentId": item.parent, "status": item.status, "output": "PRIVATE OUTPUT MUST NOT BE LOADED",
		})
	}
	response := f.query(t, `query{conversationBootstrap(projectId:"project"){
	 jobs{id command status output timeoutMs} questions{id status}
	}}`, nil)
	data := requireData(t, response)["conversationBootstrap"].(map[string]any)
	jobs := data["jobs"].([]any)
	if len(jobs) != 100 {
		t.Fatalf("active metadata cap failed: %d", len(jobs))
	}
	for _, row := range jobs {
		job := row.(map[string]any)
		if job["status"] != "running" || job["output"] != "" || job["timeoutMs"] != float64(5000) {
			t.Fatalf("non-active job, output blob or missing timeout metadata: %v", job)
		}
	}
	if len(data["questions"].([]any)) != 105 {
		t.Fatal("open questions silently truncated")
	}
	if strings.Contains(fmt.Sprint(response), "PRIVATE OUTPUT MUST NOT BE LOADED") {
		t.Fatal("job output leaked through bootstrap")
	}
}
