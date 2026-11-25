package auth

import (
	"crypto/rand"
	"errors"
	"math"
	"strings"
	"unicode"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

// SaveAccount accepts a complete set of account settings. An empty password
// preserves the existing credential; an OIDC subject is fixed after enrollment.
func (s *Service) SaveAccount(actorID string, user domain.User, password, oidcSubject string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	actor, err := s.Store.UserAccount(actorID)
	if err != nil || actor.Disabled || !actor.IsAdmin() || actor.OIDCIssuer != s.OIDCIssuer {
		return domain.User{}, errors.New("administrator required")
	}

	user.Username, err = NormalizeUsername(user.Username)
	if err != nil {
		return domain.User{}, err
	}
	user.Name = strings.TrimSpace(user.Name)
	if user.Name == "" {
		user.Name = user.Username
	}
	if len(user.Name) > 256 || strings.IndexFunc(user.Name, unicode.IsControl) != -1 {
		return domain.User{}, errors.New("invalid display name")
	}
	if user.Role == "" {
		user.Role = domain.RoleUser
	}
	if user.Role != domain.RoleUser && user.Role != domain.RoleAdmin {
		return domain.User{}, errors.New("invalid account role")
	}
	if user.MonthlyTokenLimit < 0 || user.MonthlyCostLimit < 0 ||
		math.IsNaN(user.MonthlyCostLimit) || math.IsInf(user.MonthlyCostLimit, 0) {
		return domain.User{}, errors.New("limits must be nonnegative finite numbers")
	}

	account := datastore.Account{User: user, OIDCIssuer: s.OIDCIssuer}
	creating := user.ID == ""
	if creating {
		account.ID = "user-" + rand.Text()
	} else {
		existing, err := s.Store.UserAccount(user.ID)
		if err != nil {
			return domain.User{}, err
		}
		if existing.Username != user.Username {
			return domain.User{}, errors.New("the username cannot be changed")
		}
		account.PasswordHash = existing.PasswordHash
		account.OIDCSubject = existing.OIDCSubject
	}

	if s.OIDCIssuer != "" {
		if password != "" || (!creating && oidcSubject != "") {
			return domain.User{}, errors.New("the OpenID Connect identity cannot be changed")
		}
		if creating {
			if oidcSubject == "" || len(oidcSubject) > 255 || strings.IndexFunc(oidcSubject, unicode.IsControl) != -1 {
				return domain.User{}, errors.New("a valid OpenID Connect subject is required")
			}
			account.OIDCSubject = oidcSubject
		}
	} else {
		if oidcSubject != "" {
			return domain.User{}, errors.New("OpenID Connect is not configured")
		}
		if creating || password != "" {
			account.PasswordHash, err = Hash(password)
			if err != nil {
				return domain.User{}, err
			}
		}
	}

	if creating {
		err = s.Store.InsertAccount(account)
	} else {
		err = s.Store.UpdateAccount(account, account.Disabled || password != "")
	}
	return account.User, err
}
