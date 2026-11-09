package providers

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestResponsesRecognizeEventStreamsWithoutReliableHeaders(t *testing.T) {
	completed := `{"output":[{"type":"message","content":[{"type":"output_text","text":"Ready"}]},{"type":"function_call","call_id":"call-1","name":"harness","arguments":"{}"}],"usage":{"input_tokens":12}}`

	for _, contentType := range []string{
		"",
		"application/json",
		"text/plain; charset=utf-8",
		"Text/Event-Stream; charset=utf-8",
	} {
		for _, prefix := range []string{"", "\xef\xbb\xbf", ": keepalive\r\n\r\n"} {
			t.Run(contentType+"/"+prefix, func(t *testing.T) {
				body := prefix + "event: response.completed\r\ndata: {\"response\":" + completed + "}\r\n\r\n"
				response := &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{
							contentType,
						},
					},
					Body: io.NopCloser(strings.NewReader(body)),
				}

				result, err := decodeProviderResponse(response)
				if err != nil {
					t.Fatal(err)
				}

				if len(asArray(result["output"])) != 2 ||
					asInt(asObject(result["usage"])["input_tokens"]) != 12 {
					t.Fatalf("incomplete response: %v", result)
				}
			})
		}
	}
}

func TestJSONResponsesAndHTTPFailuresRemainDistinct(t *testing.T) {
	for _, test := range []struct {
		status    int
		body      string
		wantError string
	}{
		{200, ` {"output":[]}`, ""},
		{200, "error from gateway", "invalid provider JSON"},
		{502, "event: error\ndata: {}\n\n", "provider HTTP 502"},
		{200, "event: response.failed\ndata: {}\n\n", "did not complete"},
		{200, "data: {\"type\":\"response.created\"}\n\n", "ended before completion"},
	} {
		response := &http.Response{
			StatusCode: test.status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(test.body)),
		}

		_, err := decodeProviderResponse(response)
		if test.wantError == "" &&
			err != nil ||
			test.wantError != "" &&
				(err == nil ||
					!strings.Contains(err.Error(), test.wantError)) {
			t.Fatalf("status %d: %v", test.status, err)
		}
	}
}
