package domain

// UsageReportRow aggregates one UTC day and model, account, project and
// purpose. Empty account and project IDs mean unattributed.
type UsageReportRow struct {
	Day        string `json:"day"`
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
	UserID     string `json:"userId"`
	ProjectID  string `json:"projectId"`
	Purpose    string `json:"purpose"`

	InputTokens          int64 `json:"inputTokens"`
	CachedTokens         int64 `json:"cachedTokens"`
	UncachedTokens       int64 `json:"uncachedTokens"`
	OutputTokens         int64 `json:"outputTokens"`
	CacheWriteTokens     int64 `json:"cacheWriteTokens"`
	CacheWriteLongTokens int64 `json:"cacheWriteLongTokens"`

	RealCost           float64 `json:"realCost"`
	FictionalCost      float64 `json:"fictionalCost"`
	UnknownPrices      int64   `json:"unknownPrices"`
	EstimatedCosts     int64   `json:"estimatedCosts"`
	UnknownTokenCounts int64   `json:"unknownTokenCounts"`
	UnknownCacheReads  int64   `json:"unknownCacheReads"`
	UnknownCacheWrites int64   `json:"unknownCacheWrites"`
	RequestCount       int64   `json:"requestCount"`
}
