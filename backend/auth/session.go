package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

const sessionIssuer = "zentrale"

var sessionParser = jwt.NewParser(
	jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	jwt.WithIssuer(sessionIssuer),
	jwt.WithExpirationRequired(),
)

func newSessionService(store *datastore.Instance, issuer string) (*Service, error) {
	key, err := store.SessionSigningKey()
	if err != nil {
		return nil, fmt.Errorf("load session signing key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("invalid session signing key length")
	}

	return &Service{Store: store, OIDCIssuer: issuer, sessionKey: key, attempts: make(map[string][]time.Time)}, nil
}

func (s *Service) SessionLifetime() time.Duration {
	if s.OIDCIssuer != "" {
		return 4 * time.Hour
	}

	return 7 * 24 * time.Hour
}

func (s *Service) createSession(userID string) (string, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(s.SessionLifetime())
	claims := jwt.RegisteredClaims{
		Issuer:    sessionIssuer,
		Subject:   userID,
		ID:        base64.RawURLEncoding.EncodeToString(nonce),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.sessionKey)
	if err != nil {
		return "", err
	}
	if err := s.Store.CreateUserSession(TokenHash(token), userID, expires); err != nil {
		return "", err
	}

	return token, nil
}

func (s *Service) Valid(token string) bool {
	_, valid := s.User(token)
	return valid
}

func (s *Service) User(token string) (domain.User, bool) {
	if token == "" || len(token) > 4096 {
		return domain.User{}, false
	}

	var claims jwt.RegisteredClaims
	parsed, err := sessionParser.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) {
		return s.sessionKey, nil
	})
	if err != nil || !parsed.Valid || claims.Subject == "" || claims.ID == "" {
		return domain.User{}, false
	}

	// Read current account state from the database. Signed sessions are still
	// revoked on logout, administrator action or account disablement.
	user, err := s.Store.SessionUser(TokenHash(token), s.OIDCIssuer)
	return user, err == nil && user.ID == claims.Subject
}

func (s *Service) Logout(token string) error {
	return s.Store.DeleteUserSession(TokenHash(token))
}
