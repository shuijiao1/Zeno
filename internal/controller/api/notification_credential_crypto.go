package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	notificationCredentialKeySize                = 32
	notificationCredentialLegacyCiphertextPrefix = "zeno:notification-credential:v1:aes-256-gcm:"
	notificationCredentialCiphertextPrefix       = "zeno:notification-credential:v2:aes-256-gcm:"
	notificationCredentialAADDomain              = "zeno.notification.credential\x00v1\x00"
	notificationCredentialMaximumKeyIDLength     = 64
)

var (
	errNotificationCredentialKeyRequired       = errors.New("notification credential key required")
	errNotificationCredentialCiphertextInvalid = errors.New("invalid notification credential ciphertext")
	errNotificationCredentialPlaintext         = errors.New("unencrypted notification credential")
)

type notificationCredentialCipher struct {
	keyID string
	aead  cipher.AEAD
}

type notificationCredentialKeyring struct {
	activeKeyID string
	active      *notificationCredentialCipher
	byID        map[string]*notificationCredentialCipher
	legacyOrder []*notificationCredentialCipher
}

func newNotificationCredentialCipher(key []byte) (*notificationCredentialCipher, error) {
	return newNotificationCredentialCipherWithID(notificationCredentialDerivedKeyID(key), key)
}

func newNotificationCredentialCipherWithID(keyID string, key []byte) (*notificationCredentialCipher, error) {
	normalizedKeyID := strings.TrimSpace(keyID)
	if normalizedKeyID != keyID || !validNotificationCredentialKeyID(normalizedKeyID) {
		return nil, fmt.Errorf("invalid notification credential key id")
	}
	keyID = normalizedKeyID
	if len(key) != notificationCredentialKeySize {
		return nil, fmt.Errorf("notification credential key must be %d bytes", notificationCredentialKeySize)
	}
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	block, err := aes.NewCipher(keyCopy)
	zeroBytes(keyCopy)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &notificationCredentialCipher{keyID: keyID, aead: aead}, nil
}

