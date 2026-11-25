// Package auth implements local or OIDC authentication and revocable sessions.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

type Service struct {
	Store      *datastore.Instance
	OIDCIssuer string

	mu       sync.Mutex
	attempts map[string][]time.Time
}

func New(store *datastore.Instance, envHash string) (*Service, error) {
	return NewWithIssuer(store, envHash, "")
}

const DefaultUsername = "admin"

func NewWithIssuer(store *datastore.Instance, envHash, issuer string) (*Service, error) {
	s := &Service{Store: store, OIDCIssuer: issuer, attempts: make(map[string][]time.Time)}
	initial, err := store.UserAccount(domain.InitialUserID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && initial.OIDCIssuer != issuer {
		return nil, errors.New("the configured authentication mode or OpenID Connect issuer does not match the initial administrator")
	}

	legacy, err := store.Setting("login_argon2id")
	if err != nil {
		return nil, err
	}

	if issuer != "" {
		if envHash != "" {
			return nil, errors.New("LOGIN_ARGON2ID cannot be used with OIDC")
		}
		if legacy != "" {
			return nil, errors.New("a legacy password installation cannot be claimed through OIDC")
		}
		return s, nil
	}

	if envHash != "" {
		if _, _, _, _, _, err := parse(envHash); err != nil {
			return nil, fmt.Errorf("LOGIN_ARGON2ID: %w", err)
		}
	}

	// Existing single-user installations keep the same password. Previously
	// issued anonymous session tokens are deliberately not carried forward.
	if legacy != "" {
		envHash = legacy
	}
	if envHash != "" {
		if _, err := store.BootstrapAccount(initialAccount(DefaultUsername, envHash), ""); err != nil {
			return nil, err
		}
	}

	return s, nil
}

func initialAccount(username, passwordHash string) datastore.Account {
	return datastore.Account{User: domain.User{
		ID: domain.InitialUserID, Username: username, Name: username, Role: domain.RoleAdmin,
	}, PasswordHash: passwordHash}
}

func Hash(password string) (string, error) {
	if len(password) < 10 || len(password) > 1024 {
		return "", errors.New("password must contain 10–1024 bytes")
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)

	return fmt.Sprintf(
		"$argon2id$v=19$m=65536,t=3,p=2$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func parse(hash string) (uint32, uint32, uint8, []byte, []byte, error) {
	fail := func() (uint32, uint32, uint8, []byte, []byte, error) {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2id PHC hash")
	}

	p := strings.Split(hash, "$")
	if len(p) != 6 || p[1] != "argon2id" || p[2] != "v=19" {
		return fail()
	}

	params := strings.Split(p[3], ",")
	if len(params) != 3 {
		return fail()
	}

	v := map[string]uint64{}
	for _, param := range params {
		kv := strings.Split(param, "=")
		if len(kv) != 2 {
			return fail()
		}

		n, err := strconv.ParseUint(kv[1], 10, 32)
		if err != nil {
			return fail()
		}

		v[kv[0]] = n
	}

	m, t, lanes := v["m"], v["t"], v["p"]
	if len(v) != 3 || lanes < 1 || lanes > 16 || m < 8*lanes || m > 256*1024 || t < 1 || t > 10 {
		return fail()
	}

	salt, err := base64.RawStdEncoding.DecodeString(p[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return fail()
	}

	key, err := base64.RawStdEncoding.DecodeString(p[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return fail()
	}

	return uint32(m), uint32(t), uint8(lanes), salt, key, nil
}

func Verify(hash, password string) bool {
	m, t, p, salt, key, err := parse(hash)
	if err != nil || len(password) > 1024 {
		return false
	}

	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(key)))

	return subtle.ConstantTimeCompare(got, key) == 1
}

func (s *Service) NeedsSetup() (bool, error) {
	if s.OIDCIssuer != "" {
		return false, nil
	}
	exists, err := s.Store.HasUsers()
	return !exists, err
}

func (s *Service) Setup(password string) error {
	return s.setup(DefaultUsername, password, "")
}

func (s *Service) SetupWithIdentity(password string, identity domain.CommitIdentity) error {
	return s.SetupAccount(DefaultUsername, password, identity)
}

func (s *Service) SetupAccount(username, password string, identity domain.CommitIdentity) error {
	identity, err := domain.NormalizeCommitIdentity(identity)
	if err != nil {
		return err
	}

	encoded, err := json.Marshal(identity)
	if err != nil {
		return err
	}

	return s.setup(username, password, string(encoded))
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func NormalizeUsername(username string) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) {
		return "", errors.New("username must contain 1–64 letters, numbers, dots, dashes or underscores, starting with a letter or number")
	}
	return username, nil
}

func (s *Service) setup(username, password, identity string) error {
	if s.OIDCIssuer != "" {
		return errors.New("password setup is disabled")
	}

	username, err := NormalizeUsername(username)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	need, err := s.NeedsSetup()
	if err != nil {
		return err
	}

	if !need {
		return errors.New("setup is already complete")
	}

	hash, err := Hash(password)
	if err != nil {
		return err
	}

	ok, err := s.Store.BootstrapAccount(initialAccount(username, hash), identity)
	if err != nil {
		return err
	}

	if !ok {
		return errors.New("setup is already complete")
	}

	return nil
}

func TokenHash(token string) string {
	h := sha256.Sum256([]byte(token))

	return hex.EncodeToString(h[:])
}

func (s *Service) Login(password, client string) (string, error) {
	return s.LoginAccount(DefaultUsername, password, client)
}

func (s *Service) LoginAccount(username, password, client string) (string, error) {
	if s.OIDCIssuer != "" {
		return "", errors.New("password login is disabled")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	old := s.attempts[client]
	recent := make([]time.Time, 0, 5)

	for _, t := range old {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}

	if len(recent) >= 5 {
		return "", errors.New("too many login attempts; retry in one minute")
	}

	if len(s.attempts) > 10000 {
		s.attempts = make(map[string][]time.Time)
	}

	s.attempts[client] = append(recent, now)
	account, err := s.Store.PasswordAccount(strings.TrimSpace(username))
	if err != nil || account.Disabled || !Verify(account.PasswordHash, password) {
		return "", errors.New("invalid username or password")
	}

	delete(s.attempts, client)
	return s.createSession(account.ID)
}

func (s *Service) LoginOIDC(issuer, subject, username, name string) (string, error) {
	if issuer == "" || issuer != s.OIDCIssuer || subject == "" {
		return "", errors.New("invalid OpenID Connect identity")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	account, err := s.Store.OIDCAccount(issuer, subject)
	if errors.Is(err, sql.ErrNoRows) {
		username, err = NormalizeUsername(username)
		if err != nil {
			username = DefaultUsername
		}

		account = initialAccount(username, "")
		if strings.TrimSpace(name) != "" {
			account.Name = strings.TrimSpace(name)
		}
		account.OIDCIssuer, account.OIDCSubject = issuer, subject
		created, err := s.Store.BootstrapAccount(account, "")
		if err != nil {
			return "", err
		}
		if !created {
			account, err = s.Store.OIDCAccount(issuer, subject)
			if err != nil {
				return "", errors.New("this OpenID Connect identity has no account")
			}
		}
	} else if err != nil {
		return "", err
	}
	if account.Disabled {
		return "", errors.New("this account is disabled")
	}
	return s.createSession(account.ID)
}

func (s *Service) createSession(userID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	token := base64.RawURLEncoding.EncodeToString(b)
	if err := s.Store.CreateUserSession(TokenHash(token), userID, time.Now().Add(24*time.Hour)); err != nil {
		return "", err
	}

	return token, nil
}

func (s *Service) Valid(token string) bool {
	_, valid := s.User(token)
	return valid
}

func (s *Service) User(token string) (domain.User, bool) {
	if len(token) != 43 {
		return domain.User{}, false
	}

	user, err := s.Store.SessionUser(TokenHash(token), s.OIDCIssuer)
	return user, err == nil
}

func (s *Service) Logout(token string) error {
	return s.Store.DeleteUserSession(TokenHash(token))
}
