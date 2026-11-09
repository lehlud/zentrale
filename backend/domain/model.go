// Package domain defines the resources shared by the harness and its API.
package domain

import "time"

// Storage roots expose only a managed subdirectory of the current project.
type StorageVolume struct {
	ID          string `json:"id" yaml:"id"`
	Path        string `json:"path" yaml:"path"`
	Shared      bool   `json:"shared" yaml:"shared"`
	Description string `json:"description" yaml:"description"`
}

type StorageMount struct {
	ID        string `json:"id"`
	HostPath  string `json:"hostPath"`
	GuestPath string `json:"guestPath"`
}

type Machine struct {
	WorkspaceRoot string `json:"workspaceRoot" yaml:"workspace_root"`

	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`

	Host  string   `json:"host" yaml:"host"`
	Port  int      `json:"port" yaml:"port"`
	User  string   `json:"user" yaml:"user"`
	KeyID int      `json:"keyId" yaml:"-"`
	Jumps []string `json:"jumps" yaml:"jumps"`

	Properties []string `json:"properties" yaml:"properties"`

	Sandbox    string   `json:"sandbox" yaml:"sandbox"`
	VMType     string   `json:"vmType" yaml:"vm_type"`
	GPUDevices []string `json:"gpuDevices" yaml:"gpu_devices"`

	Storage       []StorageVolume `json:"storage" yaml:"storage"`
	QuotaCommand  []string        `json:"quotaCommand" yaml:"quota_command"`
	StorageMounts []StorageMount  `json:"-" yaml:"-"`

	SingleUser bool `json:"singleUser" yaml:"single_user"`
	Containers bool `json:"containers" yaml:"containers"`

	// MicroVMCommand optionally overrides the built-in QEMU/KVM runner with an
	// administrator-installed runner using the documented root/network protocol.
	MicroVMCommand string `json:"microVMCommand" yaml:"microvm_command"`
}

type Pool struct {
	// Managed by Zentrale. Pool YAML never accepts SSH key identifiers.
	KeyID      int    `json:"keyId" yaml:"-"`
	KnownHosts string `json:"knownHosts" yaml:"known_hosts"`

	WorkspaceRoot string `json:"workspaceRoot" yaml:"workspace_root"`

	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`

	MaxMachines int  `json:"maxMachines" yaml:"max_machines"`
	SharedHome  bool `json:"sharedHome" yaml:"shared_home"`
	SingleUser  bool `json:"singleUser" yaml:"single_user"`

	Sandbox string `json:"sandbox" yaml:"sandbox"`
	VMType  string `json:"vmType" yaml:"vm_type"`

	Storage      []StorageVolume `json:"storage" yaml:"storage"`
	QuotaCommand []string        `json:"quotaCommand" yaml:"quota_command"`

	Machines []Machine `json:"machines" yaml:"machines"`
}

type Source struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`

	URL      string `json:"url"`
	KeyID    int    `json:"keyId"`
	ReadOnly bool   `json:"readOnly"`

	BindingID      string `json:"bindingId"`
	RepositoryName string `json:"repositoryName"`
	ProjectRoot    bool   `json:"projectRoot"`
}

type Project struct {
	// Managed workspaces derive their location from the selected machine or pool.
	// Directory also supports explicit user-selected paths and resolved runtime views.
	ManagedWorkspace bool `json:"managedWorkspace"`

	ID   string `json:"id"`
	Name string `json:"name"`

	Directory  string   `json:"directory"`
	MachineIDs []string `json:"machineIds"`
	PoolIDs    []string `json:"poolIds"`

	CommitName  string `json:"commitName"`
	CommitEmail string `json:"commitEmail"`

	GitConnectionID string `json:"gitConnectionId"`
	RepositoryID    string `json:"repositoryId"`
	RepositoryName  string `json:"repositoryName"`
}

// Provider stores a connection/account. Model selection belongs to ModelChoice.
type Provider struct {
	Connected    bool   `json:"connected"`
	AccountLabel string `json:"accountLabel"`

	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`

	BaseURL     string       `json:"baseUrl"`
	Credential  string       `json:"credential,omitempty"`
	AuthMode    string       `json:"authMode"`
	BillingMode string       `json:"billingMode"`
	OAuth       *OAuthTokens `json:"oauth,omitempty"`

	CatalogStatus string `json:"catalogStatus"`
	CatalogError  string `json:"catalogError"`
	ModelCount    int    `json:"modelCount"`
}

type OAuthTokens struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
	AccountID    string    `json:"accountId"`
	AccountLabel string    `json:"accountLabel"`
}

