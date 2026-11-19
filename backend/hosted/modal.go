package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	modal "github.com/modal-labs/modal-client/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lehlud/zentrale/domain"
)

func modalDestination(raw string) (environment, app, function string, err error) {
	address, parseErr := url.Parse(raw)
	if parseErr == nil && address.Scheme == "modal" && slug.MatchString(address.Host) &&
		address.User == nil && !address.ForceQuery && address.RawQuery == "" && address.Fragment == "" &&
		strings.HasPrefix(address.Path, "/") && route(strings.TrimPrefix(address.EscapedPath(), "/"), 2, 2) {
		app, function, _ = strings.Cut(strings.TrimPrefix(address.Path, "/"), "/")
		return address.Host, app, function, nil
	}
	return "", "", "", errors.New("a Modal API connection requires an environment, App name and Function name")
}

func modalAPIToken(credential string) (id, secret string, ok bool) {
	id, secret, found := strings.Cut(strings.TrimSpace(credential), ".")
	return id, secret, found && strings.HasPrefix(id, "ak-") && len(id) > 3 && route(id, 1, 1) &&
		strings.HasPrefix(secret, "as-") && len(secret) > 3 && route(secret, 1, 1) && !strings.ContainsAny(id+secret, ".")
}

func runModalFunction(ctx context.Context, provider domain.Provider, input map[string]any) (json.RawMessage, error) {
	environment, app, function, err := modalDestination(provider.BaseURL)
	if err != nil {
		return nil, err
	}
	id, secret, ok := modalAPIToken(provider.Credential)
	if !ok {
		return nil, errors.New("invalid Modal API token pair")
	}
	maxThrottleWait := time.Duration(0)
	client, err := modal.NewClientWithOptions(&modal.ClientParams{
		TokenID: id, TokenSecret: secret, Environment: environment,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxThrottleWait: &maxThrottleWait,
		GRPCUnaryInterceptors: []grpc.UnaryClientInterceptor{
			func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
				// Constrain the SDK's ambient control-plane configuration.
				switch conn.Target() {
				case "api.modal.com:443", "api.modal2.com:443", "api.modal.com", "api.modal2.com":
				default:
					return status.Error(codes.PermissionDenied, "unapproved Modal API address")
				}
				if strings.HasSuffix(method, "/FunctionRetryInputs") || strings.HasSuffix(method, "/AttemptRetry") {
					return status.Error(codes.FailedPrecondition, "retrying the approved inference would require fresh approval")
				}
				return invoke(ctx, method, req, reply, conn, options...)
			},
		},
	})
	if err != nil {
		return nil, errors.New("could not initialize the Modal API connection")
	}
	defer client.Close()
	remoteFunction, err := client.Functions.FromName(ctx, app, function, &modal.FunctionFromNameParams{Environment: environment})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not find the deployed Modal Function; check the API token, environment and names")
	}
	// Modal cancels synchronous invocations after the caller disconnects.
	// Keep a single invocation tied to Zentrale instead of spawning a job.
	value, err := remoteFunction.Remote(ctx, []any{input}, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("the Modal Function call failed; check its arguments and logs")
	}
	value, err = modalJSON(value)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxResponse {
		return nil, errors.New("invalid or oversized Modal inference response")
	}
	return encoded, nil
}

// CBOR decodes untyped dictionaries with arbitrary key types. Accept only
// text keys so the Modal result has the same JSON contract as a Web Function.
func modalJSON(value any) (any, error) {
	switch value := value.(type) {
	case map[any]any:
		output := make(map[string]any, len(value))
		for key, item := range value {
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("the Modal Function must use text keys in its result")
			}
			converted, err := modalJSON(item)
			if err != nil {
				return nil, err
			}
			output[name] = converted
		}
		return output, nil
	case []any:
		for index, item := range value {
			converted, err := modalJSON(item)
			if err != nil {
				return nil, err
			}
			value[index] = converted
		}
	}
	return value, nil
}
