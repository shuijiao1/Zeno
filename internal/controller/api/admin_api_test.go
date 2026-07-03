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
