package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/shuijiao1/zeno/internal/controller/api"
)

func TestBuildHandlerUsesSQLiteStoreWhenDBPathProvided(t *testing.T) {
	runtime, err := buildController(handlerConfig{DBPath: filepath.Join(t.TempDir(), "zeno.db")})
	if err != nil {
		t.Fatalf("build controller: %v", err)
	}
	defer runtime.Cleanup(context.Background())

	recorder := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Nodes []any `json:"nodes"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Nodes) != 0 {
		t.Fatalf("nodes len = %d, want empty sqlite-backed summary instead of mock data", len(body.Nodes))
	}
}

func TestBuildHandlerEnablesAdminAPIWithAdminToken(t *testing.T) {
	runtime, err := buildController(handlerConfig{
		DBPath:      filepath.Join(t.TempDir(), "zeno.db"),
		SeedPreview: true,
		NodeID:      "hytron",
		AgentToken:  "agent-token",
		AdminToken:  "admin-pass",
	})
	if err != nil {
		t.Fatalf("build controller: %v", err)
	}
	defer runtime.Cleanup(context.Background())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	runtime.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBuildHandlerServesConfiguredAgentBinary(t *testing.T) {
	tmp := t.TempDir()
	binaryPath := filepath.Join(tmp, "zeno-agent")
	if err := os.WriteFile(binaryPath, []byte("agent-binary"), 0o755); err != nil {
		t.Fatalf("write agent binary: %v", err)
	}
	runtime, err := buildController(handlerConfig{DBPath: filepath.Join(tmp, "zeno.db"), AgentBinaryPath: binaryPath, AgentVersion: "abc1234"})
	if err != nil {
		t.Fatalf("build controller: %v", err)
	}
	defer runtime.Cleanup(context.Background())

	recorder := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/agent/linux-amd64", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "agent-binary" {
		t.Fatalf("agent binary body = %q", recorder.Body.String())
	}
}

func TestReadNotificationCredentialKeyFileAcceptsInstallerKeyAndRejectsUnsafeFiles(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, notificationCredentialKeySize)
	encoded := base64.RawURLEncoding.EncodeToString(key)
	keyPath := filepath.Join(t.TempDir(), "notification-key")
	if err := os.WriteFile(keyPath, []byte(encoded+"\n"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	readKey, err := readNotificationCredentialKeyFile(keyPath)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if !bytes.Equal(readKey, key) {
		t.Fatalf("read key = %x, want %x", readKey, key)
	}
	if err := os.Chmod(keyPath, 0o640); err != nil {
		t.Fatalf("chmod group-readable key file: %v", err)
	}
	if _, err := readNotificationCredentialKeyFile(keyPath); err != nil {
		t.Fatalf("read root-owned/group-readable installer key: %v", err)
	}

	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("chmod loose key file: %v", err)
	}
	if _, err := readNotificationCredentialKeyFile(keyPath); err == nil {
		t.Fatalf("read key with loose permissions succeeded, want error")
	}

	shortKeyPath := filepath.Join(t.TempDir(), "short-key")
	if err := os.WriteFile(shortKeyPath, []byte("short"), 0o600); err != nil {
		t.Fatalf("write short key file: %v", err)
	}
	if _, err := readNotificationCredentialKeyFile(shortKeyPath); err == nil {
		t.Fatalf("read short key succeeded, want error")
	}

	linkPath := filepath.Join(t.TempDir(), "key-link")
	if err := os.Symlink(shortKeyPath, linkPath); err != nil {
		t.Fatalf("symlink key file: %v", err)
	}
	if _, err := readNotificationCredentialKeyFile(linkPath); err == nil {
		t.Fatalf("read symlink key succeeded, want error")
	}

	largeKeyPath := filepath.Join(t.TempDir(), "large-key")
	if err := os.WriteFile(largeKeyPath, bytes.Repeat([]byte("a"), 1025), 0o600); err != nil {
		t.Fatalf("write oversized key file: %v", err)
	}
	if _, err := readNotificationCredentialKeyFile(largeKeyPath); err == nil {
		t.Fatalf("read oversized key succeeded, want error")
	}
}

func TestReadNotificationAuthorityKeyFileRejectsUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "authority-key")
	if err := os.WriteFile(keyPath, []byte("authority-secret\n"), 0o640); err != nil {
		t.Fatalf("write authority key: %v", err)
	}
	key, err := readNotificationAuthorityKeyFile(keyPath)
	if err != nil || key != "authority-secret" {
		t.Fatalf("read authority key=%q err=%v", key, err)
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("chmod authority key: %v", err)
	}
	if _, err := readNotificationAuthorityKeyFile(keyPath); err == nil {
		t.Fatal("group/other-readable authority key was accepted")
	}
	if err := os.Chmod(keyPath, 0o640); err != nil {
		t.Fatalf("restore authority key permissions: %v", err)
	}
	linkPath := filepath.Join(dir, "authority-link")
	if err := os.Symlink(keyPath, linkPath); err != nil {
		t.Fatalf("symlink authority key: %v", err)
	}
	if _, err := readNotificationAuthorityKeyFile(linkPath); err == nil {
		t.Fatal("symlink authority key was accepted")
	}
}

func TestNotificationKeyringFilesRejectAmbiguousJSON(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, notificationCredentialKeySize))
	credentialDocuments := []struct {
		name    string
		content string
	}{
		{name: "active id whitespace", content: `{"active_key_id":" primary","keys":{"primary":"` + key + `"}}`},
		{name: "key id whitespace", content: `{"active_key_id":"primary","keys":{" primary":"` + key + `"}}`},
		{name: "duplicate key id", content: `{"active_key_id":"primary","keys":{"primary":"` + key + `","primary":"` + key + `"}}`},
		{name: "missing active member", content: `{"active_key_id":"missing","keys":{"primary":"` + key + `"}}`},
		{name: "unknown top level field", content: `{"active_key_id":"primary","keys":{"primary":"` + key + `"},"extra":true}`},
		{name: "duplicate top level field", content: `{"active_key_id":"primary","active_key_id":"other","keys":{"primary":"` + key + `"}}`},
		{name: "trailing document", content: `{"active_key_id":"primary","keys":{"primary":"` + key + `"}} {}`},
	}
	for _, tt := range credentialDocuments {
		t.Run("credential "+tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credential-keyring.json")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write keyring: %v", err)
			}
			if _, _, err := readNotificationCredentialKeyringFile(path); err == nil {
				t.Fatalf("ambiguous credential keyring was accepted: %s", tt.content)
			}
		})
	}

	authorityPath := filepath.Join(t.TempDir(), "authority-keyring.json")
	if err := os.WriteFile(authorityPath, []byte(`{"active_key_id":"primary","keys":{"primary":"authority-one","primary":"authority-two"}}`), 0o600); err != nil {
		t.Fatalf("write authority keyring: %v", err)
	}
	if _, _, err := readNotificationAuthorityKeyringFile(authorityPath); err == nil {
		t.Fatal("duplicate authority key id was accepted")
	}

	validPath := filepath.Join(t.TempDir(), "valid-keyring.json")
	if err := os.WriteFile(validPath, []byte(`{"active_key_id":"primary","keys":{"primary":"`+key+`"}}`), 0o640); err != nil {
		t.Fatalf("write valid keyring: %v", err)
	}
	active, keys, err := readNotificationCredentialKeyringFile(validPath)
	if err != nil || active != "primary" || len(keys) != 1 || !bytes.Equal(keys["primary"], bytes.Repeat([]byte{0x42}, notificationCredentialKeySize)) {
		t.Fatalf("valid keyring active=%q keys=%v err=%v", active, keys, err)
	}
}

func TestBuildControllerRequiresExternalNotificationCredentialKeyForStoredCredentials(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	dbPath := filepath.Join(t.TempDir(), "zeno.db")
	store, err := api.OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	if err := store.ConfigureNotificationCredentialEncryption(context.Background(), key); err != nil {
		t.Fatalf("configure credential encryption: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(context.Background(), api.AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "7579942307", Credential: "telegram-bot-secret-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	if runtime, err := buildController(handlerConfig{DBPath: dbPath, NotificationAuthorityKey: "authority"}); err == nil {
		_ = runtime.Cleanup(context.Background())
		t.Fatalf("build controller without credential key succeeded, want fail closed")
	}

	runtime, err := buildController(handlerConfig{DBPath: dbPath, NotificationAuthorityKey: "authority", NotificationCredentialKey: key})
	if err != nil {
		t.Fatalf("build controller with credential key: %v", err)
	}
	defer runtime.Cleanup(context.Background())
	channel, err := runtime.Store.AdminNotificationDispatchChannel(context.Background(), "ops")
	if err != nil {
		t.Fatalf("dispatch channel after startup: %v", err)
	}
	if channel.Credential != "telegram-bot-secret-value" {
		t.Fatalf("dispatch credential = %q, want stored token", channel.Credential)
	}
}
