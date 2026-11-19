package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func (client *Client) replicate(ctx context.Context, provider domain.Provider, target string, input json.RawMessage) (_ json.RawMessage, err error) {
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
	deadline, _ := ctx.Deadline()
	seconds := int(math.Ceil(time.Until(deadline).Seconds()))
	if seconds < 5 {
		return nil, errors.New("the remaining deadline is too short to start a Replicate prediction")
	}
	if err := client.request(ctx, provider, http.MethodPost, path, body,
		map[string]string{"Cancel-After": strconv.Itoa(seconds) + "s"}, &response); err != nil {
		return nil, err
	}
	if !slug.MatchString(response.ID) {
		return nil, errors.New("Replicate returned an invalid prediction ID")
	}
	path = "/predictions/" + response.ID
	terminal := false
	receipt := pendingPrediction{ID: response.ID, ProviderID: provider.ID, Owner: client.owner, Expires: deadline}
	defer func() {
		if !terminal {
			if failure := client.cancelPrediction(context.WithoutCancel(ctx), provider, receipt.ID); failure != nil {
				err = errors.Join(err, fmt.Errorf("remote cancellation could not be confirmed: %w", failure))
				return
			}
		}
		if client.store != nil {
			if failure := client.store.Remove(predictionResource, receipt.ID); failure != nil {
				slog.Warn("could not release Replicate receipt", "error", failure)
			}
		}
	}()
	if client.store != nil && (response.Status == "starting" || response.Status == "processing") {
		if err := client.store.Save(predictionResource, receipt.ID, provider.ID, receipt); err != nil {
			return nil, fmt.Errorf("could not save Replicate receipt: %w", err)
		}
	}
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

func (client *Client) cancelPrediction(ctx context.Context, provider domain.Provider, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	return client.request(ctx, provider, http.MethodPost, "/predictions/"+id+"/cancel", nil, nil, nil)
}
