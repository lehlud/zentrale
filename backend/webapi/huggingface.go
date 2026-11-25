package webapi

import (
	"reflect"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
)

func (builder *schemaBuilder) huggingFaceQueries() graphql.Fields {
	return graphql.Fields{
		"huggingFaceConnection": &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.HuggingFaceConnection]()),
			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				value, err := builder.server.Engine.HubFor(state(params.Context).user.ID).Status()
				return mapOf(value), err
			}),
		},
	}
}

func (builder *schemaBuilder) huggingFaceMutations() graphql.Fields {
	engine := builder.server.Engine
	return graphql.Fields{
		"configureHuggingFaceOAuth": &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.HuggingFaceConnection]()),
			Args: graphql.FieldConfigArgument{
				"value": {Type: graphql.NewNonNull(builder.input(reflect.TypeFor[domain.HuggingFaceOAuthConfig]()))},
			},
			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				config, err := convert[domain.HuggingFaceOAuthConfig](params.Args["value"])
				if err != nil {
					return nil, err
				}

				value, err := engine.HubFor(state(params.Context).user.ID).ConfigureOAuth(config)
				if err != nil {
					return nil, err
				}

				engine.Emit("resources", "", nil)
				return mapOf(value), nil
			}),
		},
		"beginHuggingFaceLogin": &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.HuggingFaceLogin]()),
			Args: gitStringArgs("redirectUri"),
			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				ctx, err := browserLoginContext(params.Context, params.Args["redirectUri"].(string))
				if err != nil {
					return nil, err
				}

				login, err := engine.HubFor(state(ctx).user.ID).BeginOAuth(ctx)
				if err != nil {
					return nil, err
				}

				engine.Emit("resources", "", nil)
				return mapOf(login), nil
			}),
		},
		"endHuggingFaceLogin": &graphql.Field{
			Type: graphql.Boolean,
			Args: gitStringArgs("id"),
			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				current := state(params.Context)
				if err := engine.HubFor(current.user.ID).EndOAuth(params.Args["id"].(string), current.sessionHash); err != nil {
					return false, err
				}

				engine.Emit("resources", "", nil)
				return true, nil
			}),
		},
		"connectHuggingFace": &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.HuggingFaceConnection]()),
			Args: graphql.FieldConfigArgument{"token": {Type: graphql.NewNonNull(graphql.String)}},
			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				value, err := engine.HubFor(state(params.Context).user.ID).Connect(params.Context, params.Args["token"].(string))
				if err != nil {
					return nil, err
				}

				engine.Emit("resources", "", nil)
				return mapOf(value), nil
			}),
		},
		"disconnectHuggingFace": &graphql.Field{
			Type: graphql.Boolean,
			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				if err := engine.HubFor(state(params.Context).user.ID).Disconnect(); err != nil {
					return false, err
				}

				engine.Emit("resources", "", nil)
				return true, nil
			}),
		},
	}
}
