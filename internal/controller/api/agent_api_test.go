package api

import (
	"bytes"
	"context"
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

func TestAgentProbeResultsMarksNodeWarningAndDispatchesProbeUnhealthy(t *testing.T) {
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
	var webhookBodies []string
	var webhookErrors []error
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		captureMu.Lock()
		defer captureMu.Unlock()
		if err != nil {
			webhookErrors = append(webhookErrors, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		webhookBodies = append(webhookBodies, string(body))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-webhook", Name: "Ops Webhook", Type: "webhook", Destination: webhook.URL, Credential: "dispatch-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "probe_unhealthy", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable probe_unhealthy notification type: %v", err)
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
	if status != "warning" {
		t.Fatalf("node status = %q, want warning after all probe samples fail", status)
	}

	waitUntil(t, time.Second, func() bool {
		captureMu.Lock()
		defer captureMu.Unlock()
		return len(webhookBodies)+len(webhookErrors) == 1
	})
	captureMu.Lock()
	capturedBodies := append([]string(nil), webhookBodies...)
	capturedErrors := append([]error(nil), webhookErrors...)
	captureMu.Unlock()
	if len(capturedErrors) != 0 {
		t.Fatalf("webhook handler errors = %+v", capturedErrors)
	}
	if len(capturedBodies) != 1 {
		t.Fatalf("webhook calls = %d, want one probe_unhealthy notification", len(capturedBodies))
	}
	var event struct {
		EventType      string `json:"event_type"`
		Label          string `json:"label"`
		PreviousStatus string `json:"previous_status"`
		Status         string `json:"status"`
	}
	if err := json.Unmarshal([]byte(capturedBodies[0]), &event); err != nil {
		t.Fatalf("decode webhook event: %v", err)
	}
	if event.EventType != "probe_unhealthy" || event.Label != "异常" || event.PreviousStatus != "online" || event.Status != "warning" {
		t.Fatalf("event = %+v, want online -> warning probe_unhealthy payload", event)
	}
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

func TestAgentHeartbeatDispatchesEnabledWebhookOnNodeOnlineTransition(t *testing.T) {
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
	var webhookMethods []string
	var webhookBodies []string
	var webhookAuthorization []string
	var webhookErrors []error
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		captureMu.Lock()
		defer captureMu.Unlock()
		webhookMethods = append(webhookMethods, r.Method)
		if err != nil {
			webhookErrors = append(webhookErrors, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		webhookAuthorization = append(webhookAuthorization, r.Header.Get("Authorization"))
		webhookBodies = append(webhookBodies, string(body))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "ops-webhook",
		Name:        "Ops Webhook",
		Type:        "webhook",
		Destination: webhook.URL,
		Credential:  "dispatch-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_online", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	ts := time.Now().UTC().Truncate(time.Second).Unix()
	postAgentHeartbeat(t, handler, ts, "online")

	waitUntil(t, time.Second, func() bool {
		captureMu.Lock()
		defer captureMu.Unlock()
		return len(webhookBodies)+len(webhookErrors) == 1
	})
	captureMu.Lock()
	capturedMethods := append([]string(nil), webhookMethods...)
	capturedBodies := append([]string(nil), webhookBodies...)
	capturedAuthorization := append([]string(nil), webhookAuthorization...)
	capturedErrors := append([]error(nil), webhookErrors...)
	captureMu.Unlock()
	if len(capturedErrors) != 0 {
		t.Fatalf("webhook handler errors = %+v", capturedErrors)
	}
	if len(capturedMethods) != 1 || capturedMethods[0] != http.MethodPost {
		t.Fatalf("webhook methods = %+v, want POST", capturedMethods)
	}
	if len(capturedBodies) != 1 {
		t.Fatalf("webhook calls = %d, want one online transition notification", len(capturedBodies))
	}
	if capturedAuthorization[0] != "Bearer dispatch-credential-value" {
		t.Fatalf("webhook Authorization = %q, want bearer credential header", capturedAuthorization[0])
	}
	if strings.Contains(capturedBodies[0], "dispatch-credential-value") {
		t.Fatalf("webhook body leaked credential: %s", capturedBodies[0])
	}
	var event struct {
		EventType      string `json:"event_type"`
		Label          string `json:"label"`
		NodeID         string `json:"node_id"`
		NodeName       string `json:"node_name"`
		Status         string `json:"status"`
		PreviousStatus string `json:"previous_status"`
	}
	if err := json.Unmarshal([]byte(capturedBodies[0]), &event); err != nil {
		t.Fatalf("decode webhook event: %v", err)
	}
	if event.EventType != "node_online" || event.Label != "上线" || event.NodeID != "hytron" || event.NodeName != "Hytron" || event.Status != "online" || event.PreviousStatus != "no_data" {
		t.Fatalf("webhook event = %+v, want node_online transition payload", event)
	}
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
		Type:        "telegram",
		Destination: "7579942307",
		Credential:  "telegram-bot-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create telegram channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_online", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store, TelegramAPIBaseURL: telegramAPI.URL})
	postAgentHeartbeat(t, handler, time.Now().UTC().Unix(), "online")

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
	if len(capturedForms) != 1 || !strings.Contains(capturedForms[0], "chat_id=7579942307") || !strings.Contains(capturedForms[0], "%E4%B8%8A%E7%BA%BF") {
		t.Fatalf("telegram form = %+v, want chat id and online text", capturedForms)
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
	slowWebhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer slowWebhook.Close()
	defer close(release)
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "slow-webhook", Name: "Slow Webhook", Type: "webhook", Destination: slowWebhook.URL, Credential: "dispatch-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_online", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	started := time.Now()
	postAgentHeartbeat(t, NewHandler(HandlerOptions{Store: store}), time.Now().UTC().Unix(), "online")
	elapsed := time.Since(started)
	if elapsed > 150*time.Millisecond {
		t.Fatalf("heartbeat response took %s, want notification delivery to be non-blocking", elapsed)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatalf("slow webhook was not attempted")
	}
}

func TestAgentHeartbeatDispatchesOnlineAfterStaleHeartbeatOffline(t *testing.T) {
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

	var captureMu sync.Mutex
	var webhookBodies []string
	var webhookErrors []error
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		captureMu.Lock()
		if err != nil {
			webhookErrors = append(webhookErrors, err)
		} else {
			webhookBodies = append(webhookBodies, string(body))
		}
		captureMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-webhook", Name: "Ops Webhook", Type: "webhook", Destination: webhook.URL, Credential: "dispatch-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_online", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	postAgentHeartbeat(t, NewHandler(HandlerOptions{Store: store}), time.Now().UTC().Unix(), "online")
	waitUntil(t, time.Second, func() bool {
		captureMu.Lock()
		defer captureMu.Unlock()
		return len(webhookBodies)+len(webhookErrors) == 1
	})
	captureMu.Lock()
	capturedBodies := append([]string(nil), webhookBodies...)
	capturedErrors := append([]error(nil), webhookErrors...)
	captureMu.Unlock()
	if len(capturedErrors) != 0 {
		t.Fatalf("webhook handler errors = %+v", capturedErrors)
	}
	if len(capturedBodies) != 1 {
		t.Fatalf("webhook calls = %d, want online notification after stale heartbeat offline", len(capturedBodies))
	}
	var event struct {
		PreviousStatus string `json:"previous_status"`
	}
	if err := json.Unmarshal([]byte(capturedBodies[0]), &event); err != nil {
		t.Fatalf("decode webhook event: %v", err)
	}
	if event.PreviousStatus != "offline" {
		t.Fatalf("previous status = %q, want offline derived from stale last_seen_at", event.PreviousStatus)
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

	closedWebhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closedWebhook.URL
	closedWebhook.Close()
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "broken-webhook",
		Name:        "Broken Webhook",
		Type:        "webhook",
		Destination: closedURL,
		Credential:  "dispatch-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_online", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	recorder := postAgentHeartbeat(t, NewHandler(HandlerOptions{Store: store}), time.Now().UTC().Unix(), "online")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("heartbeat status = %d, want 202 even if notification send fails; body=%s", recorder.Code, recorder.Body.String())
	}
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("node status = %q, want online persisted despite notification failure", status)
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

	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer webhook.Close()
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "ops-webhook",
		Name:        "Ops Webhook",
		Type:        "webhook",
		Destination: webhook.URL,
		Credential:  "dispatch-credential-value",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_online", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	postAgentHeartbeat(t, NewHandler(HandlerOptions{Store: store}), time.Now().UTC().Unix(), "online")
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
	if delivery.EventType != "node_online" || delivery.Label != "上线" || delivery.ChannelID != "ops-webhook" || delivery.ChannelName != "Ops Webhook" || delivery.ChannelType != "webhook" || delivery.NodeID != "hytron" || delivery.NodeName != "Hytron" || delivery.PreviousStatus != "no_data" || delivery.Status != "online" || delivery.Success {
		t.Fatalf("delivery = %+v, want failed node_online webhook delivery metadata", delivery)
	}
	if delivery.Error == "" || strings.Contains(delivery.Error, "dispatch-credential-value") || strings.Contains(delivery.Error, webhook.URL) {
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
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains(lower, []byte("hash")) || strings.Contains(raw, "dispatch-credential-value") || strings.Contains(raw, webhook.URL) {
		t.Fatalf("notification delivery history leaked sensitive data: %s", raw)
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
			"ts":                  ts,
			"cpu_percent":         cpu,
			"memory_used_bytes":   int64(3 * 1024 * 1024 * 1024),
			"memory_total_bytes":  int64(8 * 1024 * 1024 * 1024),
			"disk_used_bytes":     int64(40 * 1024 * 1024 * 1024),
			"disk_total_bytes":    int64(160 * 1024 * 1024 * 1024),
			"net_in_total_bytes":  inTotal,
			"net_out_total_bytes": outTotal,
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
	var samples int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_samples`).Scan(&samples); err != nil {
		t.Fatalf("count state samples: %v", err)
	}
	if samples != 2 {
		t.Fatalf("state samples = %d, want 2", samples)
	}
}
