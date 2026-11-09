package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

type echoProvider struct{}

func (echoProvider) Complete(_ context.Context, request providers.Request) (providers.Result, error) {
	if strings.Contains(request.System, "goal_completion_review") {
		return providers.Result{Text: `{"status":"complete","reason":"Fixture finished","evidence":"The scripted response matches the fixture request"}`, UsageKnown: true}, nil
	}
	return providers.Result{
		Text:         "Completed: " + request.Messages[len(request.Messages)-1].Content,
		UsageKnown:   true,
		InputTokens:  20,
		OutputTokens: 10,
	}, nil
}

type apiFixture struct {
	server *httptest.Server
	client *http.Client
	engine *agent.Engine
	ssh    *testssh.Server
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := auth.New(store, "")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := agent.New(store, echoProvider{})
	if err != nil {
		t.Fatal(err)
	}
	api := &Server{Store: store, Auth: authentication, Engine: engine, Frontend: t.TempDir()}
	handler, err := api.Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	sshServer, err := testssh.Start()
	if err != nil {
		t.Fatal(err)
	}
	public, err := remote.DecodePublicKey(sshServer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}
	fixture := &apiFixture{server: server, client: &http.Client{Jar: jar}, engine: engine, ssh: sshServer}
	t.Cleanup(func() { server.Close(); engine.Close(); sshServer.Close(); _ = store.Close() })
	return fixture
}
func (fixture *apiFixture) query(t *testing.T, query string, variables map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	response, err := fixture.client.Post(fixture.server.URL+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
func requireData(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	if result["errors"] != nil {
		t.Fatalf("GraphQL errors: %v", result["errors"])
	}
	data, ok := result["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing GraphQL data: %v", result)
	}
	return data
}

func TestCompleteAuthenticatedResourceAndAgentFlow(t *testing.T) {
	fixture := newAPIFixture(t)
	status := requireData(t, fixture.query(t, `query{status{needsSetup authenticated}}`, nil))["status"].(map[string]any)
	if status["needsSetup"] != true || status["authenticated"] != false {
		t.Fatal(status)
	}
	if fixture.query(t, `query{projects{id}}`, nil)["errors"] == nil {
		t.Fatal("resource access before authentication")
	}
	requireData(t, fixture.query(t, `mutation($password:String!){setup(password:$password,commitIdentity:{name:"Test Agent",email:"agent@example.test"})}`, map[string]any{"password": "correct test password"}))
	key := requireData(t, fixture.query(t, `mutation{generateSSHKey{id publicKey}}`, nil))["generateSSHKey"].(map[string]any)
	if !strings.HasPrefix(key["publicKey"].(string), "ecdsa-sha2-nistp521 ") {
		t.Fatal("unexpected SSH public key")
	}
	machine := requireData(t, fixture.query(t, `mutation($input:MachineInput!){saveMachine(input:$input){id name jumps properties}}`, map[string]any{
		"input": map[string]any{
			"name": "Remote",
			"host": fixture.ssh.Host, "port": fixture.ssh.Port,
			"user":    "developer",
			"keyId":   key["id"],
			"sandbox": "none",
		},
	}))["saveMachine"].(map[string]any)
	if machine["jumps"] == nil || machine["properties"] == nil {
		t.Fatal("nil lists leaked through non-null schema")
	}
	project := requireData(t, fixture.query(t, `mutation($input:ProjectInput!){saveProject(input:$input){id}}`, map[string]any{
		"input": map[string]any{"name": "Project", "directory": "/srv/project", "machineIds": []any{machine["id"]}},
	}))["saveProject"].(map[string]any)
	provider := requireData(t, fixture.query(t, `mutation($input:ProviderInput!){saveProvider(input:$input){id}}`, map[string]any{
		"input": map[string]any{"name": "Provider", "kind": "openai", "authMode": "device"},
	}))["saveProvider"].(map[string]any)
	if fixture.query(t, `query{providers{credential}}`, nil)["errors"] == nil {
		t.Fatal("provider credential exposed in output schema")
	}
	choice := domain.ModelChoice{ProviderID: provider["id"].(string), ModelID: "test-model"}
	model := domain.LanguageModel{ID: choice.ModelID, ProviderID: choice.ProviderID, Name: "Test", ContextWindow: 128000}
	if err := fixture.engine.Store.Save("model", choice.ProviderID+":"+choice.ModelID, choice.ProviderID, model); err != nil {
		t.Fatal(err)
	}
	// Authentication is isolated in providerlogin's protocol tests; this fixture
	// exercises agent persistence and GraphQL without contacting a real account.
	if err := fixture.engine.Store.Patch("provider", choice.ProviderID, map[string]any{"authMode": "api"}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.engine.SaveModelDefaults(domain.ModelDefaults{Main: choice, Approver: choice}); err != nil {
		t.Fatal(err)
	}
	created := requireData(t, fixture.query(t, `mutation($projectId:String!,$name:String!){createAgent(projectId:$projectId,name:$name){id approverId}}`, map[string]any{
		"projectId": project["id"],
		"name":      "Main",
	}))["createAgent"].(map[string]any)
	headers := http.Header{}
	parsedURL, _ := http.NewRequest("GET", fixture.server.URL, nil)
	for _, cookie := range fixture.client.Jar.Cookies(parsedURL.URL) {
		headers.Add("Cookie", cookie.String())
	}
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(fixture.server.URL, "http")+"/events", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	submit := `mutation($agentId:String!,$id:String!,$text:String!,$mode:String!){submitInput(agentId:$agentId,id:$id,text:$text,mode:$mode){id status}}`
	variables := map[string]any{"agentId": created["id"], "id": "http-receipt", "text": "Hello", "mode": "steer"}
	for range 2 {
		receipt := requireData(t, fixture.query(t, submit, variables))["submitInput"].(map[string]any)
		if receipt["id"] != "http-receipt" {
			t.Fatal(receipt)
		}
	}
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	completed := false
	for !completed {
		var event struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if err := connection.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		completed = event.Type == "goal_review" && strings.Contains(event.Data, `"status":"complete"`)
	}
	data := requireData(t, fixture.query(t, `query($id:String!){messages(agentId:$id){role content} usage(agentId:$id){inputTokens outputTokens} agentInputs(agentId:$id){id status} agentRunState(agentId:$id){phase hostKey{publicKey}} goal(agentId:$id){id objective origin status} goalReviews(agentId:$id){id status evidence}}`, map[string]any{"id": created["id"]}))
	if len(data["messages"].([]any)) != 2 || len(data["usage"].([]any)) != 1 {
		t.Fatal(data)
	}
	if len(data["agentInputs"].([]any)) != 0 {
		t.Fatal("completed HTTP receipt remained pending", data)
	}
	if data["goal"].(map[string]any)["status"] != "complete" || len(data["goalReviews"].([]any)) != 1 {
		t.Fatal("completion was not independently verified", data)
	}
	requireData(t, fixture.query(t, `mutation{logout}`, nil))
	if fixture.query(t, `query{projects{id}}`, nil)["errors"] == nil {
		t.Fatal("logout did not revoke access")
	}
	if fixture.query(t, `query{goal(agentId:"main"){id}}`, nil)["errors"] == nil {
		t.Fatal("goal access survived logout")
	}
}

func TestSetupRequiresCommitIdentityAndProtectsGlobalDefaults(t *testing.T) {
	fixture := newAPIFixture(t)
	if fixture.query(t, `query{commitIdentity{name email}}`, nil)["errors"] == nil {
		t.Fatal("unauthenticated identity read")
	}
	if fixture.query(t, `mutation{setup(password:"correct test password")}`, nil)["errors"] == nil {
		t.Fatal("setup accepted a missing commit identity")
	}
	if fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Team",email:"invalid"})}`, nil)["errors"] == nil {
		t.Fatal("setup accepted an invalid identity")
	}
	status := requireData(t, fixture.query(t, `query{status{needsSetup}}`, nil))["status"].(map[string]any)
	if status["needsSetup"] != true {
		t.Fatal("invalid identity consumed setup")
	}
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"First Team",email:"first@example.org"})}`, nil))
	identity := requireData(t, fixture.query(t, `query{commitIdentity{name email}}`, nil))["commitIdentity"].(map[string]any)
	if identity["name"] != "First Team" || identity["email"] != "first@example.org" {
		t.Fatal(identity)
	}
	requireData(t, fixture.query(t, `mutation{setCommitIdentity(input:{name:"Updated Team",email:"updated@example.org"})}`, nil))
	if fixture.query(t, `mutation{setCommitIdentity(input:{name:"Incomplete"})}`, nil)["errors"] == nil {
		t.Fatal("invalid default update accepted")
	}
	identity = requireData(t, fixture.query(t, `query{commitIdentity{name email}}`, nil))["commitIdentity"].(map[string]any)
	if identity["name"] != "Updated Team" || identity["email"] != "updated@example.org" {
		t.Fatal(identity)
	}
}

func TestOriginsContentTypesAndReadOnlyMCP(t *testing.T) {
	fixture := newAPIFixture(t)
	body := []byte(`{"query":"mutation{setup(password:\"correct test password\",commitIdentity:{name:\"Test Agent\",email:\"agent@example.test\"})}"}`)
	request, err := http.NewRequest("POST", fixture.server.URL+"/graphql", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://attacker.example")
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin setup: %d", response.StatusCode)
	}
	response, err = fixture.client.Post(fixture.server.URL+"/graphql", "text/plain", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatal("non-JSON request accepted")
	}
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test Agent",email:"agent@example.test"})}`, nil))
	response, err = fixture.client.Post(fixture.server.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || !strings.Contains(string(data), "Read-only") || strings.Contains(string(data), "exec") {
		t.Fatalf("MCP tool surface: %s %v", data, err)
	}
}

