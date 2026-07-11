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

func TestAdminAlertRulesListAndPatchWithoutSensitiveLeak(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/alert-rules", nil)
	listRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	assertNoSensitiveAlertRuleLeak(t, listRecorder.Body.String())
	var listResponse struct {
		Rules []struct {
			ID                    string   `json:"id"`
			Name                  string   `json:"name"`
			Category              string   `json:"category"`
			Metric                string   `json:"metric"`
			Comparator            string   `json:"comparator"`
			Threshold             float64  `json:"threshold"`
			ThresholdUnit         string   `json:"threshold_unit"`
			DurationSec           int      `json:"duration_sec"`
			Enabled               bool     `json:"enabled"`
			NotificationEventType string   `json:"notification_event_type"`
			NotificationLabel     string   `json:"notification_label"`
			Description           string   `json:"description"`
			ScopeNodeIDs          []string `json:"scope_node_ids"`
		} `json:"rules"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(listRecorder.Body.String())).Decode(&listResponse); err != nil {
		t.Fatalf("decode alert rules: %v", err)
	}
	if len(listResponse.Rules) != 5 {
		t.Fatalf("default rules len = %d, want CPU/memory/disk/offline/renewal rules", len(listResponse.Rules))
	}
	rulesByID := map[string]struct {
		ID                    string   `json:"id"`
		Name                  string   `json:"name"`
		Category              string   `json:"category"`
		Metric                string   `json:"metric"`
		Comparator            string   `json:"comparator"`
		Threshold             float64  `json:"threshold"`
		ThresholdUnit         string   `json:"threshold_unit"`
		DurationSec           int      `json:"duration_sec"`
		Enabled               bool     `json:"enabled"`
		NotificationEventType string   `json:"notification_event_type"`
		NotificationLabel     string   `json:"notification_label"`
		Description           string   `json:"description"`
		ScopeNodeIDs          []string `json:"scope_node_ids"`
	}{}
	for _, rule := range listResponse.Rules {
		rulesByID[rule.ID] = rule
	}
	cpuRule, ok := rulesByID["cpu_high"]
	if !ok || cpuRule.Name != "CPU 使用率" || cpuRule.Category != "resource" || cpuRule.Metric != "cpu_percent" || cpuRule.Comparator != ">=" || cpuRule.Threshold != 90 || cpuRule.ThresholdUnit != "%" || cpuRule.DurationSec != 300 || !cpuRule.Enabled || cpuRule.NotificationEventType != "probe_unhealthy" || cpuRule.NotificationLabel != "异常" {
		t.Fatalf("cpu_high rule = %+v, want enabled resource CPU rule mapped to probe_unhealthy notification with 300s window", cpuRule)
	}
	if cpuRule.Description != "" {
		t.Fatalf("cpu_high description = %q, want empty", cpuRule.Description)
	}
	if rulesByID["node_offline"].Name != "离线通知" || rulesByID["node_offline"].NotificationEventType != "node_offline" {
		t.Fatalf("offline rule should be the only liveness notification: %+v", rulesByID["node_offline"])
	}
	defaultDurations := map[string]int{"cpu_high": 300, "memory_high": 300, "disk_high": 300, "node_offline": 30}
	for ruleID, wantDuration := range defaultDurations {
		if rulesByID[ruleID].DurationSec != wantDuration {
			t.Fatalf("%s duration = %d, want default %ds", ruleID, rulesByID[ruleID].DurationSec, wantDuration)
		}
	}
	renewalRule := rulesByID["renewal_due"]
	if renewalRule.Name != "续费提醒" || renewalRule.Category != "billing" || renewalRule.Metric != "expiry_days" || renewalRule.Comparator != "<=" || renewalRule.Threshold != 3 || renewalRule.ThresholdUnit != "d" || renewalRule.Enabled || renewalRule.NotificationEventType != "renewal_due" || renewalRule.NotificationLabel != "续费" {
		t.Fatalf("renewal_due rule = %+v, want disabled billing renewal rule", renewalRule)
	}
	for _, retiredRuleID := range []string{"probe_latency_high", "probe_loss_high", "node_recovered"} {
		if _, ok := rulesByID[retiredRuleID]; ok {
			t.Fatalf("retired rule %s still present: %+v", retiredRuleID, rulesByID)
		}
	}

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/alert-rules/cpu_high", bytes.NewBufferString(`{"enabled": false, "threshold": 95.5, "duration_sec": 600}`))
	patchRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}
	assertNoSensitiveAlertRuleLeak(t, patchRecorder.Body.String())
	var patchResponse struct {
		Rule struct {
			ID          string  `json:"id"`
			Threshold   float64 `json:"threshold"`
			DurationSec int     `json:"duration_sec"`
			Enabled     bool    `json:"enabled"`
		} `json:"rule"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(patchRecorder.Body.String())).Decode(&patchResponse); err != nil {
		t.Fatalf("decode patched alert rule: %v", err)
	}
	if patchResponse.Rule.ID != "cpu_high" || patchResponse.Rule.Enabled || patchResponse.Rule.Threshold != 95.5 || patchResponse.Rule.DurationSec != 600 {
		t.Fatalf("patched rule = %+v, want updated enabled/threshold/duration", patchResponse.Rule)
	}
}

