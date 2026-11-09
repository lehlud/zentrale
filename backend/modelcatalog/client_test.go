package modelcatalog

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestCatalogPaginationAuthenticationAndGenerationFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("x-goog-api-key") != "secret" {
			t.Error("missing account credential")
		}
		if request.URL.Query().Get("pageToken") == "next page" {
			_, _ = writer.Write([]byte(`{"models":[{"name":"models/chat-two","displayName":"Second model","inputTokenLimit":100000,"supportedGenerationMethods":["generateContent"]}]}`))
		} else {
			_, _ = writer.Write([]byte(`{"models":[{"name":"models/embedding","supportedGenerationMethods":["embedContent"]},{"name":"models/chat-one","displayName":"First model","inputTokenLimit":32000,"supportedGenerationMethods":["generateContent"]}],"nextPageToken":"next page"}`))
		}
	}))
	defer server.Close()
	models, err := New().List(context.Background(), domain.Provider{ID: "account", Kind: "gemini", BaseURL: server.URL, Credential: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "chat-one" || models[1].ContextWindow != 100000 {
		t.Fatalf("models: %+v", models)
	}
}

func TestNativeAccountCatalogAndPrices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" || request.Header.Get("ChatGPT-Account-Id") != "account-one" {
			t.Error("native account headers missing")
		}
		if request.URL.Query().Get("client_version") == "" {
			t.Error("missing native catalog version")
		}
		_, _ = writer.Write([]byte(`{"models":[{"slug":"native-model","display_name":"Native Model","context_window":128000},{"slug":"hidden","visibility":"hidden"}]}`))
	}))
	defer server.Close()
	models, err := New().List(context.Background(), domain.Provider{ID: "account", Kind: "openai", AuthMode: "device", BaseURL: server.URL, OAuth: &domain.OAuthTokens{AccessToken: "access", AccountID: "account-one"}})
	if err != nil || len(models) != 1 || models[0].ID != "native-model" {
		t.Fatalf("native models: %+v %v", models, err)
	}
	priced, include := decodeModel(domain.Provider{ID: "account"}, map[string]any{"id": "priced", "context_length": float64(128000), "pricing": map[string]any{"prompt": "0.000002", "completion": "0.00001", "input_cache_read": "0.0000002"}})
	if !include || !priced.PricesKnown || priced.InputPrice != 2 || priced.OutputPrice != 10 || math.Abs(priced.CachedPrice-0.2) > 1e-10 {
		t.Fatalf("token prices: %+v", priced)
	}
	unknown, _ := decodeModel(domain.Provider{}, map[string]any{"id": "unknown"})
	if unknown.PricesKnown || !unknown.MetadataEstimated {
		t.Fatal("missing metadata presented as known")
	}
}

func TestDynamicAndInvalidPricesRemainUnknown(t *testing.T) {
	for _, value := range []string{"-1", "dynamic", "NaN", "+Inf", "1e300"} {
		model, _ := decodeModel(domain.Provider{}, map[string]any{"id": "router", "pricing": map[string]any{"prompt": value, "completion": "0"}})
		if model.PricesKnown || model.InputPrice != 0 {
			t.Fatalf("invalid price %q became a cost: %+v", value, model)
		}
	}
}
