package modelcatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestAccountDeliveredNativeReviewModelVisibility(t *testing.T) {
	provider := domain.Provider{ID: "native-account", Kind: "openai", AuthMode: "device"}
	for _, test := range []struct {
		name   string
		record string
		want   bool
	}{
		{"hidden review", `{"slug":"codex-auto-review","visibility":"hidden"}`, true},
		{"visible review", `{"slug":"codex-auto-review","visibility":"list"}`, true},
		{"other hidden", `{"slug":"another-review-model","visibility":"hidden"}`, false},
		{"similar hidden slug", `{"slug":"codex-auto-review-preview","visibility":"hidden"}`, false},
		{"missing slug", `{"visibility":"hidden"}`, false},
		{"retired review", `{"slug":"codex-auto-review","visibility":"hidden","lifecycle":"retired"}`, false},
		{"nontext review", `{"slug":"codex-auto-review","visibility":"hidden","architecture":{"output_modalities":["image"]}}`, false},
		{"noncompletion review", `{"slug":"codex-auto-review","visibility":"hidden","capabilities":{"completion_chat":false}}`, false},
		{"ordinary native model", `{"slug":"gpt-5.4","display_name":"Normal model","context_window":128000}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var record map[string]any
			if err := json.Unmarshal([]byte(test.record), &record); err != nil {
				t.Fatal(err)
			}
			model, include := decodeModel(provider, record)
			if include != test.want {
				t.Fatalf("included=%v, want %v: %+v", include, test.want, model)
			}
			if include && (model.ID != text(record["slug"]) || model.ProviderID != provider.ID) {
				t.Fatalf("account/model identity changed: %+v", model)
			}
		})
	}
}

func TestCatalogReasoningEffortsValidatedWithoutChangingDefaults(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want []string
	}{
		{"known efforts", `[{"effort":"minimal"},{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"}]`, []string{"minimal", "low", "medium", "high", "xhigh"}},
		{"deduplicated provider order", `[{"effort":"high"},{"effort":"low"},{"effort":"high"}]`, []string{"high", "low"}},
		{"invalid efforts and shapes", `[{"effort":"none"},{"effort":"default"},{"effort":"regular"},{"effort":"auto"},{"effort":"max"},{"effort":"LOW"},{"effort":" low "},{"effort":5},"low",null,{},["low"],{"effort":"medium"}]`, []string{"medium"}},
		{"missing metadata", `null`, nil},
		{"empty metadata", `[]`, nil},
		{"not an array", `{"effort":"low"}`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			var efforts any
			if err := json.Unmarshal([]byte(test.data), &efforts); err != nil {
				t.Fatal(err)
			}
			model, include := decodeModel(domain.Provider{ID: "account", Kind: "openai", AuthMode: "device"}, map[string]any{
				"slug": "codex-auto-review", "visibility": "hidden",
				"supported_reasoning_levels": efforts, "default_reasoning_level": "high",
			})
			if !include || !reflect.DeepEqual(model.ReasoningLevels, test.want) {
				t.Fatalf("levels=%v, want %v; included=%v", model.ReasoningLevels, test.want, include)
			}
		})
	}
	if got := domain.ResolveReasoningLevel("openai", "gpt-5.4", "", "approver"); got != "medium" {
		t.Fatalf("regular approver default changed to %q", got)
	}
	ordinary, include := decodeModel(domain.Provider{ID: "api-account", Kind: "openai", AuthMode: "api"}, map[string]any{
		"id": "gpt-5.4", "context_window": float64(128000),
	})
	if !include || ordinary.ID != "gpt-5.4" || ordinary.ContextWindow != 128000 || len(ordinary.ReasoningLevels) != 0 {
		t.Fatalf("ordinary model without effort metadata changed: %+v", ordinary)
	}
}

func TestNativeCatalogNeverInventsAbsentReviewModel(t *testing.T) {
	for _, present := range []bool{false, true} {
		name := "absent"
		if present {
			name = "account delivered"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer fixture-access" || r.Header.Get("ChatGPT-Account-Id") != "fixture-account" {
					t.Error("catalog endpoint/account scope changed")
				}
				entries := []any{
					map[string]any{"slug": "gpt-5.4", "display_name": "Normal model", "context_window": 128000},
					map[string]any{"slug": "another-hidden-model", "visibility": "hidden"},
				}
				if present {
					entries = append(entries, map[string]any{
						"slug": "codex-auto-review", "visibility": "hidden",
						"supported_reasoning_levels": []any{map[string]any{"effort": "low"}, map[string]any{"effort": "medium"}},
						"default_reasoning_level":    "low",
					})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"models": entries})
			}))
			defer server.Close()
			models, err := New().List(context.Background(), domain.Provider{
				ID: "native-account", Kind: "openai", AuthMode: "device", BaseURL: server.URL,
				OAuth: &domain.OAuthTokens{AccessToken: "fixture-access", AccountID: "fixture-account"},
			})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, model := range models {
				if model.ID == "another-hidden-model" || model.ProviderID != "native-account" {
					t.Fatalf("hidden model/account scope leaked: %+v", model)
				}
				if model.ID == "codex-auto-review" {
					found = true
					if !reflect.DeepEqual(model.ReasoningLevels, []string{"low", "medium"}) {
						t.Fatalf("review effort metadata lost: %+v", model)
					}
				}
			}
			wantCount := 1
			if present {
				wantCount++
			}
			if found != present || len(models) != wantCount {
				t.Fatalf("review present=%v, account returned=%v, models=%+v", found, present, models)
			}
		})
	}
}
