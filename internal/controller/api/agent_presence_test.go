package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAgentPresenceConfigAppliedAckIsValidatedAndRecorded(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	version, err := store.ProbeConfigVersion(ctx)
	if err != nil {
		t.Fatalf("probe config version: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	defer cleanupTestHandler(t, handler)
	server := httptest.NewServer(handler)
	defer server.Close()

	conn := dialAgentPresenceWS(t, server.URL)
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "config_applied", "version": version}); err != nil {
		t.Fatalf("write config_applied ack: %v", err)
	}
	waitUntil(t, time.Second, func() bool {
		var appliedVersion int64
		var appliedAt int64
		if err := store.db.QueryRowContext(ctx, `SELECT probe_config_applied_version, COALESCE(probe_config_applied_at, 0) FROM nodes WHERE id = 'hytron'`).Scan(&appliedVersion, &appliedAt); err != nil {
			return false
		}
		return appliedVersion == version && appliedAt > 0
	})
	var appliedVersion int64
	if err := store.db.QueryRowContext(ctx, `SELECT probe_config_applied_version FROM nodes WHERE id = 'hytron'`).Scan(&appliedVersion); err != nil {
		t.Fatalf("query applied version: %v", err)
	}
	if appliedVersion != version {
		t.Fatalf("applied version = %d, want %d", appliedVersion, version)
	}

	if err := conn.WriteJSON(map[string]any{"type": "config_applied", "version": version + 999}); err != nil {
		t.Fatalf("write invalid future ack: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := store.db.QueryRowContext(ctx, `SELECT probe_config_applied_version FROM nodes WHERE id = 'hytron'`).Scan(&appliedVersion); err != nil {
		t.Fatalf("query applied version after invalid ack: %v", err)
	}
	if appliedVersion != version {
		t.Fatalf("future ack changed applied version to %d, want %d", appliedVersion, version)
	}
}

func dialAgentPresenceWS(t *testing.T, serverURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/agent/v1/presence/ws"
	header := http.Header{}
	header.Set("X-Node-ID", "hytron")
	header.Set("Authorization", "Bearer test-agent-token")
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if response != nil {
			t.Fatalf("dial presence ws: %v status=%s", err, response.Status)
		}
		t.Fatalf("dial presence ws: %v", err)
	}
	return conn
}

func TestAgentPresenceOfflineDebounceMergesReconnectChurnAndCleansUp(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	h := &handler{store: store, liveHub: newLiveUpdateHub(), presence: newAgentPresenceManager()}

	// Repeated disconnect scheduling for one node replaces the old timer instead
	// of accumulating one 30-second goroutine per disconnect.
	for range 64 {
		h.scheduleAgentPresenceOfflineCheckAfter(store, "hytron", time.Hour)
		if got := h.presence.pendingOfflineCheckCount(); got != 1 {
			t.Fatalf("pending checks for one node = %d, want 1", got)
		}
	}

	// Reconnect cancellation happens while holding the same manager lock used to
	// reserve a timer, so a schedule/connect race cannot leave an orphan check.
	session := h.presence.connect("hytron")
	if got := h.presence.pendingOfflineCheckCount(); got != 0 {
		t.Fatalf("pending checks after reconnect = %d, want 0", got)
	}
	if !h.presence.disconnect(session) {
		t.Fatal("disconnect current presence session")
	}
	for range 32 {
		h.scheduleAgentPresenceOfflineCheckAfter(store, "hytron", time.Hour)
		session = h.presence.connect("hytron")
		if got := h.presence.pendingOfflineCheckCount(); got != 0 {
			t.Fatalf("pending checks during reconnect churn = %d, want 0", got)
		}
		if !h.presence.disconnect(session) {
			t.Fatal("disconnect churn presence session")
		}
	}

	h.scheduleAgentPresenceOfflineCheckAfter(store, "node-a", time.Hour)
	h.scheduleAgentPresenceOfflineCheckAfter(store, "node-b", time.Hour)
	if got := h.presence.pendingOfflineCheckCount(); got != 2 {
		t.Fatalf("pending per-node checks = %d, want 2", got)
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Cleanup(cleanupCtx); err != nil {
		t.Fatalf("cleanup pending presence timers: %v", err)
	}
	if got := h.presence.pendingOfflineCheckCount(); got != 0 {
		t.Fatalf("pending checks after cleanup = %d, want 0", got)
	}
}
