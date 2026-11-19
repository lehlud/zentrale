package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/hosted"
)

const hostedModelAction = "run_hosted_model"

type hostedInference interface {
	Run(context.Context, domain.Provider, string, map[string]any, time.Duration) (json.RawMessage, error)
	Close()
}

func (engine *Engine) hostedProvider(providerID string) (domain.Provider, error) {
	var provider domain.Provider
	if err := engine.loadProvider(providerID, &provider); err != nil {
		return provider, err
	}
	if !hosted.Kind(provider.Kind) || provider.Credential == "" {
		return provider, errors.New("choose a connected Modal, Replicate or fal.ai provider")
	}
	return provider, nil
}

func (engine *Engine) runHostedModel(ctx context.Context, record domain.Agent, request interaction) (any, error) {
	if record.Role == "approver" {
		return nil, errors.New("only working agents can request hosted inference")
	}
	if len(request.Arguments) == 0 {
		rows, err := engine.Store.ListFields("provider", nil, 500, 0, []string{"id", "name", "kind", "credential"})
		if err != nil {
			return nil, err
		}
		accounts := []map[string]string{}
		for _, row := range rows {
			kind := stringArg(row, "kind")
			if hosted.Kind(kind) && stringArg(row, "credential") != "" {
				accounts = append(accounts, map[string]string{"id": stringArg(row, "id"), "name": stringArg(row, "name"), "kind": kind})
			}
		}
		return accounts, nil
	}
	if !request.Network {
		return nil, errors.New("request network=true for this individual interaction")
	}
	input, valid := request.Arguments["input"].(map[string]any)
	if !valid || input == nil || len(JSON(input)) > hosted.MaxInput {
		return nil, errors.New("inference input must be a JSON object of at most 128 KiB")
	}
	provider, err := engine.hostedProvider(stringArg(request.Arguments, "providerId"))
	if err != nil {
		return nil, err
	}
	target := stringArg(request.Arguments, "target")
	if err := hosted.ValidateTarget(provider.Kind, target); err != nil {
		return nil, err
	}
	maxSeconds := int(hosted.MaxTimeout.Seconds())
	defaultSeconds := int(hosted.Timeout.Seconds())
	if provider.Kind == "modal" {
		maxSeconds = int(hosted.ModalTimeout.Seconds())
		defaultSeconds = maxSeconds
	}
	seconds, err := outputInteger(request.Arguments, "timeoutSeconds", defaultSeconds, 5, maxSeconds)
	if err != nil {
		return nil, err
	}
	request.Arguments = map[string]any{
		"providerId": provider.ID, "provider": provider.Name, "platform": provider.Kind,
		"destination": provider.BaseURL, "target": target, "input": input, "timeoutSeconds": seconds,
	}
	if _, err := engine.approve(ctx, record, request); err != nil {
		return nil, err
	}
	output, err := engine.callHostedModel(ctx, provider, target, input, time.Duration(seconds)*time.Second)
	if err != nil {
		return nil, err
	}
	return map[string]any{"output": output}, nil
}

func (engine *Engine) callHostedModel(ctx context.Context, reviewed domain.Provider, target string, input map[string]any, timeout time.Duration) (json.RawMessage, error) {
	current, err := engine.hostedProvider(reviewed.ID)
	if err != nil {
		return nil, err
	}
	if current.Kind != reviewed.Kind || current.BaseURL != reviewed.BaseURL || current.Credential != reviewed.Credential {
		return nil, errors.New("inference provider changed during review; request approval again")
	}
	return engine.hosted.Run(ctx, current, target, input, timeout)
}
