package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestAdminAlertRuleStatesExposeCurrentRuleHitsWithoutSensitiveLeak(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	ts := time.Now().UTC().Truncate(time.Second)
	if _, err := store.RecordAgentStateAlertRuleTransition(ctx, "hytron", ts, AgentStateRequest{
		TS:               ts.Unix(),
		CPUPercent:       95.25,
		MemoryUsedBytes:  512,
		MemoryTotalBytes: 2048,
		DiskUsedBytes:    1024,
		DiskTotalBytes:   4096,
	}); err != nil {
		t.Fatalf("record state alert transition: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/alert-rule-states", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	assertNoSensitiveAlertRuleLeak(t, recorder.Body.String())

	var response struct {
		States []struct {
			NodeID                string   `json:"node_id"`
			NodeName              string   `json:"node_name"`
			NodeStatus            string   `json:"node_status"`
			RuleID                string   `json:"rule_id"`
			RuleName              string   `json:"rule_name"`
			Category              string   `json:"category"`
			Metric                string   `json:"metric"`
			Comparator            string   `json:"comparator"`
			Threshold             float64  `json:"threshold"`
			ThresholdUnit         string   `json:"threshold_unit"`
			LastValue             *float64 `json:"last_value"`
			Active                bool     `json:"active"`
			NotificationEventType string   `json:"notification_event_type"`
			NotificationLabel     string   `json:"notification_label"`
			FirstSeenAt           string   `json:"first_seen_at"`
			LastSeenAt            string   `json:"last_seen_at"`
			UpdatedAt             string   `json:"updated_at"`
		} `json:"states"`
		ActiveCount int `json:"active_count"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ActiveCount != 1 || len(response.States) != 1 {
		t.Fatalf("response active_count=%d states=%d, want one active rule state: %s", response.ActiveCount, len(response.States), recorder.Body.String())
	}
	state := response.States[0]
	if state.NodeID != "hytron" || state.NodeName != "Hytron" || state.NodeStatus != "warning" {
		t.Fatalf("node fields = %+v, want hytron warning row", state)
	}
	if state.RuleID != "cpu_high" || state.RuleName != "CPU 使用率" || state.Metric != "cpu_percent" || state.Comparator != ">=" || state.Threshold != 90 || state.ThresholdUnit != "%" {
		t.Fatalf("rule fields = %+v, want CPU threshold row", state)
	}
	if state.LastValue == nil || *state.LastValue != 95.25 {
		t.Fatalf("last_value = %v, want 95.25", state.LastValue)
	}
	if !state.Active || state.NotificationEventType != "probe_unhealthy" || state.NotificationLabel != "异常" {
		t.Fatalf("notification/status fields = %+v, want active probe_unhealthy/异常", state)
	}
	if state.FirstSeenAt != ts.Format(time.RFC3339) || state.LastSeenAt != ts.Format(time.RFC3339) || state.UpdatedAt == "" {
		t.Fatalf("timestamps = first:%q last:%q updated:%q", state.FirstSeenAt, state.LastSeenAt, state.UpdatedAt)
	}
}

func TestAdminAlertRuleStatesDoNotCountDisabledNodesAsActive(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	ts := time.Date(2026, 7, 4, 11, 0, 0, 0, time.UTC)
	if _, err := store.RecordAgentStateAlertRuleTransition(ctx, "hytron", ts, AgentStateRequest{
		TS:               ts.Unix(),
		CPUPercent:       95.25,
		MemoryUsedBytes:  512,
		MemoryTotalBytes: 2048,
		DiskUsedBytes:    1024,
		DiskTotalBytes:   4096,
	}); err != nil {
		t.Fatalf("record state alert transition: %v", err)
	}
	disabled := true
	if _, err := store.UpdateAdminNode(ctx, "hytron", AdminNodeUpdateRequest{Disabled: &disabled}); err != nil {
		t.Fatalf("disable node: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/alert-rule-states", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		States []struct {
			NodeStatus string `json:"node_status"`
			Active     bool   `json:"active"`
		} `json:"states"`
		ActiveCount int `json:"active_count"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ActiveCount != 0 || len(response.States) != 1 || response.States[0].Active || response.States[0].NodeStatus != "disabled" {
		t.Fatalf("disabled node state = active_count:%d states:%+v, want inactive disabled row", response.ActiveCount, response.States)
	}
}

func TestAdminAlertRuleStatesReevaluateCurrentThresholdOnRuleUpdate(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	ts := time.Date(2026, 7, 4, 11, 0, 0, 0, time.UTC)
	if _, err := store.RecordAgentStateAlertRuleTransition(ctx, "hytron", ts, AgentStateRequest{
		TS:               ts.Unix(),
		CPUPercent:       95.25,
		MemoryUsedBytes:  512,
		MemoryTotalBytes: 2048,
		DiskUsedBytes:    1024,
		DiskTotalBytes:   4096,
	}); err != nil {
		t.Fatalf("record state alert transition: %v", err)
	}
	raisedThreshold := 99.0
	if _, err := store.UpdateAdminAlertRule(ctx, "cpu_high", AdminAlertRuleUpdateRequest{Threshold: &raisedThreshold}); err != nil {
		t.Fatalf("raise threshold: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/alert-rule-states", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		States []struct {
			Threshold float64  `json:"threshold"`
			LastValue *float64 `json:"last_value"`
			Active    bool     `json:"active"`
		} `json:"states"`
		ActiveCount int `json:"active_count"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ActiveCount != 0 || len(response.States) != 1 || response.States[0].Active || response.States[0].Threshold != 99 || response.States[0].LastValue == nil || *response.States[0].LastValue != 95.25 {
		t.Fatalf("threshold-updated state = active_count:%d states:%+v, want inactive row for 95.25 < 99", response.ActiveCount, response.States)
	}
}

func TestAdminAlertRuleStatesKeepLegacyActiveRowsWithoutLastValue(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Date(2026, 7, 4, 11, 0, 0, 0, time.UTC).Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO alert_rule_states (node_id, rule_id, active, first_seen_at, last_seen_at, last_value, updated_at)
		VALUES ('hytron', 'cpu_high', 1, ?, ?, NULL, ?)
	`, now, now, now); err != nil {
		t.Fatalf("insert legacy active row: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/alert-rule-states", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		States []struct {
			LastValue *float64 `json:"last_value"`
			Active    bool     `json:"active"`
		} `json:"states"`
		ActiveCount int `json:"active_count"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ActiveCount != 1 || len(response.States) != 1 || !response.States[0].Active || response.States[0].LastValue != nil {
		t.Fatalf("legacy active row = active_count:%d states:%+v, want active row with null last_value", response.ActiveCount, response.States)
	}
}

func TestAdminAlertRuleStatesRequireAdminToken(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/alert-rule-states", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("token")) || bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("secret")) || bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("credential")) || bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("hash")) {
		t.Fatalf("auth failure leaked sensitive wording: %s", recorder.Body.String())
	}
}
