package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func (client *Client) replicate(ctx context.Context, provider domain.Provider, target string, input json.RawMessage, timeout time.Duration) (_ json.RawMessage, err error) {
	path := "/models/" + target + "/predictions"
	payload := map[string]any{"input": input}
	if deployment, ok := strings.CutPrefix(target, "deployment:"); ok {
		path = "/deployments/" + deployment + "/predictions"
	} else if model, version, pinned := strings.Cut(target, ":"); pinned {
		var modelVersion struct {
			ID string `json:"id"`
		}
		if err := client.getQueue(ctx, provider, "/models/"+model+"/versions/"+version, &modelVersion); err != nil {
			return nil, err
		}
		if modelVersion.ID != version {
			return nil, errors.New("Replicate could not verify the selected model version")
		}
		path = "/predictions"
		payload["version"] = version
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	type prediction struct {
		ID     string          `json:"id"`
		Status string          `json:"status"`
		Output json.RawMessage `json:"output"`
	}
	var response prediction
	if err := client.request(ctx, provider, http.MethodPost, path, body,
		map[string]string{"Cancel-After": strconv.Itoa(int(timeout.Seconds())) + "s", "Prefer": "wait=10"}, &response); err != nil {
		return nil, err
	}
	if !slug.MatchString(response.ID) {
		return nil, errors.New("Replicate returned an invalid prediction ID")
	}
	path = "/predictions/" + response.ID
	terminal := false
	defer client.cancel(ctx, provider, http.MethodPost, path+"/cancel", &terminal, &err)
	for {
		switch response.Status {
		case "succeeded":
			terminal = true
			return response.Output, nil
		case "failed", "canceled", "aborted":
			terminal = true
			return nil, errors.New("Replicate prediction " + response.Status)
		case "starting", "processing":
		default:
			return nil, errors.New("unexpected Replicate prediction state")
		}
		if err := client.wait(ctx); err != nil {
			return nil, err
		}
		response = prediction{}
		if err := client.getQueue(ctx, provider, path, &response); err != nil {
			return nil, err
		}
	}
}

func (client *Client) fal(ctx context.Context, provider domain.Provider, target string, body []byte, timeout time.Duration) (_ json.RawMessage, err error) {
	var receipt struct {
		ID       string `json:"request_id"`
		Status   string `json:"status_url"`
		Response string `json:"response_url"`
		Cancel   string `json:"cancel_url"`
	}
	headers := map[string]string{
		"X-Fal-No-Retry":              "1",
		"X-Fal-Request-Start-Timeout": strconv.Itoa(min(180, int(timeout.Seconds()))),
		"X-Fal-Store-IO":              "0",
	}
	if err := client.request(ctx, provider, http.MethodPost, "/"+target, body, headers, &receipt); err != nil {
		return nil, err
	}
	if !slug.MatchString(receipt.ID) {
		return nil, errors.New("fal returned an invalid request ID")
	}
	parts := strings.SplitN(target, "/", 3)
	path := "/" + parts[0] + "/" + parts[1] + "/requests/" + receipt.ID
	terminal := false
	cancelPath, fail := falRequestPath(receipt.Cancel, path, receipt.ID, "/cancel")
	if fail != nil {
		// The receipt ID is valid. Attempt the documented app-root cancel
		// route without ever sending the account key to the supplied URL.
		defer client.cancel(ctx, provider, http.MethodPut, path+"/cancel", &terminal, &err)
		return nil, fail
	}
	defer client.cancel(ctx, provider, http.MethodPut, cancelPath, &terminal, &err)
	statusPath, failure := falRequestPath(receipt.Status, path, receipt.ID, "/status")
	if failure != nil {
		return nil, failure
	}
	responsePath, failure := falRequestPath(receipt.Response, path, receipt.ID, "")
	if failure != nil {
		return nil, failure
	}
	for {
		var state struct {
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		}
		if err := client.getQueue(ctx, provider, statusPath, &state); err != nil {
			return nil, err
		}
		switch state.Status {
		case "COMPLETED":
			terminal = true
			if string(state.Error) != "" && string(state.Error) != "null" && string(state.Error) != `""` {
				return nil, errors.New("fal model execution failed")
			}
			var output json.RawMessage
			err := client.getQueue(ctx, provider, responsePath, &output)
			return output, err
		case "IN_QUEUE", "IN_PROGRESS":
		default:
			return nil, errors.New("unexpected fal request state")
		}
		if err := client.wait(ctx); err != nil {
			return nil, err
		}
	}
}

func falRequestPath(reference, fallback, id, suffix string) (string, error) {
	if reference == "" {
		return fallback + suffix, nil
	}
	address, err := url.Parse(reference)
	if err != nil || address.Scheme != "https" || address.Host != "queue.fal.run" ||
		address.User != nil || address.RawQuery != "" || address.ForceQuery ||
		address.Fragment != "" || address.EscapedPath() != address.Path {
		return "", errors.New("fal returned an invalid result URL")
	}
	prefix, found := strings.CutSuffix(address.Path, "/requests/"+id+suffix)
	if !found || !route(strings.TrimPrefix(prefix, "/"), 2, 8) {
		return "", errors.New("fal returned a result URL for another operation")
	}
	return address.Path, nil
}
