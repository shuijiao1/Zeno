package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminNodesRequiresAdminToken(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("token")) || bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("secret")) {
		t.Fatalf("admin auth failure body should not leak token/secret wording: %s", recorder.Body.String())
	}
}

func TestAdminNodesListsEnabledAndDisabledNodesWithoutTokenHashes(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if err := store.RecordAgentHeartbeat(ctx, "hytron", time.Now().UTC().Truncate(time.Second), "online", "agent-test"); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	now := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, disabled, created_at, updated_at)
		VALUES ('disabled-node', 'Disabled Node', 'disabled-token-hash', 'no_data', 'US', 1, ?, ?)
	`, now, now); err != nil {
		t.Fatalf("insert disabled node: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	raw := recorder.Body.String()
	if bytes.Contains(bytes.ToLower([]byte(raw)), []byte("token")) || bytes.Contains(bytes.ToLower([]byte(raw)), []byte("secret")) || bytes.Contains([]byte(raw), []byte("disabled-token-hash")) {
		t.Fatalf("admin nodes response leaked sensitive fields: %s", raw)
	}

	var response struct {
		Nodes []struct {
			ID           string  `json:"id"`
			DisplayName  string  `json:"display_name"`
			Status       string  `json:"status"`
			CountryCode  string  `json:"country_code"`
			Disabled     bool    `json:"disabled"`
			LastSeenAt   *string `json:"last_seen_at"`
			AgentVersion string  `json:"agent_version"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(raw)).Decode(&response); err != nil {
		t.Fatalf("decode admin nodes: %v", err)
	}
	if len(response.Nodes) != 2 {
		t.Fatalf("admin nodes len = %d, want both enabled and disabled nodes", len(response.Nodes))
	}
	if response.Nodes[0].ID != "disabled-node" || !response.Nodes[0].Disabled {
		t.Fatalf("first admin node = %+v, want disabled-node visible with disabled=true", response.Nodes[0])
	}
	if response.Nodes[1].ID != "hytron" || response.Nodes[1].DisplayName != "Hytron" || response.Nodes[1].Status != "online" || response.Nodes[1].CountryCode != "HK" || response.Nodes[1].LastSeenAt == nil || response.Nodes[1].AgentVersion != "agent-test" {
		t.Fatalf("hytron admin node = %+v, want persisted management fields", response.Nodes[1])
	}
}

