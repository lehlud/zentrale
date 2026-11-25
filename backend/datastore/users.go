package datastore

import (
	"database/sql"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
)

type Account struct {
	domain.User
	PasswordHash string
	OIDCIssuer   string
	OIDCSubject  string
}

const userColumns = `id,username,name,role,disabled,can_create_projects,monthly_token_limit,monthly_cost_limit`
const accountColumns = userColumns + `,password_hash,oidc_issuer,oidc_subject`

func scanUser(s interface{ Scan(...any) error }, user *domain.User, suffix ...any) error {
	fields := []any{&user.ID, &user.Username, &user.Name, &user.Role, &user.Disabled,
		&user.CanCreateProjects, &user.MonthlyTokenLimit, &user.MonthlyCostLimit}
	return s.Scan(append(fields, suffix...)...)
}

func (instance *Instance) userAccount(where string, args ...any) (Account, error) {
	var account Account
	row := instance.readDB().QueryRow(`SELECT `+accountColumns+` FROM users WHERE `+where, args...)
	err := scanUser(row, &account.User, &account.PasswordHash, &account.OIDCIssuer, &account.OIDCSubject)
	return account, err
}

func (instance *Instance) UserAccount(id string) (Account, error) {
	return instance.userAccount(`id=?`, id)
}

func (instance *Instance) AccountByUsername(username string) (Account, error) {
	return instance.userAccount(`username=?`, username)
}

func (instance *Instance) PasswordAccount(username string) (Account, error) {
	return instance.userAccount(`username=? AND password_hash<>''`, username)
}

func (instance *Instance) OIDCAccount(issuer, subject string) (Account, error) {
	return instance.userAccount(`oidc_issuer=? AND oidc_subject=? AND oidc_issuer<>''`, issuer, subject)
}

func (instance *Instance) Users() ([]domain.User, error) {
	rows, err := instance.readDB().Query(`SELECT ` + userColumns + ` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []domain.User{}
	for rows.Next() {
		var user domain.User
		if err := scanUser(rows, &user); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (instance *Instance) HasUsers() (bool, error) {
	var exists bool
	err := instance.readDB().QueryRow(`SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists)
	return exists, err
}

// BootstrapAccount makes first-admin selection atomic across concurrent callbacks.
func (instance *Instance) BootstrapAccount(account Account, commitIdentity string) (bool, error) {
	tx, err := instance.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var id string
	err = tx.QueryRow(`INSERT INTO users (`+accountColumns+`)
		SELECT ?,?,?,?,?,?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM users) RETURNING id`,
		accountValues(account)...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if commitIdentity != "" {
		_, err := tx.Exec(`INSERT INTO settings VALUES ('commit_identity',?)
			ON CONFLICT(name) DO UPDATE SET value=excluded.value`, commitIdentity)
		if err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func accountValues(account Account) []any {
	return []any{account.ID, account.Username, account.Name, account.Role, account.Disabled,
		account.CanCreateProjects, account.MonthlyTokenLimit, account.MonthlyCostLimit,
		account.PasswordHash, account.OIDCIssuer, account.OIDCSubject}
}

func (instance *Instance) InsertAccount(account Account) error {
	_, err := instance.db.Exec(`INSERT INTO users (`+accountColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		accountValues(account)...)
	return err
}

// UpdateAccount keeps the initial administrator available and revokes existing
// sessions when an account can no longer authenticate with the same credentials.
func (instance *Instance) UpdateAccount(account Account, revoke bool) error {
	if account.ID == domain.InitialUserID && (!account.IsAdmin() || account.Disabled) {
		return errors.New("the initial administrator cannot be disabled or demoted")
	}

	tx, err := instance.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`UPDATE users SET username=?,name=?,role=?,disabled=?,can_create_projects=?,
		monthly_token_limit=?,monthly_cost_limit=?,password_hash=?,oidc_issuer=?,oidc_subject=? WHERE id=?`,
		append(accountValues(account)[1:], account.ID)...)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		if err != nil {
			return err
		}
		return sql.ErrNoRows
	}

	if revoke {
		if _, err := tx.Exec(`DELETE FROM user_sessions WHERE user_id=?`, account.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (instance *Instance) CreateUserSession(tokenHash, userID string, expires time.Time) error {
	if _, err := instance.db.Exec(`DELETE FROM user_sessions WHERE julianday(expires_at)<=julianday('now')`); err != nil {
		return err
	}
	_, err := instance.db.Exec(`INSERT INTO user_sessions VALUES (?,?,?)`, tokenHash, userID, expires)
	return err
}

func (instance *Instance) SessionUser(tokenHash, issuer string) (domain.User, error) {
	var user domain.User
	row := instance.readDB().QueryRow(`SELECT `+userColumns+` FROM users WHERE disabled=0
		AND oidc_issuer=? AND id=(SELECT user_id FROM user_sessions
		WHERE token_hash=? AND julianday(expires_at)>julianday('now'))`, issuer, tokenHash)
	err := scanUser(row, &user)
	return user, err
}

// UserSessionValid lets browser connection flows recheck the initiating
// account after the round trip to their external provider.
func (instance *Instance) UserSessionValid(tokenHash string) (bool, error) {
	var valid bool
	err := instance.readDB().QueryRow(`SELECT EXISTS(SELECT 1 FROM user_sessions s
		JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND u.disabled=0
		AND julianday(s.expires_at)>julianday('now'))`, tokenHash).Scan(&valid)
	return valid, err
}

func (instance *Instance) DeleteUserSession(tokenHash string) error {
	_, err := instance.db.Exec(`DELETE FROM user_sessions WHERE token_hash=?`, tokenHash)
	return err
}
