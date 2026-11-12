package providerlogin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestDeviceProtocolRetryClassification(t *testing.T) {
	for _, test := range []struct {
		name          string
		status        int
		body          string
		retry, slower bool
	}{
		{"pending", 400, `{"error":"authorization_pending"}`, false, false},
		{"slow", 400, `{"error":"slow_down"}`, false, true},
		{"rate", 429, `{}`, true, false},
		{"upstream", 503, `{}`, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := New()
			client.OpenAIAuthURL = server.URL
			_, pending, err := client.Poll(context.Background(), Transaction{Session: domain.ProviderLogin{ExpiresAt: time.Now().Add(time.Minute)}})
			var retryable *RetryablePollError
			var slower *SlowDownPollError
			if errors.As(err, &retryable) != test.retry || errors.As(err, &slower) != test.slower {
				t.Fatal(pending, err)
			}
			if !test.retry && !pending {
				t.Fatal("pending response lost")
			}
		})
	}
}
