package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentProbeTargetsRequiresBearerToken(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agent/v1/probe-targets", nil)
	request.Header.Set("X-Node-ID", "hytron")

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("token")) || bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("secret")) {
		t.Fatalf("auth failure body should not leak token/secret wording: %s", recorder.Body.String())
	}
}

func TestAgentProbeTargetsReturnsEnabledTargetsAfterAuth(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agent/v1/probe-targets", nil)
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response AgentProbeTargetsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode targets response: %v", err)
	}
	if len(response.Targets) != len(DefaultPreviewProbeTargets()) {
		t.Fatalf("targets len = %d, want %d", len(response.Targets), len(DefaultPreviewProbeTargets()))
	}
	if response.Targets[0].ID == "" || response.Targets[0].Name == "" || response.Targets[0].Address == "" {
		t.Fatalf("first target missing required public agent fields: %+v", response.Targets[0])
	}
	raw := recorder.Body.String()
	if bytes.Contains(bytes.ToLower([]byte(raw)), []byte("token")) || bytes.Contains(bytes.ToLower([]byte(raw)), []byte("secret")) {
		t.Fatalf("agent targets response leaked token/secret wording: %s", raw)
	}
}

func TestAgentProbeResultsAcceptsSamplesAndUpdatesPublicLatency(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	ts := time.Now().UTC().Truncate(time.Second).Unix()
	body := map[string]any{
		"rounds": []map[string]any{
			{
				"target_id": "google-dns",
				"ts":        ts,
				"type":      "tcping",
				"samples": []map[string]any{
					{"seq": 1, "success": true, "latency_ms": 10.0},
					{"seq": 2, "success": false, "error": "timeout"},
					{"seq": 3, "success": true, "latency_ms": 30.0},
				},
			},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
	var accepted struct {
		OK       bool `json:"ok"`
		Accepted int  `json:"accepted"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted response: %v", err)
	}
	if !accepted.OK || accepted.Accepted != 1 {
		t.Fatalf("accepted response = %+v, want ok=true accepted=1", accepted)
	}

	latency, err := store.NodeLatency(ctx, "hytron", latencyWindow{Name: "1h", Samples: 36, Step: 2 * time.Minute})
	if err != nil {
		t.Fatalf("node latency: %v", err)
	}
	if len(latency.Points) != 1 {
		t.Fatalf("latency points len = %d, want 1 posted round", len(latency.Points))
	}
	point := latency.Points[0]
	if point.TargetID != "google-dns" || point.MedianMS == nil || *point.MedianMS != 20 || math.Abs(point.LossPercent-100.0/3.0) > 0.000001 {
		t.Fatalf("latency point = %+v, want posted google-dns median=20 loss=33.333", point)
	}
	var sampleRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&sampleRows); err != nil {
		t.Fatalf("count samples: %v", err)
	}
	if sampleRows != 3 {
		t.Fatalf("probe sample rows = %d, want 3 raw samples", sampleRows)
	}
}

func TestAgentProbeResultsStoresLatencyWithoutProbeAlertNotification(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "probe_unhealthy", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable probe_unhealthy notification type: %v", err)
	}

	handler := NewHandler(telegram.handlerOptions(store))
	now := time.Now().UTC().Truncate(time.Second)
	postAgentHeartbeat(t, handler, now.Unix(), "online")

	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now.Add(time.Second).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":false,"error":"timeout"},{"seq":2,"success":false,"error":"timeout"}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("node status = %q, want online because probe alert rules were removed", status)
	}
	paths, forms, errors := telegram.waitForCalls(t, 0)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 0 || len(forms) != 0 {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want no probe alert notification", paths, forms)
	}
}

func TestAgentProbeResultsSuccessfulHighLatencyDoesNotChangeStatus(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	now := time.Now().UTC().Truncate(time.Second)
	postAgentHeartbeat(t, handler, now.Unix(), "online")

	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now.Add(time.Second).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":900},{"seq":2,"success":true,"latency_ms":950},{"seq":3,"success":true,"latency_ms":1000}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("node status = %q, want online because probe latency alert rule was removed", status)
	}
}

func TestAgentProbeResultsFailedSamplesDoNotWarn(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store})
	now := time.Now().UTC().Truncate(time.Second)
	postAgentHeartbeat(t, handler, now.Unix(), "online")

	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now.Add(time.Second).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":false,"error":"timeout"},{"seq":2,"success":false,"error":"timeout"}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("node status = %q, want online when probe warning rules are removed", status)
	}
}

func TestAgentStateResourceRuleMarksWarningAndDispatchesProbeUnhealthy(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	telegram := newTelegramTestCapture(t)
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "probe_unhealthy", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable probe_unhealthy notification type: %v", err)
	}

	handler := NewHandler(telegram.handlerOptions(store))
	now := time.Now().UTC().Truncate(time.Second)
	postAgentHeartbeat(t, handler, now.Unix(), "online")
	body := map[string]any{
		"ts":                  now.Add(time.Second).Unix(),
		"cpu_percent":         96.5,
		"memory_used_bytes":   int64(4 * 1024 * 1024 * 1024),
		"memory_total_bytes":  int64(8 * 1024 * 1024 * 1024),
		"disk_used_bytes":     int64(40 * 1024 * 1024 * 1024),
		"disk_total_bytes":    int64(160 * 1024 * 1024 * 1024),
		"net_in_total_bytes":  int64(1_000_000),
		"net_out_total_bytes": int64(2_000_000),
		"net_in_speed_bps":    2048.5,
		"net_out_speed_bps":   1024.25,
		"uptime_seconds":      int64(3600),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal state body: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/state", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("state status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "warning" {
		t.Fatalf("node status = %q, want warning after enabled CPU rule threshold is exceeded", status)
	}
	paths, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 1 || len(forms) != 1 || !strings.Contains(forms[0], "%E7%8A%B6%E6%80%81%E5%BC%82%E5%B8%B8") {
		t.Fatalf("telegram request paths=%+v forms=%+v, want one resource threshold notification", paths, forms)
	}
	assertTelegramFormsDoNotLeakCredential(t, forms, "telegram-bot-credential-value")
}

func TestAgentHeartbeatDoesNotClearExistingProbeWarning(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	freshSeen := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'warning', last_seen_at = ? WHERE id = 'hytron'`, freshSeen); err != nil {
		t.Fatalf("set warning status: %v", err)
	}

	postAgentHeartbeat(t, NewHandler(HandlerOptions{Store: store}), freshSeen+1, "online")

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "warning" {
		t.Fatalf("node status = %q, want heartbeat online to preserve probe warning until a healthy probe clears it", status)
	}
}

func TestAgentProbeResultsClearsWarningAfterHealthyProbe(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	freshSeen := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'warning', last_seen_at = ? WHERE id = 'hytron'`, freshSeen); err != nil {
		t.Fatalf("set warning status: %v", err)
	}

	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(freshSeen+1, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5},{"seq":2,"success":true,"latency_ms":13.5}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("node status = %q, want healthy probe to clear warning", status)
	}
}

func TestAgentProbeResultsRejectsUnknownTarget(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	payload := []byte(`{"rounds":[{"target_id":"not-enabled","ts":1782990000,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":1.2}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	var rounds int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&rounds); err != nil {
		t.Fatalf("count rounds: %v", err)
	}
	if rounds != 0 {
		t.Fatalf("probe rounds = %d, want no partial insert for unknown target", rounds)
	}
}

func TestAgentHeartbeatUpdatesNodeStatusAndLastSeen(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	ts := time.Now().UTC().Truncate(time.Second).Unix()
	payload := []byte(`{"ts":` + strconv.FormatInt(ts, 10) + `,"status":"online","agent_version":"agent-test"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/heartbeat", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
	var status, agentVersion string
	var lastSeen int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT n.status, n.last_seen_at, h.agent_version
		FROM nodes n LEFT JOIN host_info h ON h.node_id = n.id
		WHERE n.id = 'hytron'
	`).Scan(&status, &lastSeen, &agentVersion); err != nil {
		t.Fatalf("query heartbeat state: %v", err)
	}
	if status != "online" || lastSeen != ts || agentVersion != "agent-test" {
		t.Fatalf("heartbeat persisted status=%q last_seen=%d agent_version=%q, want online/%d/agent-test", status, lastSeen, agentVersion, ts)
	}
}

func TestAgentHeartbeatDispatchesEnabledTelegramOnNodeOfflineTransition(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	telegram := newTelegramTestCapture(t)
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "ops-telegram",
		Name:        "Ops Telegram",
		Destination: "7579942307",
		Credential:  "telegram-bot-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	handler := NewHandler(telegram.handlerOptions(store))
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	postAgentHeartbeat(t, handler, now.Add(-nodeHeartbeatOfflineAfter-time.Minute).Unix(), "online")

	paths, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 1 || paths[0] != "/bottelegram-bot-credential-value/sendMessage" {
		t.Fatalf("telegram paths = %+v, want one sendMessage request", paths)
	}
	if len(forms) != 1 || !strings.Contains(forms[0], "chat_id=7579942307") || !strings.Contains(forms[0], "%E5%B7%B2%E7%A6%BB%E7%BA%BF") {
		t.Fatalf("telegram forms = %+v, want offline text", forms)
	}
	assertTelegramFormsDoNotLeakCredential(t, forms, "telegram-bot-credential-value")
}

func TestAgentHeartbeatDispatchesEnabledTelegramChannel(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	var captureMu sync.Mutex
	var telegramPaths []string
	var telegramForms []string
	var telegramErrors []error
	telegramAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			captureMu.Lock()
			telegramErrors = append(telegramErrors, err)
			captureMu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		captureMu.Lock()
		telegramPaths = append(telegramPaths, r.URL.Path)
		telegramForms = append(telegramForms, r.Form.Encode())
		captureMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer telegramAPI.Close()

	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "home-telegram",
		Name:        "Home Telegram",
		Destination: "7579942307",
		Credential:  "telegram-bot-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create telegram channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store, TelegramAPIBaseURL: telegramAPI.URL})
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	postAgentHeartbeat(t, handler, now.Add(-nodeHeartbeatOfflineAfter-time.Minute).Unix(), "online")

	waitUntil(t, time.Second, func() bool {
		captureMu.Lock()
		defer captureMu.Unlock()
		return len(telegramPaths)+len(telegramErrors) == 1
	})
	captureMu.Lock()
	capturedPaths := append([]string(nil), telegramPaths...)
	capturedForms := append([]string(nil), telegramForms...)
	capturedErrors := append([]error(nil), telegramErrors...)
	captureMu.Unlock()
	if len(capturedErrors) != 0 {
		t.Fatalf("telegram handler errors = %+v", capturedErrors)
	}
	if len(capturedPaths) != 1 || capturedPaths[0] != "/bottelegram-bot-credential-value/sendMessage" {
		t.Fatalf("telegram paths = %+v, want sendMessage path with bot credential", capturedPaths)
	}
	if len(capturedForms) != 1 || !strings.Contains(capturedForms[0], "chat_id=7579942307") || !strings.Contains(capturedForms[0], "%E7%A6%BB%E7%BA%BF") {
		t.Fatalf("telegram form = %+v, want chat id and offline text", capturedForms)
	}
	if strings.Contains(capturedForms[0], "telegram-bot-credential-value") {
		t.Fatalf("telegram form leaked credential: %s", capturedForms[0])
	}
}

func TestAgentHeartbeatNotificationDeliveryDoesNotBlockResponse(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	called := make(chan struct{}, 1)
	release := make(chan struct{})
	slowTelegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		<-release
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer slowTelegram.Close()
	defer close(release)
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "slow-telegram", Name: "Slow Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	started := time.Now()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	postAgentHeartbeat(t, NewHandler(HandlerOptions{Store: store, NotificationClient: slowTelegram.Client(), TelegramAPIBaseURL: slowTelegram.URL}), now.Add(-nodeHeartbeatOfflineAfter-time.Minute).Unix(), "online")
	elapsed := time.Since(started)
	if elapsed > 150*time.Millisecond {
		t.Fatalf("heartbeat response took %s, want notification delivery to be non-blocking", elapsed)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatalf("slow telegram send was not attempted")
	}
}

func TestAgentHeartbeatDoesNotDispatchRecoveryAfterStaleHeartbeatOffline(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Minute).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, staleSeen); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	postAgentHeartbeat(t, NewHandler(telegram.handlerOptions(store)), time.Now().UTC().Unix(), "online")
	paths, forms, errors := telegram.waitForCalls(t, 0)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 0 || len(forms) != 0 {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want no recovery notification", paths, forms)
	}
	deliveries, err := store.AdminNotificationDeliveries(ctx, 20)
	if err != nil {
		t.Fatalf("list notification deliveries: %v", err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("deliveries = %+v, want no recovery delivery", deliveries)
	}
}

func TestAgentHeartbeatTransitionDoesNotDispatchOnlineWhenOutOfOrderHeartbeatStaysPubliclyOffline(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Minute).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, staleSeen); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}

	transition, err := store.RecordAgentHeartbeatTransition(ctx, "hytron", time.Unix(staleSeen+1, 0).UTC(), "online", "agent-test")
	if err != nil {
		t.Fatalf("record heartbeat transition: %v", err)
	}
	if transition.Previous.Status != "offline" || transition.Current.Status != "offline" {
		t.Fatalf("transition = %+v, want offline -> offline public statuses", transition)
	}
	if eventType, ok := notificationEventTypeForStatusChange(transition.Previous.Status, transition.Current.Status); ok {
		t.Fatalf("event type = %q, want no notification when public status stays offline", eventType)
	}
}