func TestAdminNodePatchUpdatesEditableFieldsAndReturnsSafeDTO(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "US", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/nodes/hytron", bytes.NewBufferString(`{
		"display_name": "  Hytron Edited  ",
		"country_code": " hk ",
		"region": "  Hong Kong  ",
		"monthly_quota_bytes": 123456789,
		"disabled": true
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	raw := recorder.Body.String()
	if bytes.Contains(bytes.ToLower([]byte(raw)), []byte("token")) || bytes.Contains(bytes.ToLower([]byte(raw)), []byte("secret")) || bytes.Contains([]byte(raw), []byte("test-agent-token")) {
		t.Fatalf("admin node update response leaked sensitive fields: %s", raw)
	}
	var response struct {
		Node struct {
			ID                string `json:"id"`
			DisplayName       string `json:"display_name"`
			Status            string `json:"status"`
			CountryCode       string `json:"country_code"`
			Region            string `json:"region"`
			Disabled          bool   `json:"disabled"`
			MonthlyQuotaBytes int64  `json:"monthly_quota_bytes"`
		} `json:"node"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(raw)).Decode(&response); err != nil {
		t.Fatalf("decode updated admin node: %v", err)
	}
	if response.Node.ID != "hytron" || response.Node.DisplayName != "Hytron Edited" || response.Node.Status != "disabled" || response.Node.CountryCode != "HK" || response.Node.Region != "Hong Kong" || !response.Node.Disabled || response.Node.MonthlyQuotaBytes != 123456789 {
		t.Fatalf("updated admin node = %+v, want trimmed editable fields and disabled status", response.Node)
	}

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary after disabling node: %v", err)
	}
	if len(summary.Nodes) != 0 {
		t.Fatalf("public summary should hide disabled node, got %+v", summary.Nodes)
	}
}

func TestAdminNodePatchRejectsUnauthorizedUnknownAndInvalidRequests(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	cases := []struct {
		name       string
		nodeID     string
		body       string
		adminToken string
		wantStatus int
	}{
		{name: "missing token", nodeID: "hytron", body: `{"display_name":"Changed"}`, wantStatus: http.StatusUnauthorized},
		{name: "unknown node", nodeID: "missing", body: `{"display_name":"Changed"}`, adminToken: "admin-pass", wantStatus: http.StatusNotFound},
		{name: "blank display name", nodeID: "hytron", body: `{"display_name":"   "}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "negative monthly quota", nodeID: "hytron", body: `{"monthly_quota_bytes":-1}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/nodes/"+tc.nodeID, bytes.NewBufferString(tc.body))
			if tc.adminToken != "" {
				request.Header.Set("X-Admin-Token", tc.adminToken)
			}
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("token")) || bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("secret")) {
				t.Fatalf("error body should not leak sensitive wording: %s", recorder.Body.String())
			}
		})
	}
}

func TestAdminNodeCreateAddsEditableNodeWithoutReturningSecrets(t *testing.T) {
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
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes", bytes.NewBufferString(`{
		"display_name": "  New Server  ",
		"country_code": " us ",
		"region": "  Los Angeles  ",
		"monthly_quota_bytes": 1099511627776
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	raw := recorder.Body.String()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) {
		t.Fatalf("admin node create response leaked sensitive wording: %s", raw)
	}
	var response struct {
		Node struct {
			ID                string `json:"id"`
			DisplayName       string `json:"display_name"`
			Status            string `json:"status"`
			CountryCode       string `json:"country_code"`
			Region            string `json:"region"`
			Disabled          bool   `json:"disabled"`
			MonthlyQuotaBytes int64  `json:"monthly_quota_bytes"`
		} `json:"node"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(raw)).Decode(&response); err != nil {
		t.Fatalf("decode created admin node: %v", err)
	}
	if response.Node.ID == "" || response.Node.DisplayName != "New Server" || response.Node.Status != "no_data" || response.Node.CountryCode != "US" || response.Node.Region != "Los Angeles" || response.Node.Disabled || response.Node.MonthlyQuotaBytes != 1099511627776 {
		t.Fatalf("created admin node = %+v, want trimmed editable no_data node", response.Node)
	}

	targets, err := store.EnabledProbeTargets(ctx, response.Node.ID)
	if err != nil {
		t.Fatalf("enabled probe targets for created node: %v", err)
	}
	if len(targets) != len(DefaultPreviewProbeTargets()) {
		t.Fatalf("created node targets = %d, want default target assignment", len(targets))
	}
}

func TestAdminNodeInstallCommandRotatesAgentCredentialAndUsesRequestHost(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/hytron/install-command", nil)
	request.Host = "probe.example.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass"), AgentBinaryPath: "/opt/zeno/current/bin/zeno-agent", AgentVersion: "testsha"}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		NodeID  string `json:"node_id"`
		Command string `json:"command"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode install command: %v", err)
	}
	if response.NodeID != "hytron" {
		t.Fatalf("node_id = %q, want hytron", response.NodeID)
	}
	if !strings.Contains(response.Command, "https://probe.example.com") || !strings.Contains(response.Command, "/api/public/v1/agent/linux-amd64") || !strings.Contains(response.Command, "-node-id 'hytron'") || !strings.Contains(response.Command, "-version 'testsha'") {
		t.Fatalf("install command missing controller URL, binary endpoint, node id, or version: %s", response.Command)
	}
	if !strings.Contains(response.Command, "/usr/local/bin/zeno-agent") || !strings.Contains(response.Command, "/etc/zeno/agent-token") || !strings.Contains(response.Command, "zeno-agent.service") {
		t.Fatalf("install command should use Zeno agent names and paths: %s", response.Command)
	}
	credential := extractQuotedInstallCredential(t, response.Command)
	if credential == "old-agent-token" {
		t.Fatalf("install command reused old agent credential")
	}
	allowed, err := store.AuthorizeAgent(ctx, "hytron", credential)
	if err != nil {
		t.Fatalf("authorize generated credential: %v", err)
	}
	if !allowed {
		t.Fatalf("generated credential should authorize agent")
	}
}

func extractQuotedInstallCredential(t *testing.T, command string) string {
	t.Helper()
	marker := "printf '%s\\n' '"
	start := strings.Index(command, marker)
	if start < 0 {
		t.Fatalf("install command does not contain quoted credential: %s", command)
	}
	start += len(marker)
	end := strings.Index(command[start:], "'")
	if end < 0 {
		t.Fatalf("install credential quote not closed: %s", command)
	}
	return command[start : start+end]
}

func TestAdminNodeInstallCommandRequiresAdminTokenAndKnownNode(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass"), AgentBinaryPath: "/opt/zeno/current/bin/zeno-agent"})

	cases := []struct {
		name       string
		nodeID     string
		adminToken string
		wantStatus int
	}{
		{name: "missing admin token", nodeID: "hytron", wantStatus: http.StatusUnauthorized},
		{name: "unknown node", nodeID: "missing", adminToken: "admin-pass", wantStatus: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/"+tc.nodeID+"/install-command", nil)
			if tc.adminToken != "" {
				request.Header.Set("X-Admin-Token", tc.adminToken)
			}
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestAdminProbeTargetsListsTargetsAndAssignmentsWithoutSecrets(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE probe_targets SET enabled = 0 WHERE id = 'google-dns'`); err != nil {
		t.Fatalf("disable target: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/probe-targets", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	raw := recorder.Body.String()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains([]byte(raw), []byte("agent-super-secret")) {
		t.Fatalf("admin probe targets response leaked sensitive fields: %s", raw)
	}
	var response struct {
		Targets []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Type        string `json:"type"`
			Address     string `json:"address"`
			Port        *int   `json:"port"`
			Count       int    `json:"count"`
			TimeoutMS   int    `json:"timeout_ms"`
			IntervalSec int    `json:"interval_sec"`
			Enabled     bool   `json:"enabled"`
			Assignments []struct {
				NodeID          string `json:"node_id"`
				NodeDisplayName string `json:"node_display_name"`
				Enabled         bool   `json:"enabled"`
			} `json:"assignments"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(raw)).Decode(&response); err != nil {
		t.Fatalf("decode admin probe targets: %v", err)
	}
	if len(response.Targets) != len(DefaultPreviewProbeTargets()) {
		t.Fatalf("targets len = %d, want %d", len(response.Targets), len(DefaultPreviewProbeTargets()))
	}
	findTarget := func(id string) struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		Address     string `json:"address"`
		Port        *int   `json:"port"`
		Count       int    `json:"count"`
		TimeoutMS   int    `json:"timeout_ms"`
		IntervalSec int    `json:"interval_sec"`
		Enabled     bool   `json:"enabled"`
		Assignments []struct {
			NodeID          string `json:"node_id"`
			NodeDisplayName string `json:"node_display_name"`
			Enabled         bool   `json:"enabled"`
		} `json:"assignments"`
	} {
		for _, target := range response.Targets {
			if target.ID == id {
				return target
			}
		}
		return struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Type        string `json:"type"`
			Address     string `json:"address"`
			Port        *int   `json:"port"`
			Count       int    `json:"count"`
			TimeoutMS   int    `json:"timeout_ms"`
			IntervalSec int    `json:"interval_sec"`
			Enabled     bool   `json:"enabled"`
			Assignments []struct {
				NodeID          string `json:"node_id"`
				NodeDisplayName string `json:"node_display_name"`
				Enabled         bool   `json:"enabled"`
			} `json:"assignments"`
		}{}
	}
	hytron := findTarget("hytron-local")
	if hytron.ID == "" || hytron.Name != "Hytron" || hytron.Type != "tcping" || hytron.Address != "127.0.0.1" || hytron.Port == nil || *hytron.Port != 18980 || hytron.Count != 3 || hytron.TimeoutMS != 1200 || hytron.IntervalSec != 60 || !hytron.Enabled {
		t.Fatalf("hytron target = %+v, want full target config", hytron)
	}
	if len(hytron.Assignments) != 1 || hytron.Assignments[0].NodeID != "hytron" || hytron.Assignments[0].NodeDisplayName != "Hytron" || !hytron.Assignments[0].Enabled {
		t.Fatalf("hytron assignments = %+v, want enabled hytron assignment", hytron.Assignments)
	}
	if google := findTarget("google-dns"); google.ID == "" || google.Enabled {
		t.Fatalf("google-dns target = %+v, want disabled target still visible in admin inventory", google)
	}
}

func TestAdminProbeTargetsReturnsEmptyAssignmentArrayForUnassignedTargets(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM node_probe_targets WHERE target_id = 'google-dns'`); err != nil {
		t.Fatalf("delete google-dns assignments: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/probe-targets", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Targets []struct {
			ID          string          `json:"id"`
			Assignments json.RawMessage `json:"assignments"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode admin probe targets: %v", err)
	}
	for _, target := range response.Targets {
		if target.ID == "google-dns" {
			if string(target.Assignments) != "[]" {
				t.Fatalf("google-dns assignments JSON = %s, want []", string(target.Assignments))
			}
			return
		}
	}
	t.Fatalf("google-dns target not found in admin response: %+v", response.Targets)
}

