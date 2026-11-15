package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

type googleInferenceTransport struct{ testing *testing.T }

func (transport googleInferenceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != "generativelanguage.googleapis.com" ||
		request.Header.Get("Authorization") != "Bearer private-access" ||
		request.Header.Get("x-goog-user-project") != "api-project" ||
		request.Header.Get("x-goog-api-key") != "" {
		transport.testing.Error("Google inference did not use official-endpoint API OAuth authentication")
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}`)),
	}, nil
}

func TestGoogleOAuthInferenceUsesBearerAndQuotaProject(t *testing.T) {
	client := New()
	client.HTTP = &http.Client{Transport: googleInferenceTransport{testing: t}}
	result, err := client.Complete(context.Background(), Request{
		Config: domain.Provider{
			Kind: "gemini", AuthMode: "oauth", BaseURL: defaults["gemini"], BillingMode: "metered",
			OAuthClientID: "own-app", OAuthProjectID: "api-project",
			OAuth: &domain.OAuthTokens{AccessToken: "private-access"},
		},
		Model:    domain.LanguageModel{ID: "test-model"},
		Messages: []domain.Message{{Role: "user", Content: "Test"}},
	})
	if err != nil || result.Text != "OK" {
		t.Fatal("Google OAuth inference failed", err)
	}
}

func TestGoogleOAuthRejectsCustomEndpointAndSubscriptionBilling(t *testing.T) {
	for _, test := range []struct{ name, endpoint, billing string }{
		{"custom endpoint", "https://other.example/v1beta", "metered"},
		{"consumer subscription", defaults["gemini"], "fixed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configuration := domain.Provider{
				Kind: "gemini", AuthMode: "oauth", BaseURL: test.endpoint, BillingMode: test.billing,
				OAuthClientID: "own-app", OAuthProjectID: "api-project",
			}
			if err := Validate(&configuration); err == nil {
				t.Fatal("unsafe or misleading OAuth configuration accepted")
			}
		})
	}
}
