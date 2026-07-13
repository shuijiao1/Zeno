package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	notificationCredentialKeySize          = 32
	notificationCredentialCiphertextPrefix = "zeno:notification-credential:v1:aes-256-gcm:"
	notificationCredentialAADDomain        = "zeno.notification.credential\x00v1\x00"
)

var (
	errNotificationCredentialKeyRequired       = errors.New("notification credential key required")
	errNotificationCredentialCiphertextInvalid = errors.New("invalid notification credential ciphertext")
	errNotificationCredentialPlaintext         = errors.New("unencrypted notification credential")
)

type notificationCredentialCipher struct {
	aead cipher.AEAD
}

func newNotificationCredentialCipher(key []byte) (*notificationCredentialCipher, error) {
	if len(key) != notificationCredentialKeySize {
		return nil, fmt.Errorf("notification credential key must be %d bytes", notificationCredentialKeySize)
	}
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	block, err := aes.NewCipher(keyCopy)
	for i := range keyCopy {
		keyCopy[i] = 0
	}
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &notificationCredentialCipher{aead: aead}, nil
}

func (cipher *notificationCredentialCipher) encrypt(channelID, channelType, credential string) (string, error) {
	if cipher == nil || cipher.aead == nil {
		return "", errNotificationCredentialKeyRequired
	}
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return "", errInvalidAdminNotificationChannelWrite
	}
	nonce := make([]byte, cipher.aead.NonceSize())
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		return "", err
	}
	plaintext := []byte(credential)
	sealed := cipher.aead.Seal(nil, nonce, plaintext, notificationCredentialAAD(channelID, channelType))
	for i := range plaintext {
		plaintext[i] = 0
	}
	payload := make([]byte, 0, len(nonce)+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	for i := range payload {
		payload[i] = 0
	}
	return notificationCredentialCiphertextPrefix + encoded, nil
}

func (cipher *notificationCredentialCipher) decrypt(channelID, channelType, storedCredential string) (string, error) {
	if cipher == nil || cipher.aead == nil {
		return "", errNotificationCredentialKeyRequired
	}
	storedCredential = strings.TrimSpace(storedCredential)
	if storedCredential == "" {
		return "", errInvalidAdminNotificationChannelWrite
	}
	if !isEncryptedNotificationCredential(storedCredential) {
		return "", errNotificationCredentialPlaintext
	}
	encoded := strings.TrimPrefix(storedCredential, notificationCredentialCiphertextPrefix)
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", errNotificationCredentialCiphertextInvalid
	}
	defer zeroBytes(payload)
	nonceSize := cipher.aead.NonceSize()
	if len(payload) <= nonceSize+cipher.aead.Overhead() {
		return "", errNotificationCredentialCiphertextInvalid
	}
	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	plaintext, err := cipher.aead.Open(nil, nonce, ciphertext, notificationCredentialAAD(channelID, channelType))
	if err != nil {
		return "", errNotificationCredentialCiphertextInvalid
	}
	defer zeroBytes(plaintext)
	credential := strings.TrimSpace(string(plaintext))
	if credential == "" {
		return "", errInvalidAdminNotificationChannelWrite
	}
	return credential, nil
}

func notificationCredentialAAD(channelID, channelType string) []byte {
	channelID = strings.TrimSpace(channelID)
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if channelType == "" {
		channelType = "telegram"
	}
	return []byte(notificationCredentialAADDomain + channelType + "\x00" + channelID)
}

func isEncryptedNotificationCredential(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), notificationCredentialCiphertextPrefix)
}

func (s *SQLiteStore) ConfigureNotificationCredentialEncryption(ctx context.Context, key []byte) error {
	cipher, err := newNotificationCredentialCipher(key)
	if err != nil {
		return err
	}
	if err := s.migrateNotificationCredentialsToEncrypted(ctx, cipher); err != nil {
		return err
	}
	s.notificationCredentialMu.Lock()
	s.notificationCredentialCipher = cipher
	s.notificationCredentialMu.Unlock()
	return nil
}

func (s *SQLiteStore) RequireNotificationCredentialKeyForExistingCredentials(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store is closed")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_channels WHERE TRIM(COALESCE(credential, '')) <> ''`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errNotificationCredentialKeyRequired
	}
	return nil
}

func (s *SQLiteStore) notificationCredentialCipherSnapshot() *notificationCredentialCipher {
	if s == nil {
		return nil
	}
	s.notificationCredentialMu.RLock()
	cipher := s.notificationCredentialCipher
	s.notificationCredentialMu.RUnlock()
	return cipher
}

func (s *SQLiteStore) encryptNotificationCredentialForStorage(channelID, channelType, credential string) (string, error) {
	cipher := s.notificationCredentialCipherSnapshot()
	if cipher == nil {
		return "", errNotificationCredentialKeyRequired
	}
	return cipher.encrypt(channelID, channelType, credential)
}

func (s *SQLiteStore) decryptNotificationCredentialFromStorage(channelID, channelType, storedCredential string) (string, error) {
	cipher := s.notificationCredentialCipherSnapshot()
	if cipher == nil {
		return "", errNotificationCredentialKeyRequired
	}
	credential, err := cipher.decrypt(channelID, channelType, storedCredential)
	if err != nil {
		return "", err
	}
	return credential, nil
}

func (s *SQLiteStore) migrateNotificationCredentialsToEncrypted(ctx context.Context, credentialCipher *notificationCredentialCipher) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store is closed")
	}
	if credentialCipher == nil {
		return errNotificationCredentialKeyRequired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	rows, err := tx.QueryContext(ctx, `
		SELECT id, credential
		FROM notification_channels
		WHERE TRIM(COALESCE(credential, '')) <> ''
		ORDER BY id ASC
	`)
	if err != nil {
		return err
	}
	type credentialUpdate struct {
		channelID string
		previous  string
		next      string
	}
	updates := make([]credentialUpdate, 0)
	for rows.Next() {
		var channelID string
		var storedCredential string
		if err := rows.Scan(&channelID, &storedCredential); err != nil {
			_ = rows.Close()
			return err
		}
		trimmedCredential := strings.TrimSpace(storedCredential)
		if isEncryptedNotificationCredential(trimmedCredential) {
			if _, err := credentialCipher.decrypt(channelID, "telegram", trimmedCredential); err != nil {
				_ = rows.Close()
				return err
			}
			continue
		}
		encryptedCredential, err := credentialCipher.encrypt(channelID, "telegram", trimmedCredential)
		if err != nil {
			_ = rows.Close()
			return err
		}
		updates = append(updates, credentialUpdate{channelID: channelID, previous: storedCredential, next: encryptedCredential})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, update := range updates {
		result, err := tx.ExecContext(ctx, `
			UPDATE notification_channels
			SET credential = ?
			WHERE id = ? AND credential = ?
		`, update.next, update.channelID, update.previous)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("notification credential migration conflict")
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
