package webapi

import (
	"slices"

	"github.com/graphql-go/graphql"
)

func (builder *schemaBuilder) activeProjectIDsField() *graphql.Field {
	server := builder.server
	return &graphql.Field{
		Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(graphql.String))),
		Resolve: authenticated(func(params graphql.ResolveParams) (any, error) {
			rows, err := server.Store.ProjectActivityCandidates(state(params.Context).user.ID)
			if err != nil {
				return nil, err
			}
			server.applyLiveAgentStatus(rows...)

			active := map[string]bool{}
			for _, row := range rows {
				if id, ok := row["projectId"].(string); ok && row["status"] == "running" {
					active[id] = true
				}
			}

			ids := make([]string, 0, len(active))
			for id := range active {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			return ids, nil
		}),
	}
}
