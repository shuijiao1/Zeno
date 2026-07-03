package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
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
			ID                    string  `json:"id"`
			Name                  string  `json:"name"`
			Category              string  `json:"category"`
			Metric                string  `json:"metric"`
			Comparator            string  `json:"comparator"`
			Threshold             float64 `json:"threshold"`
			ThresholdUnit         string  `json:"threshold_unit"`
			DurationSec           int     `json:"duration_sec"`
			Enabled               bool    `json:"enabled"`
			NotificationEventType string  `json:"notification_event_type"`
			NotificationLabel     string  `json:"notification_label"`
			Description           string  `json:"description"`
		} `json:"rules"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(listRecorder.Body.String())).Decode(&listResponse); err != nil {
		t.Fatalf("decode alert rules: %v", err)
	}
	if len(listResponse.Rules) < 7 {
		t.Fatalf("default rules len = %d, want CPU/memory/disk/probe latency/probe loss/offline/recovery rules", len(listResponse.Rules))
	}
	rulesByID := map[string]struct {
		ID                    string  `json:"id"`
		Name                  string  `json:"name"`
		Category              string  `json:"category"`
		Metric                string  `json:"metric"`
		Comparator            string  `json:"comparator"`
		Threshold             float64 `json:"threshold"`
		ThresholdUnit         string  `json:"threshold_unit"`
		DurationSec           int     `json:"duration_sec"`
		Enabled               bool    `json:"enabled"`
		NotificationEventType string  `json:"notification_event_type"`
		NotificationLabel     string  `json:"notification_label"`
		Description           string  `json:"description"`
	}{}
	for _, rule := range listResponse.Rules {
		rulesByID[rule.ID] = rule
	}
	cpuRule, ok := rulesByID["cpu_high"]
	if !ok || cpuRule.Name != "CPU 使用率" || cpuRule.Category != "resource" || cpuRule.Metric != "cpu_percent" || cpuRule.Comparator != ">=" || cpuRule.Threshold != 90 || cpuRule.ThresholdUnit != "%" || cpuRule.DurationSec != 300 || !cpuRule.Enabled || cpuRule.NotificationEventType != "probe_unhealthy" || cpuRule.NotificationLabel != "异常" {
		t.Fatalf("cpu_high rule = %+v, want enabled resource CPU rule mapped to probe_unhealthy notification", cpuRule)
	}
	if rulesByID["node_offline"].NotificationEventType != "node_offline" || rulesByID["node_recovered"].NotificationEventType != "node_online" {
		t.Fatalf("offline/recovery rules should map to existing online/offline notification types: %+v", rulesByID)
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
		{name: "patch negative duration", method: http.MethodPatch, path: "/api/admin/v1/alert-rules/cpu_high", body: `{"duration_sec":-1}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
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

func assertNoSensitiveAlertRuleLeak(t *testing.T, raw string) {
	t.Helper()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains(lower, []byte("credential")) || bytes.Contains(lower, []byte("hash")) {
		t.Fatalf("alert rule response leaked sensitive fields: %s", raw)
	}
}
