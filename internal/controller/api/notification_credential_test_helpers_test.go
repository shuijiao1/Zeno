package api

import (
	"context"
	"testing"
)

func testNotificationCredentialKey() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}

func alternateTestNotificationCredentialKey() []byte {
	return []byte("abcdef0123456789abcdef0123456789")
}

func enableTestNotificationCredentialEncryption(t *testing.T, store *SQLiteStore) {
	t.Helper()
	if err := store.ConfigureNotificationCredentialEncryption(context.Background(), testNotificationCredentialKey()); err != nil {
		t.Fatalf("configure notification credential encryption: %v", err)
	}
}
