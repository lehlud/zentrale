// Package webapi exposes resources exclusively through GraphQL and streams events
// over authenticated WebSockets. The IDE proxy is a user-facing remote surface.
package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/graphql-go/graphql"
)

type requestKey struct{}

type requestState struct {
	authenticated bool
	login         func(string) (bool, error)
	logout        func() error
	client        string
}

func state(ctx context.Context) *requestState {
	server, _ := ctx.Value(requestKey{}).(*requestState)
	if server == nil {
		return &requestState{}
	}

	return server
}

func authenticated(fn graphql.FieldResolveFn) graphql.FieldResolveFn {
	return func(params graphql.ResolveParams) (any, error) {
		if !state(params.Context).authenticated {
			return nil, errors.New("authentication required")
		}

		return fn(params)
	}
}

func mapOf(value any) map[string]any {
	b, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(b, &result)

	return result
}

func convert[T any](input any) (T, error) {
	var v T
	b, err := json.Marshal(input)
	if err != nil {
		return v, err
	}

	err = json.Unmarshal(b, &v)

	return v, err
}

// Resource saves share a typed boundary: authenticate, decode the declared input,
// preserve request cancellation, then serialize the saved domain value. Domain
// validation stays in the engine; non-resource mutations keep their own shape.
func resourceMutation[T any](builder *schemaBuilder, save func(context.Context, T) (T, error)) *graphql.Field {
	t := reflect.TypeFor[T]()
	return &graphql.Field{
		Type: builder.output(t),
		Args: graphql.FieldConfigArgument{
			"input": {Type: graphql.NewNonNull(builder.input(t))},
		},
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			value, err := convert[T](params.Args["input"])
			if err != nil {
				return nil, err
			}
			saved, err := save(params.Context, value)
			return mapOf(saved), err
		}),
	}
}

func (server *Server) Schema() (graphql.Schema, error) {
	builder := newSchemaBuilder(server)
	return graphql.NewSchema(graphql.SchemaConfig{
		Query:    graphql.NewObject(graphql.ObjectConfig{Name: "Query", Fields: builder.queries()}),
		Mutation: graphql.NewObject(graphql.ObjectConfig{Name: "Mutation", Fields: builder.mutations()}),
	})
}