func TestDepthChecksIncludeFragmentsAliasesAndCycles(t *testing.T) {
	for _, query := range []string{
		`query{projects{a{b{c{d}}}}}`,
		`query{projects{...Nested}} fragment Nested on Project {a{b{c{d}}}}`,
		`query{projects{...Cycle}} fragment Cycle on Project {...Cycle}`,
	} {
		if err := ValidateQuery(query, "", true); err == nil {
			t.Fatalf("unsafe query accepted: %s", query)
		}
	}
	if err := ValidateQuery(`query{pools{machines{properties}}}`, "", true); err != nil {
		t.Fatal(err)
	}
	aliases := "query{"
	for range 21 {
		aliases += "status{authenticated} "
	}
	if err := ValidateQuery(aliases+"}", "", true); err == nil {
		t.Fatal("root field budget not enforced")
	}
	if err := ValidateQuery(`query{...Fields} fragment Fields on Query {projects{id}}`, "", false); err == nil {
		t.Fatal("fragment bypassed authentication")
	}
}

func TestProviderDiscoveryDefaultsMetadataAndAgentModelSnapshots(t *testing.T) {
	fixture := newAPIFixture(t)
	catalog := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/models" {
			t.Error("catalog was not discovered from the account")
		}
		if request.Header.Get("Authorization") != "Bearer account-secret" {
			t.Error("catalog request lost account credential")
		}
		_, _ = writer.Write([]byte(`{"data":[{"id":"main-model","context_length":128000},{"id":"approver-model","context_length":64000},{"id":"text-embedding-model"}]}`))
	}))
	defer catalog.Close()
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test Agent",email:"agent@example.test"})}`, nil))
	result := requireData(t, fixture.query(t, `mutation($input:ProviderInput!){saveProvider(input:$input){id}}`, map[string]any{
		"input": map[string]any{"id": "account", "name": "Account", "kind": "openai", "baseUrl": catalog.URL, "credential": "account-secret"},
	}))
	if result["saveProvider"] == nil {
		t.Fatal("provider required a model during configuration")
	}
	requireData(t, fixture.query(t, `mutation{refreshModels(providerId:"account")}`, nil))
	models := requireData(t, fixture.query(t, `query{models(providerId:"account"){id} providers{connected modelCount}}`, nil))
	if len(models["models"].([]any)) != 2 {
		t.Fatalf("non-language models leaked into catalog: %+v", models)
	}
	if fixture.query(t, `query{providers{oauth}}`, nil)["errors"] == nil {
		t.Fatal("OAuth token schema exposed")
	}
	requireData(t, fixture.query(t, `mutation{setModelDefaults(input:{main:{providerId:"account",modelId:"main-model"},approver:{providerId:"account",modelId:"approver-model"}})}`, nil))
	if fixture.query(t, `mutation{setModelDefaults(input:{main:{providerId:"account",modelId:"invented"},approver:{providerId:"account",modelId:"approver-model"}})}`, nil)["errors"] == nil {
		t.Fatal("unavailable model accepted")
	}
	if err := fixture.engine.Store.Save("project", "project", "", domain.Project{ID: "project", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	created := requireData(t, fixture.query(t, `mutation{createAgent(projectId:"project",name:"Agent"){id modelId approverId}}`, nil))["createAgent"].(map[string]any)
	if created["modelId"] != "main-model" {
		t.Fatal(created)
	}
	var approver domain.Agent
	if err := fixture.engine.Store.Load("agent", created["approverId"].(string), &approver); err != nil {
		t.Fatal(err)
	}
	if approver.ModelID != "approver-model" {
		t.Fatal("global approver default ignored")
	}
	requireData(t, fixture.query(t, `mutation{setModelDefaults(input:{main:{providerId:"account",modelId:"approver-model"},approver:{providerId:"account",modelId:"main-model"}})}`, nil))
	current := requireData(t, fixture.query(t, `query($id:String!){agent(id:$id){modelId}}`, map[string]any{"id": created["id"]}))["agent"].(map[string]any)
	if current["modelId"] != "main-model" {
		t.Fatal("changing defaults rewrote a running conversation's model")
	}
	requireData(t, fixture.query(t, `mutation{saveModelMetadata(providerId:"account",modelId:"main-model",input:{contextWindow:200000,inputPrice:2,outputPrice:10,cachedPrice:0.2,pricesKnown:true})}`, nil))
	requireData(t, fixture.query(t, `mutation{refreshModels(providerId:"account")}`, nil))
	metadata := requireData(t, fixture.query(t, `query{model(providerId:"account",modelId:"main-model"){contextWindow inputPrice metadataEstimated}}`, nil))["model"].(map[string]any)
	if metadata["contextWindow"] != float64(200000) || metadata["inputPrice"] != float64(2) || metadata["metadataEstimated"] != true {
		t.Fatalf("explicit metadata lost on refresh: %+v", metadata)
	}
	requireData(t, fixture.query(t, `mutation{disconnectProvider(providerId:"account")}`, nil))
	models = requireData(t, fixture.query(t, `query{models(providerId:"account"){id} providers{connected modelCount}}`, nil))
	if len(models["models"].([]any)) != 0 {
		t.Fatal("disconnected account still offers stale models")
	}
}
