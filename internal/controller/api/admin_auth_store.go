package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const (
	settingKeyAdminUsername     = "admin_username"
	settingKeyAdminPasswordHash = "admin_password_hash"
	adminSessionIdleTimeout     = 24 * time.Hour
	adminSessionAbsoluteTimeout = 24 * time.Hour
	adminSessionMaxActive       = 8
	adminSessionLastSeenBucket  = 5 * time.Minute
	adminSessionPruneInterval   = time.Minute
)

var (
	errInvalidAdminLogin          = errors.New("invalid admin login")
	errInvalidAdminPasswordUpdate = errors.New("invalid admin password update")
)

type AdminSession struct {
	Username string `json:"username"`
	Token    string `json:"token"`
}

type AdminAccount struct {
	Username string `json:"username"`
}

func (s *SQLiteStore) AdminLogin(ctx context.Context, username, password, fallbackHash string) (AdminSession, error) {
	account, err := s.AdminAccount(ctx)
	if err != nil {
		return AdminSession{}, err
	}
	passwordOK := s.adminPasswordMatches(ctx, password, fallbackHash)
	if strings.TrimSpace(username) != account.Username || !passwordOK {
		return AdminSession{}, errInvalidAdminLogin
	}
	token, err := randomToken()
	if err != nil {
		return AdminSession{}, err
	}
	if err := s.createAdminSession(ctx, token); err != nil {
		return AdminSession{}, err
	}
	return AdminSession{Username: account.Username, Token: token}, nil
}

func (s *SQLiteStore) AuthorizeAdminSession(ctx context.Context, token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}
	now := time.Now().UTC().Unix()
	if err := s.pruneExpiredAdminSessionsOccasionally(ctx, now); err != nil {
		return false, err
	}
	var createdAt, lastSeenAt int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT created_at, last_seen_at
		FROM admin_sessions
		WHERE token_hash = ?
	`, HashAdminToken(token)).Scan(&createdAt, &lastSeenAt); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if createdAt <= now-int64(adminSessionAbsoluteTimeout.Seconds()) || lastSeenAt <= now-int64(adminSessionIdleTimeout.Seconds()) {
		return false, nil
	}
	if lastSeenAt <= now-int64(adminSessionLastSeenBucket.Seconds()) {
		result, err := s.db.ExecContext(ctx, `
			UPDATE admin_sessions
			SET last_seen_at = ?
			WHERE token_hash = ?
			  AND created_at > ?
			  AND last_seen_at > ?
		`, now, HashAdminToken(token), now-int64(adminSessionAbsoluteTimeout.Seconds()), now-int64(adminSessionIdleTimeout.Seconds()))
		if err != nil {
			return false, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected == 0 {
			return false, nil
		}
	}
	return true, nil
}

func (s *SQLiteStore) RevokeAdminSession(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash = ?`, HashAdminToken(token))
	return err
}

func (s *SQLiteStore) AdminAccount(ctx context.Context) (AdminAccount, error) {
	var username string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingKeyAdminUsername).Scan(&username)
	if err != nil {
		if err == sql.ErrNoRows {
			return AdminAccount{Username: "admin"}, nil
		}
		return AdminAccount{}, err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	return AdminAccount{Username: username}, nil
}

func (s *SQLiteStore) AdminAccountConfigured(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM settings
		WHERE key IN (?, ?) AND TRIM(value) <> ''
	`, settingKeyAdminUsername, settingKeyAdminPasswordHash).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ResetAdminAccount is an offline recovery operation. Callers must stop the
// Controller and protect the supplied password; it resets the username to
// "admin" and revokes every active session in one transaction.
func (s *SQLiteStore) ResetAdminAccount(ctx context.Context, password string) error {
	password = strings.TrimSpace(password)
	if length := len([]rune(password)); length < 8 || length > 128 {
		return errInvalidAdminPasswordUpdate
	}
	passwordHash, err := hashAdminPassword(password)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	for key, value := range map[string]string{
		settingKeyAdminUsername:     "admin",
		settingKeyAdminPasswordHash: passwordHash,
	} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, key, value, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *SQLiteStore) UpdateAdminAccount(ctx context.Context, username, currentPassword, newPassword, fallbackHash string) (AdminSession, error) {
	username = strings.TrimSpace(username)
	currentPassword = strings.TrimSpace(currentPassword)
	newPassword = strings.TrimSpace(newPassword)
	if !validAdminUsername(username) || currentPassword == "" {
		return AdminSession{}, errInvalidAdminPasswordUpdate
	}
	if newPassword != "" && (len([]rune(newPassword)) < 8 || len([]rune(newPassword)) > 128) {
		return AdminSession{}, errInvalidAdminPasswordUpdate
	}
	if !s.adminPasswordMatches(ctx, currentPassword, fallbackHash) {
		return AdminSession{}, errInvalidAdminPasswordUpdate
	}
	passwordHash := ""
	var err error
	if newPassword != "" {
		passwordHash, err = hashAdminPassword(newPassword)
		if err != nil {
			return AdminSession{}, err
		}
	}
	token, err := randomToken()
	if err != nil {
		return AdminSession{}, err
	}
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminSession{}, err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, settingKeyAdminUsername, username, now); err != nil {
		return AdminSession{}, err
	}
	if passwordHash != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, settingKeyAdminPasswordHash, passwordHash, now); err != nil {
			return AdminSession{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_sessions`); err != nil {
		return AdminSession{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_sessions (token_hash, created_at, last_seen_at)
		VALUES (?, ?, ?)
	`, HashAdminToken(token), now, now); err != nil {
		return AdminSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminSession{}, err
	}
	tx = nil
	return AdminSession{Username: username, Token: token}, nil
}

func (s *SQLiteStore) adminPasswordMatches(ctx context.Context, password, fallbackHash string) bool {
	var storedHash string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingKeyAdminPasswordHash).Scan(&storedHash)
	if err != nil && err != sql.ErrNoRows {
		return false
	}
	if err == sql.ErrNoRows {
		storedHash = ""
	}
	return adminPasswordMatches(storedHash, fallbackHash, password)
}

func (s *SQLiteStore) createAdminSession(ctx context.Context, token string) error {
	now := time.Now().UTC().Unix()
	if err := s.pruneExpiredAdminSessions(ctx, now); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_sessions (token_hash, created_at, last_seen_at)
		VALUES (?, ?, ?)
	`, HashAdminToken(token), now, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM admin_sessions
		WHERE token_hash NOT IN (
			SELECT token_hash FROM admin_sessions
			ORDER BY last_seen_at DESC, created_at DESC, token_hash DESC
			LIMIT ?
		)
	`, adminSessionMaxActive); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *SQLiteStore) pruneExpiredAdminSessions(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM admin_sessions
		WHERE created_at <= ? OR last_seen_at <= ?
	`, now-int64(adminSessionAbsoluteTimeout.Seconds()), now-int64(adminSessionIdleTimeout.Seconds()))
	return err
}

func (s *SQLiteStore) pruneExpiredAdminSessionsOccasionally(ctx context.Context, now int64) error {
	s.adminSessionPruneMu.Lock()
	lastPruned := s.adminSessionLastPruned
	if !lastPruned.IsZero() && time.Unix(now, 0).UTC().Sub(lastPruned) < adminSessionPruneInterval {
		s.adminSessionPruneMu.Unlock()
		return nil
	}
	s.adminSessionLastPruned = time.Unix(now, 0).UTC()
	s.adminSessionPruneMu.Unlock()
	return s.pruneExpiredAdminSessions(ctx, now)
}
