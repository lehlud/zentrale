package webapi

import (
	"reflect"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
)

func (builder *schemaBuilder) accountFields(queries, mutations graphql.Fields) {
	server := builder.server
	userType := reflect.TypeFor[domain.User]()
	queries["users"] = &graphql.Field{
		Type: graphql.NewList(builder.output(userType)),
		Resolve: adminOnly(func(_ graphql.ResolveParams) (any, error) {
			users, err := server.Store.Users()
			return mapList(users), err
		}),
	}
	mutations["saveUser"] = &graphql.Field{
		Type: builder.output(userType),
		Args: graphql.FieldConfigArgument{
			"input":       {Type: graphql.NewNonNull(builder.input(userType))},
			"password":    {Type: graphql.String, DefaultValue: ""},
			"oidcSubject": {Type: graphql.String, DefaultValue: ""},
		},
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			user, err := convert[domain.User](params.Args["input"])
			if err != nil {
				return nil, err
			}

			updated, err := server.Auth.SaveAccount(state(params.Context).user.ID, user,
				params.Args["password"].(string), params.Args["oidcSubject"].(string))
			return mapOf(updated), err
		}),
	}
}
