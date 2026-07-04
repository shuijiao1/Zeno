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
	"strings"
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
	salt, err := randomToken()
	if err != nil {
		return "", err
	}
	return hashAdminPasswordWithSalt(password, salt), nil
}

func hashAdminPasswordWithSalt(password, salt string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(password) + ":" + salt))
	return fmt.Sprintf("sha256:%s:%s", salt, hex.EncodeToString(sum[:]))
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