func TestDefaultAlertRuleDurationMigration(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM settings WHERE key = 'alert_default_durations_v2_migrated'`); err != nil {
		t.Fatalf("clear duration migration marker: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM settings WHERE key = 'resource_alert_duration_5m_migrated'`); err != nil {
		t.Fatalf("clear resource duration migration marker: %v", err)
	}
	oldDurations := map[string]int{
		"cpu_high":     300,
		"memory_high":  300,
		"disk_high":    600,
		"node_offline": 180,
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE alert_rules SET threshold = 180 WHERE id = 'node_offline'`); err != nil {
		t.Fatalf("set old offline threshold: %v", err)
	}
	for ruleID, duration := range oldDurations {
		if _, err := store.db.ExecContext(ctx, `UPDATE alert_rules SET duration_sec = ? WHERE id = ?`, duration, ruleID); err != nil {
			t.Fatalf("set old duration for %s: %v", ruleID, err)
		}
	}
	if err := store.ensureDefaultAlertRules(ctx); err != nil {
		t.Fatalf("ensure default alert rules: %v", err)
	}
	wantDurations := map[string]int{"cpu_high": 300, "memory_high": 300, "disk_high": 300, "node_offline": 30}
	for ruleID := range oldDurations {
		var duration int
		if err := store.db.QueryRowContext(ctx, `SELECT duration_sec FROM alert_rules WHERE id = ?`, ruleID).Scan(&duration); err != nil {
			t.Fatalf("read duration for %s: %v", ruleID, err)
		}
		if duration != wantDurations[ruleID] {
			t.Fatalf("%s duration = %d, want migrated default %ds", ruleID, duration, wantDurations[ruleID])
		}
	}
	var offlineThreshold float64
	if err := store.db.QueryRowContext(ctx, `SELECT threshold FROM alert_rules WHERE id = 'node_offline'`).Scan(&offlineThreshold); err != nil {
		t.Fatalf("read offline threshold: %v", err)
	}
	if offlineThreshold != 30 {
		t.Fatalf("node_offline threshold = %v, want migrated default 30", offlineThreshold)
	}
}

func TestDefaultAlertRuleDurationMigrationPreservesUserEditedValues(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM settings WHERE key IN ('alert_default_durations_v2_migrated', 'resource_alert_duration_5m_migrated')`); err != nil {
		t.Fatalf("clear migration markers: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE alert_rules
		SET duration_sec = 600, updated_at = created_at + 1
		WHERE id = 'disk_high'
	`); err != nil {
		t.Fatalf("set user-edited duration: %v", err)
	}
	if err := store.ensureDefaultAlertRules(ctx); err != nil {
		t.Fatalf("ensure default alert rules: %v", err)
	}
	var duration int
	if err := store.db.QueryRowContext(ctx, `SELECT duration_sec FROM alert_rules WHERE id = 'disk_high'`).Scan(&duration); err != nil {
		t.Fatalf("read disk duration: %v", err)
	}
	if duration != 600 {
		t.Fatalf("user-edited disk duration = %d, want preserved 600", duration)
	}
}

func TestResourceAlertDurationMigrationRecoversPartialLegacyMigration(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM settings WHERE key = 'resource_alert_duration_5m_migrated'`); err != nil {
		t.Fatalf("clear resource migration marker: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE alert_rules
		SET duration_sec = 60,
		    created_at = (SELECT updated_at - 100 FROM settings WHERE key = 'alert_default_durations_v2_migrated'),
		    updated_at = (SELECT updated_at FROM settings WHERE key = 'alert_default_durations_v2_migrated')
		WHERE id = 'cpu_high'
	`); err != nil {
		t.Fatalf("seed partially migrated resource rule: %v", err)
	}
	if err := store.ensureDefaultAlertRules(ctx); err != nil {
		t.Fatalf("ensure default alert rules: %v", err)
	}
	var duration int
	if err := store.db.QueryRowContext(ctx, `SELECT duration_sec FROM alert_rules WHERE id = 'cpu_high'`).Scan(&duration); err != nil {
		t.Fatalf("read cpu duration: %v", err)
	}
	if duration != 300 {
		t.Fatalf("partially migrated cpu duration = %d, want 300", duration)
	}
}

func TestNotificationTypeMigrationPreservesEditedAlertRules(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM settings WHERE key = 'notification_types_alert_rules_migrated'`); err != nil {
		t.Fatalf("clear migration marker: %v", err)
	}
	legacyUpdatedAt := time.Now().UTC().Add(-time.Hour).Unix()
	for _, eventType := range []string{"node_offline", "probe_unhealthy"} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO notification_types (event_type, enabled, updated_at)
			VALUES (?, 0, ?)
			ON CONFLICT(event_type) DO UPDATE SET enabled = 0, updated_at = excluded.updated_at
		`, eventType, legacyUpdatedAt); err != nil {
			t.Fatalf("set legacy notification type %s: %v", eventType, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE alert_rules SET enabled = 1, updated_at = created_at + 86400 WHERE id IN ('node_offline', 'cpu_high')`); err != nil {
		t.Fatalf("mark alert rules user-edited: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE alert_rules SET enabled = 1, updated_at = created_at WHERE id = 'memory_high'`); err != nil {
		t.Fatalf("reset untouched alert rule marker: %v", err)
	}
	if err := store.migrateNotificationTypesToAlertRules(ctx); err != nil {
		t.Fatalf("migrate notification types: %v", err)
	}
	for ruleID, wantEnabled := range map[string]int{"node_offline": 1, "cpu_high": 1, "memory_high": 0} {
		var enabled int
		if err := store.db.QueryRowContext(ctx, `SELECT enabled FROM alert_rules WHERE id = ?`, ruleID).Scan(&enabled); err != nil {
			t.Fatalf("read %s: %v", ruleID, err)
		}
		if enabled != wantEnabled {
			t.Fatalf("%s enabled = %d, want %d", ruleID, enabled, wantEnabled)
		}
	}
}

func TestResourceAlertRulesUseDurationWindowAverage(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateAdminNode(ctx, AdminNodeCreateRequest{ID: "hytron", DisplayName: "Hytron", CountryCode: "HK"}); err != nil {
		t.Fatalf("create node: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Second)
	record := func(ts time.Time, cpu float64) notificationStatusTransition {
		t.Helper()
		state := AgentStateRequest{
			TS:               ts.Unix(),
			CPUPercent:       cpu,
			MemoryUsedBytes:  512,
			MemoryTotalBytes: 2048,
			DiskUsedBytes:    1024,
			DiskTotalBytes:   4096,
		}
		if err := store.InsertAgentState(ctx, "hytron", state); err != nil {
			t.Fatalf("insert state at %s: %v", ts, err)
		}
		transition, err := store.RecordAgentStateAlertRuleTransition(ctx, "hytron", ts, state)
		if err != nil {
			t.Fatalf("record alert transition at %s: %v", ts, err)
		}
		return transition
	}

	record(base.Add(-300*time.Second), 50)
	currentHighButAverageLow := record(base, 100)
	if currentHighButAverageLow.Current.Status != "online" {
		t.Fatalf("current high status = %+v, want online while 300s CPU average is below threshold", currentHighButAverageLow.Current)
	}
	record(base.Add(150*time.Second), 100)
	windowHigh := record(base.Add(300*time.Second), 100)
	if windowHigh.Current.Status != "warning" {
		t.Fatalf("window high status = %+v, want warning when 300s CPU average exceeds threshold", windowHigh.Current)
	}
}

func TestAdminAlertRulesRejectUnauthorizedUnknownAndInvalidRequests(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		adminToken string
		wantStatus int
	}{
		{name: "list missing admin token", method: http.MethodGet, path: "/api/admin/v1/alert-rules", wantStatus: http.StatusUnauthorized},
		{name: "patch unknown rule", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/missing", body: `{"enabled":true}`, adminToken: "admin-pass", wantStatus: http.StatusNotFound},
		{name: "patch empty body", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/cpu_high", body: `{}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch negative threshold", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/cpu_high", body: `{"threshold":-1}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch renewal threshold unsupported days", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/renewal_due", body: `{"threshold":2}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch renewal threshold above 30 days", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/renewal_due", body: `{"threshold":31}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch renewal threshold fractional days", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/renewal_due", body: `{"threshold":1.5}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch negative duration", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/cpu_high", body: `{"duration_sec":-1}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch blank scope node", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/cpu_high", body: `{"scope_node_ids":[""]}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch duplicate scope node", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/cpu_high", body: `{"scope_node_ids":["hytron","hytron"]}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch unknown scope node", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/cpu_high", body: `{"scope_node_ids":["missing"]}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "delete unsupported", method: http.MethodDelete, path: "/api/admin/v1/alert-rules/cpu_high", adminToken: "admin-pass", wantStatus: http.StatusMethodNotAllowed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			if tc.adminToken != "" {
				request.Header.Set("X-Admin-Token", tc.adminToken)
			}
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			assertNoSensitiveAlertRuleLeak(t, recorder.Body.String())
		})
	}
}

func TestNotificationDispatchRequiresEnabledAlertRuleForEvent(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "dispatch-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable node_offline notification type: %v", err)
	}
	label, channels, err := store.EnabledNotificationChannelsForEvent(ctx, "node_offline", "hytron")
	if err != nil {
		t.Fatalf("enabled channels before disabling rule: %v", err)
	}
	if label != "离线" || len(channels) != 1 {
		t.Fatalf("channels before disabling rule label=%q len=%d, want one node_offline channel", label, len(channels))
	}

	disabled := false
	if _, err := store.UpdateAdminAlertRule(ctx, "node_offline", AdminAlertRuleUpdateRequest{Enabled: &disabled}); err != nil {
		t.Fatalf("disable node offline rule: %v", err)
	}
	label, channels, err = store.EnabledNotificationChannelsForEvent(ctx, "node_offline", "hytron")
	if err != nil {
		t.Fatalf("enabled channels after disabling rule: %v", err)
	}
	if label != "离线" || len(channels) != 0 {
		t.Fatalf("channels after disabling node_offline rule label=%q len=%d, want no dispatch channels", label, len(channels))
	}
}