func TestAdminProbeTargetCreateAddsAssignedTargetWithoutSecrets(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/probe-targets", bytes.NewBufferString(`{
		"name": "  Example HTTPS  ",
		"type": "tcping",
		"address": "  example.com  ",
		"port": 443,
		"count": 5,
		"timeout_ms": 1500,
		"interval_sec": 90
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	raw := recorder.Body.String()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains([]byte(raw), []byte("agent-super-secret")) {
		t.Fatalf("admin probe target create response leaked sensitive fields: %s", raw)
	}
	var response struct {
		Target struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Type        string `json:"type"`
			Address     string `json:"address"`
			Port        int    `json:"port"`
			Count       int    `json:"count"`
			TimeoutMS   int    `json:"timeout_ms"`
			IntervalSec int    `json:"interval_sec"`
			Enabled     bool   `json:"enabled"`
			Assignments []struct {
				NodeID  string `json:"node_id"`
				Enabled bool   `json:"enabled"`
			} `json:"assignments"`
		} `json:"target"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(raw)).Decode(&response); err != nil {
		t.Fatalf("decode created target: %v", err)
	}
	if response.Target.ID == "" || response.Target.Name != "Example HTTPS" || response.Target.Type != "tcping" || response.Target.Address != "example.com" || response.Target.Port != 443 || response.Target.Count != 5 || response.Target.TimeoutMS != 1500 || response.Target.IntervalSec != 90 || !response.Target.Enabled {
		t.Fatalf("created target = %+v, want trimmed enabled tcping target", response.Target)
	}
	if len(response.Target.Assignments) != 1 || response.Target.Assignments[0].NodeID != "hytron" || !response.Target.Assignments[0].Enabled {
		t.Fatalf("created target assignments = %+v, want enabled assignment to existing node", response.Target.Assignments)
	}
	targets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	found := false
	for _, target := range targets {
		if target.ID == response.Target.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created target %q not assigned to hytron enabled target set", response.Target.ID)
	}
}

func TestAdminProbeTargetCreateAcceptsPingWithoutPort(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/probe-targets", bytes.NewBufferString(`{
		"name": "  Example ICMP  ",
		"type": "icmp",
		"address": "  8.8.8.8  ",
		"count": 4,
		"timeout_ms": 900,
		"interval_sec": 45
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	assertNoSensitiveAdminProbeTargetLeak(t, recorder.Body.String())
	var response struct {
		Target struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Type        string `json:"type"`
			Address     string `json:"address"`
			Port        *int   `json:"port"`
			Count       int    `json:"count"`
			TimeoutMS   int    `json:"timeout_ms"`
			IntervalSec int    `json:"interval_sec"`
			Enabled     bool   `json:"enabled"`
			Assignments []struct {
				NodeID  string `json:"node_id"`
				Enabled bool   `json:"enabled"`
			} `json:"assignments"`
		} `json:"target"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode created ping target: %v", err)
	}
	if response.Target.ID == "" || response.Target.Name != "Example ICMP" || response.Target.Type != "ping" || response.Target.Address != "8.8.8.8" || response.Target.Port != nil || response.Target.Count != 4 || response.Target.TimeoutMS != 900 || response.Target.IntervalSec != 45 || !response.Target.Enabled {
		t.Fatalf("created ping target = %+v, want normalized enabled ping target without port", response.Target)
	}
	if len(response.Target.Assignments) != 1 || response.Target.Assignments[0].NodeID != "hytron" || !response.Target.Assignments[0].Enabled {
		t.Fatalf("created ping assignments = %+v, want enabled assignment to existing node", response.Target.Assignments)
	}
	targets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	found := false
	for _, target := range targets {
		if target.ID == response.Target.ID {
			found = true
			if target.Type != "ping" || target.Port != nil {
				t.Fatalf("agent target = %+v, want ping target without port", target)
			}
		}
	}
	if !found {
		t.Fatalf("created ping target %q not assigned to hytron enabled target set", response.Target.ID)
	}
}

func TestAdminProbeTargetCreateAcceptsHTTPGETWithoutPort(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/probe-targets", bytes.NewBufferString(`{
		"name": "  Zeno Health  ",
		"type": "http_get",
		"address": "  https://example.com/health  ",
		"count": 2,
		"timeout_ms": 1500,
		"interval_sec": 60
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	assertNoSensitiveAdminProbeTargetLeak(t, recorder.Body.String())
	var response struct {
		Target struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Type        string `json:"type"`
			Address     string `json:"address"`
			Port        *int   `json:"port"`
			Count       int    `json:"count"`
			TimeoutMS   int    `json:"timeout_ms"`
			IntervalSec int    `json:"interval_sec"`
			Enabled     bool   `json:"enabled"`
			Assignments []struct {
				NodeID  string `json:"node_id"`
				Enabled bool   `json:"enabled"`
			} `json:"assignments"`
		} `json:"target"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode created http_get target: %v", err)
	}
	if response.Target.ID == "" || response.Target.Name != "Zeno Health" || response.Target.Type != "http_get" || response.Target.Address != "https://example.com/health" || response.Target.Port != nil || response.Target.Count != 2 || response.Target.TimeoutMS != 1500 || response.Target.IntervalSec != 60 || !response.Target.Enabled {
		t.Fatalf("created http_get target = %+v, want normalized enabled HTTP GET target without port", response.Target)
	}
	if len(response.Target.Assignments) != 1 || response.Target.Assignments[0].NodeID != "hytron" || !response.Target.Assignments[0].Enabled {
		t.Fatalf("created http_get assignments = %+v, want enabled assignment to existing node", response.Target.Assignments)
	}
	targets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	found := false
	for _, target := range targets {
		if target.ID == response.Target.ID {
			found = true
			if target.Type != "http_get" || target.Port != nil {
				t.Fatalf("agent target = %+v, want http_get target without port", target)
			}
		}
	}
	if !found {
		t.Fatalf("created http_get target %q not assigned to hytron enabled target set", response.Target.ID)
	}
}

func TestAdminProbeTargetPatchCanSwitchToPingAndClearPort(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/probe-targets/hytron-local", bytes.NewBufferString(`{
		"type": "icmp",
		"address": "  1.1.1.1  "
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	assertNoSensitiveAdminProbeTargetLeak(t, recorder.Body.String())
	var response struct {
		Target struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Address string `json:"address"`
			Port    *int   `json:"port"`
		} `json:"target"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode updated ping target: %v", err)
	}
	if response.Target.ID != "hytron-local" || response.Target.Type != "ping" || response.Target.Address != "1.1.1.1" || response.Target.Port != nil {
		t.Fatalf("updated target = %+v, want ping target with cleared port", response.Target)
	}
	targets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	for _, target := range targets {
		if target.ID == "hytron-local" && (target.Type != "ping" || target.Port != nil) {
			t.Fatalf("agent target = %+v, want ping target without port", target)
		}
	}
}

func TestAdminProbeTargetPatchCanSwitchToHTTPGETAndClearPort(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/probe-targets/hytron-local", bytes.NewBufferString(`{
		"type": "http_get",
		"address": "  https://example.com/health  "
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	assertNoSensitiveAdminProbeTargetLeak(t, recorder.Body.String())
	var response struct {
		Target struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Address string `json:"address"`
			Port    *int   `json:"port"`
		} `json:"target"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode updated http_get target: %v", err)
	}
	if response.Target.ID != "hytron-local" || response.Target.Type != "http_get" || response.Target.Address != "https://example.com/health" || response.Target.Port != nil {
		t.Fatalf("updated target = %+v, want http_get target with cleared port", response.Target)
	}
	targets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	for _, target := range targets {
		if target.ID == "hytron-local" && (target.Type != "http_get" || target.Port != nil) {
			t.Fatalf("agent target = %+v, want http_get target without port", target)
		}
	}
}

func TestAdminProbeTargetPatchRejectsHTTPGETWithoutFullURL(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/probe-targets/hytron-local", bytes.NewBufferString(`{
		"type": "http_get"
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for http_get target without full URL; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminProbeTargetPatchUpdatesEditableFieldsAndAffectsAgentTargets(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/probe-targets/hytron-local", bytes.NewBufferString(`{
		"name": "  Local Controller  ",
		"address": "  127.0.0.1  ",
		"port": 18981,
		"count": 4,
		"timeout_ms": 900,
		"interval_sec": 30,
		"enabled": false
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	raw := recorder.Body.String()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains([]byte(raw), []byte("agent-super-secret")) {
		t.Fatalf("admin probe target update response leaked sensitive fields: %s", raw)
	}
	var response struct {
		Target struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Address     string `json:"address"`
			Port        int    `json:"port"`
			Count       int    `json:"count"`
			TimeoutMS   int    `json:"timeout_ms"`
			IntervalSec int    `json:"interval_sec"`
			Enabled     bool   `json:"enabled"`
		} `json:"target"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(raw)).Decode(&response); err != nil {
		t.Fatalf("decode updated target: %v", err)
	}
	if response.Target.ID != "hytron-local" || response.Target.Name != "Local Controller" || response.Target.Address != "127.0.0.1" || response.Target.Port != 18981 || response.Target.Count != 4 || response.Target.TimeoutMS != 900 || response.Target.IntervalSec != 30 || response.Target.Enabled {
		t.Fatalf("updated target = %+v, want edited disabled target", response.Target)
	}
	targets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	for _, target := range targets {
		if target.ID == "hytron-local" {
			t.Fatalf("disabled target should be removed from agent target set, got %+v", target)
		}
	}
}

func TestAdminProbeTargetPatchUpdatesNodeAssignments(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if _, err := store.CreateAdminNode(ctx, AdminNodeCreateRequest{ID: "backup", DisplayName: "Backup", CountryCode: "US"}); err != nil {
		t.Fatalf("create backup node: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/probe-targets/hytron-local", bytes.NewBufferString(`{
		"assignments": [
			{"node_id": "hytron", "enabled": false},
			{"node_id": "backup", "enabled": true}
		]
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	raw := recorder.Body.String()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains([]byte(raw), []byte("agent-super-secret")) {
		t.Fatalf("admin probe target assignment response leaked sensitive fields: %s", raw)
	}
	var response struct {
		Target struct {
			ID          string `json:"id"`
			Assignments []struct {
				NodeID  string `json:"node_id"`
				Enabled bool   `json:"enabled"`
			} `json:"assignments"`
		} `json:"target"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(raw)).Decode(&response); err != nil {
		t.Fatalf("decode updated target assignments: %v", err)
	}
	if response.Target.ID != "hytron-local" {
		t.Fatalf("target id = %q, want hytron-local", response.Target.ID)
	}
	assignmentEnabled := map[string]bool{}
	for _, assignment := range response.Target.Assignments {
		assignmentEnabled[assignment.NodeID] = assignment.Enabled
	}
	if assignmentEnabled["hytron"] || !assignmentEnabled["backup"] {
		t.Fatalf("assignments = %+v, want hytron disabled and backup enabled", response.Target.Assignments)
	}

	hytronTargets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("hytron enabled probe targets: %v", err)
	}
	for _, target := range hytronTargets {
		if target.ID == "hytron-local" {
			t.Fatalf("hytron-local should be removed from hytron agent targets after assignment disable")
		}
	}
	backupTargets, err := store.EnabledProbeTargets(ctx, "backup")
	if err != nil {
		t.Fatalf("backup enabled probe targets: %v", err)
	}
	backupHasTarget := false
	for _, target := range backupTargets {
		if target.ID == "hytron-local" {
			backupHasTarget = true
		}
	}
	if !backupHasTarget {
		t.Fatalf("hytron-local should remain enabled for backup agent targets")
	}
}

func TestAdminProbeTargetWritesRejectUnauthorizedUnknownAndInvalidRequests(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		adminToken string
		wantStatus int
	}{
		{name: "create missing token", method: http.MethodPost, path: "/api/admin/v1/probe-targets", body: `{"name":"A","type":"tcping","address":"example.com","port":443,"count":3,"timeout_ms":1000,"interval_sec":60}`, wantStatus: http.StatusUnauthorized},
		{name: "create blank name", method: http.MethodPost, path: "/api/admin/v1/probe-targets", body: `{"name":"   ","type":"tcping","address":"example.com","port":443,"count":3,"timeout_ms":1000,"interval_sec":60}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "create bad port", method: http.MethodPost, path: "/api/admin/v1/probe-targets", body: `{"name":"A","type":"tcping","address":"example.com","port":70000,"count":3,"timeout_ms":1000,"interval_sec":60}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch unknown target", method: http.MethodPatch, path: "/api/admin/v1/probe-targets/missing", body: `{"name":"Changed"}`, adminToken: "admin-pass", wantStatus: http.StatusNotFound},
		{name: "patch negative count", method: http.MethodPatch, path: "/api/admin/v1/probe-targets/hytron-local", body: `{"count":0}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch unknown assignment node", method: http.MethodPatch, path: "/api/admin/v1/probe-targets/hytron-local", body: `{"assignments":[{"node_id":"missing","enabled":false}]}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
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
			if bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("token")) || bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("secret")) {
				t.Fatalf("error body should not leak sensitive wording: %s", recorder.Body.String())
			}
		})
	}
}

func TestAdminProbeTargetsRequiresAdminToken(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/probe-targets", nil)
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("token")) || bytes.Contains(bytes.ToLower(recorder.Body.Bytes()), []byte("secret")) {
		t.Fatalf("admin target auth failure body should not leak token/secret wording: %s", recorder.Body.String())
	}
}

func TestAdminNotificationChannelsCreateListAndPatchWithoutCredentialLeak(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-channels", bytes.NewBufferString(`{
		"name": "  Telegram Home  ",
		"type": "telegram",
		"destination": "  7579942307  ",
		"credential": "telegram-bot-secret-value",
		"enabled": true
	}`))
	createRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(createRecorder, createRequest)

	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	assertNoSensitiveNotificationLeak(t, createRecorder.Body.String())
	var createResponse struct {
		Channel struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Type          string `json:"type"`
			Destination   string `json:"destination"`
			CredentialSet bool   `json:"credential_set"`
			Enabled       bool   `json:"enabled"`
		} `json:"channel"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(createRecorder.Body.String())).Decode(&createResponse); err != nil {
		t.Fatalf("decode created notification channel: %v", err)
	}
	if createResponse.Channel.ID == "" || createResponse.Channel.Name != "Telegram Home" || createResponse.Channel.Type != "telegram" || createResponse.Channel.Destination != "7579942307" || !createResponse.Channel.CredentialSet || !createResponse.Channel.Enabled {
		t.Fatalf("created channel = %+v, want normalized enabled telegram channel with credential marker", createResponse.Channel)
	}

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/notification-channels", nil)
	listRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	assertNoSensitiveNotificationLeak(t, listRecorder.Body.String())
	var listResponse struct {
		Channels []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Type          string `json:"type"`
			Destination   string `json:"destination"`
			CredentialSet bool   `json:"credential_set"`
			Enabled       bool   `json:"enabled"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(listRecorder.Body.String())).Decode(&listResponse); err != nil {
		t.Fatalf("decode notification channels: %v", err)
	}
	if len(listResponse.Channels) != 1 || listResponse.Channels[0].ID != createResponse.Channel.ID || !listResponse.Channels[0].CredentialSet {
		t.Fatalf("listed channels = %+v, want one persisted channel with credential marker", listResponse.Channels)
	}

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/notification-channels/"+createResponse.Channel.ID, bytes.NewBufferString(`{
		"name": "  Home Telegram Updated  ",
		"enabled": false
	}`))
	patchRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}
	assertNoSensitiveNotificationLeak(t, patchRecorder.Body.String())
	var patchResponse struct {
		Channel struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			CredentialSet bool   `json:"credential_set"`
			Enabled       bool   `json:"enabled"`
		} `json:"channel"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(patchRecorder.Body.String())).Decode(&patchResponse); err != nil {
		t.Fatalf("decode patched notification channel: %v", err)
	}
	if patchResponse.Channel.ID != createResponse.Channel.ID || patchResponse.Channel.Name != "Home Telegram Updated" || !patchResponse.Channel.CredentialSet || patchResponse.Channel.Enabled {
		t.Fatalf("patched channel = %+v, want renamed disabled channel preserving credential marker", patchResponse.Channel)
	}
}

