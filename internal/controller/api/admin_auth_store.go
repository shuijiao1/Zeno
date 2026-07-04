package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const settingKeyAdminPasswordHash = "admin_password_hash"

var (
	errInvalidAdminLogin          = errors.New("invalid admin login")
	errInvalidAdminPasswordUpdate = errors.New("invalid admin password update")
)

type AdminSession struct {
	Username string `json:"username"`
	Token    string `json:"token"`
}

func (s *SQLiteStore) AdminLogin(ctx context.Context, username, password, fallbackHash string) (AdminSession, error) {
	if strings.TrimSpace(username) != "admin" || !s.adminPasswordMatches(ctx, password, fallbackHash) {
		return AdminSession{}, errInvalidAdminLogin
	}
	token, err := randomToken()
	if err != nil {
		return AdminSession{}, err
	}
	if err := s.createAdminSession(ctx, token); err != nil {
		return AdminSession{}, err
	}
	return AdminSession{Username: "admin", Token: token}, nil
}

func (s *SQLiteStore) AuthorizeAdminSession(ctx context.Context, token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}
	now := time.Now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `
		UPDATE admin_sessions
		SET last_seen_at = ?
		WHERE token_hash = ?
	`, now, HashAdminToken(token))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *SQLiteStore) RevokeAdminSession(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash = ?`, HashAdminToken(token))
	return err
}

func (s *SQLiteStore) AdminPasswordConfigured(ctx context.Context) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingKeyAdminPasswordHash).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(value) != "", nil
}

func (s *SQLiteStore) UpdateAdminPassword(ctx context.Context, currentPassword, newPassword, fallbackHash string) (AdminSession, error) {
	currentPassword = strings.TrimSpace(currentPassword)
	newPassword = strings.TrimSpace(newPassword)
	if currentPassword == "" || len([]rune(newPassword)) < 8 || len([]rune(newPassword)) > 128 {
		return AdminSession{}, errInvalidAdminPasswordUpdate
	}
	if !s.adminPasswordMatches(ctx, currentPassword, fallbackHash) {
		return AdminSession{}, errInvalidAdminPasswordUpdate
	}
	passwordHash, err := hashAdminPassword(newPassword)
	if err != nil {
		return AdminSession{}, err
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
	`, settingKeyAdminPasswordHash, passwordHash, now); err != nil {
		return AdminSession{}, err
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
	return AdminSession{Username: "admin", Token: token}, nil
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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_sessions (token_hash, created_at, last_seen_at)
		VALUES (?, ?, ?)
	`, HashAdminToken(token), now, now)
	return err
}
