// Package auth implements password-only authentication and revocable sessions.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

type Service struct {
	Store *datastore.Instance

	mu       sync.Mutex
	attempts map[string][]time.Time
}

func New(store *datastore.Instance, envHash string) (*Service, error) {
	s := &Service{Store: store, attempts: make(map[string][]time.Time)}

	if envHash != "" {
		if _, _, _, _, _, err := parse(envHash); err != nil {
			return nil, fmt.Errorf("LOGIN_ARGON2ID: %w", err)
		}

		if _, err := store.InitializePassword(envHash); err != nil {
			return nil, err
		}
	}

	return s, nil
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
	v, err := s.Store.Setting("login_argon2id")

	return v == "", err
}

func (s *Service) Setup(password string) error {
	return s.setup(password, "")
}

func (s *Service) SetupWithIdentity(password string, identity domain.CommitIdentity) error {
	identity, err := domain.NormalizeCommitIdentity(identity)
	if err != nil {
		return err
	}

	encoded, err := json.Marshal(identity)
	if err != nil {
		return err
	}

	return s.setup(password, string(encoded))
}

func (s *Service) setup(password, identity string) error {
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

	ok, err := s.Store.InitializeSetup(hash, identity)
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
	hash, err := s.Store.Setting("login_argon2id")
	if err != nil {
		return "", err
	}

	if hash == "" || !Verify(hash, password) {
		return "", errors.New("invalid password")
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	token := base64.RawURLEncoding.EncodeToString(b)
	if err := s.Store.CreateSession(TokenHash(token), now.Add(24*time.Hour)); err != nil {
		return "", err
	}

	delete(s.attempts, client)

	return token, nil
}

func (s *Service) Valid(token string) bool {
	if len(token) != 43 {
		return false
	}

	ok, err := s.Store.SessionValid(TokenHash(token))

	return err == nil && ok
}

func (s *Service) Logout(token string) error {
	return s.Store.DeleteSession(TokenHash(token))
}
