package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (run transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return run(request)
}

func reply(request *http.Request, data string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(data)), Request: request}
}

func TestReplicateCancelsPredictionAfterCallerStops(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var calls []string
	client := New()
	client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.replicate.com" || request.Header.Get("Authorization") != "Bearer example-secret" {
			t.Fatal("request left the configured provider")
		}
		calls = append(calls, request.Method+" "+request.URL.Path)
		if len(calls) == 1 {
			var payload map[string]json.RawMessage
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["input"] == nil || request.Header.Get("Cancel-After") == "" {
				t.Fatalf("missing input or provider deadline: %s (%v)", payload, err)
			}
			stop()
			return reply(request, `{"id":"prediction-1","status":"processing","urls":{"cancel":"https://untrusted.example"}}`), nil
		}
		if request.Context().Err() != nil {
			t.Fatal("cancellation request inherited the stopped context")
		}
		return reply(request, `{}`), nil
	})
	_, err := client.Run(ctx, domain.Provider{Kind: "replicate", BaseURL: ReplicateURL, Credential: "example-secret"},
		"owner/model", map[string]any{"text": "input"}, Timeout)
	want := []string{"POST /v1/models/owner/model/predictions", "POST /v1/predictions/prediction-1/cancel"}
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, error = %v", calls, err)
	}
}

func TestReplicateRecoversCancellationFromPreviousProcess(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zentrale.sqlite")
	store, err := datastore.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider := domain.Provider{ID: "replicate", Kind: "replicate", BaseURL: ReplicateURL, Credential: "example-secret"}
	if err := store.Save("provider", provider.ID, "", provider); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	oldClient := NewWithPredictionStore(store)
	oldClient.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/v1/models/owner/model/predictions" {
			if request.Header.Get("Cancel-After") != "120s" || request.Header.Get("Prefer") != "" {
				t.Fatal("prediction needs a provider deadline and immediate receipt")
			}
			stop()
			return reply(request, `{"id":"previous-job","status":"starting"}`), nil
		}
		return nil, errors.New("connection was lost during cancellation")
	})
	if _, err := oldClient.Run(ctx, provider, "owner/model", map[string]any{}, Timeout); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled prediction, got %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := datastore.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	current := NewWithPredictionStore(reopened)
	active := pendingPrediction{ID: "current-job", ProviderID: provider.ID, Owner: current.owner, Expires: time.Now().Add(Timeout)}
	if err := reopened.Save(predictionResource, active.ID, provider.ID, active); err != nil {
		t.Fatal(err)
	}
	calls := 0
	current.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.URL.String() != ReplicateURL+"/predictions/previous-job/cancel" ||
			request.Header.Get("Authorization") != "Bearer example-secret" {
			t.Fatal("recovery did not cancel exactly the previous job")
		}
		return reply(request, `{}`), nil
	})
	for range 2 {
		current.RecoverPredictions(context.Background())
	}
	if calls != 1 {
		t.Fatalf("wanted exactly one cancellation of the previous job, got %d", calls)
	}
}

func TestFalRunsSynchronouslyAndDisconnectsOnCancellation(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	calls := 0
	client := New()
	client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.URL.String() != FalURL+"/owner/model/rewrite" ||
			request.Header.Get("Authorization") != "Key example-secret" ||
			request.Header.Get("X-App-Fal-Disable-Fallback") != "true" ||
			request.Header.Get("X-Fal-Request-Start-Timeout") != "120" {
			t.Fatal("unexpected fal synchronous request")
		}
		stop()
		return nil, request.Context().Err()
	})
	_, err := client.Run(ctx, domain.Provider{Kind: "fal", BaseURL: oldFalURL, Credential: "example-secret"},
		"owner/model/rewrite", map[string]any{"texts": []string{"a", "b"}}, Timeout)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestReplicateVerifiesPinnedVersionBeforeSubmission(t *testing.T) {
	version := strings.Repeat("a", 64)
	for _, verified := range []bool{true, false} {
		var calls []string
		client := New()
		client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			calls = append(calls, request.Method+" "+request.URL.Path)
			if request.Method == http.MethodGet {
				if verified {
					return reply(request, `{"id":"`+version+`"}`), nil
				}
				response := reply(request, `{}`)
				response.StatusCode = http.StatusNotFound
				return response, nil
			}
			var payload struct{ Version string }
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Version != version || request.Header.Get("Cancel-After") != "120s" {
				t.Fatalf("unexpected versioned submission: %+v, error %v", payload, err)
			}
			return reply(request, `{"id":"prediction-1","status":"succeeded","output":{"texts":["first"]}}`), nil
		})
		output, err := client.Run(context.Background(), domain.Provider{Kind: "replicate", BaseURL: ReplicateURL, Credential: "example-secret"},
			"owner/model:"+version, map[string]any{}, Timeout)
		want := []string{"GET /v1/models/owner/model/versions/" + version}
		if verified {
			want = append(want, "POST /v1/predictions")
		}
		if (err == nil) != verified || !reflect.DeepEqual(calls, want) || (verified && string(output) != `{"texts":["first"]}`) {
			t.Fatalf("verified %v, output = %s, calls = %v, error = %v", verified, output, calls, err)
		}
	}
}

