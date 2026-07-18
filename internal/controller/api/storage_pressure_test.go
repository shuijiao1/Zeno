package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestTelemetryStoragePressureRejectsStateButKeepsHeartbeatRecoveryPath(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{
		NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "test-agent-token",
	}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	store.telemetryStorage.minFreeBytes = 1
	store.telemetryStorage.checkedAt = time.Time{}
	store.telemetryStorage.freeBytes = func(string) (uint64, error) { return 0, nil }
	handler := NewHandler(HandlerOptions{Store: store})
	now := time.Now().UTC().Unix()

	postAgent := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("X-Node-ID", "example-node-a")
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	state := postAgent("/api/agent/v1/state", `{"sample_id":"pressure-1","ts":`+strconv.FormatInt(now, 10)+`}`)
	if state.Code != http.StatusInsufficientStorage || state.Header().Get("Retry-After") != "60" {
		t.Fatalf("state response = %d retry-after=%q body=%s, want 507/60", state.Code, state.Header().Get("Retry-After"), state.Body.String())
	}

	heartbeat := postAgent("/api/agent/v1/heartbeat", `{"ts":`+strconv.FormatInt(now, 10)+`,"status":"online"}`)
	if heartbeat.Code != http.StatusAccepted {
		t.Fatalf("heartbeat response = %d body=%s, want recovery path accepted", heartbeat.Code, heartbeat.Body.String())
	}

	store.telemetryStorage.mu.Lock()
	store.telemetryStorage.checkedAt = time.Time{}
	store.telemetryStorage.blocked = false
	store.telemetryStorage.freeBytes = func(string) (uint64, error) { return 2, nil }
	store.telemetryStorage.mu.Unlock()
	recovered := postAgent("/api/agent/v1/state", `{"sample_id":"pressure-1","ts":`+strconv.FormatInt(now, 10)+`}`)
	if recovered.Code != http.StatusAccepted {
		t.Fatalf("state after free-space recovery = %d body=%s, want 202", recovered.Code, recovered.Body.String())
	}
}
