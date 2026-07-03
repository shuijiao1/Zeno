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
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass"), AgentBinaryPath: "/opt/jiaoprobe/current/bin/jiaoprobe-agent", AgentVersion: "testsha"}).ServeHTTP(recorder, request)

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
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass"), AgentBinaryPath: "/opt/jiaoprobe/current/bin/jiaoprobe-agent"})

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
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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

func TestAdminProbeTargetsRequiresAdminToken(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "jiaoprobe.db"))
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
