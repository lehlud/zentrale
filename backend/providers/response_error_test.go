package providers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestTerminalResponseErrorsRetainOnlyDiagnosticFields(t *testing.T) {
	for _, fixture := range []struct {
		name, event, code, param, reason, message string
	}{
		{"schema error", `{"type":"error","error":{"code":"invalid_function_parameters","param":"tools","message":"Invalid schema: null is not an array"}}`, "invalid_function_parameters", "tools", "", "Invalid schema"},
		{"flat error", `{"type":"error","code":"invalid_request_error","message":"Unsupported argument"}`, "invalid_request_error", "", "", "Unsupported argument"},
		{"failed response", `{"type":"response.failed","response":{"error":{"code":"server_error","message":"Generation failed"},"output":[{"text":"private unfinished output"}]}}`, "server_error", "", "", "Generation failed"},
		{"incomplete response", `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"},"output":[{"text":"private unfinished output"}]}}`, "", "", "max_output_tokens", ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result, err := readResponseStream(strings.NewReader("data: " + fixture.event + "\n\n"))
			var failure *ResponseError
			if result != nil || !errors.As(err, &failure) {
				t.Fatal(result, err)
			}
			if failure.Code != fixture.code || failure.Param != fixture.param || failure.Reason != fixture.reason || !strings.Contains(failure.Message, fixture.message) {
				t.Fatal(failure)
			}
			if strings.Contains(err.Error(), "private unfinished output") {
				t.Fatal("response content leaked into diagnostic")
			}
		})
	}
}

func TestResponseDiagnosticRedactsCredentialsBeforeTruncationAndDoesNotRetry(t *testing.T) {
	for _, format := range []string{"sse", "json"} {
		t.Run(format, func(t *testing.T) {
			client, attempts := New(), 0
			secret := "credential-that-must-not-leak"
			client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
				attempts++
				_ = request.Body.Close()
				message := strings.Repeat("x", 1990) + secret + strings.Repeat("x", 100)
				envelope := map[string]any{"type": "error", "error": map[string]any{"code": "invalid_request_error", "message": message}}
				if format == "json" {
					envelope["status"] = "failed"
					return fixtureResponse(200, encodeWire(envelope)), nil
				}
				return fixtureResponse(200, "data: "+encodeWire(envelope)+"\n\n"), nil
			})
			_, err := client.Complete(context.Background(), Request{
				Config: domain.Provider{Kind: "openai", BaseURL: "https://provider.example", OAuth: &domain.OAuthTokens{AccessToken: secret}},
				Model:  domain.LanguageModel{ID: "test"},
			})
			var failure *ResponseError
			if !errors.As(err, &failure) || attempts != 1 || strings.Contains(err.Error(), "credential-") {
				t.Fatal(err, attempts)
			}
			if len([]rune(failure.Message)) > 2001 {
				t.Fatal("unbounded provider diagnostic")
			}
		})
	}
}
