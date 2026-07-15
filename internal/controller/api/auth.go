package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	adminPasswordArgonMemoryKB = 64 * 1024
	adminPasswordArgonTime     = 3
	adminPasswordArgonThreads  = 2
	adminPasswordArgonKeyLen   = 32

	adminPasswordArgonMaxMemoryKB  = 64 * 1024
	adminPasswordArgonMaxTime      = 4
	adminPasswordArgonMaxThreads   = 4
	adminPasswordArgonMaxKeyLen    = 64
	adminPasswordArgonMaxSaltBytes = 64
	adminPasswordArgonMaxParallel  = 2
	adminPasswordArgonMaxQueued    = 8
)

var (
	adminArgon2Slots      = make(chan struct{}, adminPasswordArgonMaxParallel)
	adminArgon2Admissions = make(chan struct{}, adminPasswordArgonMaxQueued)
)

const dummyAdminPasswordHash = "argon2id:v=19:m=65536:t=3:p=2:emVuby1kdW1teS1zYWx0:MfaHhKQHaOt+QsALfIOerW4EtUmf5zKMiHhxvflHstY"

func hashAgentToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func HashAdminToken(token string) string {
	return hashAgentToken(strings.TrimSpace(token))
}

func adminTokenMatches(expectedHash, token string) bool {
	if expectedHash == "" || strings.TrimSpace(token) == "" {
		return false
	}
	computed := HashAdminToken(token)
	return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(computed)) == 1
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func hashAdminPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := deriveAdminArgon2ID([]byte(strings.TrimSpace(password)), salt, adminPasswordArgonTime, adminPasswordArgonMemoryKB, adminPasswordArgonThreads, adminPasswordArgonKeyLen)
	return fmt.Sprintf("argon2id:v=19:m=%d:t=%d:p=%d:%s:%s", adminPasswordArgonMemoryKB, adminPasswordArgonTime, adminPasswordArgonThreads, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func reserveAdminArgon2Request() (func(), bool) {
	select {
	case adminArgon2Admissions <- struct{}{}:
		return func() { <-adminArgon2Admissions }, true
	default:
		return nil, false
	}
}

func deriveAdminArgon2ID(password, salt []byte, timeCost uint32, memoryKB uint32, threads uint8, keyLen uint32) []byte {
	adminArgon2Slots <- struct{}{}
	defer func() { <-adminArgon2Slots }()
	return argon2.IDKey(password, salt, timeCost, memoryKB, threads, keyLen)
}

func consumeDummyAdminPasswordKDF(password string) {
	_ = adminArgon2PasswordMatches(dummyAdminPasswordHash, password)
}

func hashAdminPasswordWithSalt(password, salt string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(password) + ":" + salt))
	return fmt.Sprintf("sha256:%s:%s", salt, hex.EncodeToString(sum[:]))
}

func adminArgon2PasswordMatches(storedHash, password string) bool {
	parts := strings.Split(storedHash, ":")
	if len(parts) != 7 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false
	}
	memoryKB, ok := parseKDFParam(parts[2], "m")
	if !ok || memoryKB > adminPasswordArgonMaxMemoryKB {
		return false
	}
	timeCost, ok := parseKDFParam(parts[3], "t")
	if !ok || timeCost > adminPasswordArgonMaxTime {
		return false
	}
	threads, ok := parseKDFParam(parts[4], "p")
	if !ok || threads == 0 || threads > adminPasswordArgonMaxThreads {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(salt) == 0 || len(salt) > adminPasswordArgonMaxSaltBytes {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[6])
	if err != nil || len(expected) == 0 || len(expected) > adminPasswordArgonMaxKeyLen {
		return false
	}
	computed := deriveAdminArgon2ID([]byte(strings.TrimSpace(password)), salt, uint32(timeCost), uint32(memoryKB), uint8(threads), uint32(len(expected)))
	return subtle.ConstantTimeCompare(expected, computed) == 1
}

func parseKDFParam(raw, key string) (int, bool) {
	prefix := key + "="
	if !strings.HasPrefix(raw, prefix) {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimPrefix(raw, prefix))
	return value, err == nil && value > 0
}

func validAdminUsername(username string) bool {
	username = strings.TrimSpace(username)
	if len([]rune(username)) < 3 || len([]rune(username)) > 64 {
		return false
	}
	for _, char := range username {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func adminPasswordMatches(storedHash, fallbackHash, password string) bool {
	password = strings.TrimSpace(password)
	if password == "" {
		consumeDummyAdminPasswordKDF(password)
		return false
	}
	storedHash = strings.TrimSpace(storedHash)
	if strings.HasPrefix(storedHash, "argon2id:") {
		return adminArgon2PasswordMatches(storedHash, password)
	}
	if strings.HasPrefix(storedHash, "sha256:") {
		parts := strings.Split(storedHash, ":")
		if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
			return false
		}
		computed := hashAdminPasswordWithSalt(password, parts[1])
		return subtle.ConstantTimeCompare([]byte(storedHash), []byte(computed)) == 1
	}
	if storedHash != "" {
		matched := adminTokenMatches(storedHash, password)
		if !matched {
			consumeDummyAdminPasswordKDF(password)
		}
		return matched
	}
	matched := adminTokenMatches(fallbackHash, password)
	if !matched {
		consumeDummyAdminPasswordKDF(password)
	}
	return matched
}

func (s *SQLiteStore) AuthorizeAgent(ctx context.Context, nodeID, token string) (bool, error) {
	if nodeID == "" || token == "" {
		return false, nil
	}
	computed := hashAgentToken(token)
	now := time.Now().UTC().Unix()
	var storedHash string
	var pendingHash sql.NullString
	var pendingExpiresAt sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT token_hash, pending_token_hash, pending_token_expires_at
		FROM nodes
		WHERE id = ? AND disabled = 0
	`, nodeID).Scan(&storedHash, &pendingHash, &pendingExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(computed)) == 1 {
		return true, nil
	}
	if !pendingHash.Valid || !pendingExpiresAt.Valid || pendingExpiresAt.Int64 <= now || subtle.ConstantTimeCompare([]byte(pendingHash.String), []byte(computed)) != 1 {
		return false, nil
	}
	// Enrollment stages a second hash without invalidating the currently
	// running Agent. The first authenticated use of the staged credential
	// atomically promotes it and retires the old hash. The read-only checks above
	// ensure an invalid credential never enters SQLite's single-writer path.
	result, err := s.db.ExecContext(ctx, `
		UPDATE nodes
		SET token_hash = pending_token_hash,
			pending_token_hash = NULL,
			pending_token_expires_at = NULL,
			updated_at = ?
		WHERE id = ?
		  AND disabled = 0
		  AND pending_token_hash = ?
		  AND pending_token_expires_at > ?
	`, now, nodeID, computed, now)
	if err != nil {
		return false, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return false, err
	} else if affected == 1 {
		return true, nil
	}
	// A concurrent request may have promoted the same pending credential after
	// our read. Re-read the current hash so every request carrying that valid
	// credential succeeds without weakening the conditional promotion.
	err = s.db.QueryRowContext(ctx, `
		SELECT token_hash
		FROM nodes
		WHERE id = ? AND disabled = 0
	`, nodeID).Scan(&storedHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(storedHash), []byte(computed)) == 1, nil
}
