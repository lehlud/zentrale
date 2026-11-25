package webapi

import (
	"errors"
	"reflect"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/domain"
)

type ProjectMember struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

type ProjectTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func (builder *schemaBuilder) projectMemberFields(queries, mutations graphql.Fields) {
	server := builder.server
	queries["projectTargets"] = &graphql.Field{
		Type: graphql.NewList(builder.output(reflect.TypeFor[ProjectTarget]())),
		Resolve: authenticated(func(_ graphql.ResolveParams) (any, error) {
			targets := make([]map[string]any, 0)
			for _, kind := range []string{"machine", "pool"} {
				rows, err := server.Store.ListFields(kind, nil, 500, 0, []string{"id", "name", "jumpHostOnly"})
				if err != nil {
					return nil, err
				}

				for _, row := range rows {
					if row["jumpHostOnly"] != true {
						targets = append(targets, map[string]any{"id": row["id"], "name": row["name"], "kind": kind})
					}
				}
			}
			return targets, nil
		}),
	}

	mutations["archiveProject"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: gitStringArgs("projectId"),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			err := server.Engine.ArchiveProject(params.Context, params.Args["projectId"].(string), state(params.Context).user.ID)
			return err == nil, err
		}),
	}

	queries["projectMembers"] = &graphql.Field{
		Type: graphql.NewList(builder.output(reflect.TypeFor[ProjectMember]())),
		Args: gitStringArgs("projectId"),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			var project domain.Project
			if err := server.Store.Load("project", params.Args["projectId"].(string), &project); err != nil ||
				project.Owner() != state(params.Context).user.ID {
				return nil, errProjectUnavailable
			}

			members := make([]ProjectMember, 0, len(project.MemberIDs))
			for _, id := range project.MemberIDs {
				account, err := server.Store.UserAccount(id)
				if err != nil {
					return nil, err
				}
				members = append(members, ProjectMember{Username: account.Username, Name: account.Name})
			}
			return mapList(members), nil
		}),
	}

	args := gitStringArgs("projectId", "username")
	args["add"] = &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.Boolean)}
	mutations["setProjectMember"] = &graphql.Field{
		Type: graphql.Boolean,
		Args: args,
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			user, id := state(params.Context).user, params.Args["projectId"].(string)
			if err := server.projectAccess(user, id, true, true); err != nil {
				return false, err
			}

			username, err := auth.NormalizeUsername(params.Args["username"].(string))
			if err != nil {
				return false, err
			}
			account, err := server.Store.AccountByUsername(username)
			if err != nil {
				return false, errors.New("user not found")
			}
			err = server.Engine.SetProjectMember(id, user.ID, account.ID, params.Args["add"].(bool))
			return err == nil, err
		}),
	}
}
