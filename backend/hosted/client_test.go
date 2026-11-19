package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestFalSubmitsOneBatchAndRetriesOnlyResultLookups(t *testing.T) {
	client := New()
	client.pollInterval = time.Millisecond
	var calls []string
	client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "queue.fal.run" || request.Header.Get("Authorization") != "Key example-secret" {
			t.Fatal("unexpected fal destination or key")
		}
		calls = append(calls, request.Method+" "+request.URL.Path)
		if len(calls) == 1 {
			if request.Header.Get("X-Fal-No-Retry") != "1" || request.Header.Get("X-Fal-Request-Start-Timeout") != "120" {
				t.Fatal("fal must bound queue time and disable platform retries for this request")
			}
			return reply(request, `{"request_id":"request-1","status_url":"https://queue.fal.run/canonical/model/requests/request-1/status","response_url":"https://queue.fal.run/canonical/model/requests/request-1"}`), nil
		}
		switch len(calls) {
		case 2:
			response := reply(request, `{}`)
			response.StatusCode = http.StatusServiceUnavailable
			return response, nil
		case 3:
			return reply(request, `{"status":"COMPLETED"}`), nil
		}
		return reply(request, `{"texts":["one","two"]}`), nil
	})
	output, err := client.Run(context.Background(), domain.Provider{Kind: "fal", BaseURL: FalURL, Credential: "example-secret"},
		"owner/model/rewrite", map[string]any{"texts": []string{"a", "b"}}, Timeout)
	want := []string{"POST /owner/model/rewrite", "GET /canonical/model/requests/request-1/status", "GET /canonical/model/requests/request-1/status", "GET /canonical/model/requests/request-1"}
	if err != nil || string(output) != `{"texts":["one","two"]}` || !reflect.DeepEqual(calls, want) {
		t.Fatalf("output = %s, calls = %v, error = %v", output, calls, err)
	}
}

func TestFalRejectsInvalidReceiptAndCancelsOriginalRequest(t *testing.T) {
	for _, statusURL := range []string{"https://untrusted.example/owner/model/requests/request-1/status", "https://queue.fal.run/owner/model/requests/wrong-id/status"} {
		var calls []string
		client := New()
		client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			calls = append(calls, request.Method+" "+request.URL.String())
			if request.Method == http.MethodPost {
				return reply(request, `{"request_id":"request-1","status_url":"`+statusURL+`"}`), nil
			}
			return reply(request, `{}`), nil
		})
		_, err := client.Run(context.Background(), domain.Provider{Kind: "fal", BaseURL: FalURL, Credential: "example-secret"},
			"owner/model/rewrite", map[string]any{}, Timeout)
		want := []string{"POST " + FalURL + "/owner/model/rewrite", "PUT " + FalURL + "/owner/model/requests/request-1/cancel"}
		if err == nil || !reflect.DeepEqual(calls, want) {
			t.Fatalf("unexpected calls for %s: %v, error = %v", statusURL, calls, err)
		}
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
	calls := 0
	client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.String() != "https://test--rewrite.modal.run/run" || request.Method != http.MethodPost {
			t.Fatal(request.URL)
		}
		return reply(request, `{"ok":true}`), nil
	})
	provider := domain.Provider{Kind: "modal", BaseURL: "https://test--rewrite.modal.run/run", Credential: "example-secret"}
	if _, err := client.Run(context.Background(), provider, "https://untrusted.example", map[string]any{}, Timeout); err == nil || calls != 0 {
		t.Fatal("accepted an arbitrary Modal endpoint override")
	}
	if _, err := client.Run(context.Background(), provider, "", map[string]any{"prompt": "text"}, Timeout); err != nil || calls != 1 {
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
