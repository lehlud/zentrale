package webapi

import (
	"errors"
	"reflect"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

var errUsageDateRange = errors.New("usage report requires a valid UTC date range of up to 366 days (YYYY-MM-DD, end exclusive)")

func (builder *schemaBuilder) usageReportField() *graphql.Field {
	return &graphql.Field{
		Type: graphql.NewNonNull(builder.output(reflect.TypeFor[[]domain.UsageReportRow]())),
		Args: graphql.FieldConfigArgument{
			"from":      {Type: graphql.String, Description: "Inclusive UTC date (YYYY-MM-DD)."},
			"to":        {Type: graphql.String, Description: "Exclusive UTC date (YYYY-MM-DD)."},
			"userId":    {Type: graphql.ID},
			"projectId": {Type: graphql.ID},
		},
		Resolve: authenticated(builder.server.resolveUsageReport),
	}
}

func (server *Server) resolveUsageReport(params graphql.ResolveParams) (any, error) {
	user := state(params.Context).user
	if user.ID == "" || user.Disabled {
		return nil, errAuthenticationRequired
	}
	if !user.IsAdmin() {
		return nil, errAdministratorRequired
	}

	userID, _ := params.Args["userId"].(string)
	projectID, _ := params.Args["projectId"].(string)
	fromDate, _ := params.Args["from"].(string)
	toDate, _ := params.Args["to"].(string)
	from, to, err := usageDateRange(time.Now(), fromDate, toDate)
	if err != nil {
		return nil, err
	}

	rows, err := server.Store.UsageReport(params.Context, datastore.UsageReportFilter{
		From: from, To: to, UserID: userID, ProjectID: projectID,
	})
	if err != nil {
		return nil, err
	}

	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		result = append(result, mapOf(row))
	}
	return result, nil
}

func usageDateRange(now time.Time, fromDate, toDate string) (time.Time, time.Time, error) {
	to := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
	if toDate != "" {
		parsed, err := time.Parse(time.DateOnly, toDate)
		if err != nil {
			return time.Time{}, time.Time{}, errUsageDateRange
		}
		to = parsed
	}

	from := to.AddDate(0, 0, -30)
	if fromDate != "" {
		parsed, err := time.Parse(time.DateOnly, fromDate)
		if err != nil {
			return time.Time{}, time.Time{}, errUsageDateRange
		}
		from = parsed
	}

	if !from.Before(to) || from.Before(to.AddDate(0, 0, -366)) {
		return time.Time{}, time.Time{}, errUsageDateRange
	}
	return from, to, nil
}
