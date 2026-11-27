package webapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestShortChatKnowledgeAccessAndSearch(t *testing.T) {
	admin := newAPIFixture(t)
	requireData(t, admin.query(t, `mutation {setup(username:"owner",password:"correct test password",commitIdentity:{name:"Owner",email:"owner@example.org"})}`, nil))
	requireData(t, admin.query(t, `mutation {saveUser(input:{username:"reader",role:"user"},password:"reader test password"){id}}`, nil))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	reader := *admin
	reader.client = &http.Client{Jar: jar}
	requireData(t, reader.query(t, `mutation {login(username:"reader",password:"reader test password")}`, nil))

	const prompt = "Use the company terminology."
	if data := requireData(t, admin.query(t, `mutation($prompt:String!){saveShortChatSystemPrompt(prompt:$prompt)}`, map[string]any{"prompt": prompt})); data["saveShortChatSystemPrompt"] != true {
		t.Fatal(data)
	}
	if data := requireData(t, admin.query(t, `{shortChatSystemPrompt}`, nil)); data["shortChatSystemPrompt"] != prompt {
		t.Fatal(data)
	}
	if reply := reader.query(t, `{shortChatSystemPrompt shortChatDocuments {id}}`, nil); reply["errors"] == nil {
		t.Fatal("regular user could manage short-chat settings")
	}

	const content = "# Betriebsanleitung\nZum Öffnen des Leistungsverzeichnisses den Sonderknopf drücken."
	upload := func(client *http.Client, want int) {
		t.Helper()
		response, err := client.Post(admin.server.URL+"/short-chat-knowledge?name=Betrieb.md", "text/markdown", strings.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			message, _ := io.ReadAll(response.Body)
			t.Fatalf("upload HTTP %d (want %d): %s", response.StatusCode, want, message)
		}
	}
	upload(reader.client, http.StatusForbidden)
	upload(admin.client, http.StatusCreated)
	documents := requireData(t, admin.query(t, `{shortChatDocuments {id name size}}`, nil))["shortChatDocuments"].([]any)
	if len(documents) != 1 {
		t.Fatal(documents)
	}
	id := documents[0].(map[string]any)["id"].(string)
	checkDownload := func(client *http.Client, want int) {
		t.Helper()
		response, err := client.Get(admin.server.URL + "/short-chat-knowledge/" + url.PathEscape(id))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("download HTTP %d (want %d)", response.StatusCode, want)
		}
		if want == http.StatusOK {
			data, err := io.ReadAll(response.Body)
			if err != nil || string(data) != content || response.Header.Get("Content-Disposition") != `attachment; filename=Betrieb.md` {
				t.Fatalf("unexpected download %v: %q (%v)", response.Header, data, err)
			}
		}
	}
	checkDownload(reader.client, http.StatusOK)
	checkDownload(&http.Client{}, http.StatusUnauthorized)

	ctx := context.Background()
	store := admin.engine.Store
	matches, err := store.SearchShortChatDocuments(ctx, "Öffnen Leistungsverzeichnis")
	if err != nil || len(matches) != 1 || matches[0].DocumentID != id || matches[0].Name != "Betrieb.md" {
		t.Fatalf("unexpected search hits %+v, %v", matches, err)
	}
	passage, err := store.ShortChatPassage(ctx, id, matches[0].Section)
	if err != nil || passage.Text != content {
		t.Fatalf("unexpected passage %+v, %v", passage, err)
	}
	deleteQuery := `mutation($id:String!){deleteShortChatDocument(id:$id)}`
	variables := map[string]any{"id": id}
	if reply := reader.query(t, deleteQuery, variables); reply["errors"] == nil {
		t.Fatal("regular user could delete a reference")
	}
	requireData(t, admin.query(t, deleteQuery, variables))
	checkDownload(admin.client, http.StatusNotFound)
	matches, err = store.SearchShortChatDocuments(ctx, "Sonderknopf")
	if err != nil || len(matches) != 0 {
		t.Fatalf("deleted reference remains searchable: %+v, %v", matches, err)
	}
}

func TestShortChatKnowledgeGAEB(t *testing.T) {
	path := os.Getenv("ZENTRALE_TEST_GAEB_PDF")
	if path == "" {
		t.Skip("set ZENTRALE_TEST_GAEB_PDF to a local copy of Das Freie GAEB Buch")
	}
	fixture := newAPIFixture(t)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	doc, err := fixture.engine.AddShortChatDocument(ctx, "Das Freie GAEB Buch.pdf", bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	_, recovered, err := fixture.engine.Store.ShortChatDocument(ctx, doc.ID)
	if err != nil || !bytes.Equal(recovered, source) {
		t.Fatal("original PDF was changed or missing", err)
	}
	matches, err := fixture.engine.Store.SearchShortChatDocuments(ctx, "GAEB Datenaustauschphasen")
	if err != nil || len(matches) == 0 {
		t.Fatalf("no matching PDF excerpts: %v", err)
	}
	for _, match := range matches {
		if match.Page > 0 && strings.Contains(strings.ToLower(match.Text), "datenaustauschphasen") {
			t.Logf("found the requested term on PDF page %d (section %d)", match.Page, match.Section)
			return
		}
	}
	t.Fatal("search returned no page with the requested term")
}