func newNotificationCredentialKeyring(activeKeyID string, keys map[string][]byte) (*notificationCredentialKeyring, error) {
	normalizedActiveKeyID := strings.TrimSpace(activeKeyID)
	if normalizedActiveKeyID != activeKeyID || !validNotificationCredentialKeyID(normalizedActiveKeyID) || len(keys) == 0 {
		return nil, errNotificationCredentialKeyRequired
	}
	normalizedKeys := make(map[string][]byte, len(keys))
	keyIDs := make([]string, 0, len(keys))
	for rawKeyID, key := range keys {
		keyID := strings.TrimSpace(rawKeyID)
		if keyID != rawKeyID || !validNotificationCredentialKeyID(keyID) {
			return nil, fmt.Errorf("invalid notification credential key id")
		}
		if _, duplicate := normalizedKeys[keyID]; duplicate {
			return nil, fmt.Errorf("duplicate notification credential key id")
		}
		normalizedKeys[keyID] = key
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	ring := &notificationCredentialKeyring{
		activeKeyID: normalizedActiveKeyID,
		byID:        make(map[string]*notificationCredentialCipher, len(keys)),
	}
	for _, keyID := range keyIDs {
		cipher, err := newNotificationCredentialCipherWithID(keyID, normalizedKeys[keyID])
		if err != nil {
			return nil, err
		}
		ring.byID[keyID] = cipher
	}
	ring.active = ring.byID[normalizedActiveKeyID]
	if ring.active == nil {
		return nil, fmt.Errorf("active notification credential key id is not in key ring")
	}
	// Legacy v1 ciphertext had no key id. Try the active key first, then the
	// remaining keys in deterministic order during a rolling rotation.
	ring.legacyOrder = append(ring.legacyOrder, ring.active)
	for _, keyID := range keyIDs {
		if keyID != normalizedActiveKeyID {
			ring.legacyOrder = append(ring.legacyOrder, ring.byID[keyID])
		}
	}
	return ring, nil
}

func (cipher *notificationCredentialCipher) encrypt(channelID, channelType, credential string) (string, error) {
	if cipher == nil || cipher.aead == nil || !validNotificationCredentialKeyID(cipher.keyID) {
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
	sealed := cipher.aead.Seal(nil, nonce, plaintext, notificationCredentialAADV2(channelID, channelType, cipher.keyID))
	zeroBytes(plaintext)
	payload := make([]byte, 0, len(nonce)+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	zeroBytes(payload)
	return notificationCredentialCiphertextPrefix + cipher.keyID + ":" + encoded, nil
}

func (cipher *notificationCredentialCipher) decrypt(channelID, channelType, storedCredential string) (string, error) {
	if cipher == nil || cipher.aead == nil {
		return "", errNotificationCredentialKeyRequired
	}
	storedCredential = strings.TrimSpace(storedCredential)
	if storedCredential == "" {
		return "", errInvalidAdminNotificationChannelWrite
	}
	if strings.HasPrefix(storedCredential, notificationCredentialCiphertextPrefix) {
		keyID, encoded, ok := parseNotificationCredentialV2Envelope(storedCredential)
		if !ok || keyID != cipher.keyID {
			return "", errNotificationCredentialCiphertextInvalid
		}
		return cipher.decryptPayload(channelID, channelType, keyID, encoded, true)
	}
	if strings.HasPrefix(storedCredential, notificationCredentialLegacyCiphertextPrefix) {
		encoded := strings.TrimPrefix(storedCredential, notificationCredentialLegacyCiphertextPrefix)
		return cipher.decryptPayload(channelID, channelType, "", encoded, false)
	}
	return "", errNotificationCredentialPlaintext
}

func (cipher *notificationCredentialCipher) decryptPayload(channelID, channelType, keyID, encoded string, v2 bool) (string, error) {
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
	aad := notificationCredentialAAD(channelID, channelType)
	if v2 {
		aad = notificationCredentialAADV2(channelID, channelType, keyID)
	}
	plaintext, err := cipher.aead.Open(nil, nonce, ciphertext, aad)
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

func (ring *notificationCredentialKeyring) encrypt(channelID, channelType, credential string) (string, error) {
	if ring == nil || ring.active == nil {
		return "", errNotificationCredentialKeyRequired
	}
	return ring.active.encrypt(channelID, channelType, credential)
}

func (ring *notificationCredentialKeyring) decrypt(channelID, channelType, storedCredential string) (string, error) {
	if ring == nil || ring.active == nil {
		return "", errNotificationCredentialKeyRequired
	}
	storedCredential = strings.TrimSpace(storedCredential)
	if strings.HasPrefix(storedCredential, notificationCredentialCiphertextPrefix) {
		keyID, _, ok := parseNotificationCredentialV2Envelope(storedCredential)
		if !ok {
			return "", errNotificationCredentialCiphertextInvalid
		}
		cipher := ring.byID[keyID]
		if cipher == nil {
			return "", errNotificationCredentialCiphertextInvalid
		}
		return cipher.decrypt(channelID, channelType, storedCredential)
	}
	if strings.HasPrefix(storedCredential, notificationCredentialLegacyCiphertextPrefix) {
		for _, cipher := range ring.legacyOrder {
			credential, err := cipher.decrypt(channelID, channelType, storedCredential)
			if err == nil {
				return credential, nil
			}
		}
		return "", errNotificationCredentialCiphertextInvalid
	}
	if storedCredential == "" {
		return "", errInvalidAdminNotificationChannelWrite
	}
	return "", errNotificationCredentialPlaintext
}

func parseNotificationCredentialV2Envelope(value string) (string, string, bool) {
	remainder := strings.TrimPrefix(strings.TrimSpace(value), notificationCredentialCiphertextPrefix)
	keyID, encoded, ok := strings.Cut(remainder, ":")
	if !ok || !validNotificationCredentialKeyID(keyID) || encoded == "" {
		return "", "", false
	}
	return keyID, encoded, true
}

func notificationCredentialDerivedKeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return "key-" + hex.EncodeToString(sum[:8])
}

func validNotificationCredentialKeyID(keyID string) bool {
	if keyID == "" || len(keyID) > notificationCredentialMaximumKeyIDLength {
		return false
	}
	for _, character := range keyID {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func notificationCredentialAAD(channelID, channelType string) []byte {
	channelID = strings.TrimSpace(channelID)
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if channelType == "" {
		channelType = "telegram"
	}
	return []byte(notificationCredentialAADDomain + channelType + "\x00" + channelID)
}

func notificationCredentialAADV2(channelID, channelType, keyID string) []byte {
	return append(notificationCredentialAAD(channelID, channelType), []byte("\x00key-id\x00"+keyID)...)
}

func isEncryptedNotificationCredential(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, notificationCredentialCiphertextPrefix) ||
		strings.HasPrefix(value, notificationCredentialLegacyCiphertextPrefix)
}

func (s *SQLiteStore) ConfigureNotificationCredentialEncryption(ctx context.Context, key []byte) error {
	keyID := notificationCredentialDerivedKeyID(key)
	return s.ConfigureNotificationCredentialKeyring(ctx, keyID, map[string][]byte{keyID: key})
}

// ConfigureNotificationCredentialKeyring installs a key-id-addressable ring.
// Existing ciphertext is decrypted with the supplied ring and atomically
// re-encrypted under activeKeyID. Callers can therefore remove retired keys on
// a later restart without asking administrators to re-enter every credential.
func (s *SQLiteStore) ConfigureNotificationCredentialKeyring(ctx context.Context, activeKeyID string, keys map[string][]byte) error {
	ring, err := newNotificationCredentialKeyring(activeKeyID, keys)
	if err != nil {
		return err
	}
	if err := s.migrateNotificationCredentialsToEncrypted(ctx, ring); err != nil {
		return err
	}
	s.notificationCredentialMu.Lock()
	s.notificationCredentialKeyring = ring
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

func (s *SQLiteStore) notificationCredentialKeyringSnapshot() *notificationCredentialKeyring {
	if s == nil {
		return nil
	}
	s.notificationCredentialMu.RLock()
	ring := s.notificationCredentialKeyring
	s.notificationCredentialMu.RUnlock()
	return ring
}

func (s *SQLiteStore) encryptNotificationCredentialForStorage(channelID, channelType, credential string) (string, error) {
	ring := s.notificationCredentialKeyringSnapshot()
	if ring == nil {
		return "", errNotificationCredentialKeyRequired
	}
	return ring.encrypt(channelID, channelType, credential)
}

func (s *SQLiteStore) decryptNotificationCredentialFromStorage(channelID, channelType, storedCredential string) (string, error) {
	ring := s.notificationCredentialKeyringSnapshot()
	if ring == nil {
		return "", errNotificationCredentialKeyRequired
	}
	return ring.decrypt(channelID, channelType, storedCredential)
}

func (s *SQLiteStore) migrateNotificationCredentialsToEncrypted(ctx context.Context, keyring *notificationCredentialKeyring) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store is closed")
	}
	if keyring == nil || keyring.active == nil {
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
		plaintextCredential := trimmedCredential
		if isEncryptedNotificationCredential(trimmedCredential) {
			decryptedCredential, err := keyring.decrypt(channelID, "telegram", trimmedCredential)
			if err != nil {
				_ = rows.Close()
				return err
			}
			if strings.HasPrefix(trimmedCredential, notificationCredentialCiphertextPrefix+keyring.activeKeyID+":") {
				continue
			}
			plaintextCredential = decryptedCredential
		}
		encryptedCredential, err := keyring.encrypt(channelID, "telegram", plaintextCredential)
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