func TestAdminNotificationChannelTestSendsWebhookAndRecordsSanitizedDelivery(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	var received struct {
		EventType string `json:"event_type"`
		Label     string `json:"label"`
		NodeName  string `json:"node_name"`
	}
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("webhook method = %s, want POST", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer webhook-secret-value" {
			t.Errorf("webhook authorization = %q, want bearer credential", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode webhook body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()
	enabled := false
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "smoke-webhook",
		Name:        "Smoke Webhook",
		Type:        "webhook",
		Destination: webhook.URL,
		Credential:  "webhook-secret-value",
		Enabled:     &enabled,
	})
	if err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass"), NotificationClient: webhook.Client()})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-channels/"+channel.ID+"/test", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("test status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	assertNoSensitiveNotificationLeak(t, recorder.Body.String())
	if received.EventType != "test_notification" || received.Label != "测试发送" || received.NodeName != "Zeno" {
		t.Fatalf("webhook test event = %+v, want sanitized Zeno test notification", received)
	}
	var response struct {
		Delivery struct {
			EventType   string `json:"event_type"`
			Label       string `json:"label"`
			ChannelID   string `json:"channel_id"`
			ChannelName string `json:"channel_name"`
			Success     bool   `json:"success"`
			Error       string `json:"error"`
		} `json:"delivery"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode test delivery response: %v", err)
	}
	if response.Delivery.EventType != "test_notification" || response.Delivery.Label != "测试发送" || response.Delivery.ChannelID != channel.ID || response.Delivery.ChannelName != "Smoke Webhook" || !response.Delivery.Success || response.Delivery.Error != "" {
		t.Fatalf("test delivery response = %+v, want successful sanitized test delivery", response.Delivery)
	}
	deliveries, err := store.AdminNotificationDeliveries(ctx, 5)
	if err != nil {
		t.Fatalf("list notification deliveries after test: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].EventType != "test_notification" || deliveries[0].ChannelID != channel.ID || !deliveries[0].Success {
		t.Fatalf("recorded deliveries = %+v, want one successful test delivery", deliveries)
	}
}

func TestAdminNotificationChannelDeleteRemovesChannelWithoutCredentialLeak(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	enabled := true
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "smoke-webhook",
		Name:        "Smoke Webhook",
		Type:        "webhook",
		Destination: "https://example.com/notify",
		Credential:  "webhook-secret-value",
		Enabled:     &enabled,
	})
	if err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	deleteRecorder := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/admin/v1/notification-channels/"+channel.ID, nil)
	deleteRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(deleteRecorder, deleteRequest)

	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	assertNoSensitiveNotificationLeak(t, deleteRecorder.Body.String())
	channels, err := store.AdminNotificationChannels(ctx)
	if err != nil {
		t.Fatalf("list notification channels after delete: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("channels after delete = %+v, want none", channels)
	}

	missingRecorder := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodDelete, "/api/admin/v1/notification-channels/"+channel.ID, nil)
	missingRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(missingRecorder, missingRequest)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404; body=%s", missingRecorder.Code, missingRecorder.Body.String())
	}
	assertNoSensitiveNotificationLeak(t, missingRecorder.Body.String())
}

func TestAdminNotificationChannelsRejectUnauthorizedUnknownAndInvalidRequests(t *testing.T) {
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
		{name: "create missing admin token", method: http.MethodPost, path: "/api/admin/v1/notification-channels", body: `{"name":"Home","type":"telegram","destination":"7579942307","credential":"telegram-bot-secret-value"}`, wantStatus: http.StatusUnauthorized},
		{name: "create blank name", method: http.MethodPost, path: "/api/admin/v1/notification-channels", body: `{"name":"   ","type":"telegram","destination":"7579942307","credential":"telegram-bot-secret-value"}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "create unsupported type", method: http.MethodPost, path: "/api/admin/v1/notification-channels", body: `{"name":"Home","type":"email","destination":"ops@example.com","credential":"email-secret-value"}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "create missing credential", method: http.MethodPost, path: "/api/admin/v1/notification-channels", body: `{"name":"Home","type":"telegram","destination":"7579942307"}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch unknown channel", method: http.MethodPatch, path: "/api/admin/v1/notification-channels/missing", body: `{"enabled":false}`, adminToken: "admin-pass", wantStatus: http.StatusNotFound},
		{name: "patch empty body", method: http.MethodPatch, path: "/api/admin/v1/notification-channels/missing", body: `{}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "delete missing admin token", method: http.MethodDelete, path: "/api/admin/v1/notification-channels/missing", wantStatus: http.StatusUnauthorized},
		{name: "delete unknown channel", method: http.MethodDelete, path: "/api/admin/v1/notification-channels/missing", adminToken: "admin-pass", wantStatus: http.StatusNotFound},
		{name: "test missing admin token", method: http.MethodPost, path: "/api/admin/v1/notification-channels/missing/test", wantStatus: http.StatusUnauthorized},
		{name: "test unknown channel", method: http.MethodPost, path: "/api/admin/v1/notification-channels/missing/test", adminToken: "admin-pass", wantStatus: http.StatusNotFound},
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
			assertNoSensitiveNotificationLeak(t, recorder.Body.String())
		})
	}
}

func TestAdminNotificationTypesListAndPatch(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/notification-types", nil)
	listRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var listResponse struct {
		Types []struct {
			EventType string `json:"event_type"`
			Label     string `json:"label"`
			Enabled   bool   `json:"enabled"`
		} `json:"types"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(listRecorder.Body.String())).Decode(&listResponse); err != nil {
		t.Fatalf("decode notification types: %v", err)
	}
	if len(listResponse.Types) != 3 || listResponse.Types[0].EventType != "node_online" || listResponse.Types[0].Label != "上线" || listResponse.Types[0].Enabled {
		t.Fatalf("notification types = %+v, want disabled default online/offline/unhealthy types", listResponse.Types)
	}

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/notification-types/node_offline", bytes.NewBufferString(`{"enabled": true}`))
	patchRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}
	var patchResponse struct {
		Type struct {
			EventType string `json:"event_type"`
			Label     string `json:"label"`
			Enabled   bool   `json:"enabled"`
		} `json:"type"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(patchRecorder.Body.String())).Decode(&patchResponse); err != nil {
		t.Fatalf("decode patched notification type: %v", err)
	}
	if patchResponse.Type.EventType != "node_offline" || patchResponse.Type.Label != "离线" || !patchResponse.Type.Enabled {
		t.Fatalf("patched notification type = %+v, want enabled offline type", patchResponse.Type)
	}
}

func TestAdminNotificationTypesRejectUnauthorizedUnknownAndInvalidRequests(t *testing.T) {
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
		{name: "list missing admin token", method: http.MethodGet, path: "/api/admin/v1/notification-types", wantStatus: http.StatusUnauthorized},
		{name: "patch unknown type", method: http.MethodPatch, path: "/api/admin/v1/notification-types/missing", body: `{"enabled":true}`, adminToken: "admin-pass", wantStatus: http.StatusNotFound},
		{name: "patch missing enabled", method: http.MethodPatch, path: "/api/admin/v1/notification-types/node_offline", body: `{}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
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
			assertNoSensitiveNotificationLeak(t, recorder.Body.String())
		})
	}
}

func assertNoSensitiveAdminProbeTargetLeak(t *testing.T, raw string) {
	t.Helper()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains([]byte(raw), []byte("agent-super-secret")) {
		t.Fatalf("admin probe target response leaked sensitive fields: %s", raw)
	}
}

func assertNoSensitiveNotificationLeak(t *testing.T, raw string) {
	t.Helper()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains([]byte(raw), []byte("telegram-bot-secret-value")) || bytes.Contains([]byte(raw), []byte("email-secret-value")) {
		t.Fatalf("notification response leaked sensitive fields: %s", raw)
	}
}
