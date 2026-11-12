package webapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/domain"
)

func TestAttachmentAPIAuthOriginScopeBinaryRetryAndRasterPreview(t *testing.T) {
	fixture := newAPIFixture(t)
	request := func(method, path, origin string, data []byte, expected int) []byte {
		t.Helper()
		r, err := http.NewRequest(method, fixture.server.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", "text/html") // Deliberate MIME spoofing.
		response, err := fixture.client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != expected {
			t.Fatalf("%s %s: %d %s (%v)", method, path, response.StatusCode, body, err)
		}
		if expected == 200 && method == http.MethodGet && (response.Header.Get("Content-Type") != "image/png" || response.Header.Get("X-Content-Type-Options") != "nosniff" || response.Header.Get("Cache-Control") != "private, no-store") {
			t.Fatal("unsafe preview headers", response.Header)
		}
		return body
	}
	request("POST", "/attachments/main/id?name=file", "", nil, 401)
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	for _, item := range []struct {
		kind, id, parent string
		value            any
	}{
		{"project", "project", "", domain.Project{ID: "project"}},
		{"agent", "main", "project", domain.Agent{ID: "main", Role: "main", ProjectID: "project"}},
		{"agent", "child", "project", domain.Agent{ID: "child", Role: "subagent", ParentID: "main", ProjectID: "project"}},
		{"agent", "approver", "project", domain.Agent{ID: "approver", Role: "approver", ParentID: "main", ProjectID: "project"}},
		{"agent", "orphan", "missing", domain.Agent{ID: "orphan", Role: "main", ProjectID: "missing"}},
	} {
		if err := fixture.engine.Store.Save(item.kind, item.id, item.parent, item.value); err != nil {
			t.Fatal(err)
		}
	}
	request("POST", "/attachments/main/id?name=file", "https://evil.invalid", nil, 403)
	for _, target := range []string{"child", "approver", "orphan", "missing"} {
		request("POST", "/attachments/"+target+"/id?name=file", "", nil, 403)
	}
	request("POST", "/attachments/main/id?name=..%2Fescape", "", nil, 400)
	request("POST", "/attachments/main/id?name=file", "", bytes.Repeat([]byte{'x'}, agent.MaxAttachmentBytes+1), 413)
	data := []byte("\x89PNG\r\n\x1a\n\x00\xffbinary")
	a := domain.Attachment{ID: "image", AgentID: "main", ProjectID: "project", Name: "picture.png", MimeType: "image/png", Size: int64(len(data)), Path: "/tmp/zentrale-main/attachments/image/picture.png", MachineID: "machine"}
	if err := fixture.engine.Store.SaveAttachment(a, data); err != nil {
		t.Fatal(err)
	}
	var retry domain.Attachment
	if err := json.Unmarshal(request("POST", "/attachments/main/image?name=picture.png", "", data, 200), &retry); err != nil || retry != a {
		t.Fatal(retry, err)
	}
	request("POST", "/attachments/main/image?name=picture.png", "", []byte("changed"), 400)
	request("POST", "/attachments/main/image?name=other.png", "", data, 400)
	if got := request("GET", "/attachments/main/image", "", nil, 200); !bytes.Equal(got, data) {
		t.Fatal("binary preview corrupted", got)
	}
	a.ID, a.MimeType, a.Name = "svg", "image/svg+xml", "evil.svg"
	if err := fixture.engine.Store.SaveAttachment(a, []byte(`<svg onload="alert(1)"/>`)); err != nil {
		t.Fatal(err)
	}
	request("GET", "/attachments/main/svg", "", nil, 404)
	request("GET", "/attachments/child/image", "", nil, 403)
	if response := fixture.query(t, `mutation{submitInput(agentId:"main",id:"foreign",text:"",mode:"queue",attachmentIds:["not-uploaded"]){id}}`, nil); response["errors"] == nil {
		t.Fatal("unknown attachment submitted", response)
	}
	if err := fixture.engine.Store.RemoveProject("project"); err != nil {
		t.Fatal(err)
	}
	request("GET", "/attachments/main/image", "", nil, 403)
	if strings.Contains(string(request("GET", "/attachments/main/image", "", nil, 403)), "base64") {
		t.Fatal("blob leaked into error")
	}
}
