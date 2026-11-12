package webapi

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const bootstrapTestQuery = `query($projectId:String!, $agentId:String) {
 conversationBootstrap(projectId:$projectId,agentId:$agentId) {
  project{id name} agents{id role summary} agent{id role projectId parentId}
  messages{id role content attachments{id name path} activities{name} changes{id path}}
  questions{id status title} agentInputs{id status}
 }
}`

func bootstrapFixture(t *testing.T) *apiFixture {
	t.Helper()
	f := newAPIFixture(t)
	requireData(t, f.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.test"})}`, nil))
	return f
}

func saveBootstrapResource(t *testing.T, f *apiFixture, kind, id, parent string, value map[string]any) {
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
