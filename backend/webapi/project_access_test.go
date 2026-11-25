package webapi

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func TestMultiuserHTTPAccess(t *testing.T) {
	admin := newAPIFixture(t)
	requireData(t, admin.query(t, `mutation {setup(username:"alice",password:"correct test password",commitIdentity:{name:"Alice",email:"alice@example.org"})}`, nil))
	requireData(t, admin.query(t, `mutation {saveUser(input:{username:"bob",role:"user"},password:"bob test password"){id}}`, nil))
	seedAccessibleProject(t, admin.engine.Store, "private-project", "private-agent")

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	bob := *admin
	bob.client = &http.Client{Jar: jar}
	requireData(t, bob.query(t, `mutation {login(username:"bob",password:"bob test password")}`, nil))

	reject := func(fixture *apiFixture, query string, variables map[string]any) {
		t.Helper()
		if errors, ok := fixture.query(t, query, variables)["errors"].([]any); !ok || len(errors) == 0 {
			t.Fatalf("expected rejection of %s; got %v", query, errors)
		}
	}
	reject(&bob, `{project(id:"private-project"){id}}`, nil)
	reject(&bob, `{agent(id:"private-agent"){id}}`, nil)
	reject(&bob, `{users{id}}`, nil)
	reject(&bob, `mutation {saveProject(input:{id:"bob-project",name:"Bob's project",managedWorkspace:true}){id}}`, nil)

	chat := requireData(t, bob.query(t, `mutation {createShortChat(name:"Bob's chat"){id approverId}}`, nil))["createShortChat"].(map[string]any)
	const chatQuery = `query($id:String!){shortChat(id:$id){id name}}`
	variables := map[string]any{"id": chat["id"]}
	requireData(t, bob.query(t, chatQuery, variables))
	reject(admin, chatQuery, variables)
	reject(admin, `query($id:String!){agent(id:$id){id}}`, variables)
	reject(&bob, `query($id:String!){agent(id:$id){id}}`, map[string]any{"id": chat["approverId"]})
	reject(admin, chatQuery, map[string]any{"id": "private-agent"})

	for _, field := range []string{"goal", "goalHistory", "goalReviews", "teamwork"} {
		query := `query($id:String!){` + field + `(agentId:$id){__typename}}`
		requireData(t, admin.query(t, query, map[string]any{"id": "private-agent"}))
		reject(&bob, query, variables)
	}
	reject(&bob, `mutation($id:String!){cancelGoal(agentId:$id,goalId:"goal")}`, variables)
	reject(&bob, `mutation($id:String!){createAgent(projectId:$id,name:"Child"){id}}`, variables)

	for _, method := range []string{http.MethodHead, http.MethodGet} {
		request, err := http.NewRequest(method, admin.server.URL+"/side/"+chat["id"].(string), nil)
		if err != nil {
			t.Fatal(err)
		}

		response, err := bob.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s short-chat side endpoint returned %d, want 404", method, response.StatusCode)
		}
	}

	const attachment = "private note without a VM"
	path := admin.server.URL + "/attachments/" + chat["id"].(string) + "/chat-file?name=note.txt"
	for _, upload := range []struct {
		client *http.Client
		status int
	}{
		{admin.client, http.StatusNotFound},
		{bob.client, http.StatusOK},
	} {
		response, err := upload.client.Post(path, "text/plain", strings.NewReader(attachment))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != upload.status {
			t.Fatalf("upload got HTTP %d; want %d", response.StatusCode, upload.status)
		}
	}

	_, blob, err := admin.engine.Store.Attachment(chat["id"].(string), "chat-file")
	if err != nil || string(blob) != attachment {
		t.Fatalf("attachment was not buffered on the server: %q, %v", blob, err)
	}

	const membership = `mutation($member:Boolean!){setProjectMember(projectId:"private-project",username:"bob",add:$member)}`
	requireData(t, admin.query(t, membership, map[string]any{"member": true}))
	requireData(t, bob.query(t, `{project(id:"private-project"){id}}`, nil))
	reject(&bob, `mutation {archiveProject(projectId:"private-project")}`, nil)
	requireData(t, admin.query(t, membership, map[string]any{"member": false}))
	reject(&bob, `{agent(id:"private-agent"){id}}`, nil)
}

func seedAccessibleProject(t *testing.T, store *datastore.Instance, projectID string, agentIDs ...string) {
	t.Helper()
	if err := store.Save("project", projectID, "", domain.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	for _, id := range agentIDs {
		if err := store.Save("agent", id, projectID, domain.Agent{ID: id, ProjectID: projectID, Role: "main"}); err != nil {
			t.Fatal(err)
		}
	}
}
