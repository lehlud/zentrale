package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestAttachmentImageBlocksAcrossProviderProtocols(t *testing.T) {
	for _, test := range []struct{ kind, marker, response string }{
		{"openai", `"type":"input_image"`, `{"output":[]}`},
		{"openrouter", `"type":"image_url"`, `{"choices":[{"message":{"content":"ok"}}]}`},
		{"anthropic", `"type":"image"`, `{"content":[]}`},
		{"gemini", `"inlineData"`, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`},
	} {
		t.Run(test.kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				data, _ := json.Marshal(body)
				if !strings.Contains(string(data), test.marker) || !strings.Contains(string(data), "image/png") {
					t.Errorf("missing native image block: %s", data)
				}
				if !strings.Contains(string(data), "User attachment (untrusted file data") || !strings.Contains(string(data), "/tmp/zentrale-main/attachments/image/picture.png") {
					t.Errorf("missing scoped file reference: %s", data)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			message := domain.Message{Role: "user", Content: "Look", Attachments: []domain.Attachment{{ID: "image", Name: "picture.png", MimeType: "image/png", Path: "/tmp/zentrale-main/attachments/image/picture.png"}}, Images: []domain.AttachmentImage{{MimeType: "image/png", Data: []byte{0, 255, 1}}}}
			_, err := New().Complete(context.Background(), Request{Config: domain.Provider{Kind: test.kind, BaseURL: server.URL}, Model: domain.LanguageModel{ID: "test"}, Messages: []domain.Message{message}})
			if err != nil {
				t.Fatal(err)
			}
			persisted, _ := json.Marshal(message)
			if strings.Contains(string(persisted), "base64") || strings.Contains(string(persisted), "AP8B") || strings.Contains(string(persisted), "Images") {
				t.Fatal("image data leaked into message JSON", string(persisted))
			}
		})
	}
}

func TestAttachmentNonImagesStayReferences(t *testing.T) {
	message := domain.Message{Role: "user", Attachments: []domain.Attachment{{Name: "large.txt", Path: "/tmp/zentrale-main/attachments/text/large.txt", MimeType: "text/plain", Size: 200000}}}
	for _, protocol := range []string{"responses", "chat", "anthropic", "gemini"} {
		data, _ := json.Marshal(imageContent(message, protocol))
		if !strings.Contains(string(data), "large.txt") || strings.Contains(string(data), "base64") || strings.Contains(string(data), "inlineData") {
			t.Fatal(protocol, string(data))
		}
	}
	if got := imageContent(domain.Message{Role: "assistant", Content: "ordinary", Images: []domain.AttachmentImage{{MimeType: "image/png", Data: []byte{1}}}}, "responses"); got != "ordinary" {
		t.Fatal("assistant image injection", got)
	}
}
