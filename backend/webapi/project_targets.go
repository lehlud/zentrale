package webapi

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/domain"
)

func hasProjectTarget(user domain.User, grants []string, kind, id string) bool {
	return user.IsAdmin() || slices.Contains(grants, kind+":"+id)
}

func (server *Server) projectTargetGrants(userID string) ([]string, error) {
	grants := []string{}
	encoded, err := server.Store.Setting("project_targets:" + userID)
	if err != nil || encoded == "" {
		return grants, err
	}
	err = json.Unmarshal([]byte(encoded), &grants)
	return grants, err
}

func (server *Server) requireProjectTargets(user domain.User, machines, pools []string) error {
	grants, err := server.projectTargetGrants(user.ID)
	if err != nil {
		return err
	}
	for kind, ids := range map[string][]string{"machine": machines, "pool": pools} {
		for _, id := range ids {
			if id != "" && !hasProjectTarget(user, grants, kind, id) {
				return errors.New("this machine or pool is not enabled for your account")
			}
		}
	}
	return nil
}

func (builder *schemaBuilder) projectTargetGrantFields(queries, mutations graphql.Fields) {
	server := builder.server
	grantList := graphql.NewList(graphql.NewNonNull(graphql.String))
	queries["projectTargetGrants"] = &graphql.Field{
		Type: grantList, Args: gitStringArgs("userId"),
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			return server.projectTargetGrants(params.Args["userId"].(string))
		}),
	}
	args := gitStringArgs("userId")
	args["targets"] = &graphql.ArgumentConfig{Type: graphql.NewNonNull(grantList)}
	mutations["setProjectTargetGrants"] = &graphql.Field{
		Type: graphql.Boolean, Args: args,
		Resolve: adminOnly(func(params graphql.ResolveParams) (any, error) {
			userID := params.Args["userId"].(string)
			if _, err := server.Store.UserAccount(userID); err != nil {
				return false, err
			}
			grants, err := convert[[]string](params.Args["targets"])
			if err != nil {
				return false, err
			}
			slices.Sort(grants)
			grants = slices.Compact(grants)
			for _, grant := range grants {
				kind, id, _ := strings.Cut(grant, ":")
				var target struct {
					JumpHostOnly bool `json:"jumpHostOnly"`
				}
				if (kind != "machine" && kind != "pool") || id == "" ||
					server.Store.Load(kind, id, &target) != nil || target.JumpHostOnly {
					return false, errors.New("unknown project machine or pool")
				}
			}
			encoded, err := json.Marshal(grants)
			if err != nil {
				return false, err
			}
			if err := server.Store.SetSetting("project_targets:"+userID, string(encoded)); err != nil {
				return false, err
			}
			server.Engine.Emit("resources", "", nil)
			return true, nil
		}),
	}
}
