package webapi

import (
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestShortChatDeliveredFileDownloadIsPrivateAndForced(t *testing.T) {
	owner := newAPIFixture(t)
	requireData(t, owner.query(t, `mutation {setup(username:"alice",password:"correct test password",commitIdentity:{name:"Alice",email:"alice@example.org"})}`, nil))
	requireData(t, owner.query(t, `mutation {saveUser(input:{username:"bob",role:"user"},password:"bob test password"){id}}`, nil))

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	outsider := *owner
	outsider.client = &http.Client{Jar: jar}
	requireData(t, outsider.query(t, `mutation {login(username:"bob",password:"bob test password")}`, nil))
	chatID := requireData(t, owner.query(t, `mutation {createShortChat{id}}`, nil))["createShortChat"].(map[string]any)["id"].(string)
	siblingID := requireData(t, owner.query(t, `mutation {createShortChat{id}}`, nil))["createShortChat"].(map[string]any)["id"].(string)

	const content = "<!DOCTYPE html><title>generated report</title>"
	file := domain.Attachment{
		ID: domain.DeliveredAttachmentPrefix + "report", AgentID: chatID,
		Name: "résumé.html", MimeType: "text/html", Size: int64(len(content)),
	}
	if err := owner.engine.Store.SaveAttachment(file, []byte(content)); err != nil {
		t.Fatal(err)
	}

	get := func(client *http.Client, path string, status int) (http.Header, string) {
		t.Helper()
		response, err := client.Get(owner.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("GET %s: status=%d (want %d), body=%q, error=%v", path, response.StatusCode, status, body, err)
		}
		return response.Header, string(body)
	}

	download := file.Delivery().DownloadURL
	headers, data := get(owner.client, download, http.StatusOK)
	disposition, arguments, err := mime.ParseMediaType(headers.Get("Content-Disposition"))
	if err != nil || disposition != "attachment" || arguments["filename"] != file.Name || data != content ||
		headers.Get("Content-Type") != "application/octet-stream" || headers.Get("X-Content-Type-Options") != "nosniff" ||
		headers.Get("Cache-Control") != "private, no-store" || !strings.Contains(headers.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("unsafe or incorrect forced download response: %v, data=%q, error=%v", headers, data, err)
	}

	get(outsider.client, download, http.StatusNotFound)
	get(owner.client, strings.Replace(download, chatID, siblingID, 1), http.StatusNotFound)
	get(owner.client, strings.TrimSuffix(download, "?download=1"), http.StatusNotFound)

	input := strings.ReplaceAll(strings.ReplaceAll(download, file.ID, "workbook"), "?download=1", "?name=workbook.xlsx")
	upload, err := owner.client.Post(owner.server.URL+input, "application/octet-stream", strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	upload.Body.Close()
	if upload.StatusCode != http.StatusOK {
		t.Fatalf("upload workbook: HTTP %d", upload.StatusCode)
	}
	input = strings.Replace(input, "?name=workbook.xlsx", "?download=1", 1)
	_, uploadedData := get(owner.client, input, http.StatusOK)
	if uploadedData != content {
		t.Fatalf("uploaded attachment download changed bytes: %q", uploadedData)
	}
	get(outsider.client, input, http.StatusNotFound)

	response, err := owner.client.Post(owner.server.URL+strings.Replace(download, "?download=1", "?name=report.html", 1), "text/html", strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("ordinary upload could claim a delivery ID: HTTP %d", response.StatusCode)
	}
}
