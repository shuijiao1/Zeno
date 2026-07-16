package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

func TestAdminNodesEmptyStoreReturnsEmptyList(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	nodes, err := store.AdminNodes(context.Background())
	if err != nil {
		t.Fatalf("admin nodes: %v", err)
	}
	if nodes == nil || len(nodes) != 0 {
		t.Fatalf("empty admin nodes = %#v, want non-nil empty slice", nodes)
	}

	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"nodes":[]`) {
		t.Fatalf("empty admin nodes response = %s, want nodes:[]", recorder.Body.String())
	}
}

func TestAdminLoginCreatesSessionAndPasswordUpdateInvalidatesOldPassword(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	publicURL := "https://zeno.example.com"
	if _, err := store.UpdateAdminSettings(context.Background(), AdminSettingsUpdateRequest{AgentControllerURL: &publicURL}); err != nil {
		t.Fatalf("set agent controller URL: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	loginRecorder := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", loginRecorder.Code, loginRecorder.Body.String())
	}
	var loginResponse AdminLoginResponse
	if err := json.NewDecoder(loginRecorder.Body).Decode(&loginResponse); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if loginResponse.Username != "admin" || loginResponse.Token == "" || loginResponse.Token == "admin-pass" {
		t.Fatalf("login response = %+v, want opaque admin session", loginResponse)
	}

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)
	listRequest.Header.Set("X-Admin-Token", loginResponse.Token)
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("session list status = %d, want 200; body=%s", listRecorder.Code, listRecorder.Body.String())
	}

	passwordRecorder := httptest.NewRecorder()
	passwordRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/account", strings.NewReader(`{"username":"admin","current_password":"admin-pass","new_password":"new-admin-pass"}`))
	passwordRequest.Header.Set("X-Admin-Token", loginResponse.Token)
	handler.ServeHTTP(passwordRecorder, passwordRequest)
	if passwordRecorder.Code != http.StatusOK {
		t.Fatalf("account password status = %d, want 200; body=%s", passwordRecorder.Code, passwordRecorder.Body.String())
	}
	var passwordResponse AdminLoginResponse
	if err := json.NewDecoder(passwordRecorder.Body).Decode(&passwordResponse); err != nil {
		t.Fatalf("decode account password response: %v", err)
	}
	if passwordResponse.Token == "" || passwordResponse.Token == loginResponse.Token {
		t.Fatalf("account password response = %+v, want rotated session", passwordResponse)
	}

	oldTokenRecorder := httptest.NewRecorder()
	oldTokenRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)
	oldTokenRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(oldTokenRecorder, oldTokenRequest)
	if oldTokenRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("old bootstrap token status = %d, want 401", oldTokenRecorder.Code)
	}

	oldLoginRecorder := httptest.NewRecorder()
	oldLoginRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
	oldLoginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(oldLoginRecorder, oldLoginRequest)
	if oldLoginRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("old password status = %d, want 401", oldLoginRecorder.Code)
	}

	newLoginRecorder := httptest.NewRecorder()
	newLoginRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"new-admin-pass"}`))
	newLoginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(newLoginRecorder, newLoginRequest)
	if newLoginRecorder.Code != http.StatusOK {
		t.Fatalf("new password status = %d, want 200; body=%s", newLoginRecorder.Code, newLoginRecorder.Body.String())
	}
	var newLoginResponse AdminLoginResponse
	if err := json.NewDecoder(newLoginRecorder.Body).Decode(&newLoginResponse); err != nil {
		t.Fatalf("decode new login response: %v", err)
	}

	accountRecorder := httptest.NewRecorder()
	accountRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/account", strings.NewReader(`{"username":"zeno-admin","current_password":"new-admin-pass","new_password":""}`))
	accountRequest.Header.Set("X-Admin-Token", newLoginResponse.Token)
	handler.ServeHTTP(accountRecorder, accountRequest)
	if accountRecorder.Code != http.StatusOK {
		t.Fatalf("account status = %d, want 200; body=%s", accountRecorder.Code, accountRecorder.Body.String())
	}
	var accountResponse AdminLoginResponse
	if err := json.NewDecoder(accountRecorder.Body).Decode(&accountResponse); err != nil {
		t.Fatalf("decode account response: %v", err)
	}
	if accountResponse.Username != "zeno-admin" || accountResponse.Token == "" || accountResponse.Token == newLoginResponse.Token {
		t.Fatalf("account response = %+v, want renamed admin and rotated session", accountResponse)
	}

	oldUsernameRecorder := httptest.NewRecorder()
	oldUsernameRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"new-admin-pass"}`))
	oldUsernameRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(oldUsernameRecorder, oldUsernameRequest)
	if oldUsernameRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("old username status = %d, want 401", oldUsernameRecorder.Code)
	}

	accountGetRecorder := httptest.NewRecorder()
	accountGetRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/account", nil)
	accountGetRequest.Header.Set("X-Admin-Token", accountResponse.Token)
	handler.ServeHTTP(accountGetRecorder, accountGetRequest)
	if accountGetRecorder.Code != http.StatusOK {
		t.Fatalf("account get status = %d, want 200; body=%s", accountGetRecorder.Code, accountGetRecorder.Body.String())
	}
	var accountGetResponse AdminAccountResponse
	if err := json.NewDecoder(accountGetRecorder.Body).Decode(&accountGetResponse); err != nil {
		t.Fatalf("decode account get response: %v", err)
	}
	if accountGetResponse.Account.Username != "zeno-admin" {
		t.Fatalf("account get = %+v, want zeno-admin", accountGetResponse)
	}
}

func TestAdminSessionExpiresAfterOneDay(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	loginRecorder := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", loginRecorder.Code, loginRecorder.Body.String())
	}
	var loginResponse AdminLoginResponse
	if err := json.NewDecoder(loginRecorder.Body).Decode(&loginResponse); err != nil {
		t.Fatalf("decode login: %v", err)
	}

	now := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(context.Background(), `UPDATE admin_sessions SET created_at = ?, last_seen_at = ? WHERE token_hash = ?`, now-int64((23*time.Hour).Seconds()), now, HashAdminToken(loginResponse.Token)); err != nil {
		t.Fatalf("age fresh session: %v", err)
	}
	freshRecorder := httptest.NewRecorder()
	freshRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)
	freshRequest.Header.Set("X-Admin-Token", loginResponse.Token)
	handler.ServeHTTP(freshRecorder, freshRequest)
	if freshRecorder.Code != http.StatusOK {
		t.Fatalf("fresh one-day session status = %d, want 200; body=%s", freshRecorder.Code, freshRecorder.Body.String())
	}

	if _, err := store.db.ExecContext(context.Background(), `UPDATE admin_sessions SET created_at = ?, last_seen_at = ? WHERE token_hash = ?`, now-int64((25*time.Hour).Seconds()), now, HashAdminToken(loginResponse.Token)); err != nil {
		t.Fatalf("age expired session: %v", err)
	}
	expiredRecorder := httptest.NewRecorder()
	expiredRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)
	expiredRequest.Header.Set("X-Admin-Token", loginResponse.Token)
	handler.ServeHTTP(expiredRecorder, expiredRequest)
	if expiredRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("expired one-day session status = %d, want 401; body=%s", expiredRecorder.Code, expiredRecorder.Body.String())
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
		"billing_mode": "max",
		"monthly_reset_day": 15,
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
			BillingMode       string `json:"billing_mode"`
			MonthlyResetDay   int    `json:"monthly_reset_day"`
			MonthlyQuotaBytes int64  `json:"monthly_quota_bytes"`
		} `json:"node"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(raw)).Decode(&response); err != nil {
		t.Fatalf("decode updated admin node: %v", err)
	}
	if response.Node.ID != "hytron" || response.Node.DisplayName != "Hytron Edited" || response.Node.Status != "disabled" || response.Node.CountryCode != "HK" || response.Node.Region != "Hong Kong" || !response.Node.Disabled || response.Node.BillingMode != "max" || response.Node.MonthlyResetDay != 15 || response.Node.MonthlyQuotaBytes != 123456789 {
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

func TestAdminNodePatchReplacesProbeAssignmentsInOneRequest(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if _, err := store.CreateAdminProbeTarget(ctx, AdminProbeTargetCreateRequest{
		ID: "batch-target-a", Name: "Batch A", Type: "ping", Address: "1.1.1.1", Count: 3, TimeoutMS: 1000, IntervalSec: 30,
		Assignments: []AdminProbeTargetAssignmentUpdate{{NodeID: "hytron", Enabled: true}},
	}); err != nil {
		t.Fatalf("create assigned target: %v", err)
	}
	if _, err := store.CreateAdminProbeTarget(ctx, AdminProbeTargetCreateRequest{
		ID: "batch-target-b", Name: "Batch B", Type: "ping", Address: "8.8.8.8", Count: 3, TimeoutMS: 1000, IntervalSec: 30,
	}); err != nil {
		t.Fatalf("create unassigned target: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/nodes/hytron", bytes.NewBufferString(`{
		"display_name":"Hytron Fast",
		"home_probe_target_id":"batch-target-b",
		"probe_target_ids":["batch-target-b"]
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	targets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled targets: %v", err)
	}
	if len(targets) != 1 || targets[0].ID != "batch-target-b" {
		t.Fatalf("enabled targets = %+v, want only batch-target-b", targets)
	}
	nodes, err := store.AdminNodes(ctx)
	if err != nil {
		t.Fatalf("admin nodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].DisplayName != "Hytron Fast" || nodes[0].HomeProbeTargetID != "batch-target-b" {
		t.Fatalf("updated node = %+v, want node fields and home target committed together", nodes)
	}
}

func TestAdminNodePatchRejectsHomeTargetOutsideBatchSelection(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	for _, target := range []AdminProbeTargetCreateRequest{
		{ID: "batch-target-a", Name: "Batch A", Type: "ping", Address: "1.1.1.1", Count: 3, TimeoutMS: 1000, IntervalSec: 30},
		{ID: "batch-target-b", Name: "Batch B", Type: "ping", Address: "8.8.8.8", Count: 3, TimeoutMS: 1000, IntervalSec: 30},
	} {
		if _, err := store.CreateAdminProbeTarget(ctx, target); err != nil {
			t.Fatalf("create target %s: %v", target.ID, err)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/nodes/hytron", bytes.NewBufferString(`{
		"display_name":"Must Not Persist",
		"home_probe_target_id":"batch-target-a",
		"probe_target_ids":["batch-target-b"]
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	nodes, err := store.AdminNodes(ctx)
	if err != nil {
		t.Fatalf("admin nodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].DisplayName != "Hytron" || nodes[0].HomeProbeTargetID != "" {
		t.Fatalf("invalid batch partially persisted node = %+v", nodes)
	}
}

func TestAdminNodeBillingIPAndDisplayOrderFieldsFlowThroughAdminAndPublicSummary(t *testing.T) {
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

	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes", bytes.NewBufferString(`{
		"id": "backup",
		"display_name": "Backup",
		"country_code": " jp ",
		"expiry_date": "2026-12-31",
		"billing_cycle": "年付",
		"billing_mode": "in",
		"monthly_reset_day": 10,
		"display_order": 30,
		"public_ipv4": "203.0.113.10",
		"public_ipv6": "2001:db8::10"
	}`))
	createRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(createRecorder, createRequest)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createRecorder.Code, createRecorder.Body.String())
	}

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/nodes/hytron", bytes.NewBufferString(`{
		"expiry_date": "2026-08-01",
		"billing_cycle": "月付",
		"billing_mode": "max",
		"monthly_reset_day": 15,
		"display_order": 10,
		"public_ipv4": "198.51.100.8",
		"public_ipv6": "2001:db8::8"
	}`))
	patchRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)
	listRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	raw := listRecorder.Body.String()
	lower := bytes.ToLower([]byte(raw))
	if bytes.Contains(lower, []byte("token")) || bytes.Contains(lower, []byte("secret")) || bytes.Contains(lower, []byte("credential")) || bytes.Contains(lower, []byte("hash")) {
		t.Fatalf("admin node metadata response leaked sensitive wording: %s", raw)
	}
	var response struct {
		Nodes []struct {
			ID           string `json:"id"`
			ExpiryDate   string `json:"expiry_date"`
			BillingCycle string `json:"billing_cycle"`
			BillingMode  string `json:"billing_mode"`
			ResetDay     int    `json:"monthly_reset_day"`
			DisplayOrder int    `json:"display_order"`
			PublicIPv4   string `json:"public_ipv4"`
			PublicIPv6   string `json:"public_ipv6"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(raw)).Decode(&response); err != nil {
		t.Fatalf("decode admin nodes metadata: %v", err)
	}
	if len(response.Nodes) != 2 {
		t.Fatalf("nodes len = %d, want 2", len(response.Nodes))
	}
	if response.Nodes[0].ID != "hytron" || response.Nodes[0].DisplayOrder != 10 || response.Nodes[0].ExpiryDate != "2026-08-01" || response.Nodes[0].BillingCycle != "月付" || response.Nodes[0].BillingMode != "max" || response.Nodes[0].ResetDay != 15 || response.Nodes[0].PublicIPv4 != "198.51.100.8" || response.Nodes[0].PublicIPv6 != "2001:db8::8" {
		t.Fatalf("hytron metadata = %+v, want edited billing/IP/order fields", response.Nodes[0])
	}
	if response.Nodes[1].ID != "backup" || response.Nodes[1].DisplayOrder != 30 || response.Nodes[1].BillingMode != "in" || response.Nodes[1].ResetDay != 10 {
		t.Fatalf("second node = %+v, want display-order sorted backup", response.Nodes[1])
	}

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 2 || summary.Nodes[0].ID != "hytron" || summary.Nodes[1].ID != "backup" {
		t.Fatalf("summary nodes order = %+v, want display_order order", summary.Nodes)
	}
	expectedHytronExpiry := expiryLabelValue(sql.NullString{String: "2026-08-01", Valid: true}, sql.NullString{String: "月付", Valid: true}, false, time.Now())
	expectedBackupExpiry := expiryLabelValue(sql.NullString{String: "2026-12-31", Valid: true}, sql.NullString{String: "年付", Valid: true}, false, time.Now())
	if summary.Nodes[0].ExpiryLabel != expectedHytronExpiry || summary.Nodes[1].ExpiryLabel != expectedBackupExpiry {
		t.Fatalf("summary expiry labels = %q/%q, want %q/%q", summary.Nodes[0].ExpiryLabel, summary.Nodes[1].ExpiryLabel, expectedHytronExpiry, expectedBackupExpiry)
	}
	expectedPeriod := billingPeriodFor(time.Now(), 15)
	if summary.Nodes[0].BillingMode != "max" || summary.Nodes[0].MonthlyResetDay != 15 || summary.Nodes[0].MonthlyPeriodStart != expectedPeriod.StartDate || summary.Nodes[0].MonthlyPeriodEnd != expectedPeriod.EndDate {
		t.Fatalf("summary billing period = %+v, want billing mode/reset day and current period", summary.Nodes[0])
	}
}

func TestAdminNodePatchRefreshesCachedPublicSummaryImmediately(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	initialRecorder := httptest.NewRecorder()
	handler.ServeHTTP(initialRecorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil))
	if initialRecorder.Code != http.StatusOK {
		t.Fatalf("initial summary status = %d, want 200; body=%s", initialRecorder.Code, initialRecorder.Body.String())
	}

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/nodes/hytron", strings.NewReader(`{"expiry_date":"2026-09-09","monthly_quota_bytes":987654321}`))
	patchRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}

	refreshedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(refreshedRecorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil))
	if refreshedRecorder.Code != http.StatusOK {
		t.Fatalf("refreshed summary status = %d, want 200; body=%s", refreshedRecorder.Code, refreshedRecorder.Body.String())
	}
	var summary SummaryResponse
	if err := json.NewDecoder(refreshedRecorder.Body).Decode(&summary); err != nil {
		t.Fatalf("decode refreshed summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("summary nodes len = %d, want 1", len(summary.Nodes))
	}
	if summary.Nodes[0].ExpiryLabel != "2026-09-09" {
		t.Fatalf("expiry label = %q, want patched value", summary.Nodes[0].ExpiryLabel)
	}
	if summary.Nodes[0].MonthlyQuotaBytes == nil || *summary.Nodes[0].MonthlyQuotaBytes != 987654321 {
		t.Fatalf("monthly quota = %v, want patched value", summary.Nodes[0].MonthlyQuotaBytes)
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
		{name: "invalid billing mode", nodeID: "hytron", body: `{"billing_mode":"95th"}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "zero monthly reset day", nodeID: "hytron", body: `{"monthly_reset_day":0}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "invalid monthly reset day", nodeID: "hytron", body: `{"monthly_reset_day":32}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
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

func TestAdminNodeDeleteRemovesNodeAndDependentData(t *testing.T) {
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
	now := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO host_info (node_id, hostname, updated_at)
		VALUES ('backup', 'backup-host', ?)
	`, now); err != nil {
		t.Fatalf("seed backup host info: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO state_samples (node_id, ts, cpu_percent)
		VALUES ('backup', ?, 42.5)
	`, now); err != nil {
		t.Fatalf("seed backup state sample: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO traffic_monthly (node_id, month, in_bytes, out_bytes, billable_bytes, updated_at)
		VALUES ('backup', '2026-07', 1, 2, 3, ?)
	`, now); err != nil {
		t.Fatalf("seed backup traffic: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO traffic_lifetime (node_id, in_bytes, out_bytes, updated_at)
		VALUES ('backup', 4, 5, ?)
	`, now); err != nil {
		t.Fatalf("seed backup lifetime traffic: %v", err)
	}
	roundResult, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent)
		VALUES ('backup', 'hytron-local', ?, 'tcping', 1, 1, 0)
	`, now)
	if err != nil {
		t.Fatalf("seed backup probe round: %v", err)
	}
	roundID, err := roundResult.LastInsertId()
	if err != nil {
		t.Fatalf("backup probe round id: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_samples (round_id, seq, success, latency_ms)
		VALUES (?, 1, 1, 0.42)
	`, roundID); err != nil {
		t.Fatalf("seed backup probe sample: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO alert_rule_states (node_id, rule_id, active, updated_at)
		VALUES ('backup', 'cpu_high', 1, ?)
	`, now); err != nil {
		t.Fatalf("seed backup alert state: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO alert_rule_node_scopes (rule_id, node_id, created_at)
		VALUES ('cpu_high', 'backup', ?)
	`, now); err != nil {
		t.Fatalf("seed backup alert scope: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/admin/v1/nodes/backup", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.TrimSpace(recorder.Body.String()) != "" {
		t.Fatalf("delete body = %q, want empty", recorder.Body.String())
	}
	checks := []struct {
		name  string
		query string
	}{
		{name: "nodes", query: `SELECT COUNT(*) FROM nodes WHERE id = 'backup'`},
		{name: "host_info", query: `SELECT COUNT(*) FROM host_info WHERE node_id = 'backup'`},
		{name: "state_samples", query: `SELECT COUNT(*) FROM state_samples WHERE node_id = 'backup'`},
		{name: "traffic_monthly", query: `SELECT COUNT(*) FROM traffic_monthly WHERE node_id = 'backup'`},
		{name: "traffic_lifetime", query: `SELECT COUNT(*) FROM traffic_lifetime WHERE node_id = 'backup'`},
		{name: "node_probe_targets", query: `SELECT COUNT(*) FROM node_probe_targets WHERE node_id = 'backup'`},
		{name: "probe_rounds", query: `SELECT COUNT(*) FROM probe_rounds WHERE node_id = 'backup'`},
		{name: "probe_samples", query: `SELECT COUNT(*) FROM probe_samples WHERE round_id = ?`},
		{name: "alert_rule_states", query: `SELECT COUNT(*) FROM alert_rule_states WHERE node_id = 'backup'`},
	}
	for _, check := range checks {
		var count int
		var err error
		if check.name == "probe_samples" {
			err = store.db.QueryRowContext(ctx, check.query, roundID).Scan(&count)
		} else {
			err = store.db.QueryRowContext(ctx, check.query).Scan(&count)
		}
		if err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("%s rows after node delete = %d, want 0", check.name, count)
		}
	}
	var preservedScopes int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_rule_node_scopes WHERE rule_id = 'cpu_high' AND node_id = 'backup'`).Scan(&preservedScopes); err != nil {
		t.Fatalf("count preserved scopes: %v", err)
	}
	if preservedScopes != 0 {
		t.Fatalf("alert rule scope rows = %d, want foreign-key cascade cleanup", preservedScopes)
	}
	var cpuRuleEnabled int
	if err := store.db.QueryRowContext(ctx, `SELECT enabled FROM alert_rules WHERE id = 'cpu_high'`).Scan(&cpuRuleEnabled); err != nil {
		t.Fatalf("query cpu rule enabled: %v", err)
	}
	if cpuRuleEnabled != 0 {
		t.Fatalf("cpu_high enabled = %d, want disabled after deleting its last scoped node", cpuRuleEnabled)
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
		"billing_mode": "out",
		"monthly_reset_day": 20,
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
			BillingMode       string `json:"billing_mode"`
			MonthlyResetDay   int    `json:"monthly_reset_day"`
			MonthlyQuotaBytes int64  `json:"monthly_quota_bytes"`
		} `json:"node"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(raw)).Decode(&response); err != nil {
		t.Fatalf("decode created admin node: %v", err)
	}
	if response.Node.ID == "" || response.Node.DisplayName != "New Server" || response.Node.Status != "no_data" || response.Node.CountryCode != "US" || response.Node.Region != "Los Angeles" || response.Node.Disabled || response.Node.BillingMode != "out" || response.Node.MonthlyResetDay != 20 || response.Node.MonthlyQuotaBytes != 1099511627776 {
		t.Fatalf("created admin node = %+v, want trimmed editable no-data node", response.Node)
	}

	targets, err := store.EnabledProbeTargets(ctx, response.Node.ID)
	if err != nil {
		t.Fatalf("enabled probe targets for created node: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("created node targets = %d, want no default enabled target assignment", len(targets))
	}
}

func TestAdminNodeInstallCommandIssuesOneTimeEnrollmentWithoutRotatingActiveAgent(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	publicURL := "https://probe.example.com"
	if _, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{AgentControllerURL: &publicURL}); err != nil {
		t.Fatalf("set agent controller URL: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/hytron/install-command", nil)
	request.Host = "probe.example.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass"), AgentVersion: "testsha"}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		NodeID              string            `json:"node_id"`
		Command             string            `json:"command"`
		Commands            map[string]string `json:"commands"`
		EnrollmentExpiresAt string            `json:"enrollment_expires_at"`
		EnrollmentOneTime   bool              `json:"enrollment_one_time"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode install command: %v", err)
	}
	if response.NodeID != "hytron" {
		t.Fatalf("node_id = %q, want hytron", response.NodeID)
	}
	if !strings.Contains(response.Command, "https://zeno.shuijiao.de/agent/install.sh") || !strings.Contains(response.Command, "bash -o pipefail") || !strings.Contains(response.Command, "ZENO_CONTROLLER_URL='https://probe.example.com'") || !strings.Contains(response.Command, "ZENO_NODE_ID='hytron'") || !strings.Contains(response.Command, "ZENO_AGENT_VERSION='testsha'") {
		t.Fatalf("install command missing proxied installer, pipefail, controller URL, node id, or version: %s", response.Command)
	}
	if !strings.Contains(response.Commands["macos"], "https://zeno.shuijiao.de/agent/install.sh") || !strings.Contains(response.Commands["windows"], "https://zeno.shuijiao.de/agent/install.ps1") || !strings.Contains(response.Commands["windows"], "$env:ZENO_AGENT_VERSION='testsha'") {
		t.Fatalf("install commands should include macOS and Windows proxied variants: %#v", response.Commands)
	}
	if !strings.Contains(response.Command, "ZENO_ENROLLMENT_TOKEN='") || strings.Contains(response.Command, "ZENO_AGENT_TOKEN='") || !strings.Contains(response.Command, "sudo env") {
		t.Fatalf("install command should use Zeno agent names and paths: %s", response.Command)
	}
	if !response.EnrollmentOneTime || response.EnrollmentExpiresAt == "" {
		t.Fatalf("install response should describe expiring one-time enrollment: %+v", response)
	}
	credential := extractQuotedInstallCredential(t, response.Command)
	if credential == "old-agent-token" || credential == "" {
		t.Fatalf("install command leaked or omitted enrollment credential: %q", credential)
	}
	allowed, err := store.AuthorizeAgent(ctx, "hytron", "old-agent-token")
	if err != nil || !allowed {
		t.Fatalf("existing runtime credential must remain active while enrollment is pending: allowed=%v err=%v", allowed, err)
	}
	allowed, err = store.AuthorizeAgent(ctx, "hytron", credential)
	if err != nil {
		t.Fatalf("authorize enrollment credential as runtime: %v", err)
	}
	if allowed {
		t.Fatal("one-time enrollment credential must not authorize Agent API")
	}

	secondRecorder := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/hytron/install-command", nil)
	secondRequest.Host = "probe.example.com"
	secondRequest.Header.Set("X-Forwarded-Proto", "https")
	secondRequest.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass"), AgentVersion: "testsha"}).ServeHTTP(secondRecorder, secondRequest)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", secondRecorder.Code, secondRecorder.Body.String())
	}
	var secondResponse struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(secondRecorder.Body.String())).Decode(&secondResponse); err != nil {
		t.Fatalf("decode second install command: %v", err)
	}
	secondCredential := extractQuotedInstallCredential(t, secondResponse.Command)
	if secondCredential == credential || secondCredential == "" {
		t.Fatalf("second command must supersede the first enrollment: first=%q second=%q", credential, secondCredential)
	}
	if err := store.RedeemAgentEnrollment(ctx, "hytron", credential, strings.Repeat("a", 64)); !errors.Is(err, errAgentEnrollmentUnavailable) {
		t.Fatalf("superseded enrollment redemption error = %v, want unavailable", err)
	}
	newRuntimeToken := strings.Repeat("b", 64)
	if err := store.RedeemAgentEnrollment(ctx, "hytron", secondCredential, newRuntimeToken); err != nil {
		t.Fatalf("redeem current enrollment: %v", err)
	}
	if allowed, err := store.AuthorizeAgent(ctx, "hytron", newRuntimeToken); err != nil || !allowed {
		t.Fatalf("pending runtime credential should activate: allowed=%v err=%v", allowed, err)
	}
	if allowed, err := store.AuthorizeAgent(ctx, "hytron", "old-agent-token"); err != nil || allowed {
		t.Fatalf("old runtime credential should retire after activation: allowed=%v err=%v", allowed, err)
	}
}

func TestAdminNodeInstallCommandRejectsUnconfiguredRemoteHost(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/hytron/install-command", nil)
	request.Host = "attacker.example"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "old-agent-token") || strings.Contains(recorder.Body.String(), "attacker.example") {
		t.Fatalf("rejected response leaked credential or untrusted host: %s", recorder.Body.String())
	}
}

func TestAdminNodeInstallCommandPrefersConfiguredAgentControllerURL(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	publicURL := "https://zeno.example.com"
	if _, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{AgentControllerURL: &publicURL}); err != nil {
		t.Fatalf("set agent controller URL: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/hytron/install-command", nil)
	request.Host = "admin.localhost:18980"
	request.Header.Set("X-Forwarded-Proto", "http")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Command  string            `json:"command"`
		Commands map[string]string `json:"commands"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode install command: %v", err)
	}
	if !strings.Contains(response.Command, "ZENO_CONTROLLER_URL='https://zeno.example.com'") || !strings.Contains(response.Command, "zeno.shuijiao.de/agent/install.sh") {
		t.Fatalf("install command should use configured agent controller URL and proxied installer: %s", response.Command)
	}
	if !strings.Contains(response.Commands["windows"], "$env:ZENO_CONTROLLER_URL='https://zeno.example.com'") {
		t.Fatalf("windows install command should use configured agent controller URL: %s", response.Commands["windows"])
	}
	if strings.Contains(response.Command, "admin.localhost") {
		t.Fatalf("install command should not fall back to request host when configured URL exists: %s", response.Command)
	}
}

func TestAdminNodeInstallCommandFallsBackToDirectIPAddressAndPort(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/hytron/install-command", nil)
	request.Host = "203.0.113.10:18980"
	request.Header.Set("X-Forwarded-Proto", "http")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "ZENO_CONTROLLER_URL='http://203.0.113.10:18980'") {
		t.Fatalf("install command should use the direct IP and port: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "ZENO_ALLOW_INSECURE_HTTP") {
		t.Fatalf("remote HTTP install command must carry an explicit plaintext transport opt-in: %s", recorder.Body.String())
	}
}

func TestAdminNodeInstallCommandUsesAuthenticatedBrowserOriginWhenSettingIsEmpty(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", AgentToken: "old-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/nodes/hytron/install-command", bytes.NewBufferString(`{"controller_url":"https://zeno.example.com"}`))
	request.Host = "attacker.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "ZENO_CONTROLLER_URL='https://zeno.example.com'") {
		t.Fatalf("install command should use the authenticated browser origin: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "attacker.example") {
		t.Fatalf("install command trusted the request Host instead of the explicit origin: %s", recorder.Body.String())
	}
}

func extractQuotedInstallCredential(t *testing.T, command string) string {
	t.Helper()
	marker := "ZENO_ENROLLMENT_TOKEN='"
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
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

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
	if hytron.ID == "" || hytron.Name != "Hytron" || hytron.Type != "tcping" || hytron.Address != "127.0.0.1" || hytron.Port == nil || *hytron.Port != 18980 || hytron.Count != 3 || hytron.TimeoutMS != 1000 || hytron.IntervalSec != 30 || !hytron.Enabled {
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

func TestAdminProbeTargetCreateDefaultsToNoAssignedServersWithoutSecrets(t *testing.T) {
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
	if len(response.Target.Assignments) != 0 {
		t.Fatalf("created target assignments = %+v, want no server enabled by default", response.Target.Assignments)
	}
	targets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	for _, target := range targets {
		if target.ID == response.Target.ID {
			t.Fatalf("created target %q unexpectedly assigned to hytron enabled target set", response.Target.ID)
		}
	}
}

func TestAdminProbeTargetCreateAcceptsExplicitServerAssignments(t *testing.T) {
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
		"name": "Assigned HTTPS",
		"type": "http_get",
		"address": "https://example.com/health",
		"count": 2,
		"timeout_ms": 1500,
		"interval_sec": 30,
		"assignments": [{"node_id":"hytron","enabled":true}]
	}`))
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
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
	if err := json.NewDecoder(bytes.NewBufferString(recorder.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode created target: %v", err)
	}
	if len(response.Target.Assignments) != 1 || response.Target.Assignments[0].NodeID != "hytron" || !response.Target.Assignments[0].Enabled {
		t.Fatalf("created assignments = %+v, want explicit hytron enabled", response.Target.Assignments)
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
	if len(response.Target.Assignments) != 0 {
		t.Fatalf("created ping assignments = %+v, want no server enabled by default", response.Target.Assignments)
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
	if found {
		t.Fatalf("created ping target %q unexpectedly assigned to hytron enabled target set", response.Target.ID)
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
		"interval_sec": 30
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
	if response.Target.ID == "" || response.Target.Name != "Zeno Health" || response.Target.Type != "http_get" || response.Target.Address != "https://example.com/health" || response.Target.Port != nil || response.Target.Count != 2 || response.Target.TimeoutMS != 1500 || response.Target.IntervalSec != 30 || !response.Target.Enabled {
		t.Fatalf("created http_get target = %+v, want normalized enabled HTTP GET target without port", response.Target)
	}
	if len(response.Target.Assignments) != 0 {
		t.Fatalf("created http_get assignments = %+v, want no server enabled by default", response.Target.Assignments)
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
	if found {
		t.Fatalf("created http_get target %q unexpectedly assigned to hytron enabled target set", response.Target.ID)
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

func TestAdminProbeTargetDisplayOrderControlsInventoryAndAgentOrder(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "agent-super-secret"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	for targetID, order := range map[string]int{"google-dns": 5, "hytron-local": 250} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/probe-targets/"+targetID, bytes.NewBufferString(fmt.Sprintf(`{"display_order": %d}`, order)))
		request.Header.Set("X-Admin-Token", "admin-pass")
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("patch %s status = %d, want 200; body=%s", targetID, recorder.Code, recorder.Body.String())
		}
		assertNoSensitiveAdminProbeTargetLeak(t, recorder.Body.String())
	}

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/probe-targets", nil)
	listRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var listResponse struct {
		Targets []struct {
			ID           string `json:"id"`
			DisplayOrder int    `json:"display_order"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(listRecorder.Body.String())).Decode(&listResponse); err != nil {
		t.Fatalf("decode target list: %v", err)
	}
	if len(listResponse.Targets) == 0 || listResponse.Targets[0].ID != "google-dns" || listResponse.Targets[0].DisplayOrder != 5 {
		t.Fatalf("first admin target = %+v, want google-dns display_order 5", listResponse.Targets)
	}

	agentTargets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	if len(agentTargets) == 0 || agentTargets[0].ID != "google-dns" {
		t.Fatalf("first agent target = %+v, want google-dns by display_order", agentTargets)
	}
}

func TestAdminProbeTargetDeleteRemovesTargetAndAssignments(t *testing.T) {
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
	if _, err := store.UpdateAdminProbeTarget(ctx, "hytron-local", AdminProbeTargetUpdateRequest{Assignments: []AdminProbeTargetAssignmentUpdate{
		{NodeID: "hytron", Enabled: true},
		{NodeID: "backup", Enabled: true},
	}}); err != nil {
		t.Fatalf("seed assignments: %v", err)
	}
	roundResult, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent)
		VALUES ('hytron', 'hytron-local', ?, 'tcping', 1, 1, 0)
	`, time.Now().UTC().Unix())
	if err != nil {
		t.Fatalf("seed probe round: %v", err)
	}
	roundID, err := roundResult.LastInsertId()
	if err != nil {
		t.Fatalf("seed probe round id: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_samples (round_id, seq, success, latency_ms)
		VALUES (?, 1, 1, 0.42)
	`, roundID); err != nil {
		t.Fatalf("seed probe sample: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/admin/v1/probe-targets/hytron-local", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.TrimSpace(recorder.Body.String()) != "" {
		t.Fatalf("delete body = %q, want empty", recorder.Body.String())
	}
	targets, err := store.AdminProbeTargets(ctx)
	if err != nil {
		t.Fatalf("admin targets after delete: %v", err)
	}
	for _, target := range targets {
		if target.ID == "hytron-local" {
			t.Fatalf("deleted target still visible in admin inventory: %+v", target)
		}
	}
	for _, nodeID := range []string{"hytron", "backup"} {
		enabledTargets, err := store.EnabledProbeTargets(ctx, nodeID)
		if err != nil {
			t.Fatalf("enabled targets for %s after delete: %v", nodeID, err)
		}
		for _, target := range enabledTargets {
			if target.ID == "hytron-local" {
				t.Fatalf("deleted target still assigned to %s agent targets", nodeID)
			}
		}
	}
	var remainingRounds, remainingSamples int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds WHERE target_id = 'hytron-local'`).Scan(&remainingRounds); err != nil {
		t.Fatalf("count remaining probe rounds: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples WHERE round_id = ?`, roundID).Scan(&remainingSamples); err != nil {
		t.Fatalf("count remaining probe samples: %v", err)
	}
	if remainingRounds != 0 || remainingSamples != 0 {
		t.Fatalf("deleted target history remains: rounds=%d samples=%d", remainingRounds, remainingSamples)
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
		{name: "create missing token", method: http.MethodPost, path: "/api/admin/v1/probe-targets", body: `{"name":"A","type":"tcping","address":"example.com","port":443,"count":3,"timeout_ms":1000,"interval_sec":30}`, wantStatus: http.StatusUnauthorized},
		{name: "create blank name", method: http.MethodPost, path: "/api/admin/v1/probe-targets", body: `{"name":"   ","type":"tcping","address":"example.com","port":443,"count":3,"timeout_ms":1000,"interval_sec":30}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "create ping option-looking address", method: http.MethodPost, path: "/api/admin/v1/probe-targets", body: `{"name":"A","type":"ping","address":"-f","count":3,"timeout_ms":1000,"interval_sec":30}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "create bad port", method: http.MethodPost, path: "/api/admin/v1/probe-targets", body: `{"name":"A","type":"tcping","address":"example.com","port":70000,"count":3,"timeout_ms":1000,"interval_sec":30}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "create count above resource cap", method: http.MethodPost, path: "/api/admin/v1/probe-targets", body: `{"name":"A","type":"tcping","address":"example.com","port":443,"count":33,"timeout_ms":1000,"interval_sec":60}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "create timeout below resource floor", method: http.MethodPost, path: "/api/admin/v1/probe-targets", body: `{"name":"A","type":"tcping","address":"example.com","port":443,"count":3,"timeout_ms":50,"interval_sec":30}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "create exceeds single round budget", method: http.MethodPost, path: "/api/admin/v1/probe-targets", body: `{"name":"A","type":"tcping","address":"example.com","port":443,"count":32,"timeout_ms":5000,"interval_sec":60}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch unknown target", method: http.MethodPatch, path: "/api/admin/v1/probe-targets/missing", body: `{"name":"Changed"}`, adminToken: "admin-pass", wantStatus: http.StatusNotFound},
		{name: "patch negative count", method: http.MethodPatch, path: "/api/admin/v1/probe-targets/hytron-local", body: `{"count":0}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch interval too small for final budget", method: http.MethodPatch, path: "/api/admin/v1/probe-targets/hytron-local", body: `{"count":32}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "patch unknown assignment node", method: http.MethodPatch, path: "/api/admin/v1/probe-targets/hytron-local", body: `{"assignments":[{"node_id":"missing","enabled":false}]}`, adminToken: "admin-pass", wantStatus: http.StatusBadRequest},
		{name: "delete missing token", method: http.MethodDelete, path: "/api/admin/v1/probe-targets/hytron-local", adminToken: "", wantStatus: http.StatusUnauthorized},
		{name: "delete unknown target", method: http.MethodDelete, path: "/api/admin/v1/probe-targets/missing", adminToken: "admin-pass", wantStatus: http.StatusNotFound},
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

func TestAdminProbeTargetAssignmentRejectsNodeTargetCountOverflow(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	existingTargets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets before fill: %v", err)
	}
	if len(existingTargets) >= maxProbeTargetsPerNode {
		t.Fatalf("seeded target count=%d unexpectedly already at cap %d", len(existingTargets), maxProbeTargetsPerNode)
	}

	for index := 0; index < maxProbeTargetsPerNode-len(existingTargets); index++ {
		_, err := store.CreateAdminProbeTarget(ctx, AdminProbeTargetCreateRequest{
			Name:        fmt.Sprintf("Bulk %02d", index),
			Type:        "tcping",
			Address:     "203.0.113.10",
			Port:        adminOptionalInt64{Set: true, Valid: true, Value: 443},
			Count:       1,
			TimeoutMS:   minProbeTargetTimeoutMS,
			IntervalSec: minProbeTargetIntervalSec,
			Assignments: []AdminProbeTargetAssignmentUpdate{{NodeID: "hytron", Enabled: true}},
		})
		if err != nil {
			t.Fatalf("create filler target %d: %v", index, err)
		}
	}
	filledTargets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets after fill: %v", err)
	}
	if len(filledTargets) != maxProbeTargetsPerNode {
		t.Fatalf("enabled target count after fill=%d, want cap %d", len(filledTargets), maxProbeTargetsPerNode)
	}
	_, err = store.CreateAdminProbeTarget(ctx, AdminProbeTargetCreateRequest{
		Name:        "Overflow",
		Type:        "tcping",
		Address:     "203.0.113.11",
		Port:        adminOptionalInt64{Set: true, Valid: true, Value: 443},
		Count:       1,
		TimeoutMS:   minProbeTargetTimeoutMS,
		IntervalSec: minProbeTargetIntervalSec,
		Assignments: []AdminProbeTargetAssignmentUpdate{{NodeID: "hytron", Enabled: true}},
	})
	if err != errInvalidAdminTargetWrite {
		t.Fatalf("overflow create error=%v, want errInvalidAdminTargetWrite", err)
	}
}

func TestAdminProbeTargetAssignmentRejectsNodeRoundBudgetOverflow(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	heavyCreate := func(name, address string) error {
		_, err := store.CreateAdminProbeTarget(ctx, AdminProbeTargetCreateRequest{
			Name:        name,
			Type:        "tcping",
			Address:     address,
			Port:        adminOptionalInt64{Set: true, Valid: true, Value: 443},
			Count:       12,
			TimeoutMS:   maxProbeTargetTimeoutMS,
			IntervalSec: 60,
			Assignments: []AdminProbeTargetAssignmentUpdate{{NodeID: "hytron", Enabled: true}},
		})
		return err
	}
	if err := heavyCreate("Heavy A", "203.0.113.20"); err != nil {
		t.Fatalf("create first heavy target: %v", err)
	}
	if err := heavyCreate("Heavy B", "203.0.113.21"); err != errInvalidAdminTargetWrite {
		t.Fatalf("second heavy target error=%v, want errInvalidAdminTargetWrite", err)
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

func TestAdminNotificationChannelsAreTelegramOnlyAndDoNotExposeChannelType(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-channels", bytes.NewBufferString(`{
		"name": "Telegram Home",
		"destination": "7579942307",
		"credential": "telegram-bot-secret-value",
		"enabled": true
	}`))
	createRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(createRecorder, createRequest)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	if bytes.Contains(createRecorder.Body.Bytes(), []byte(`"type"`)) {
		t.Fatalf("telegram-only channel create response exposed channel type: %s", createRecorder.Body.String())
	}

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/notification-channels", nil)
	listRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	if bytes.Contains(listRecorder.Body.Bytes(), []byte(`"type"`)) {
		t.Fatalf("telegram-only channel list response exposed channel type: %s", listRecorder.Body.String())
	}

	explicitTypeRecorder := httptest.NewRecorder()
	explicitTypeRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-channels", bytes.NewBufferString(`{
		"name": "Explicit Type",
		"type": "unsupported",
		"destination": "7579942307",
		"credential": "telegram-bot-secret-value"
	}`))
	explicitTypeRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(explicitTypeRecorder, explicitTypeRequest)
	if explicitTypeRecorder.Code != http.StatusBadRequest {
		t.Fatalf("explicit type create status = %d, want 400; body=%s", explicitTypeRecorder.Code, explicitTypeRecorder.Body.String())
	}
	assertNoSensitiveNotificationLeak(t, explicitTypeRecorder.Body.String())
}

func TestAdminNotificationChannelsCreateListAndPatchHideStoredCredentials(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-channels", bytes.NewBufferString(`{
		"name": "  Telegram Home  ",
		"destination": "  7579942307  ",
		"credential": "telegram-bot-secret-value",
		"enabled": true
	}`))
	createRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(createRecorder, createRequest)

	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	assertNoNotificationCredentialField(t, createRecorder.Body.String())
	assertNoSensitiveNotificationLeak(t, createRecorder.Body.String())
	var createResponse struct {
		Channel struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Destination   string `json:"destination"`
			CredentialSet bool   `json:"credential_set"`
			Enabled       bool   `json:"enabled"`
		} `json:"channel"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(createRecorder.Body.String())).Decode(&createResponse); err != nil {
		t.Fatalf("decode created notification channel: %v", err)
	}
	if createResponse.Channel.ID == "" || createResponse.Channel.Name != "Telegram Home" || createResponse.Channel.Destination != "7579942307" || !createResponse.Channel.CredentialSet || !createResponse.Channel.Enabled {
		t.Fatalf("created channel = %+v, want normalized enabled telegram channel with credential_set only", createResponse.Channel)
	}

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/admin/v1/notification-channels", nil)
	listRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	assertNoNotificationCredentialField(t, listRecorder.Body.String())
	assertNoSensitiveNotificationLeak(t, listRecorder.Body.String())
	var listResponse struct {
		Channels []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Destination   string `json:"destination"`
			CredentialSet bool   `json:"credential_set"`
			Enabled       bool   `json:"enabled"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(listRecorder.Body.String())).Decode(&listResponse); err != nil {
		t.Fatalf("decode notification channels: %v", err)
	}
	if len(listResponse.Channels) != 1 || listResponse.Channels[0].ID != createResponse.Channel.ID || !listResponse.Channels[0].CredentialSet {
		t.Fatalf("listed channels = %+v, want one persisted channel with credential_set only", listResponse.Channels)
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
	assertNoNotificationCredentialField(t, patchRecorder.Body.String())
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
		t.Fatalf("patched channel = %+v, want renamed disabled channel preserving credential_set only", patchResponse.Channel)
	}
	if _, err := store.UpdateAdminNotificationChannel(ctx, createResponse.Channel.ID, AdminNotificationChannelUpdateRequest{Credential: stringPtr("   ")}); err == nil {
		t.Fatalf("blank-only notification channel patch succeeded, want no-op patch to be rejected")
	} else if err != errInvalidAdminNotificationChannelWrite {
		t.Fatalf("blank-only notification channel patch error = %v, want invalid write", err)
	}
	if _, err := store.UpdateAdminNotificationChannel(ctx, createResponse.Channel.ID, AdminNotificationChannelUpdateRequest{Credential: stringPtr("telegram-bot-secret-value")}); err != nil {
		t.Fatalf("rewrite notification credential: %v", err)
	}
	if _, err := store.UpdateAdminNotificationChannel(ctx, createResponse.Channel.ID, AdminNotificationChannelUpdateRequest{Name: stringPtr("Home Telegram Still Secret"), Credential: stringPtr("   ")}); err != nil {
		t.Fatalf("blank credential with other patch fields should preserve old credential: %v", err)
	}
	dispatchChannel, err := store.AdminNotificationDispatchChannel(ctx, createResponse.Channel.ID)
	if err != errNotificationChannelNotFound {
		t.Fatalf("disabled dispatch channel lookup err = %v, want not found while channel disabled", err)
	}
	enabled := true
	if _, err := store.UpdateAdminNotificationChannel(ctx, createResponse.Channel.ID, AdminNotificationChannelUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("re-enable notification channel: %v", err)
	}
	dispatchChannel, err = store.AdminNotificationDispatchChannel(ctx, createResponse.Channel.ID)
	if err != nil {
		t.Fatalf("lookup dispatch channel after preserving credential: %v", err)
	}
	if dispatchChannel.Credential != "telegram-bot-secret-value" {
		t.Fatalf("stored credential after blank edit = %q, want original secret preserved", dispatchChannel.Credential)
	}
}

func TestAdminNotificationChannelTestSendsTelegramAndReturnsSanitizedDelivery(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	var receivedPath string
	var receivedForm string
	telegramAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("telegram method = %s, want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse telegram form: %v", err)
		}
		receivedPath = r.URL.Path
		receivedForm = r.Form.Encode()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer telegramAPI.Close()
	enabled := true
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "smoke-telegram",
		Name:        "Smoke Telegram",
		Destination: "7579942307",
		Credential:  "telegram-bot-secret-value",
		Enabled:     &enabled,
	})
	if err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass"), NotificationClient: telegramAPI.Client(), TelegramAPIBaseURL: telegramAPI.URL})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-channels/"+channel.ID+"/test", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("test status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	assertNoSensitiveNotificationLeak(t, recorder.Body.String())
	if receivedPath != "/bottelegram-bot-secret-value/sendMessage" || !strings.Contains(receivedForm, "chat_id=7579942307") || !strings.Contains(receivedForm, "%E9%80%9A%E7%9F%A5%E6%B8%A0%E9%81%93%E6%B5%8B%E8%AF%95") {
		t.Fatalf("telegram test request path=%q form=%q, want test sendMessage", receivedPath, receivedForm)
	}
	if strings.Contains(receivedForm, "telegram-bot-secret-value") {
		t.Fatalf("telegram form leaked credential: %s", receivedForm)
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
	if response.Delivery.EventType != "test_notification" || response.Delivery.Label != "测试发送" || response.Delivery.ChannelID != channel.ID || response.Delivery.ChannelName != "Smoke Telegram" || !response.Delivery.Success || response.Delivery.Error != "" {
		t.Fatalf("test delivery response = %+v, want successful sanitized test delivery", response.Delivery)
	}
}

func TestAdminNotificationChannelTestIsBlockedWithoutNotificationAuthority(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	enabled := true
	channel, err := store.CreateAdminNotificationChannel(context.Background(), AdminNotificationChannelCreateRequest{
		ID: "candidate-telegram", Name: "Candidate Telegram", Destination: "7579942307", Credential: "telegram-bot-secret-value", Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass"), DisableNotifications: true})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-channels/"+channel.ID+"/test", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("disabled notification test status=%d want=%d body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}

func TestAdminNotificationChannelTestRespectsChannelEnabledState(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	enabled := false
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "disabled-telegram",
		Name:        "Disabled Telegram",
		Destination: "7579942307",
		Credential:  "telegram-bot-secret-value",
		Enabled:     &enabled,
	})
	if err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-channels/"+channel.ID+"/test", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled channel test status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminNotificationChannelDeleteRemovesChannelWithoutCredentialLeak(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	enabled := true
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID:          "smoke-telegram",
		Name:        "Smoke Telegram",
		Destination: "https://example.com/notify",
		Credential:  "telegram-bot-secret-value",
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

func TestAdminNotificationTypesPatch(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/notification-types/node_offline", bytes.NewBufferString(`{"enabled": false}`))
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
	if patchResponse.Type.EventType != "node_offline" || patchResponse.Type.Label != "离线" || patchResponse.Type.Enabled {
		t.Fatalf("patched notification type = %+v, want disabled offline type", patchResponse.Type)
	}
	var enabled int
	if err := store.db.QueryRowContext(context.Background(), `SELECT enabled FROM alert_rules WHERE id = 'node_offline'`).Scan(&enabled); err != nil {
		t.Fatalf("query alert rule enabled: %v", err)
	}
	if enabled != 0 {
		t.Fatalf("node_offline alert rule enabled = %d, want notification type compatibility endpoint to update alert_rules", enabled)
	}
}

func TestAdminNotificationTypesPatchRejectsSharedEventCompatibilityNoOp(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `UPDATE alert_rules SET enabled = 0 WHERE notification_event_type = 'probe_unhealthy'`); err != nil {
		t.Fatalf("disable shared event rules: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/notification-types/probe_unhealthy", bytes.NewBufferString(`{"enabled": true}`))
	patchRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusGone {
		t.Fatalf("patch status = %d, want 410; body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}
	rows, err := store.db.QueryContext(ctx, `SELECT id, enabled FROM alert_rules WHERE notification_event_type = 'probe_unhealthy' ORDER BY id ASC`)
	if err != nil {
		t.Fatalf("query shared event rules: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ruleID string
		var enabled int
		if err := rows.Scan(&ruleID, &enabled); err != nil {
			t.Fatalf("scan shared event rule: %v", err)
		}
		if enabled != 0 {
			t.Fatalf("shared event rule %s enabled = %d, want notification-types patch not to batch enable alert rules", ruleID, enabled)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("shared event rows: %v", err)
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

func assertNoNotificationCredentialField(t *testing.T, raw string) {
	t.Helper()
	if bytes.Contains([]byte(raw), []byte(`"credential":`)) {
		t.Fatalf("notification response exposed write-only credential field: %s", raw)
	}
}

func stringPtr(value string) *string {
	return &value
}
