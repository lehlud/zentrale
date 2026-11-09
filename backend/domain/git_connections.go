package domain

import "time"

type GitConnection struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	BaseURL    string `json:"baseUrl"`
	ClientID   string `json:"clientId"`
	Configured bool   `json:"configured"`
	Connected  bool   `json:"connected"`
	UserName   string `json:"userName"`
}

type GitConnectionConfig struct {
	Kind         string `json:"kind"`
	BaseURL      string `json:"baseUrl"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type GitLogin struct {
	ID              string    `json:"id"`
	ConnectionID    string    `json:"connectionId"`
	VerificationURL string    `json:"verificationUrl"`
	UserCode        string    `json:"userCode"`
	Status          string    `json:"status"`
	ExpiresAt       time.Time `json:"expiresAt"`
	Interval        int       `json:"interval"`
	Error           string    `json:"error"`
}

type GitNamespace struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Personal bool   `json:"personal"`
}

type GitRepository struct {
	ID                  string `json:"id"`
	Identity            string `json:"identity"`
	Name                string `json:"name"`
	FullName            string `json:"fullName"`
	SSHURL              string `json:"sshUrl"`
	WebURL              string `json:"webUrl"`
	Private             bool   `json:"private"`
	CanManageDeployKeys bool   `json:"canManageDeployKeys"`
}

type GitRepositoryPage struct {
	Items      []GitRepository `json:"items"`
	HasMore    bool            `json:"hasMore"`
	NextOffset int             `json:"nextOffset"`
}

type GitRepositoryInput struct {
	ConnectionID string `json:"connectionId"`
	NamespaceID  string `json:"namespaceId"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Private      bool   `json:"private"`
}
