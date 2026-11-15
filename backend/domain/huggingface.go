package domain

import "time"

// HuggingFaceConnection contains public connection metadata, never credentials.
type HuggingFaceConnection struct {
	Connected bool      `json:"connected"`
	UserName  string    `json:"userName"`
	CheckedAt time.Time `json:"checkedAt"`
	AuthMode  string    `json:"authMode"`

	OAuthClientID     string           `json:"oauthClientId"`
	OAuthConfigured   bool             `json:"oauthConfigured"`
	OAuthConfidential bool             `json:"oauthConfidential"`
	Login             HuggingFaceLogin `json:"login"`
}

// HuggingFaceOAuthConfig is input-only. Secrets never enter connection metadata.
type HuggingFaceOAuthConfig struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	PublicClient bool   `json:"publicClient"`
}

type HuggingFaceLogin struct {
	ID               string    `json:"id"`
	AuthorizationURL string    `json:"authorizationUrl"`
	ExpiresAt        time.Time `json:"expiresAt"`
}