func TestAgentHeartbeatTransitionDispatchesOfflineWhenOutOfOrderOnlineMakesNodePubliclyOffline(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}

	staleHeartbeat := now.Add(-nodeHeartbeatOfflineAfter - time.Minute)
	transition, err := store.RecordAgentHeartbeatTransition(ctx, "hytron", staleHeartbeat, "online", "agent-test")
	if err != nil {
		t.Fatalf("record heartbeat transition: %v", err)
	}
	if transition.Previous.Status != "online" || transition.Current.Status != "offline" {
		t.Fatalf("transition = %+v, want online -> offline public statuses", transition)
	}
	eventType, ok := notificationEventTypeForStatusChange(transition.Previous.Status, transition.Current.Status)
	if !ok || eventType != "node_offline" {
		t.Fatalf("event type = %q ok=%v, want node_offline", eventType, ok)
	}
}

func TestAgentHeartbeatNotificationFailureDoesNotRejectHeartbeat(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	closedTelegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closedTelegram.URL
	closedTelegram.Close()
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "broken-telegram",
		Name:        "Broken Telegram",
		Destination: "7579942307",
		Credential:  "telegram-bot-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	recorder := postAgentHeartbeat(t, NewHandler(HandlerOptions{Store: store, TelegramAPIBaseURL: closedURL}), now.Add(-nodeHeartbeatOfflineAfter-time.Minute).Unix(), "online")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("heartbeat status = %d, want 202 even if notification send fails; body=%s", recorder.Code, recorder.Body.String())
	}
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("stored node status = %q, want heartbeat status persisted despite notification failure", status)
	}
}

