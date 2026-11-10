package datastore

import (
	"encoding/json"

	"github.com/lehlud/zentrale/domain"
)

// Aggregate in DuckDB: totals must include the entire history, regardless of the
// batch currently displayed in the browser.
func (instance *Instance) UsageSummary(agentID string) (domain.UsageSummary, error) {
	var result domain.UsageSummary

	if instance == nil || instance.Closed() {
		return result, errClosed
	}

	err := instance.db.QueryRow(`SELECT
        coalesce(sum((data->>'inputTokens')::BIGINT),0),
        coalesce(sum((data->>'outputTokens')::BIGINT),0),
        coalesce(sum((data->>'cachedTokens')::BIGINT),0),
        coalesce(sum((data->>'inputTokens')::BIGINT) FILTER (WHERE (data->>'tokensKnown')::BOOLEAN AND (data->>'cacheReadKnown')::BOOLEAN),0),
        coalesce(sum((data->>'cachedTokens')::BIGINT) FILTER (WHERE (data->>'tokensKnown')::BOOLEAN AND (data->>'cacheReadKnown')::BOOLEAN),0),
        coalesce(sum((data->>'cacheWriteTokens')::BIGINT),0),
        count(*) FILTER (WHERE NOT coalesce((data->>'tokensKnown')::BOOLEAN,false)),
        count(*) FILTER (WHERE NOT coalesce((data->>'cacheWriteKnown')::BOOLEAN,false)),
        coalesce(sum((data->>'realCost')::DOUBLE),0),
        coalesce(sum((data->>'fictionalCost')::DOUBLE),0),
        coalesce(bool_or((data->>'estimated')::BOOLEAN),false),
        count(*) FILTER (WHERE NOT coalesce((data->>'costKnown')::BOOLEAN,false)),
        count(*)
        FROM resources WHERE kind='usage' AND parent_id=?`, agentID).Scan(
		&result.InputTokens,
		&result.OutputTokens,
		&result.CachedTokens,
		&result.CacheInputTokens,
		&result.CacheReadTokens,
		&result.CacheWriteTokens,
		&result.UnknownTokenCounts,
		&result.UnknownCacheWrites,
		&result.RealCost,
		&result.FictionalCost,
		&result.Estimated,
		&result.UnknownPrices,
		&result.RequestCount,
	)

	return result, err
}

func (instance *Instance) RecentMessages(agentID string, limit int) ([]map[string]any, error) {
	return instance.MessagesBefore(agentID, limit, "")
}

// MessagesBefore uses a stable message cursor so new output cannot shift
// already loaded scrollback pages, unlike descending OFFSET pagination.
func (instance *Instance) MessagesBefore(agentID string, limit int, beforeID string) ([]map[string]any, error) {
	if instance == nil || instance.Closed() {
		return nil, errClosed
	}

	if limit < 1 || limit > 100 {
		limit = 100
	}

	query := `SELECT data::VARCHAR FROM resources WHERE kind='message' AND parent_id=? ORDER BY created_at DESC,id DESC LIMIT ?`
	args := []any{agentID, limit}
	if beforeID != "" {
		query = `WITH anchor AS (SELECT created_at,id FROM resources WHERE kind='message' AND parent_id=? AND id=?)
		SELECT r.data::VARCHAR FROM resources r, anchor a WHERE r.kind='message' AND r.parent_id=?
		AND (r.created_at<a.created_at OR (r.created_at=a.created_at AND r.id<a.id))
		ORDER BY r.created_at DESC,r.id DESC LIMIT ?`
		args = []any{agentID, beforeID, agentID, limit}
	}
	rows, err := instance.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []map[string]any{}
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}

		var message map[string]any
		if err := json.Unmarshal([]byte(encoded), &message); err != nil {
			return nil, err
		}

		result = append(result, message)
	}

	return result, rows.Err()
}
