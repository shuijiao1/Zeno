package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
)

func hashAgentToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
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
