package domain

import "time"

// InferenceTrace contains estimates and transport diagnostics, never prompts,
// credentials, routing tokens or private provider reasoning.
type InferenceTrace struct {
	ID                          string             `json:"id"`
	AgentID                     string             `json:"agentId"`
	ProviderID                  string             `json:"providerId"`
	ModelID                     string             `json:"modelId"`
	Purpose                     string             `json:"purpose"`
	ReasoningLevel              string             `json:"reasoningLevel"`
	CreatedAt                   time.Time          `json:"createdAt"`
	DurationMillis              int                `json:"durationMillis"`
	Outcome                     string             `json:"outcome"`
	EstimatedSystemTokens       int                `json:"estimatedSystemTokens"`
	EstimatedToolTokens         int                `json:"estimatedToolTokens"`
	EstimatedConversationTokens int                `json:"estimatedConversationTokens"`
	EstimatedRuntimeTokens      int                `json:"estimatedRuntimeTokens"`
	PreviousTraceID             string             `json:"previousTraceId"`
	PrefixChange                string             `json:"prefixChange"`
	SharedMessages              int                `json:"sharedMessages"`
	MessageCount                int                `json:"messageCount"`
	InputTokens                 int                `json:"inputTokens"`
	OutputTokens                int                `json:"outputTokens"`
	CachedTokens                int                `json:"cachedTokens"`
	TokensKnown                 bool               `json:"tokensKnown"`
	CacheReadKnown              bool               `json:"cacheReadKnown"`
	Attempts                    []InferenceAttempt `json:"attempts"`
}

type InferenceAttempt struct {
	Number         int    `json:"number"`
	Transport      string `json:"transport"`
	DurationMillis int    `json:"durationMillis"`
	SentBytes      int    `json:"sentBytes"`
	Status         int    `json:"status"`
	Outcome        string `json:"outcome"`
	InputTokens    int    `json:"inputTokens"`
	OutputTokens   int    `json:"outputTokens"`
	CachedTokens   int    `json:"cachedTokens"`
	TokensKnown    bool   `json:"tokensKnown"`
	CacheReadKnown bool   `json:"cacheReadKnown"`
}