func TestAdminAlertRuleScopeCanLimitAndClearServers(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if _, err := store.CreateAdminNode(ctx, AdminNodeCreateRequest{ID: "backup", DisplayName: "Backup", CountryCode: "US", DisplayOrder: 9}); err != nil {
		t.Fatalf("create backup node: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/alert-rules/cpu_high", bytes.NewBufferString(`{"scope_node_ids":["backup"]}`))
	patchRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("scope patch status = %d, want 200; body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}
	assertNoSensitiveAlertRuleLeak(t, patchRecorder.Body.String())
	var patchResponse struct {
		Rule struct {
			ID           string   `json:"id"`
			ScopeNodeIDs []string `json:"scope_node_ids"`
		} `json:"rule"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(patchRecorder.Body.String())).Decode(&patchResponse); err != nil {
		t.Fatalf("decode scoped rule: %v", err)
	}
	if patchResponse.Rule.ID != "cpu_high" || len(patchResponse.Rule.ScopeNodeIDs) != 1 || patchResponse.Rule.ScopeNodeIDs[0] != "backup" {
		t.Fatalf("scoped rule = %+v, want backup-only scope", patchResponse.Rule)
	}

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/alert-rules", nil)
	listRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var listResponse struct {
		Rules []struct {
			ID           string   `json:"id"`
			ScopeNodeIDs []string `json:"scope_node_ids"`
		} `json:"rules"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(listRecorder.Body.String())).Decode(&listResponse); err != nil {
		t.Fatalf("decode rule list: %v", err)
	}
	foundBackupScope := false
	for _, rule := range listResponse.Rules {
		if rule.ID == "cpu_high" {
			foundBackupScope = len(rule.ScopeNodeIDs) == 1 && rule.ScopeNodeIDs[0] == "backup"
		}
	}
	if !foundBackupScope {
		t.Fatalf("list response did not preserve backup scope: %s", listRecorder.Body.String())
	}

	thresholdRecorder := httptest.NewRecorder()
	thresholdRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/alert-rules/cpu_high", bytes.NewBufferString(`{"threshold":91}`))
	thresholdRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(thresholdRecorder, thresholdRequest)
	if thresholdRecorder.Code != http.StatusOK {
		t.Fatalf("threshold-only patch status = %d, want 200; body=%s", thresholdRecorder.Code, thresholdRecorder.Body.String())
	}
	var thresholdResponse struct {
		Rule struct {
			Threshold    float64  `json:"threshold"`
			ScopeNodeIDs []string `json:"scope_node_ids"`
		} `json:"rule"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(thresholdRecorder.Body.String())).Decode(&thresholdResponse); err != nil {
		t.Fatalf("decode threshold-only rule: %v", err)
	}
	if thresholdResponse.Rule.Threshold != 91 || len(thresholdResponse.Rule.ScopeNodeIDs) != 1 || thresholdResponse.Rule.ScopeNodeIDs[0] != "backup" {
		t.Fatalf("threshold-only patch = %+v, want threshold update with backup scope preserved", thresholdResponse.Rule)
	}

	clearRecorder := httptest.NewRecorder()
	clearRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/alert-rules/cpu_high", bytes.NewBufferString(`{"scope_node_ids":[]}`))
	clearRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(clearRecorder, clearRequest)
	if clearRecorder.Code != http.StatusOK {
		t.Fatalf("clear scope status = %d, want 200; body=%s", clearRecorder.Code, clearRecorder.Body.String())
	}
	var clearResponse struct {
		Rule struct {
			ScopeNodeIDs []string `json:"scope_node_ids"`
		} `json:"rule"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(clearRecorder.Body.String())).Decode(&clearResponse); err != nil {
		t.Fatalf("decode cleared rule: %v", err)
	}
	if len(clearResponse.Rule.ScopeNodeIDs) != 0 {
		t.Fatalf("cleared scope = %+v, want global empty scope", clearResponse.Rule.ScopeNodeIDs)
	}
}

func TestAlertRuleScopeLimitsStateEvaluationAndCurrentStates(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if _, err := store.CreateAdminNode(ctx, AdminNodeCreateRequest{ID: "backup", DisplayName: "Backup", CountryCode: "US"}); err != nil {
		t.Fatalf("create backup node: %v", err)
	}
	scopeNodeIDs := []string{"backup"}
	zeroDuration := 0
	if _, err := store.UpdateAdminAlertRule(ctx, "cpu_high", AdminAlertRuleUpdateRequest{ScopeNodeIDs: &scopeNodeIDs, DurationSec: &zeroDuration}); err != nil {
		t.Fatalf("scope cpu rule: %v", err)
	}

	ts := time.Now().UTC()
	hytronTransition, err := store.RecordAgentStateAlertRuleTransition(ctx, "hytron", ts, AgentStateRequest{
		TS:               ts.Unix(),
		CPUPercent:       95,
		MemoryUsedBytes:  512,
		MemoryTotalBytes: 2048,
		DiskUsedBytes:    1024,
		DiskTotalBytes:   4096,
	})
	if err != nil {
		t.Fatalf("record hytron state: %v", err)
	}
	if hytronTransition.Current.Status != "online" {
		t.Fatalf("hytron status = %+v, want online because cpu_high is scoped to backup", hytronTransition.Current)
	}
	backupTransition, err := store.RecordAgentStateAlertRuleTransition(ctx, "backup", ts, AgentStateRequest{
		TS:               ts.Unix(),
		CPUPercent:       95,
		MemoryUsedBytes:  512,
		MemoryTotalBytes: 2048,
		DiskUsedBytes:    1024,
		DiskTotalBytes:   4096,
	})
	if err != nil {
		t.Fatalf("record backup state: %v", err)
	}
	if backupTransition.Current.Status != "warning" {
		t.Fatalf("backup status = %+v, want warning because cpu_high applies", backupTransition.Current)
	}

	var activeBackupCPU int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_rule_states WHERE rule_id = 'cpu_high' AND node_id = 'backup' AND active = 1`).Scan(&activeBackupCPU); err != nil {
		t.Fatalf("count backup cpu state: %v", err)
	}
	var activeHytronCPU int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_rule_states WHERE rule_id = 'cpu_high' AND node_id = 'hytron' AND active = 1`).Scan(&activeHytronCPU); err != nil {
		t.Fatalf("count hytron cpu state: %v", err)
	}
	if activeBackupCPU != 1 || activeHytronCPU != 0 {
		t.Fatalf("active scoped CPU states backup=%d hytron=%d, want backup only", activeBackupCPU, activeHytronCPU)
	}
}

func TestNotificationDispatchRespectsAlertRuleNodeScope(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if _, err := store.CreateAdminNode(ctx, AdminNodeCreateRequest{ID: "backup", DisplayName: "Backup", CountryCode: "US"}); err != nil {
		t.Fatalf("create backup node: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "dispatch-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable node_offline notification type: %v", err)
	}
	scopeNodeIDs := []string{"backup"}
	if _, err := store.UpdateAdminAlertRule(ctx, "node_offline", AdminAlertRuleUpdateRequest{ScopeNodeIDs: &scopeNodeIDs}); err != nil {
		t.Fatalf("scope node_offline rule: %v", err)
	}

	label, hytronChannels, err := store.EnabledNotificationChannelsForEvent(ctx, "node_offline", "hytron")
	if err != nil {
		t.Fatalf("hytron channels: %v", err)
	}
	if label != "离线" || len(hytronChannels) != 0 {
		t.Fatalf("hytron channels label=%q len=%d, want no channels outside node scope", label, len(hytronChannels))
	}
	label, backupChannels, err := store.EnabledNotificationChannelsForEvent(ctx, "node_offline", "backup")
	if err != nil {
		t.Fatalf("backup channels: %v", err)
	}
	if label != "离线" || len(backupChannels) != 1 {
		t.Fatalf("backup channels label=%q len=%d, want one scoped channel", label, len(backupChannels))
	}
}

func assertNoSensitiveAlertRuleLeak(t *testing.T, raw string) {
	t.Helper()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains(lower, []byte("credential")) || bytes.Contains(lower, []byte("hash")) {
		t.Fatalf("alert rule response leaked sensitive fields: %s", raw)
	}
}
