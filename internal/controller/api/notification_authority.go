package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	settingKeyNotificationAuthorityFingerprint = "internal.notification_authority_fingerprint"
	settingKeyNotificationAuthorityKeyID       = "internal.notification_authority_key_id"
)

// AuthorizeNotificationAuthority preserves the original single-key API while
// using a deterministic key id in the persisted authority binding.
func (s *SQLiteStore) AuthorizeNotificationAuthority(ctx context.Context, authorityKey string) (bool, error) {
	authorityKey = strings.TrimSpace(authorityKey)
	if authorityKey == "" {
		return false, nil
	}
	keyID := notificationAuthorityDerivedKeyID(authorityKey)
	return s.AuthorizeNotificationAuthorityKeyring(ctx, keyID, map[string]string{keyID: authorityKey})
}

// AuthorizeNotificationAuthorityKeyring binds delivery authority to an
// external key ring without storing any key in SQLite. If the stored binding
// matches any supplied old key, it is atomically advanced to activeKeyID; a
// subsequent restart can safely remove the old key from the ring.
func (s *SQLiteStore) AuthorizeNotificationAuthorityKeyring(ctx context.Context, activeKeyID string, keys map[string]string) (bool, error) {
	normalizedActiveKeyID := strings.TrimSpace(activeKeyID)
	if normalizedActiveKeyID == "" && len(keys) == 0 {
		return false, nil
	}
	if normalizedActiveKeyID != activeKeyID || !validNotificationCredentialKeyID(normalizedActiveKeyID) || len(keys) == 0 {
		return false, fmt.Errorf("invalid notification authority key ring")
	}
	fingerprints := make(map[string]string, len(keys))
	keyIDs := make([]string, 0, len(keys))
	for rawKeyID, key := range keys {
		keyID := strings.TrimSpace(rawKeyID)
		key = strings.TrimSpace(key)
		if keyID != rawKeyID || !validNotificationCredentialKeyID(keyID) || key == "" {
			return false, fmt.Errorf("invalid notification authority key ring")
		}
		if _, duplicate := fingerprints[keyID]; duplicate {
			return false, fmt.Errorf("invalid notification authority key ring")
		}
		fingerprints[keyID] = notificationAuthorityFingerprint(key)
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	activeFingerprint, ok := fingerprints[normalizedActiveKeyID]
	if !ok {
		return false, fmt.Errorf("active notification authority key id is not in key ring")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer rollbackUnlessCommitted(tx)
	nowUnix := time.Now().UTC().Unix()
	// Write first so concurrent controller startups serialize before deciding
	// who owns an unbound database. A read-then-upsert transaction could let two
	// different keys both observe no binding and each report authorization.
	result, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO NOTHING
	`, settingKeyNotificationAuthorityFingerprint, activeFingerprint, nowUnix)
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if inserted == 1 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, settingKeyNotificationAuthorityKeyID, normalizedActiveKeyID, nowUnix); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		tx = nil
		return true, nil
	}

	var storedFingerprint string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingKeyNotificationAuthorityFingerprint).Scan(&storedFingerprint)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, fmt.Errorf("notification authority binding disappeared")
		}
		return false, err
	}

	matched := false
	for _, keyID := range keyIDs {
		candidate := fingerprints[keyID]
		if len(candidate) == len(storedFingerprint) && subtle.ConstantTimeCompare([]byte(candidate), []byte(storedFingerprint)) == 1 {
			matched = true
		}
	}
	if !matched {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		tx = nil
		return false, nil
	}
	// Matching an old authority proves continuity. Advance both fingerprint and
	// key id in one transaction; neither secret nor key material is persisted.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, settingKeyNotificationAuthorityFingerprint, activeFingerprint, nowUnix); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, settingKeyNotificationAuthorityKeyID, normalizedActiveKeyID, nowUnix); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	tx = nil
	return true, nil
}

func notificationAuthorityFingerprint(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}

func notificationAuthorityDerivedKeyID(key string) string {
	fingerprint := notificationAuthorityFingerprint(key)
	return "authority-" + fingerprint[:16]
}