func TestAgentHeartbeatRecordsNotificationDeliveryHistoryWithoutCredentialLeak(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	telegramAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer telegramAPI.Close()
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "ops-telegram",
		Name:        "Ops Telegram",
		Destination: "7579942307",
		Credential:  "telegram-bot-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	postAgentHeartbeat(t, NewHandler(HandlerOptions{Store: store, NotificationClient: telegramAPI.Client(), TelegramAPIBaseURL: telegramAPI.URL}), now.Add(-nodeHeartbeatOfflineAfter-time.Minute).Unix(), "online")
	waitUntil(t, time.Second, func() bool {
		deliveries, err := store.AdminNotificationDeliveries(ctx, 20)
		return err == nil && len(deliveries) == 1
	})
	deliveries, err := store.AdminNotificationDeliveries(ctx, 20)
	if err != nil {
		t.Fatalf("list notification deliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("deliveries len = %d, want one recorded dispatch", len(deliveries))
	}
	delivery := deliveries[0]
	if delivery.EventType != "node_offline" || delivery.Label != "离线" || delivery.ChannelID != "ops-telegram" || delivery.ChannelName != "Ops Telegram" || delivery.NodeID != "hytron" || delivery.NodeName != "Hytron" || delivery.PreviousStatus != "online" || delivery.Status != "offline" || delivery.Success {
		t.Fatalf("delivery = %+v, want failed node_offline telegram delivery metadata", delivery)
	}
	if delivery.Error == "" || strings.Contains(delivery.Error, "telegram-bot-credential-value") || strings.Contains(delivery.Error, telegramAPI.URL) {
		t.Fatalf("delivery error should be sanitized and useful, got %q", delivery.Error)
	}

	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/notification-deliveries", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("history status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	raw := recorder.Body.String()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains(lower, []byte("hash")) || bytes.Contains(lower, []byte("channel_type")) || strings.Contains(raw, "telegram-bot-credential-value") || strings.Contains(raw, telegramAPI.URL) {
		t.Fatalf("notification delivery history leaked sensitive or channel-type data: %s", raw)
	}
}

func postAgentHeartbeat(t *testing.T, handler http.Handler, ts int64, status string) *httptest.ResponseRecorder {
	t.Helper()
	payload := []byte(`{"ts":` + strconv.FormatInt(ts, 10) + `,"status":"` + status + `","agent_version":"agent-test"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/heartbeat", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("heartbeat status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
	return recorder
}

func readAllString(t *testing.T, reader io.Reader) string {
	t.Helper()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type telegramTestCapture struct {
	server *httptest.Server
	mu     sync.Mutex
	paths  []string
	forms  []string
	errors []error
}

func newTelegramTestCapture(t *testing.T) *telegramTestCapture {
	t.Helper()
	capture := &telegramTestCapture{}
	capture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			capture.mu.Lock()
			capture.errors = append(capture.errors, err)
			capture.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		capture.mu.Lock()
		capture.paths = append(capture.paths, r.URL.Path)
		capture.forms = append(capture.forms, r.Form.Encode())
		capture.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	t.Cleanup(capture.server.Close)
	return capture
}

func (capture *telegramTestCapture) handlerOptions(store Store) HandlerOptions {
	return HandlerOptions{Store: store, NotificationClient: capture.server.Client(), TelegramAPIBaseURL: capture.server.URL}
}

func (capture *telegramTestCapture) waitForCalls(t *testing.T, want int) ([]string, []string, []error) {
	t.Helper()
	waitUntil(t, time.Second, func() bool {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		return len(capture.paths)+len(capture.errors) >= want
	})
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]string(nil), capture.paths...), append([]string(nil), capture.forms...), append([]error(nil), capture.errors...)
}

func assertTelegramFormsDoNotLeakCredential(t *testing.T, forms []string, credential string) {
	t.Helper()
	for _, form := range forms {
		if strings.Contains(form, credential) {
			t.Fatalf("telegram form leaked credential: %s", form)
		}
	}
}

func TestAgentHostUpsertUpdatesPublicSummaryTotals(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	body := map[string]any{
		"hostname":           "hytron-real",
		"os_name":            "debian",
		"os_version":         "13",
		"kernel":             "6.12.0",
		"arch":               "x86_64",
		"virtualization":     "kvm",
		"cpu_model":          "AMD EPYC",
		"cpu_cores":          4,
		"memory_total_bytes": int64(8 * 1024 * 1024 * 1024),
		"disk_total_bytes":   int64(160 * 1024 * 1024 * 1024),
		"boot_time":          int64(1782980000),
		"agent_version":      "agent-test",
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal host body: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/host", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	node := summary.Nodes[0]
	if node.OS != "debian" || node.CPUCores == nil || *node.CPUCores != 4 || node.MemoryTotalBytes == nil || *node.MemoryTotalBytes != float64(8*1024*1024*1024) || node.DiskTotalBytes == nil || *node.DiskTotalBytes != float64(160*1024*1024*1024) {
		t.Fatalf("summary node after host = %+v, want host totals and cores", node)
	}
}

func TestAgentHostUpsertAutoFillsPublicNetworkIdentity(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	body := map[string]any{
		"hostname":           "hytron-real",
		"os_name":            "debian",
		"os_version":         "13",
		"kernel":             "6.12.0",
		"arch":               "x86_64",
		"virtualization":     "kvm",
		"cpu_model":          "AMD EPYC",
		"cpu_cores":          4,
		"memory_total_bytes": int64(8 * 1024 * 1024 * 1024),
		"disk_total_bytes":   int64(160 * 1024 * 1024 * 1024),
		"boot_time":          int64(1782980000),
		"agent_version":      "agent-test",
		"public_ipv4":        " 198.51.100.8 ",
		"public_ipv6":        "2001:db8::8",
		"country_code":       " jp ",
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal host body: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/host", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	nodes, err := store.AdminNodes(ctx)
	if err != nil {
		t.Fatalf("admin nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(nodes))
	}
	if nodes[0].PublicIPv4 != "198.51.100.8" || nodes[0].PublicIPv6 != "2001:db8::8" || nodes[0].CountryCode != "JP" {
		t.Fatalf("node network identity = %+v, want auto-filled normalized IPs and country", nodes[0])
	}
}

func TestAgentHostUpsertKeepsNetworkIdentityWhenDiscoveryOmitted(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET public_ipv4 = '198.51.100.8', public_ipv6 = '2001:db8::8', country_code = 'JP' WHERE id = 'hytron'`); err != nil {
		t.Fatalf("seed network identity: %v", err)
	}

	body := []byte(`{"hostname":"hytron-real","os_name":"debian","os_version":"13","kernel":"6.12.0","arch":"x86_64","virtualization":"kvm","cpu_model":"AMD EPYC","cpu_cores":4,"memory_total_bytes":8589934592,"disk_total_bytes":171798691840,"boot_time":1782980000,"agent_version":"agent-test"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/host", bytes.NewReader(body))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	nodes, err := store.AdminNodes(ctx)
	if err != nil {
		t.Fatalf("admin nodes: %v", err)
	}
	if nodes[0].PublicIPv4 != "198.51.100.8" || nodes[0].PublicIPv6 != "2001:db8::8" || nodes[0].CountryCode != "JP" {
		t.Fatalf("node network identity after omitted discovery = %+v, want existing values preserved", nodes[0])
	}
}

func TestAgentStateSamplesDrivePublicSummaryAndMonthlyTrafficDeltas(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	postState := func(ts int64, inTotal, outTotal int64, cpu float64) {
		t.Helper()
		body := map[string]any{
			"ts":                   ts,
			"cpu_percent":          cpu,
			"load1":                0.42,
			"load5":                0.35,
			"load15":               0.28,
			"memory_used_bytes":    int64(3 * 1024 * 1024 * 1024),
			"memory_total_bytes":   int64(8 * 1024 * 1024 * 1024),
			"swap_used_bytes":      int64(512 * 1024 * 1024),
			"swap_total_bytes":     int64(2 * 1024 * 1024 * 1024),
			"disk_used_bytes":      int64(40 * 1024 * 1024 * 1024),
			"disk_total_bytes":     int64(160 * 1024 * 1024 * 1024),
			"net_in_total_bytes":   inTotal,
			"net_out_total_bytes":  outTotal,
			"net_in_speed_bps":     2048.5,
			"net_out_speed_bps":    1024.25,
			"process_count":        88,
			"tcp_connection_count": 34,
			"udp_connection_count": 12,
			"uptime_seconds":       int64(3600),
		}
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal state body: %v", err)
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/state", bytes.NewReader(payload))
		request.Header.Set("X-Node-ID", "hytron")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
		}
	}

	ts := time.Now().UTC().Truncate(time.Second).Unix()
	postState(ts, 1_000_000, 2_000_000, 12.5)
	postState(ts+60, 1_400_000, 2_600_000, 22.5)

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	node := summary.Nodes[0]
	if node.Status != "online" || node.CPUPercent == nil || *node.CPUPercent != 22.5 || node.NetInSpeedBps == nil || *node.NetInSpeedBps != 2048.5 || node.NetOutSpeedBps == nil || *node.NetOutSpeedBps != 1024.25 {
		t.Fatalf("summary node after state = %+v, want latest state values", node)
	}
	if node.MonthlyBillableBytes == nil || *node.MonthlyBillableBytes != 1_000_000 {
		t.Fatalf("monthly billable = %v, want second sample delta in+out = 1000000", node.MonthlyBillableBytes)
	}
	state, err := store.NodeState(ctx, "hytron", latencyWindow{Name: "1h", Samples: 36, Step: 2 * time.Minute})
	if err != nil {
		t.Fatalf("node state: %v", err)
	}
	if len(state.Points) != 2 {
		t.Fatalf("state points = %d, want 2", len(state.Points))
	}
	latest := state.Points[1]
	if latest.Load1 == nil || *latest.Load1 != 0.42 || latest.Load5 == nil || *latest.Load5 != 0.35 || latest.Load15 == nil || *latest.Load15 != 0.28 {
		t.Fatalf("load averages = %+v, want persisted load1/load5/load15", latest)
	}
	if latest.SwapUsedBytes == nil || *latest.SwapUsedBytes != float64(512*1024*1024) || latest.SwapTotalBytes == nil || *latest.SwapTotalBytes != float64(2*1024*1024*1024) {
		t.Fatalf("swap fields = %+v, want persisted swap usage", latest)
	}
	if latest.ProcessCount == nil || *latest.ProcessCount != 88 || latest.TCPConnectionCount == nil || *latest.TCPConnectionCount != 34 || latest.UDPConnectionCount == nil || *latest.UDPConnectionCount != 12 {
		t.Fatalf("process/tcp/udp counts = %+v, want persisted process, tcp and udp connection counts", latest)
	}
	var samples int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_samples`).Scan(&samples); err != nil {
		t.Fatalf("count state samples: %v", err)
	}
	if samples != 2 {
		t.Fatalf("state samples = %d, want 2", samples)
	}
}

func TestBillingTrafficModeAndResetPeriodHelpers(t *testing.T) {
	if got := billableTrafficDelta("in", 100, 400); got != 100 {
		t.Fatalf("in billable = %d, want 100", got)
	}
	if got := billableTrafficDelta("out", 100, 400); got != 400 {
		t.Fatalf("out billable = %d, want 400", got)
	}
	if got := billableTrafficDelta("max", 100, 400); got != 400 {
		t.Fatalf("max billable = %d, want 400", got)
	}
	if got := billableTrafficDelta("both", 100, 400); got != 500 {
		t.Fatalf("both billable = %d, want 500", got)
	}
	if got := billingPeriodKey(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), 15); got != "2026-06" {
		t.Fatalf("billing period before reset = %s, want 2026-06", got)
	}
	if got := billingPeriodKey(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), 15); got != "2026-07" {
		t.Fatalf("billing period on reset = %s, want 2026-07", got)
	}
	period := billingPeriodFor(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), 15)
	if period.Key != "2026-06" || period.StartDate != "2026-06-15" || period.EndDate != "2026-07-14" {
		t.Fatalf("billing period window = %+v, want 2026-06 2026-06-15..2026-07-14", period)
	}
	clamped := billingPeriodFor(time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC), 31)
	if clamped.Key != "2026-02" || clamped.StartDate != "2026-02-28" || clamped.EndDate != "2026-03-30" {
		t.Fatalf("clamped billing period window = %+v, want reset day clamped to month end", clamped)
	}
}

