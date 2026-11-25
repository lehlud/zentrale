package datastore

import (
	"context"
	"database/sql"
	"time"

	"github.com/lehlud/zentrale/domain"
)

// UsageReportFilter uses the UTC day interval [From, To). Nonempty UserID and
// ProjectID are exact filters; callers must authorize the requested scope.
type UsageReportFilter struct {
	From      time.Time
	To        time.Time
	UserID    string
	ProjectID string
}

const usageReportSQL = `WITH usage_window AS (
	SELECT * FROM resources WHERE kind = 'usage' AND :from_day <= created_at AND created_at < :to_day
), attributed AS (` + attributionSelect + ` FROM usage_window AS usage` + attributionJoins + `
), scoped AS (
	SELECT substr(created_at, 1, 10) AS day,
		coalesce(data->>'providerId', '') AS provider_id, coalesce(data->>'modelId', '') AS model_id,
		user_id, project_id, coalesce(data->>'purpose', '') AS purpose,
		coalesce(CAST(data->>'inputTokens' AS INTEGER), 0) AS input_tokens,
		coalesce(CAST(data->>'cachedTokens' AS INTEGER), 0) AS cached_tokens,
		data
	FROM attributed WHERE (:user_filter = '' OR user_id = :user_filter)
		AND (:project_filter = '' OR project_id = :project_filter)
)
SELECT day, provider_id, model_id, user_id, project_id, purpose,
	sum(input_tokens), sum(cached_tokens), sum(max(input_tokens-cached_tokens, 0)),
	sum(coalesce(CAST(data->>'outputTokens' AS INTEGER), 0)),
	sum(coalesce(CAST(data->>'cacheWriteTokens' AS INTEGER), 0)),
	sum(coalesce(CAST(data->>'cacheWriteLongTokens' AS INTEGER), 0)),
	coalesce(sum(CAST(data->>'realCost' AS REAL)) FILTER (WHERE data->>'costKnown'), 0),
	coalesce(sum(CAST(data->>'fictionalCost' AS REAL)) FILTER (WHERE data->>'costKnown'), 0),
	count(*) FILTER (WHERE NOT coalesce(data->>'costKnown', false)),
	count(*) FILTER (WHERE (data->>'costKnown') AND (data->>'estimated')),
	count(*) FILTER (WHERE NOT coalesce(data->>'tokensKnown', false)),
	count(*) FILTER (WHERE NOT coalesce(data->>'cacheReadKnown', false)),
	count(*) FILTER (WHERE NOT coalesce(data->>'cacheWriteKnown', false)), count(*)
FROM scoped GROUP BY day, provider_id, model_id, user_id, project_id, purpose
ORDER BY day DESC, provider_id, model_id, user_id, project_id, purpose`

// UsageReport groups every matching row, not just the current activity page.
func (instance *Instance) UsageReport(ctx context.Context, filter UsageReportFilter) ([]domain.UsageReportRow, error) {
	if instance == nil || instance.Closed() {
		return nil, errClosed
	}

	rows, err := instance.readDB().QueryContext(ctx, usageReportSQL,
		sql.Named("from_day", filter.From.UTC().Format(time.DateOnly)),
		sql.Named("to_day", filter.To.UTC().Format(time.DateOnly)),
		sql.Named("initial_user", (domain.Project{}).Owner()),
		sql.Named("user_filter", filter.UserID), sql.Named("project_filter", filter.ProjectID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []domain.UsageReportRow{}
	for rows.Next() {
		var row domain.UsageReportRow
		err := rows.Scan(
			&row.Day, &row.ProviderID, &row.ModelID, &row.UserID, &row.ProjectID, &row.Purpose,
			&row.InputTokens, &row.CachedTokens, &row.UncachedTokens, &row.OutputTokens, &row.CacheWriteTokens, &row.CacheWriteLongTokens,
			&row.RealCost, &row.FictionalCost, &row.UnknownPrices, &row.EstimatedCosts,
			&row.UnknownTokenCounts, &row.UnknownCacheReads, &row.UnknownCacheWrites, &row.RequestCount,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}

	return result, rows.Err()
}
