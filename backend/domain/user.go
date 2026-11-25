package domain

const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	// InitialUserID also owns projects and connections from single-user stores.
	InitialUserID = "initial-admin"
)

// User is the public account record. Authentication material stays in datastore.
type User struct {
	ID                string  `json:"id"`
	Username          string  `json:"username"`
	Name              string  `json:"name"`
	Role              string  `json:"role"`
	Disabled          bool    `json:"disabled"`
	CanCreateProjects bool    `json:"canCreateProjects"`
	MonthlyTokenLimit int64   `json:"monthlyTokenLimit"`
	MonthlyCostLimit  float64 `json:"monthlyCostLimit"`
}

func (user User) IsAdmin() bool { return user.Role == RoleAdmin }