func TestAgentStateLegacyPayloadKeepsExtraMetricsNull(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	body := map[string]any{
		"ts":                  time.Now().UTC().Unix(),
		"cpu_percent":         18.75,
		"memory_used_bytes":   int64(3 * 1024 * 1024 * 1024),
		"memory_total_bytes":  int64(8 * 1024 * 1024 * 1024),
		"disk_used_bytes":     int64(40 * 1024 * 1024 * 1024),
		"disk_total_bytes":    int64(160 * 1024 * 1024 * 1024),
		"net_in_total_bytes":  int64(1_000_000),
		"net_out_total_bytes": int64(2_000_000),
		"net_in_speed_bps":    2048.5,
		"net_out_speed_bps":   1024.25,
		"uptime_seconds":      int64(3600),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal state body: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/state", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for legacy state payload; body=%s", recorder.Code, recorder.Body.String())
	}

	state, err := store.NodeState(ctx, "hytron", latencyWindow{Name: "1h", Samples: 36, Step: 2 * time.Minute})
	if err != nil {
		t.Fatalf("node state: %v", err)
	}
	if len(state.Points) != 1 {
		t.Fatalf("state points = %d, want 1", len(state.Points))
	}
	point := state.Points[0]
	if point.CPUPercent == nil || *point.CPUPercent != 18.75 {
		t.Fatalf("cpu percent = %v, want persisted legacy metric", point.CPUPercent)
	}
	if point.Load1 != nil || point.Load5 != nil || point.Load15 != nil || point.SwapUsedBytes != nil || point.SwapTotalBytes != nil || point.ProcessCount != nil || point.TCPConnectionCount != nil || point.UDPConnectionCount != nil {
		t.Fatalf("legacy payload should keep extra metrics null, got %+v", point)
	}
}

