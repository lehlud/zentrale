package webapi

import (
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/domain"
)

func settingsAccounts(t *testing.T) (*apiFixture, *apiFixture, string) {
	t.Helper()
	admin := newAPIFixture(t)
	requireData(t, admin.query(t, `mutation{setup(username:"alice",password:"alice test password",commitIdentity:{name:"Alice",email:"alice@example.org"})}`, nil))
	user := requireData(t, admin.query(t, `mutation{saveUser(input:{username:"bob",role:"user",canCreateProjects:true},password:"bob test password"){id}}`, nil))["saveUser"].(map[string]any)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	bob := *admin
	bob.client = &http.Client{Jar: jar}
	requireData(t, bob.query(t, `mutation{login(username:"bob",password:"bob test password")}`, nil))
	return admin, &bob, user["id"].(string)
}

func rejectSettings(t *testing.T, fixture *apiFixture, query string, vars map[string]any) {
	t.Helper()
	if result := fixture.query(t, query, vars); result["errors"] == nil {
		t.Fatalf("expected rejection: %s: %v", query, result)
	}
}

func TestPersonalIdentityAndProjectTargetGrants(t *testing.T) {
	admin, bob, bobID := settingsAccounts(t)
	const identityQuery = `{commitIdentity{name email}}`
	if got := requireData(t, bob.query(t, identityQuery, nil))["commitIdentity"].(map[string]any)["name"]; got != "" {
		t.Fatalf("another user's commit identity was returned: %v", got)
	}
	requireData(t, bob.query(t, `mutation{setCommitIdentity(input:{name:"Bob",email:"bob@example.org"})}`, nil))
	if got := requireData(t, admin.query(t, identityQuery, nil))["commitIdentity"].(map[string]any)["name"]; got != "Alice" {
		t.Fatalf("initial identity changed: %v", got)
	}
	if got, err := admin.engine.ProjectCommitIdentity(domain.Project{OwnerID: bobID}); err != nil || got.Name != "Bob" {
		t.Fatalf("project did not use owner's identity: %+v %v", got, err)
	}

	store := admin.engine.Store
	for _, machine := range []domain.Machine{{ID: "allowed", Name: "Allowed", WorkspaceRoot: "/work"}, {ID: "other", Name: "Other", WorkspaceRoot: "/work"}} {
		if err := store.Save("machine", machine.ID, "", machine); err != nil {
			t.Fatal(err)
		}
	}
	const targetsQuery = `{projectTargets{id kind}}`
	if targets := requireData(t, bob.query(t, targetsQuery, nil))["projectTargets"].([]any); len(targets) != 0 {
		t.Fatal("ungranted targets are visible", targets)
	}
	const saveProject = `mutation($id:String!,$machine:String!){saveProject(input:{id:$id,name:"Work",managedWorkspace:true,machineIds:[$machine]}){id}}`
	const grants = `mutation($id:String!,$targets:[String!]!){setProjectTargetGrants(userId:$id,targets:$targets)}`
	requested := map[string]any{"id": bobID, "targets": []string{"machine:allowed"}}
	rejectSettings(t, bob, grants, requested)
	rejectSettings(t, bob, saveProject, map[string]any{"id": "bob-work", "machine": "allowed"})
	requireData(t, admin.query(t, grants, requested))
	targets := requireData(t, bob.query(t, targetsQuery, nil))["projectTargets"].([]any)
	if len(targets) != 1 || targets[0].(map[string]any)["id"] != "allowed" {
		t.Fatal("incorrect grant listing", targets)
	}
	rejectSettings(t, bob, saveProject, map[string]any{"id": "bob-work", "machine": "other"})
	requireData(t, bob.query(t, saveProject, map[string]any{"id": "bob-work", "machine": "allowed"}))
	rejectSettings(t, bob, `mutation{moveProject(projectId:"bob-work",machineId:"other")}`, nil)
	requireData(t, admin.query(t, grants, map[string]any{"id": bobID, "targets": []string{}}))
	rejectSettings(t, bob, saveProject, map[string]any{"id": "bob-new", "machine": "allowed"})
	requireData(t, bob.query(t, saveProject, map[string]any{"id": "bob-work", "machine": "allowed"}))
}

