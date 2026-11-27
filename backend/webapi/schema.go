// Package webapi exposes resources exclusively through GraphQL and streams events
// over authenticated WebSockets. The IDE proxy is a user-facing remote surface.
package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
)

type requestKey struct{}
type resolverContextKey struct{}

type requestState struct {
	authenticated bool
	user          domain.User
	sessionHash   string
	origin        string
	login         func(string, string) (bool, error)
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
		// graphql-go's executor waits for actual completion to retain the bounded
		// worker slot; resolvers still receive the original cancellable context.
		if original, ok := params.Context.Value(resolverContextKey{}).(context.Context); ok {
			params.Context = context.WithValue(original, requestKey{}, state(params.Context))
			if err := params.Context.Err(); err != nil {
				return nil, err
			}
		}
		if !state(params.Context).authenticated {
			return nil, errAuthenticationRequired
		}

		return fn(params)
	}
}

var errAdministratorRequired = errors.New("administrator required")

func adminOnly(resolve graphql.FieldResolveFn) graphql.FieldResolveFn {
	return authenticated(func(params graphql.ResolveParams) (any, error) {
		if !state(params.Context).user.IsAdmin() {
			return nil, errAdministratorRequired
		}
		return resolve(params)
	})
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
	queries, mutations := builder.queries(), builder.mutations()
	queries["usageReport"] = builder.usageReportField()
	queries["activeProjectIds"] = builder.activeProjectIDsField()
	builder.shortChatFields(queries, mutations)
	builder.conversationBranchFields(queries, mutations)
	builder.accountFields(queries, mutations)
	builder.modelPresetFields(queries, mutations)
	builder.projectMemberFields(queries, mutations)
	builder.projectTargetGrantFields(queries, mutations)
	server.protectProjectFields(queries, mutations)
	restrictAdminFields(queries, mutations)
	return graphql.NewSchema(graphql.SchemaConfig{
		Query:    graphql.NewObject(graphql.ObjectConfig{Name: "Query", Fields: queries}),
		Mutation: graphql.NewObject(graphql.ObjectConfig{Name: "Mutation", Fields: mutations}),
	})
}

func restrictAdminFields(queries, mutations graphql.Fields) {
	for _, name := range []string{
		"machines", "pools", "poolConfiguration", "sshKeys", "trustedKeys", "providerLogin", "providers", "providerKinds", "humanizerSettings",
	} {
		queries[name].Resolve = adminOnly(queries[name].Resolve)
	}
	for _, name := range []string{
		"saveMachine", "savePool", "trustHostKey", "generateSSHKey", "renameSSHKey", "deleteSSHKey", "testMachine", "testPool",
		"saveProvider", "testProvider", "saveModelMetadata", "setModelDefaults", "setHumanizer", "setupModalHumanizer",
		"refreshModels", "beginProviderLogin", "pollProviderLogin", "cancelProviderLogin", "disconnectProvider",
		"setGitHubToken",
	} {
		mutations[name].Resolve = adminOnly(mutations[name].Resolve)
	}
}
