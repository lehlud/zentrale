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

func TestResponsesPhaseAndEncryptedReasoningReplay(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		response.Header().Set("Content-Type", "application/json")

		if requests == 2 {
			replay := encodeWire(body["input"])
			if !strings.Contains(replay, `"phase":"commentary"`) ||
				!strings.Contains(replay, "encrypted-state") {
				t.Error("native continuation metadata lost", replay)
			}

			_, _ = response.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Done."}]}]}`))
		} else {
			_, _ = response.Write([]byte(`{"status":"completed","output":[{"type":"reasoning","encrypted_content":"encrypted-state"},{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Working."}]}]}`))
		}
	}))
	defer server.Close()

	request := Request{
		Config: domain.Provider{
			Kind:    "openai",
			BaseURL: server.URL,
		},
		Model: domain.LanguageModel{
			ID: "test-model",
		},
		Messages: []domain.Message{
			{
				Role:    "user",
				Content: "Work",
			},
		},
	}

	client := New()
	progress, err := client.Complete(context.Background(), request)
	if err != nil || progress.Phase != "commentary" {
		t.Fatal(progress, err)
	}

	request.Messages = append(request.Messages, domain.Message{
		Role:    "assistant",
		Content: progress.Text,
		Wire:    progress.Wire,
	})

	final, err := client.Complete(context.Background(), request)
	if err != nil || final.Phase != "final_answer" {
		t.Fatal(final, err)
	}
}

func TestIncompleteJSONResponseIsNotACompletedAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"unfinished"}]}]}`))
	}))
	defer server.Close()

	_, err := New().Complete(
		context.Background(),
		Request{
			Config: domain.Provider{
				Kind:    "openai",
				BaseURL: server.URL,
			},
			Model: domain.LanguageModel{
				ID: "test-model",
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "did not complete") {
		t.Fatal("incomplete response was accepted", err)
	}
}
