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

	"golang.org/x/crypto/argon2"
)

const (
	adminPasswordArgonMemoryKB = 64 * 1024
	adminPasswordArgonTime     = 3
	adminPasswordArgonThreads  = 2
	adminPasswordArgonKeyLen   = 32
)

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
	hash := argon2.IDKey([]byte(strings.TrimSpace(password)), salt, adminPasswordArgonTime, adminPasswordArgonMemoryKB, adminPasswordArgonThreads, adminPasswordArgonKeyLen)
	return fmt.Sprintf("argon2id:v=19:m=%d:t=%d:p=%d:%s:%s", adminPasswordArgonMemoryKB, adminPasswordArgonTime, adminPasswordArgonThreads, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
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
	if !ok {
		return false
	}
	timeCost, ok := parseKDFParam(parts[3], "t")
	if !ok {
		return false
	}
	threads, ok := parseKDFParam(parts[4], "p")
	if !ok || threads == 0 || threads > 255 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[6])
	if err != nil || len(expected) == 0 {
		return false
	}
	computed := argon2.IDKey([]byte(strings.TrimSpace(password)), salt, uint32(timeCost), uint32(memoryKB), uint8(threads), uint32(len(expected)))
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
		return adminTokenMatches(storedHash, password)
	}
	return adminTokenMatches(fallbackHash, password)
}

func (s *SQLiteStore) AuthorizeAgent(ctx context.Context, nodeID, token string) (bool, error) {
	if nodeID == "" || token == "" {
		return false, nil
	}
	var storedHash string
	err := s.db.QueryRowContext(ctx, `
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
	computed := hashAgentToken(token)
	return subtle.ConstantTimeCompare([]byte(storedHash), []byte(computed)) == 1, nil
}
