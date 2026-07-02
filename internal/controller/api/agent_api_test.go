package api

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentProbeTargetsRequiresBearerToken(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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

func TestAgentProbeResultsRejectsUnknownTarget(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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
