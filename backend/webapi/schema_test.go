package webapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
)

func TestResourceMutationAuthenticatesDecodesAndPreservesContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), requestKey{}, &requestState{authenticated: true})
	calls := 0
	var seen context.Context
	var saveErr error
	field := resourceMutation(newSchemaBuilder(nil), func(ctx context.Context, value domain.Machine) (domain.Machine, error) {
		calls++
		seen = ctx
		value.ID = "saved"
		return value, saveErr
	})
	if field.Type.String() != "Machine" || field.Args["input"].Type.String() != "MachineInput!" {
		t.Fatal("resource schema changed", field.Type, field.Args)
	}
	malformed := map[string]any{"input": map[string]any{"name": []any{1}}}
	if _, err := field.Resolve(graphql.ResolveParams{Context: context.Background(), Args: malformed}); err == nil || !strings.Contains(err.Error(), "authentication required") || calls != 0 {
		t.Fatal("authentication must precede conversion and mutation", calls, err)
	}
	if _, err := field.Resolve(graphql.ResolveParams{Context: ctx, Args: malformed}); err == nil || calls != 0 {
		t.Fatal("malformed input reached the engine", calls, err)
	}
	args := map[string]any{"input": map[string]any{"name": "machine"}}
	result, err := field.Resolve(graphql.ResolveParams{Context: ctx, Args: args})
	if err != nil || calls != 1 || seen != ctx {
		t.Fatal("request context or input was lost", result, calls, err)
	}
	row, ok := result.(map[string]any)
	if !ok || row["id"] != "saved" || row["name"] != "machine" {
		t.Fatal("saved value was not serialized", result)
	}
	saveErr = errors.New("domain validation failed")
	if _, err := field.Resolve(graphql.ResolveParams{Context: ctx, Args: args}); !errors.Is(err, saveErr) {
		t.Fatal("domain error was swallowed", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := field.Resolve(graphql.ResolveParams{Context: cancelled, Args: args}); seen != cancelled || seen.Err() != context.Canceled || !errors.Is(err, saveErr) {
		t.Fatal("request cancellation was detached", seen, err)
	}
}