func TestAgentStateSchemaMigratesExtraMetricColumnsAsNullable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "zeno.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite db: %v", err)
	}
	if _, err := legacyDB.Exec(`
		CREATE TABLE state_samples (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			ts INTEGER NOT NULL,
			cpu_percent REAL,
			memory_used_bytes INTEGER,
			memory_total_bytes INTEGER,
			disk_used_bytes INTEGER,
			disk_total_bytes INTEGER,
			net_in_total_bytes INTEGER,
			net_out_total_bytes INTEGER,
			net_in_speed_bps REAL,
			net_out_speed_bps REAL,
			uptime_seconds INTEGER
		);
	`); err != nil {
		_ = legacyDB.Close()
		t.Fatalf("create legacy state_samples table: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open migrated sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	columns := map[string]bool{}
	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(state_samples)`)
	if err != nil {
		t.Fatalf("query migrated state_samples schema: %v", err)
	}
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			t.Fatalf("scan schema row: %v", err)
		}
		columns[name] = true
		if (name == "load1" || name == "load5" || name == "load15" || name == "swap_used_bytes" || name == "swap_total_bytes" || name == "process_count" || name == "tcp_connection_count" || name == "udp_connection_count") && notNull != 0 {
			_ = rows.Close()
			t.Fatalf("migrated column %s should be nullable", name)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close schema rows: %v", err)
	}
	for _, column := range []string{"load1", "load5", "load15", "swap_used_bytes", "swap_total_bytes", "process_count", "tcp_connection_count", "udp_connection_count"} {
		if !columns[column] {
			t.Fatalf("migrated state_samples missing column %s", column)
		}
	}
}