func TestScopedModelSettings(t *testing.T) {
	admin, bob, _ := settingsAccounts(t)
	store := admin.engine.Store
	if err := store.Save("provider", "account", "", domain.Provider{ID: "account", Kind: "openai"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"chat-model", "project-model"} {
		model := domain.LanguageModel{ID: id, ReasoningLevels: []string{"low", "high"}}
		if err := store.Save("model", "account:"+id, "account", model); err != nil {
			t.Fatal(err)
		}
	}
	low := domain.ModelChoice{ProviderID: "account", ModelID: "chat-model", ReasoningLevel: "low"}
	high := low
	high.ReasoningLevel = "high"
	project := domain.ModelChoice{ProviderID: "account", ModelID: "project-model", ReasoningLevel: "low"}
	defaults := domain.ModelDefaults{Main: low, Approver: low}
	const policy = `mutation($scope:String!,$presets:[ModelChoiceInput!]!,$defaults:ModelDefaultsInput!){setModelPresetPolicy(scope:$scope,presets:$presets,defaults:$defaults)}`
	const personal = `mutation($scope:String!,$defaults:ModelDefaultsInput!){setPersonalModelDefaults(scope:$scope,defaults:$defaults)}`
	chatPolicy := map[string]any{"scope": "chat", "presets": []domain.ModelChoice{low, high}, "defaults": defaults}
	rejectSettings(t, bob, policy, chatPolicy)
	requireData(t, admin.query(t, policy, chatPolicy))
	requireData(t, admin.query(t, policy, map[string]any{"scope": "project", "presets": []domain.ModelChoice{project}, "defaults": domain.ModelDefaults{Main: project, Approver: project}}))
	personalHigh := map[string]any{"scope": "chat", "defaults": domain.ModelDefaults{Main: high}}
	rejectSettings(t, bob, personal, personalHigh)
	requireData(t, admin.query(t, personal, personalHigh))
	options := requireData(t, bob.query(t, `{shortChatModelOptions{presets{modelId} defaults{main{reasoningLevel}}}}`, nil))["shortChatModelOptions"].(map[string]any)
	if len(options["presets"].([]any)) != 2 || options["defaults"].(map[string]any)["main"].(map[string]any)["reasoningLevel"] != "low" {
		t.Fatal("draft received incorrect released choices", options)
	}
	const createShort = `mutation($choice:ModelChoiceInput!){createShortChat(choice:$choice){id modelId reasoningLevel}}`
	rejectSettings(t, bob, createShort, map[string]any{"choice": project})
	created := requireData(t, bob.query(t, createShort, map[string]any{"choice": high}))["createShortChat"].(map[string]any)
	if created["reasoningLevel"] != "high" {
		t.Fatal("draft selection was not saved", created)
	}
	if got, err := admin.engine.EffectiveModelDefaults(agent.ChatModels, domain.InitialUserID); err != nil || got.Main != high || got.Approver != low {
		t.Fatalf("incorrect personal override: %+v %v", got, err)
	}
	if got, err := admin.engine.EffectiveModelDefaults(agent.ProjectModels, domain.InitialUserID); err != nil || got.Main != project {
		t.Fatalf("chat preferences changed project defaults: %+v %v", got, err)
	}

	chat := requireData(t, bob.query(t, `mutation{createShortChat(name:"Bob"){id}}`, nil))["createShortChat"].(map[string]any)["id"].(string)
	const available = `query($id:String!){conversationModelPresets(agentId:$id){modelId reasoningLevel} conversationModels(agentId:$id){main{reasoningLevel}}}`
	vars := map[string]any{"id": chat}
	data := requireData(t, bob.query(t, available, vars))
	if len(data["conversationModelPresets"].([]any)) != 2 || data["conversationModels"].(map[string]any)["main"].(map[string]any)["reasoningLevel"] != "low" {
		t.Fatal("chat received incorrect scope or default", data)
	}
	rejectSettings(t, admin, available, vars)
	const choose = `mutation($id:String!,$choice:ModelChoiceInput!){setConversationModel(agentId:$id,role:"main",choice:$choice)}`
	rejectSettings(t, bob, choose, map[string]any{"id": chat, "choice": project})
	wrongLevel := low
	wrongLevel.ReasoningLevel = "xhigh"
	rejectSettings(t, bob, choose, map[string]any{"id": chat, "choice": wrongLevel})
	requireData(t, bob.query(t, choose, map[string]any{"id": chat, "choice": high}))
	requireData(t, admin.query(t, policy, map[string]any{"scope": "chat", "presets": []domain.ModelChoice{low}, "defaults": defaults}))
	if got, err := admin.engine.EffectiveModelDefaults(agent.ChatModels, domain.InitialUserID); err != nil || got.Main != low {
		t.Fatalf("removed override did not fall back: %+v %v", got, err)
	}
	rejectSettings(t, bob, choose, map[string]any{"id": chat, "choice": high})
}
