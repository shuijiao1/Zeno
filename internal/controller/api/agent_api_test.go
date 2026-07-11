package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	if response.Version <= 0 {
		t.Fatalf("probe config version = %d, want positive version", response.Version)
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

func TestAgentProbeResultsRejectsStaleConfigVersionWithoutPartialWrite(t *testing.T) {
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
	post := func(payload string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", strings.NewReader(payload))
		request.Header.Set("X-Node-ID", "hytron")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	now := time.Now().UTC().Truncate(time.Second).Unix()
	currentPayload := `{"config_version":` + strconv.FormatInt(version, 10) + `,"rounds":[{"round_id":"current-version-a","target_id":"google-dns","ts":` + strconv.FormatInt(now, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5}]}]}`
	if recorder := post(currentPayload); recorder.Code != http.StatusAccepted {
		t.Fatalf("current version status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
	var roundsBefore, samplesBefore int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&roundsBefore); err != nil {
		t.Fatalf("count rounds before stale write: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&samplesBefore); err != nil {
		t.Fatalf("count samples before stale write: %v", err)
	}
	if roundsBefore != 1 || samplesBefore != 1 {
		t.Fatalf("initial accepted write counts rounds/samples=%d/%d, want 1/1", roundsBefore, samplesBefore)
	}
	displayOrder := 99
	if _, err := store.UpdateAdminProbeTarget(ctx, "google-dns", AdminProbeTargetUpdateRequest{DisplayOrder: &displayOrder}); err != nil {
		t.Fatalf("commit probe config mutation: %v", err)
	}
	newVersion, err := store.ProbeConfigVersion(ctx)
	if err != nil {
		t.Fatalf("probe config version after mutation: %v", err)
	}
	if newVersion == version {
		t.Fatalf("probe config mutation committed without bumping version %d", newVersion)
	}
	stalePayload := `{"config_version":` + strconv.FormatInt(version, 10) + `,"rounds":[` +
		`{"round_id":"stale-version-a","target_id":"google-dns","ts":` + strconv.FormatInt(now+1, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":13.5}]},` +
		`{"round_id":"stale-version-b","target_id":"cloudflare-dns","ts":` + strconv.FormatInt(now+1, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":14.5}]}` +
		`]}`
	staleRecorder := post(stalePayload)
	if staleRecorder.Code != http.StatusConflict {
		t.Fatalf("stale version status = %d, want 409; body=%s", staleRecorder.Code, staleRecorder.Body.String())
	}
	if !strings.Contains(staleRecorder.Body.String(), "stale_probe_config") {
		t.Fatalf("stale response body = %s, want recognizable stale_probe_config error", staleRecorder.Body.String())
	}
	var roundsAfter, samplesAfter int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&roundsAfter); err != nil {
		t.Fatalf("count rounds after stale write: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&samplesAfter); err != nil {
		t.Fatalf("count samples after stale write: %v", err)
	}
	if roundsAfter != roundsBefore || samplesAfter != samplesBefore {
		t.Fatalf("stale batch wrote partial rows rounds/samples %d/%d -> %d/%d", roundsBefore, samplesBefore, roundsAfter, samplesAfter)
	}
	// The previous Agent build used top-level "version". Keep that rolling-upgrade
	// alias version-checked too, rather than silently treating it as legacy zero.
	futurePayload := `{"version":999999,"rounds":[{"round_id":"future-version-a","target_id":"google-dns","ts":` + strconv.FormatInt(now+2, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":15.0}]}]}`
	if recorder := post(futurePayload); recorder.Code != http.StatusConflict {
		t.Fatalf("future version status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&roundsAfter); err != nil {
		t.Fatalf("count rounds after future-version write: %v", err)
	}
	if roundsAfter != roundsBefore {
		t.Fatalf("future-version batch wrote rounds %d -> %d", roundsBefore, roundsAfter)
	}
	currentAgainPayload := `{"config_version":` + strconv.FormatInt(newVersion, 10) + `,"rounds":[{"round_id":"current-version-b","target_id":"cloudflare-dns","ts":` + strconv.FormatInt(now+2, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":15.5}]}]}`
	if recorder := post(currentAgainPayload); recorder.Code != http.StatusAccepted {
		t.Fatalf("new current version status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentProbeResultsVersionZeroUsesCurrentConfigValidationAtomically(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	count := 1
	if _, err := store.UpdateAdminProbeTarget(ctx, "google-dns", AdminProbeTargetUpdateRequest{Count: &count}); err != nil {
		t.Fatalf("shrink google-dns count: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store})
	post := func(payload string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", strings.NewReader(payload))
		request.Header.Set("X-Node-ID", "hytron")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	now := time.Now().UTC().Truncate(time.Second).Unix()
	rejectedLegacyPayload := `{"config_version":0,"rounds":[` +
		`{"round_id":"legacy-zero-valid-first","target_id":"cloudflare-dns","ts":` + strconv.FormatInt(now, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":11.5}]},` +
		`{"round_id":"legacy-zero-too-many","target_id":"google-dns","ts":` + strconv.FormatInt(now, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5},{"seq":2,"success":true,"latency_ms":13.5}]}` +
		`]}`
	if recorder := post(rejectedLegacyPayload); recorder.Code != http.StatusBadRequest {
		t.Fatalf("legacy config_version=0 invalid current target status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	var rounds int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&rounds); err != nil {
		t.Fatalf("count rounds after rejected legacy zero batch: %v", err)
	}
	if rounds != 0 {
		t.Fatalf("legacy config_version=0 invalid batch wrote %d rounds, want 0", rounds)
	}
	acceptedLegacyPayload := `{"config_version":0,"rounds":[{"round_id":"legacy-zero-current-valid","target_id":"google-dns","ts":` + strconv.FormatInt(now+1, 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5}]}]}`
	if recorder := post(acceptedLegacyPayload); recorder.Code != http.StatusAccepted {
		t.Fatalf("legacy config_version=0 current-valid status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminProbeTargetMutationsBumpProbeConfigVersionInStoreTransaction(t *testing.T) {
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
		t.Fatalf("initial probe config version: %v", err)
	}
	assertBumped := func(action string) {
		t.Helper()
		current, err := store.ProbeConfigVersion(ctx)
		if err != nil {
			t.Fatalf("probe config version after %s: %v", action, err)
		}
		if current <= version {
			t.Fatalf("probe config version after %s = %d, want > %d", action, current, version)
		}
		version = current
	}
	port := 443
	if _, err := store.CreateAdminProbeTarget(ctx, AdminProbeTargetCreateRequest{
		ID:          "version-bump-target",
		Name:        "Version Bump Target",
		Type:        "tcping",
		Address:     "203.0.113.10",
		Port:        adminOptionalInt64{Set: true, Valid: true, Value: int64(port)},
		Count:       1,
		TimeoutMS:   minProbeTargetTimeoutMS,
		IntervalSec: minProbeTargetIntervalSec,
		Assignments: []AdminProbeTargetAssignmentUpdate{{NodeID: "hytron", Enabled: true}},
	}); err != nil {
		t.Fatalf("create probe target: %v", err)
	}
	assertBumped("create")
	displayOrder := 123
	if _, err := store.UpdateAdminProbeTarget(ctx, "version-bump-target", AdminProbeTargetUpdateRequest{DisplayOrder: &displayOrder}); err != nil {
		t.Fatalf("update probe target: %v", err)
	}
	assertBumped("update")
	if err := store.DeleteAdminProbeTarget(ctx, "version-bump-target"); err != nil {
		t.Fatalf("delete probe target: %v", err)
	}
	assertBumped("delete")
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
	zeroDuration := 0
	if _, err := store.UpdateAdminAlertRule(ctx, "cpu_high", AdminAlertRuleUpdateRequest{DurationSec: &zeroDuration}); err != nil {
		t.Fatalf("set cpu rule duration: %v", err)
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

func TestAgentProbeResultsStoresMeasuredTimeoutLatencyForCharts(t *testing.T) {
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

	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now.Add(time.Second).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":false,"latency_ms":2400,"error":"timeout"},{"seq":2,"success":false,"latency_ms":2600,"error":"timeout"}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var received int
	var loss, avg, median float64
	if err := store.db.QueryRowContext(ctx, `SELECT received, loss_percent, avg_ms, median_ms FROM probe_rounds WHERE node_id = 'hytron' AND target_id = 'google-dns' ORDER BY id DESC LIMIT 1`).Scan(&received, &loss, &avg, &median); err != nil {
		t.Fatalf("query probe round: %v", err)
	}
	if received != 0 || loss != 100 || avg != 2500 || median != 2500 {
		t.Fatalf("round received/loss/avg/median = %d/%.2f/%.2f/%.2f, want 0/100/2500/2500", received, loss, avg, median)
	}
}

func TestAgentProbeResultsCapsOverFiveSecondSamplesAndCountsTimeoutLoss(t *testing.T) {
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
	payload := []byte(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now.Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":7600}]}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("probe results status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
	}

	var received int
	var loss, avg float64
	if err := store.db.QueryRowContext(ctx, `SELECT received, loss_percent, avg_ms FROM probe_rounds WHERE target_id = 'google-dns' ORDER BY id DESC LIMIT 1`).Scan(&received, &loss, &avg); err != nil {
		t.Fatalf("query probe round: %v", err)
	}
	if received != 0 || loss != 100 || avg != 5000 {
		t.Fatalf("received/loss/avg = %d/%.0f/%.0f, want 0/100/5000", received, loss, avg)
	}
}

func TestLocalProbeObservationUsesConfiguredTimeoutWithHardFiveSecondCap(t *testing.T) {
	if got := localLatencyObservationTimeout(12 * time.Second); got != 5*time.Second {
		t.Fatalf("observation timeout = %s, want 5s", got)
	}
	if got := localLatencyObservationTimeout(500 * time.Millisecond); got != 500*time.Millisecond {
		t.Fatalf("short target observation timeout = %s, want configured timeout", got)
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
	zeroDuration := 0
	if _, err := store.UpdateAdminAlertRule(ctx, "cpu_high", AdminAlertRuleUpdateRequest{DurationSec: &zeroDuration}); err != nil {
		t.Fatalf("set cpu rule duration: %v", err)
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
	if len(paths) != 1 || len(forms) != 1 || !strings.Contains(forms[0], "CPU%E6%8C%81%E7%BB%AD%E5%8D%A0%E7%94%A8%E8%BF%87%E9%AB%98") {
		t.Fatalf("telegram request paths=%+v forms=%+v, want one CPU threshold notification", paths, forms)
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

func TestAgentProbeResultsDoNotClearResourceWarning(t *testing.T) {
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
	if status != "warning" {
		t.Fatalf("node status = %q, want service probe results to preserve resource warning", status)
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

func TestAgentProbeResultsDeduplicateExactRetriesButKeepDistinctRoundsInSameSecond(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	payload := []byte(`{"rounds":[{"round_id":"round-a","target_id":"google-dns","ts":` + ts + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5},{"seq":2,"success":true,"latency_ms":13.5}]}]}`)
	post := func(payload []byte) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
		request.Header.Set("X-Node-ID", "hytron")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		post(payload)
	}
	distinctPayload := []byte(`{"rounds":[{"round_id":"round-b","target_id":"google-dns","ts":` + ts + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5},{"seq":2,"success":true,"latency_ms":13.5}]}]}`)
	post(distinctPayload)
	conflictingPayload := []byte(`{"rounds":[{"round_id":"round-a","target_id":"google-dns","ts":` + strconv.FormatInt(time.Now().UTC().Add(time.Second).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":22.5}]}]}`)
	conflictRecorder := httptest.NewRecorder()
	conflictRequest := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(conflictingPayload))
	conflictRequest.Header.Set("X-Node-ID", "hytron")
	conflictRequest.Header.Set("Authorization", "Bearer test-agent-token")
	conflictRequest.Header.Set("Content-Type", "application/json")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(conflictRecorder, conflictRequest)
	if conflictRecorder.Code != http.StatusInternalServerError {
		t.Fatalf("conflicting reuse status = %d, want 500; body=%s", conflictRecorder.Code, conflictRecorder.Body.String())
	}
	var rounds, samples int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&rounds); err != nil {
		t.Fatalf("count probe rounds: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&samples); err != nil {
		t.Fatalf("count probe samples: %v", err)
	}
	if rounds != 2 || samples != 4 {
		t.Fatalf("probe retry/same-second distinct payload stored rounds=%d samples=%d, want 2/4", rounds, samples)
	}
}

func TestAgentProbeResultsRejectsTimestampSkewAndDuplicateSequence(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	tests := []struct {
		name    string
		payload string
	}{
		{name: "future timestamp", payload: `{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(time.Now().UTC().Add(6*time.Minute).Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5}]}]}`},
		{name: "duplicate sequence", payload: `{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(time.Now().UTC().Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5},{"seq":1,"success":true,"latency_ms":13.5}]}]}`},
		{name: "invalid round id", payload: `{"rounds":[{"round_id":"bad id!","target_id":"google-dns","ts":` + strconv.FormatInt(time.Now().UTC().Unix(), 10) + `,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":12.5}]}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", strings.NewReader(tc.payload))
			request.Header.Set("X-Node-ID", "hytron")
			request.Header.Set("Authorization", "Bearer test-agent-token")
			request.Header.Set("Content-Type", "application/json")
			NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	var rounds int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM probe_rounds`).Scan(&rounds); err != nil {
		t.Fatalf("count probe rounds: %v", err)
	}
	if rounds != 0 {
		t.Fatalf("probe rounds = %d, want no writes for invalid batches", rounds)
	}
}

func TestAgentProbeResultsRejectsProbeResourceLimitOverages(t *testing.T) {
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
	post := func(payload string) int {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", strings.NewReader(payload))
		request.Header.Set("X-Node-ID", "hytron")
		request.Header.Set("Authorization", "Bearer test-agent-token")
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	now := time.Now().UTC().Unix()
	fourSamples := `[{"seq":1,"success":true,"latency_ms":1},{"seq":2,"success":true,"latency_ms":2},{"seq":3,"success":true,"latency_ms":3},{"seq":4,"success":true,"latency_ms":4}]`
	if got := post(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now, 10) + `,"type":"tcping","samples":` + fourSamples + `}]}`); got != http.StatusBadRequest {
		t.Fatalf("status for samples above target count=%d, want 400", got)
	}

	count := 32
	timeoutMS := 100
	intervalSec := 5
	if _, err := store.UpdateAdminProbeTarget(ctx, "google-dns", AdminProbeTargetUpdateRequest{Count: &count, TimeoutMS: &timeoutMS, IntervalSec: &intervalSec}); err != nil {
		t.Fatalf("raise google-dns probe count for max-sample test: %v", err)
	}
	samples := make([]string, 0, maxProbeTargetCount+1)
	for seq := 1; seq <= maxProbeTargetCount+1; seq++ {
		samples = append(samples, fmt.Sprintf(`{"seq":%d,"success":true,"latency_ms":1}`, seq))
	}
	if got := post(`{"rounds":[{"target_id":"google-dns","ts":` + strconv.FormatInt(now, 10) + `,"type":"tcping","samples":[` + strings.Join(samples, ",") + `]}]}`); got != http.StatusBadRequest {
		t.Fatalf("status for samples above hard count=%d, want 400", got)
	}

	rounds := make([]string, 0, maxAgentProbeRounds+1)
	for index := 0; index < maxAgentProbeRounds+1; index++ {
		rounds = append(rounds, `{"target_id":"google-dns","ts":`+strconv.FormatInt(now, 10)+`,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":1}]}`)
	}
	if got := post(`{"rounds":[` + strings.Join(rounds, ",") + `]}`); got != http.StatusBadRequest {
		t.Fatalf("status for rounds above target cap=%d, want 400", got)
	}
	var written int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&written); err != nil {
		t.Fatalf("count probe rounds after rejected overages: %v", err)
	}
	if written != 0 {
		t.Fatalf("probe rounds written after rejected overages=%d, want 0", written)
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

	before := time.Now().UTC().Unix()
	ts := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second).Unix()
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
	after := time.Now().UTC().Unix()
	if status != "online" || lastSeen < before || lastSeen > after || agentVersion != "agent-test" {
		t.Fatalf("heartbeat persisted status=%q last_seen=%d agent_version=%q, want online/received-at %d..%d/agent-test", status, lastSeen, agentVersion, before, after)
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
	postAgentHeartbeat(t, handler, now.Unix(), "offline")

	paths, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 1 || paths[0] != "/bottelegram-bot-credential-value/sendMessage" {
		t.Fatalf("telegram paths = %+v, want one sendMessage request", paths)
	}
	if len(forms) != 1 || !strings.Contains(forms[0], "chat_id=7579942307") || !strings.Contains(forms[0], "%E7%A6%BB%E7%BA%BF") {
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
	defer cleanupTestHandler(t, handler)
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, now.Unix()); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	postAgentHeartbeat(t, handler, now.Unix(), "offline")

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
	httpHandler := NewHandler(HandlerOptions{Store: store, NotificationClient: slowTelegram.Client(), TelegramAPIBaseURL: slowTelegram.URL})
	defer cleanupTestHandler(t, httpHandler)
	defer close(release)
	postAgentHeartbeat(t, httpHandler, now.Unix(), "offline")
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

func TestStaleAgentOfflineCheckDispatchesWhenPublicStatusExpires(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Second).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, staleSeen); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	h := &handler{store: store, notificationSender: newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL), liveHub: newLiveUpdateHub(), presence: newAgentPresenceManager()}
	h.dispatchStaleAgentOfflineChecks(ctx)

	paths, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	if len(paths) != 1 || len(forms) != 1 || !strings.Contains(forms[0], "%E7%A6%BB%E7%BA%BF") {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want stale offline notification", paths, forms)
	}
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "offline" {
		t.Fatalf("stored status = %q, want stale check to persist offline", status)
	}
	var storedLastSeen int64
	if err := store.db.QueryRowContext(ctx, `SELECT last_seen_at FROM nodes WHERE id = 'hytron'`).Scan(&storedLastSeen); err != nil {
		t.Fatalf("query node last_seen_at: %v", err)
	}
	if storedLastSeen != staleSeen {
		t.Fatalf("last_seen_at = %d, want original agent heartbeat time %d", storedLastSeen, staleSeen)
	}
}

func TestStaleAgentOfflineCheckSkipsFreshHeartbeatRace(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Second).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, staleSeen); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}
	nodeIDs, err := store.StaleAgentOfflineNodeIDs(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("stale node ids: %v", err)
	}
	if len(nodeIDs) != 1 || nodeIDs[0] != "hytron" {
		t.Fatalf("stale node ids = %+v, want hytron", nodeIDs)
	}
	freshSeen := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, freshSeen); err != nil {
		t.Fatalf("set fresh heartbeat: %v", err)
	}
	transition, ok, err := store.RecordStaleAgentOfflineTransition(ctx, "hytron", time.Now().UTC())
	if err != nil {
		t.Fatalf("record stale offline transition: %v", err)
	}
	if ok || transition.Current.Status != "" {
		t.Fatalf("transition = %+v ok=%v, want skipped stale update after fresh heartbeat", transition, ok)
	}
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "online" {
		t.Fatalf("stored status = %q, want online after fresh heartbeat wins race", status)
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
}

func TestAgentStateDispatchesRecoveryAfterPersistedOffline(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	staleSeen := time.Now().UTC().Add(-nodeHeartbeatOfflineAfter - time.Second).Unix()
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, staleSeen); err != nil {
		t.Fatalf("set stale heartbeat: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegr…alue", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	h := &handler{store: store, notificationSender: newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL), liveHub: newLiveUpdateHub(), presence: newAgentPresenceManager()}
	if transition, ok, err := store.RecordStaleAgentOfflineTransition(ctx, "hytron", time.Now().UTC()); err != nil {
		t.Fatalf("record stale offline transition: %v", err)
	} else if !ok {
		t.Fatalf("stale offline transition skipped, want persisted offline")
	} else {
		h.dispatchAgentStatusNotification(store, transition, time.Now().UTC())
	}
	paths, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors after offline = %+v", errors)
	}
	if len(paths) != 1 || len(forms) != 1 || !strings.Contains(forms[0], "%E7%A6%BB%E7%BA%BF") {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want offline notification", paths, forms)
	}

	postAgentState(t, h.handleAgentState, time.Now().UTC().Unix(), 22.5)
	paths, forms, errors = telegram.waitForCalls(t, 2)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors after recovery = %+v", errors)
	}
	if len(paths) != 2 || len(forms) != 2 || !strings.Contains(forms[1], "%E6%81%A2%E5%A4%8D") {
		t.Fatalf("telegram calls paths=%+v forms=%+v, want state-triggered recovery notification", paths, forms)
	}
}

func TestAgentHeartbeatTransitionTreatsReceivedHeartbeatAsFreshLiveness(t *testing.T) {
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
	if transition.Previous.Status != "online" || transition.Current.Status != "online" {
		t.Fatalf("transition = %+v, want stored online -> online so stale public state does not send recovery-only notifications", transition)
	}
	eventType, ok := notificationEventTypeForStatusChange(transition.Previous.Status, transition.Current.Status)
	if ok || eventType != "" {
		t.Fatalf("event type = %q ok=%v, want no recovery-only notification", eventType, ok)
	}
}

func TestAgentHeartbeatTransitionDispatchesOfflineOnExplicitOfflineStatus(t *testing.T) {
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

	transition, err := store.RecordAgentHeartbeatTransition(ctx, "hytron", now, "offline", "agent-test")
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
	httpHandler := NewHandler(HandlerOptions{Store: store, TelegramAPIBaseURL: closedURL})
	defer cleanupTestHandler(t, httpHandler)
	recorder := postAgentHeartbeat(t, httpHandler, now.Unix(), "offline")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("heartbeat status = %d, want 202 even if notification send fails; body=%s", recorder.Code, recorder.Body.String())
	}
	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE id = 'hytron'`).Scan(&status); err != nil {
		t.Fatalf("query node status: %v", err)
	}
	if status != "offline" {
		t.Fatalf("stored node status = %q, want heartbeat status persisted despite notification failure", status)
	}
}

func TestRenewalNotificationScannerDispatchesDueNotificationOncePerDay(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	expiryDate := now.Add(3 * 24 * time.Hour).Format("2006-01-02")
	if _, err := store.UpdateAdminNode(ctx, "hytron", AdminNodeUpdateRequest{ExpiryDate: &expiryDate}); err != nil {
		t.Fatalf("set expiry date: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminAlertRule(ctx, "renewal_due", AdminAlertRuleUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable renewal_due alert rule: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	h := &handler{store: store, notificationSender: newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL)}
	if queued := h.queueDueRenewalNotifications(ctx, now); queued != 1 {
		t.Fatalf("first renewal scan queued %d deliveries, want 1", queued)
	}
	paths, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	messageText := ""
	if len(forms) == 1 {
		messageText = decodedTelegramText(forms[0])
	}
	if len(paths) != 1 || len(forms) != 1 || !strings.Contains(messageText, "⚠️[到期]") || !strings.Contains(messageText, formatRenewalMessageDate(expiryDate)) {
		t.Fatalf("telegram request paths=%+v forms=%+v, want one renewal due notification", paths, forms)
	}
	assertTelegramFormsDoNotLeakCredential(t, forms, "telegram-bot-credential-value")

	if queued := h.queueDueRenewalNotifications(ctx, now.Add(time.Minute)); queued != 0 {
		t.Fatalf("same-day duplicate renewal scan queued %d deliveries, want 0", queued)
	}
	paths, forms, errors = telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors after duplicate scan = %+v", errors)
	}
	if len(paths) != 1 || len(forms) != 1 {
		t.Fatalf("telegram calls after duplicate scan paths=%+v forms=%+v, want still one renewal notification", paths, forms)
	}

	if queued := h.queueDueRenewalNotifications(ctx, now.Add(24*time.Hour)); queued != 1 {
		t.Fatalf("next-day renewal scan queued %d deliveries, want 1", queued)
	}
	var deliveryCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_deliveries WHERE event_type = 'renewal_due'`).Scan(&deliveryCount); err != nil {
		t.Fatalf("count renewal deliveries after next-day scan: %v", err)
	}
	if deliveryCount != 2 {
		t.Fatalf("renewal delivery count after next-day scan = %d, want 2 daily deliveries", deliveryCount)
	}
	var markCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_event_marks WHERE event_type = 'renewal_due'`).Scan(&markCount); err != nil {
		t.Fatalf("count renewal marks after next-day scan: %v", err)
	}
	if markCount != 2 {
		t.Fatalf("renewal mark count after next-day scan = %d, want 2 daily marks", markCount)
	}
}

func TestRenewalNotificationScannerDispatchesRecurringBillingCycle(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Sharon", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	cycleDueDate := dateOnlyUTC(now).AddDate(0, 0, 1)
	finalExpiryDate := addMonthsClampedUTC(cycleDueDate, 1).Format("2006-01-02")
	billingCycle := "月"
	if _, err := store.UpdateAdminNode(ctx, "hytron", AdminNodeUpdateRequest{ExpiryDate: &finalExpiryDate, BillingCycle: &billingCycle}); err != nil {
		t.Fatalf("set recurring expiry date: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	threshold := 1.0
	if _, err := store.UpdateAdminAlertRule(ctx, "renewal_due", AdminAlertRuleUpdateRequest{Enabled: &enabled, Threshold: &threshold}); err != nil {
		t.Fatalf("enable renewal_due alert rule: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	h := &handler{store: store, notificationSender: newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL)}
	if queued := h.queueDueRenewalNotifications(ctx, now); queued != 1 {
		t.Fatalf("recurring renewal scan queued %d deliveries, want 1", queued)
	}
	paths, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram handler errors = %+v", errors)
	}
	cycleDueText := cycleDueDate.Format("2006-01-02")
	messageText := ""
	if len(forms) == 1 {
		messageText = decodedTelegramText(forms[0])
	}
	if len(paths) != 1 || len(forms) != 1 || !strings.Contains(messageText, "⚠️[到期]") || !strings.Contains(messageText, formatRenewalMessageDate(cycleDueText)) {
		t.Fatalf("telegram request paths=%+v forms=%+v, want renewal due notification for recurring billing date %s", paths, forms, cycleDueText)
	}
	if strings.Contains(messageText, finalExpiryDate) || strings.Contains(messageText, formatRenewalMessageDate(finalExpiryDate)) {
		t.Fatalf("telegram text %q used final expiry date %s, want recurring billing date %s", messageText, finalExpiryDate, cycleDueText)
	}
}

func TestAgentHeartbeatHostAndStateDoNotDispatchRenewalDueNotification(t *testing.T) {
	tests := []struct {
		name string
		path string
		body func(time.Time) map[string]any
	}{
		{
			name: "heartbeat",
			path: "/api/agent/v1/heartbeat",
			body: func(now time.Time) map[string]any {
				return map[string]any{"ts": now.Unix(), "status": "online", "agent_version": "agent-test"}
			},
		},
		{
			name: "host",
			path: "/api/agent/v1/host",
			body: func(time.Time) map[string]any {
				return map[string]any{"hostname": "hytron", "os_name": "Linux", "arch": "amd64", "cpu_cores": 2, "memory_total_bytes": 1024, "disk_total_bytes": 2048}
			},
		},
		{
			name: "state",
			path: "/api/agent/v1/state",
			body: func(now time.Time) map[string]any {
				return map[string]any{"ts": now.Unix(), "cpu_percent": 10, "memory_used_bytes": 512, "memory_total_bytes": 1024, "disk_used_bytes": 1024, "disk_total_bytes": 2048, "net_in_total_bytes": 100, "net_out_total_bytes": 200, "net_in_speed_bps": 1, "net_out_speed_bps": 2, "uptime_seconds": 60}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
			if err != nil {
				t.Fatalf("open sqlite store: %v", err)
			}
			defer store.Close()
			ctx := context.Background()
			if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
				t.Fatalf("seed preview data: %v", err)
			}
			expiryDate := time.Now().UTC().Add(3 * 24 * time.Hour).Format("2006-01-02")
			if _, err := store.UpdateAdminNode(ctx, "hytron", AdminNodeUpdateRequest{ExpiryDate: &expiryDate}); err != nil {
				t.Fatalf("set expiry date: %v", err)
			}
			enabled := true
			if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
				t.Fatalf("create notification channel: %v", err)
			}
			if _, err := store.UpdateAdminAlertRule(ctx, "renewal_due", AdminAlertRuleUpdateRequest{Enabled: &enabled}); err != nil {
				t.Fatalf("enable renewal rule: %v", err)
			}

			telegram := newTelegramTestCapture(t)
			now := time.Now().UTC().Truncate(time.Second)
			payload, err := json.Marshal(tc.body(now))
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(payload))
			request.Header.Set("X-Node-ID", "hytron")
			request.Header.Set("Authorization", "Bearer test-agent-token")
			request.Header.Set("Content-Type", "application/json")
			NewHandler(telegram.handlerOptions(store)).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
			}
			_, forms, captureErrors := telegram.waitForCalls(t, 0)
			if len(captureErrors) != 0 || len(forms) != 0 {
				t.Fatalf("renewal calls=%d errors=%v forms=%v, want no high-frequency renewal dispatch", len(forms), captureErrors, forms)
			}
		})
	}
}

func TestRenewalNotificationScheduledScannerRunsIndependently(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	expiryDate := time.Now().UTC().Add(3 * 24 * time.Hour).Format("2006-01-02")
	if _, err := store.UpdateAdminNode(ctx, "hytron", AdminNodeUpdateRequest{ExpiryDate: &expiryDate}); err != nil {
		t.Fatalf("set expiry date: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminAlertRule(ctx, "renewal_due", AdminAlertRuleUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable renewal rule: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	backgroundCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpHandler := NewHandler(HandlerOptions{Store: store, NotificationClient: telegram.server.Client(), TelegramAPIBaseURL: telegram.server.URL, RenewalNotificationInterval: 10 * time.Millisecond, BackgroundContext: backgroundCtx})
	defer cleanupTestHandler(t, httpHandler)
	_, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 || len(forms) != 1 || !strings.Contains(decodedTelegramText(forms[0]), "⚠️[到期]") {
		t.Fatalf("scheduled renewal calls=%d errors=%v forms=%v", len(forms), errors, forms)
	}
}

func TestQueueDueRenewalNotificationsDeduplicatesConcurrentScans(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	expiryDate := now.Add(3 * 24 * time.Hour).Format("2006-01-02")
	if _, err := store.UpdateAdminNode(ctx, "hytron", AdminNodeUpdateRequest{ExpiryDate: &expiryDate}); err != nil {
		t.Fatalf("set expiry date: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminAlertRule(ctx, "renewal_due", AdminAlertRuleUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable renewal rule: %v", err)
	}

	const workers = 8
	start := make(chan struct{})
	results := make(chan int, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			queued, err := store.QueueDueRenewalNotifications(ctx, now)
			if err != nil {
				errs <- err
				return
			}
			results <- queued
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent renewal scan failed: %v", err)
	}
	queuedTotal := 0
	for queued := range results {
		queuedTotal += queued
	}
	if queuedTotal != 1 {
		t.Fatalf("concurrent scans queued %d deliveries, want exactly 1", queuedTotal)
	}
	var deliveryCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_deliveries WHERE event_type = 'renewal_due'`).Scan(&deliveryCount); err != nil {
		t.Fatalf("count renewal deliveries: %v", err)
	}
	if deliveryCount != 1 {
		t.Fatalf("renewal delivery count = %d, want 1", deliveryCount)
	}
	var markCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_event_marks WHERE event_type = 'renewal_due'`).Scan(&markCount); err != nil {
		t.Fatalf("count renewal marks: %v", err)
	}
	if markCount != 1 {
		t.Fatalf("renewal mark count = %d, want 1", markCount)
	}
}

func TestQueueDueRenewalNotificationsSkipsPermanentNode(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	expiryDate := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")
	permanent := true
	if _, err := store.UpdateAdminNode(ctx, "hytron", AdminNodeUpdateRequest{ExpiryDate: &expiryDate, ExpiryPermanent: &permanent}); err != nil {
		t.Fatalf("set permanent expiry: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminAlertRule(ctx, "renewal_due", AdminAlertRuleUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable renewal rule: %v", err)
	}
	if queued, err := store.QueueDueRenewalNotifications(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("queue permanent renewal notifications: %v", err)
	} else if queued != 0 {
		t.Fatalf("permanent renewal scan queued %d deliveries, want 0", queued)
	}
	var deliveryCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_deliveries WHERE event_type = 'renewal_due'`).Scan(&deliveryCount); err != nil {
		t.Fatalf("count renewal deliveries: %v", err)
	}
	if deliveryCount != 0 {
		t.Fatalf("permanent renewal delivery count = %d, want 0", deliveryCount)
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

func cleanupTestHandler(t *testing.T, httpHandler http.Handler) {
	t.Helper()
	cleanup, ok := httpHandler.(interface{ Cleanup(context.Context) error })
	if !ok {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cleanup.Cleanup(shutdownCtx); err != nil {
		t.Errorf("cleanup handler: %v", err)
	}
}

func postAgentState(t *testing.T, handle func(http.ResponseWriter, *http.Request), ts int64, cpuPercent float64) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{
		"ts":                  ts,
		"cpu_percent":         cpuPercent,
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
	handle(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("state status = %d, want 202; body=%s", recorder.Code, recorder.Body.String())
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
	postState(ts+200, 1_400_000, 2_600_000, 22.5)

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

func TestAgentStateRejectsLargeClockSkewAndIgnoresOutOfOrderTrafficBaseline(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	postState := func(ts int64, inTotal, outTotal int64, want int) {
		t.Helper()
		body := map[string]any{
			"ts":                  ts,
			"cpu_percent":         12.5,
			"memory_used_bytes":   int64(3 * 1024 * 1024 * 1024),
			"memory_total_bytes":  int64(8 * 1024 * 1024 * 1024),
			"disk_used_bytes":     int64(40 * 1024 * 1024 * 1024),
			"disk_total_bytes":    int64(160 * 1024 * 1024 * 1024),
			"net_in_total_bytes":  inTotal,
			"net_out_total_bytes": outTotal,
			"net_in_speed_bps":    128.0,
			"net_out_speed_bps":   256.0,
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
		if recorder.Code != want {
			t.Fatalf("state status = %d, want %d; body=%s", recorder.Code, want, recorder.Body.String())
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	postState(now.Add(10*time.Minute).Unix(), 1_000, 1_000, http.StatusBadRequest)
	postState(now.Unix(), 1_000, 1_000, http.StatusAccepted)
	postState(now.Add(100*time.Second).Unix(), 2_000, 2_000, http.StatusAccepted)
	postState(now.Add(50*time.Second).Unix(), 100, 100, http.StatusAccepted)
	postState(now.Add(101*time.Second).Unix(), 2_100, 2_100, http.StatusAccepted)

	var billable int64
	if err := store.db.QueryRowContext(ctx, `SELECT billable_bytes FROM traffic_monthly WHERE node_id = 'hytron'`).Scan(&billable); err != nil {
		t.Fatalf("query monthly billable: %v", err)
	}
	if billable != 2200 {
		t.Fatalf("billable bytes = %d, want 2200 with out-of-order sample ignored as baseline", billable)
	}
}

func TestTrafficLastSampleMigrationBackfillsLatestStateTimestamp(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, ts := range []int64{now.Add(-time.Minute).Unix(), now.Unix()} {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES ('hytron', ?, 10)`, ts); err != nil {
			t.Fatalf("insert historical state sample: %v", err)
		}
	}
	month := billingPeriodKey(now, 1)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO traffic_monthly (
			node_id, month, in_bytes, out_bytes, billable_bytes,
			last_in_total_bytes, last_out_total_bytes, last_sample_ts, updated_at
		) VALUES ('hytron', ?, 250, 250, 500, 1000, 1000, NULL, ?)
	`, month, now.Add(-2*time.Minute).Unix()); err != nil {
		t.Fatalf("insert legacy traffic baseline: %v", err)
	}
	if err := store.ensureSchema(ctx); err != nil {
		t.Fatalf("rerun schema migration: %v", err)
	}
	var lastSampleTS int64
	if err := store.db.QueryRowContext(ctx, `SELECT last_sample_ts FROM traffic_monthly WHERE node_id = 'hytron' AND month = ?`, month).Scan(&lastSampleTS); err != nil {
		t.Fatalf("read migrated last sample timestamp: %v", err)
	}
	if lastSampleTS != now.Unix() {
		t.Fatalf("last_sample_ts = %d, want latest state timestamp %d", lastSampleTS, now.Unix())
	}
	baseState := AgentStateRequest{
		CPUPercent:       10,
		MemoryUsedBytes:  1,
		MemoryTotalBytes: 2,
		DiskUsedBytes:    1,
		DiskTotalBytes:   2,
		NetInTotalBytes:  100,
		NetOutTotalBytes: 100,
		NetInSpeedBps:    1,
		NetOutSpeedBps:   1,
		UptimeSeconds:    1,
	}
	baseState.TS = now.Add(-30 * time.Second).Unix()
	if err := store.InsertAgentState(ctx, "hytron", baseState); err != nil {
		t.Fatalf("insert delayed state: %v", err)
	}
	baseState.TS = now.Add(time.Second).Unix()
	baseState.NetInTotalBytes = 1100
	baseState.NetOutTotalBytes = 1100
	if err := store.InsertAgentState(ctx, "hytron", baseState); err != nil {
		t.Fatalf("insert current state: %v", err)
	}
	var billable int64
	if err := store.db.QueryRowContext(ctx, `SELECT billable_bytes FROM traffic_monthly WHERE node_id = 'hytron' AND month = ?`, month).Scan(&billable); err != nil {
		t.Fatalf("read billable traffic: %v", err)
	}
	if billable != 700 {
		t.Fatalf("billable bytes = %d, want 700 after ignoring delayed baseline", billable)
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
