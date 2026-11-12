package webapi

import (
	"reflect"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/gitforge"
)

func gitStringArgs(names ...string) graphql.FieldConfigArgument {
	args := graphql.FieldConfigArgument{}
	for _, name := range names {
		args[name] = &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}
	}

	return args
}

func (builder *schemaBuilder) gitQueries() graphql.Fields {
	service := func() *gitforge.Service { return builder.server.Engine.GitForge }
	args := gitStringArgs("connectionId")
	args["search"] = &graphql.ArgumentConfig{Type: graphql.String, DefaultValue: ""}
	args["offset"] = &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 0}

	return graphql.Fields{
		"gitLogin": &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.GitLogin]()),
			Args: gitStringArgs("connectionId", "loginId"),
			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				value, err := service().Login(params.Args["connectionId"].(string), params.Args["loginId"].(string))
				if err != nil || value == nil {
					return nil, err
				}
				return mapOf(*value), nil
			}),
		},
		"gitConnections": &graphql.Field{
			Type: graphql.NewList(builder.output(reflect.TypeFor[domain.GitConnection]())),
			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				values, err := service().Connections()
				return mapList(values), err
			}),
		},

		"gitRepositories": &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.GitRepositoryPage]()),
			Args: args,

			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				page, err := service().Repositories(
					params.Context,
					params.Args["connectionId"].(string),
					params.Args["search"].(string),
					params.Args["offset"].(int),
				)

				return mapOf(page), err
			}),
		},

		"gitNamespaces": &graphql.Field{
			Type: graphql.NewList(builder.output(reflect.TypeFor[domain.GitNamespace]())),
			Args: gitStringArgs("connectionId"),

			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				values, err := service().Namespaces(params.Context, params.Args["connectionId"].(string))
				return mapList(values), err
			}),
		},
	}
}

func mapList[T any](values []T) []map[string]any {
	result := make([]map[string]any, 0, len(values))

	for _, value := range values {
		result = append(result, mapOf(value))
	}

	return result
}

func (builder *schemaBuilder) gitMutations() graphql.Fields {
	engine := builder.server.Engine
	service := func() *gitforge.Service { return engine.GitForge }
	inputArgs := func(value reflect.Type) graphql.FieldConfigArgument {
		return graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: graphql.NewNonNull(builder.input(value)),
			},
		}
	}

	return graphql.Fields{
		"refreshGitRepositories": &graphql.Field{
			Type: graphql.Boolean,
			Args: gitStringArgs("connectionId"),

			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				return true, service().RefreshRepositories(params.Args["connectionId"].(string))
			}),
		},

		"configureGitConnection": &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.GitConnection]()),
			Args: inputArgs(reflect.TypeFor[domain.GitConnectionConfig]()),

			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				input, err := convert[domain.GitConnectionConfig](params.Args["input"])
				if err != nil {
					return nil, err
				}

				engine.StopGitLogin(input.Kind)
				value, err := service().Configure(input)

				return mapOf(value), err
			}),
		},

		"beginGitLogin": &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.GitLogin]()),
			Args: gitStringArgs("connectionId"),

			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				value, err := engine.BeginGitLogin(params.Context, params.Args["connectionId"].(string))
				return mapOf(value), err
			}),
		},

		"pollGitLogin": &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.GitLogin]()),
			Args: gitStringArgs("connectionId", "loginId"),

			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				value, err := engine.PollGitLogin(params.Context, params.Args["connectionId"].(string), params.Args["loginId"].(string))
				return mapOf(value), err
			}),
		},

		"cancelGitLogin": &graphql.Field{
			Type: graphql.Boolean,
			Args: gitStringArgs("connectionId", "loginId"),
			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				return true, engine.CancelGitLogin(params.Args["connectionId"].(string), params.Args["loginId"].(string))
			}),
		},
		"disconnectGitConnection": &graphql.Field{
			Type: graphql.Boolean,
			Args: gitStringArgs("connectionId"),

			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				id := params.Args["connectionId"].(string)
				engine.StopGitLogin(id)
				if err := service().Disconnect(id); err != nil {
					return false, err
				}

				if id == "github" {
					return true, builder.server.Store.SetSetting("github_token", "")
				}

				return true, nil
			}),
		},

		"createGitRepository": &graphql.Field{
			Type: builder.output(reflect.TypeFor[domain.GitRepository]()),
			Args: inputArgs(reflect.TypeFor[domain.GitRepositoryInput]()),

			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				input, err := convert[domain.GitRepositoryInput](params.Args["input"])
				if err != nil {
					return nil, err
				}

				value, err := service().CreateRepository(params.Context, input)

				return mapOf(value), err
			}),
		},

		"connectRepository": &graphql.Field{
			Type: builder.output(resourceTypes["source"]),
			Args: gitStringArgs("projectId", "connectionId", "repositoryId"),

			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				value, err := engine.ConnectRepository(
					params.Context,
					params.Args["projectId"].(string),
					params.Args["connectionId"].(string),
					params.Args["repositoryId"].(string),
				)

				return mapOf(value), err
			}),
		},

		"revokeRepositoryKey": &graphql.Field{
			Type: graphql.Boolean,
			Args: graphql.FieldConfigArgument{
				"id": &graphql.ArgumentConfig{
					Type: graphql.NewNonNull(graphql.Int),
				},
			},

			Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
				return true, engine.RevokeRepositoryKey(params.Context, params.Args["id"].(int))
			}),
		},
	}
}