type LanguageModel struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerId"`
	Name       string `json:"name"`

	ContextWindow int `json:"contextWindow"`

	InputPrice        float64 `json:"inputPrice"`
	OutputPrice       float64 `json:"outputPrice"`
	CachedPrice       float64 `json:"cachedPrice"`
	PricesKnown       bool    `json:"pricesKnown"`
	MetadataEstimated bool    `json:"metadataEstimated"`
}

type ModelMetadata struct {
	ContextWindow int `json:"contextWindow"`

	InputPrice  float64 `json:"inputPrice"`
	OutputPrice float64 `json:"outputPrice"`
	CachedPrice float64 `json:"cachedPrice"`
	PricesKnown bool    `json:"pricesKnown"`
}

type UsageSummary struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	CachedTokens int `json:"cachedTokens"`

	CacheInputTokens int `json:"cacheInputTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`
	CacheWriteTokens int `json:"cacheWriteTokens"`

	UnknownTokenCounts int `json:"unknownTokenCounts"`
	UnknownCacheWrites int `json:"unknownCacheWrites"`

	RealCost      float64 `json:"realCost"`
	FictionalCost float64 `json:"fictionalCost"`
	Estimated     bool    `json:"estimated"`
	UnknownPrices int     `json:"unknownPrices"`

	RequestCount int `json:"requestCount"`
}

type ModelChoice struct {
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
}

type ModelDefaults struct {
	Main     ModelChoice `json:"main"`
	Approver ModelChoice `json:"approver"`
}

type ProviderLogin struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerId"`
	Method     string `json:"method"`
	Status     string `json:"status"`

	VerificationURL string `json:"verificationUrl"`
	UserCode        string `json:"userCode"`

	Interval  int       `json:"interval"`
	ExpiresAt time.Time `json:"expiresAt"`

	Error string `json:"error"`
}

type Agent struct {
	ID         string `json:"id"`
	ProjectID  string `json:"projectId"`
	ParentID   string `json:"parentId"`
	ApproverID string `json:"approverId"`
	Name       string `json:"name"`

	Role       string `json:"role"`
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`

	Status    string `json:"status"`
	MachineID string `json:"machineId"`
	Error     string `json:"error"`

	ContextStart  int    `json:"contextStart"`
	ContextTokens int    `json:"contextTokens"`
	Summary       string `json:"summary"`
}

type Message struct {
	ID      string `json:"id"`
	AgentID string `json:"agentId"`
	Role    string `json:"role"`

	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`

	Wire     string `json:"wire,omitempty"`
	CallID   string `json:"callId,omitempty"`
	ToolName string `json:"toolName,omitempty"`
}

type Note struct {
	ID      string `json:"id"`
	AgentID string `json:"agentId"`

	Title   string `json:"title"`
	Content string `json:"content"`
}

type Question struct {
	ID      string `json:"id"`
	AgentID string `json:"agentId"`

	Title   string   `json:"title"`
	Options []string `json:"options"`

	Status string `json:"status"`
	Answer string `json:"answer"`
}

type Approval struct {
	ID      string `json:"id"`
	AgentID string `json:"agentId"`

	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`

	Allowed   bool      `json:"allowed"`
	Reason    string    `json:"reason"`
	Blanket   bool      `json:"blanket"`
	CreatedAt time.Time `json:"createdAt"`

	Wire     string `json:"wire,omitempty"`
	CallID   string `json:"callId,omitempty"`
	ToolName string `json:"toolName,omitempty"`
}

type Usage struct {
	ID         string `json:"id"`
	AgentID    string `json:"agentId"`
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`

	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	CachedTokens int `json:"cachedTokens"`

	TokensKnown          bool `json:"tokensKnown"`
	CacheReadKnown       bool `json:"cacheReadKnown"`
	CacheWriteTokens     int  `json:"cacheWriteTokens"`
	CacheWriteLongTokens int  `json:"cacheWriteLongTokens"`
	CacheWriteKnown      bool `json:"cacheWriteKnown"`

	RealCost      float64 `json:"realCost"`
	FictionalCost float64 `json:"fictionalCost"`
	Estimated     bool    `json:"estimated"`
	CostKnown     bool    `json:"costKnown"`
}

type Job struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	MachineID string `json:"machineId"`

	Command string `json:"command"`

	Status    string    `json:"status"`
	Output    string    `json:"output"`
	Error     string    `json:"error"`
	StartedAt time.Time `json:"startedAt"`
}

type Event struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	AgentID string `json:"agentId"`

	Data      string    `json:"data"`
	CreatedAt time.Time `json:"createdAt"`
}
