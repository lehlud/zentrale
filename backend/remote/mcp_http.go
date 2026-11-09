package remote

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

//go:embed scripts/mcp_http.py
var mcpHTTPRelay string

// SandboxHTTPTransport reaches loopback endpoints from inside the sandbox, never
// from Zentrale's own network namespace. Open must apply the approved execution
// policy to every request. Redirects to another origin are rejected here too.
type SandboxHTTPTransport struct {
	Endpoint *url.URL
	Open     func(context.Context, string) (*Process, error)
}

func ValidateMCPEndpoint(endpoint string) (*url.URL, error) {
	address, err := url.Parse(endpoint)
	if err != nil ||
		address.Scheme != "http" ||
		address.User != nil ||
		address.Fragment != "" ||
		address.Host == "" {
		return nil, errors.New("MCP endpoint must be an HTTP loopback URL inside your sandbox")
	}

	switch address.Hostname() {
	case "127.0.0.1", "::1", "localhost":
		return address, nil
	default:
		return nil, errors.New("MCP endpoint must use localhost, 127.0.0.1 or [::1] inside your sandbox")
	}
}

func (transport *SandboxHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != transport.Endpoint.Scheme || request.URL.Host != transport.Endpoint.Host {
		return nil, errors.New("MCP request left the approved endpoint origin")
	}

	metadata, err := json.Marshal(map[string]any{
		"url":     request.URL.String(),
		"method":  request.Method,
		"headers": request.Header,
	})
	if err != nil {
		return nil, err
	}

	process, err := transport.Open(
		request.Context(),
		"python3 -u -c "+Quote(mcpHTTPRelay)+" "+Quote(base64.StdEncoding.EncodeToString(metadata)),
	)
	if err != nil {
		return nil, err
	}

	// Reading EOF on stdin starts the request. Copy concurrently so cancellation
	// can close the SSH channel even when the remote HTTP peer stops consuming.
	go func() {
		if request.Body != nil {
			defer request.Body.Close()

			_, err := io.Copy(process, io.LimitReader(request.Body, 1024*1024+1))
			if err != nil {
				_ = process.Close()
				return
			}
		}

		_ = process.CloseWrite()
	}()
	reader := bufio.NewReaderSize(process, 64*1024)
	line, err := reader.ReadSlice('\n')
	if err != nil {
		_ = process.Close()
		return nil, fmt.Errorf("connect to sandbox MCP endpoint: %w", err)
	}

	var envelope struct {
		Status  int         `json:"status"`
		Headers [][2]string `json:"headers"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil || envelope.Status < 100 || envelope.Status > 599 {
		_ = process.Close()
		return nil, errors.New("invalid HTTP response from sandbox MCP relay")
	}

	headers := make(http.Header)

	for _, header := range envelope.Headers {
		headers.Add(header[0], header[1])
	}

	remaining := int64(1024 * 1024)

	if strings.HasPrefix(strings.ToLower(headers.Get("Content-Type")), "text/event-stream") {
		// SSE is bounded per event by the SDK and per operation by its context.
		remaining = -1
	}

	return &http.Response{
		StatusCode: envelope.Status,
		Status:     fmt.Sprintf("%d %s", envelope.Status, http.StatusText(envelope.Status)),

		Header: headers,
		Body:   &mcpResponseBody{reader: reader, process: process, remaining: remaining},

		ContentLength: -1,
		Request:       request,
	}, nil
}

type mcpResponseBody struct {
	reader    io.Reader
	process   *Process
	remaining int64
}

func (body *mcpResponseBody) Read(buffer []byte) (int, error) {
	if body.remaining == 0 {
		return 0, errors.New("MCP JSON response exceeds 1 MiB")
	}

	if body.remaining > 0 {
		buffer = buffer[:min(int64(len(buffer)), body.remaining)]
	}

	count, err := body.reader.Read(buffer)

	if body.remaining > 0 {
		body.remaining -= int64(count)
	}

	return count, err
}

func (body *mcpResponseBody) Close() error {
	return body.process.Close()
}
