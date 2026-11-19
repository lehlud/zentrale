package huggingface

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
)

func TestSpaceAnonymousRequestIsStoppedWhenServiceCloses(t *testing.T) {
	service := testService(t, nil)
	service.spaceClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		service.Close()
		return nil, request.Context().Err()
	})
	_, err := service.RewriteInSpace(context.Background(), "https://team-writer.hf.space", "rewrite", "one", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the request to be canceled, got %v", err)
	}
}

func TestSpaceBatchUsesOnePrediction(t *testing.T) {
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		t.Fatal("anonymous request unexpectedly used the Hub")
		return nil, nil
	})
	var calls []string
	service.spaceClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls = append(calls, request.Method)
		if request.Method == http.MethodPost {
			var payload struct{ Data [][]string }
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil ||
				len(payload.Data) != 1 || !slices.Equal(payload.Data[0], []string{"one", "two"}) {
				t.Fatalf("unexpected batch payload: %v (%v)", payload, err)
			}
			return testResponse(request, http.StatusOK, `{"event_id":"batch-id"}`), nil
		}
		return testResponse(request, http.StatusOK, "event: complete\ndata: [[\"first\",\"second\"]]\n\n"), nil
	})
	result, err := service.RewriteBatchInSpace(context.Background(), "https://team-writer.hf.space", "rewrite", []string{"one", "two"}, "")
	if err != nil || !slices.Equal(calls, []string{"POST", "GET"}) || !slices.Equal(result, []string{"first", "second"}) {
		t.Fatalf("result = %v, calls = %v, error = %v", result, calls, err)
	}
}

func TestSpaceTextUsesQueueAndTheApprovedAccount(t *testing.T) {
	ctx := context.Background()
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		return testResponse(request, http.StatusOK, `{"type":"user","name":"writer"}`), nil
	})
	if _, err := service.Connect(ctx, testToken); err != nil {
		t.Fatal(err)
	}
	authenticated := false
	calls := 0
	service.spaceClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		token := request.Header.Get("Authorization")
		if request.URL.Host != "team-rewriter.hf.space" || (authenticated && token != "Bearer "+testToken) || (!authenticated && token != "") {
			t.Error("unexpected Space target or account")
		}
		switch request.Method + " " + request.URL.Path {
		case "POST /gradio_api/call/predict":
			var payload struct{ Data []string }
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || len(payload.Data) != 1 || payload.Data[0] != "original" {
				t.Errorf("payload = %+v, error = %v", payload, err)
			}
			return testResponse(request, http.StatusOK, `{"event_id":"unique-123"}`), nil
		case "GET /gradio_api/call/predict/unique-123":
			return testResponse(request, http.StatusOK, "event: heartbeat\n\nevent: generating\ndata: [\"partial\"]\n\nevent: complete\ndata: [\"rewritten\",42]\n\n"), nil
		default:
			t.Errorf("unexpected method or route: %s %s", request.Method, request.URL.Path)
			return testResponse(request, http.StatusBadRequest, ""), nil
		}
	})
	for _, accountName := range []string{"", "writer"} {
		authenticated = accountName != ""
		text, err := service.RewriteInSpace(ctx, "https://team-rewriter.hf.space/", "/predict", "original", accountName)
		if err != nil || text != "rewritten" {
			t.Fatalf("text = %q, error = %v", text, err)
		}
	}
	if _, err := service.RewriteInSpace(ctx, "https://team-rewriter.hf.space", "predict", "original", "other-user"); err == nil || calls != 4 {
		t.Fatalf("used an unreviewed account: %v, calls %d", err, calls)
	}
}

func TestSpaceTargetRejectsCredentialsPathsAndExternalHosts(t *testing.T) {
	for _, target := range []string{
		"http://example.hf.space", "https://example.hf.space.attacker.test",
		"https://name@example.hf.space", "https://example.hf.space:444", "https://example.hf.space/path",
		"https://example.hf.space/?token=other", "https://localhost",
	} {
		if _, _, err := SpaceTarget(target, "predict"); err == nil {
			t.Errorf("invalid Space target accepted: %s", target)
		}
	}
	if _, _, err := SpaceTarget("https://example.hf.space", "../predict"); err == nil {
		t.Fatal("invalid Space endpoint accepted")
	}
}
