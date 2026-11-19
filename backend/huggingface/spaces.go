package huggingface

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var spaceHost = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.hf\.space$`)
var spaceEndpoint = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_-]{0,95}$`)
var spaceEventID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func SpaceTarget(address, endpoint string) (string, string, error) {
	address = strings.TrimSpace(address)
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || !spaceHost.MatchString(strings.ToLower(parsed.Host)) ||
		(parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") || parsed.User != nil ||
		parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errors.New("use the HTTPS address of a Hugging Face Space ending in .hf.space")
	}
	endpoint = strings.TrimPrefix(strings.TrimSpace(endpoint), "/")
	if !spaceEndpoint.MatchString(endpoint) {
		return "", "", errors.New("enter a Gradio API endpoint name, such as predict")
	}
	return "https://" + strings.ToLower(parsed.Host), endpoint, nil
}

// RewriteInSpace invokes a Gradio endpoint with one text input and takes its
// first text output. No browser session, file uploads or automatic retries.
func (service *Service) RewriteInSpace(ctx context.Context, address, endpoint, input, accountName string) (string, error) {
	var output string
	err := service.predictText(ctx, address, endpoint, input, accountName, &output)
	if err == nil && strings.TrimSpace(output) == "" {
		err = errors.New("the Gradio endpoint must return text as its first output")
	}
	return output, err
}

func (service *Service) RewriteBatchInSpace(ctx context.Context, address, endpoint string, input []string, accountName string) ([]string, error) {
	var output []string
	err := service.predictText(ctx, address, endpoint, input, accountName, &output)
	return output, err
}

func (service *Service) predictText(ctx context.Context, address, endpoint string, input any, accountName string, output any) error {
	address, endpoint, err := SpaceTarget(address, endpoint)
	if err != nil {
		return err
	}
	var token string
	var release func()
	if accountName != "" {
		ctx, token, release, err = service.beginRequestFor(ctx, accountName)
		if err != nil {
			return err
		}
	} else {
		service.mu.Lock()
		if service.closed {
			service.mu.Unlock()
			return errors.New("Hugging Face connection service is closed")
		}
		ctx, release = service.requestContext(ctx)
		service.mu.Unlock()
	}
	defer release()

	address += "/gradio_api/call/" + endpoint
	payload, err := json.Marshal(map[string][]any{"data": {input}})
	if err != nil {
		return err
	}
	response, err := service.callSpace(ctx, http.MethodPost, address, token, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	var receipt struct {
		EventID string `json:"event_id"`
	}
	err = decodeResponse(response.Body, &receipt, 16*1024)
	response.Body.Close()
	if err != nil || !spaceEventID.MatchString(receipt.EventID) {
		return errors.New("the Gradio Space did not return a valid event ID")
	}
	response, err = service.callSpace(ctx, http.MethodGet, address+"/"+receipt.EventID, token, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	encoded, err := readSpaceOutput(response.Body)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if token != "" {
		credential, _ := json.Marshal(token)
		encoded = bytes.ReplaceAll(encoded, credential[1:len(credential)-1], []byte("[redacted]"))
	}
	if json.Unmarshal(encoded, output) != nil {
		return errors.New("the Gradio endpoint returned an incompatible text result")
	}
	return nil
}

func (service *Service) callSpace(ctx context.Context, method, address, token string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, errors.New("invalid Gradio Space request")
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
	} else {
		request.Header.Set("Accept", "text/event-stream")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := service.spaceClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not connect to the Hugging Face Space")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("Gradio Space returned HTTP %d; check the endpoint, access and ZeroGPU quota", response.StatusCode)
	}
	return response, nil
}

func readSpaceOutput(body io.Reader) (json.RawMessage, error) {
	scanner := bufio.NewScanner(io.LimitReader(body, 2*1024*1024))
	scanner.Buffer(make([]byte, 4096), 128*1024)
	var event string
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			data.WriteByte('\n')
		case line == "":
			switch event {
			case "complete":
				var outputs []json.RawMessage
				if json.Unmarshal([]byte(data.String()), &outputs) == nil && len(outputs) > 0 {
					return outputs[0], nil
				}
				return nil, errors.New("the Gradio endpoint returned no result")
			case "error":
				return nil, errors.New("the Gradio Space could not finish; check the endpoint and ZeroGPU quota")
			}
			event = ""
			data.Reset()
		}
	}
	return nil, errors.New("the Gradio Space stream ended before returning a text result")
}