func TestModalOnlyCallsTheConfiguredWebFunction(t *testing.T) {
	client := New()
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	deadline, _ := ctx.Deadline()
	calls := 0
	client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.String() != "https://test--rewrite.modal.run/run" || request.Method != http.MethodPost {
			t.Fatal(request.URL)
		}
		if request.Header.Get("X-Zentrale-Inference-Deadline") != strconv.FormatInt(deadline.Unix(), 10) {
			t.Fatal("the remote deadline must include the caller's deadline")
		}
		return reply(request, `{"ok":true}`), nil
	})
	provider := domain.Provider{Kind: "modal", BaseURL: "https://test--rewrite.modal.run/run", Credential: "example-secret"}
	if _, err := client.Run(context.Background(), provider, "https://untrusted.example", map[string]any{}, Timeout); err == nil || calls != 0 {
		t.Fatal("accepted an arbitrary Modal endpoint override")
	}
	if _, err := client.Run(ctx, provider, "", map[string]any{"prompt": "text"}, Timeout); err != nil || calls != 1 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestModalUsesDelayedResultWithoutResubmitting(t *testing.T) {
	const endpoint = "https://test--rewrite.modal.run"
	receipt := strings.Repeat("a", 737)
	for _, test := range []struct {
		name     string
		location string
		status   int
		allowed  bool
	}{
		{"valid", endpoint + "/?__modal_attempt_token=" + receipt, http.StatusSeeOther, true},
		{"foreign host", "https://untrusted.example/?__modal_attempt_token=" + receipt, http.StatusSeeOther, false},
		{"different path", endpoint + "/unrelated?__modal_attempt_token=" + receipt, http.StatusSeeOther, false},
		{"different parameter", endpoint + "/?unrelated=" + receipt, http.StatusSeeOther, false},
		{"oversized token", endpoint + "/?__modal_attempt_token=" + strings.Repeat("a", 4097), http.StatusSeeOther, false},
		{"repeat submission", endpoint + "/?__modal_attempt_token=" + receipt, http.StatusTemporaryRedirect, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := New()
			var calls []string
			client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
				calls = append(calls, request.Method)
				if len(calls) == 1 {
					response := reply(request, `{}`)
					response.StatusCode = test.status
					response.Header = http.Header{"Location": {test.location}}
					return response, nil
				}
				if request.URL.String() != test.location || request.ContentLength != 0 || request.Header.Get("Authorization") != "Bearer example-secret" {
					t.Fatal("unexpected Modal result lookup")
				}
				return reply(request, `{"texts":["first"]}`), nil
			})
			output, err := client.Run(context.Background(), domain.Provider{Kind: "modal", BaseURL: endpoint, Credential: "example-secret"},
				"", map[string]any{}, ModalTimeout)
			want := []string{"POST"}
			if test.allowed {
				want = append(want, "GET")
			}
			if (err == nil) != test.allowed || !reflect.DeepEqual(calls, want) || (test.allowed && string(output) != `{"texts":["first"]}`) {
				t.Fatalf("output = %s, calls = %v, error = %v", output, calls, err)
			}
		})
	}
}
