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

func TestAdminMaintenanceReportsRetentionSettingsAndCleanupCandidates(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	seedMaintenanceFixture(t, store)

	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/admin/v1/maintenance", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401; body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	if containsSensitiveMaintenanceText(unauthorized.Body.String()) {
		t.Fatalf("unauthorized body leaked sensitive wording: %s", unauthorized.Body.String())
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/maintenance", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if containsSensitiveMaintenanceText(recorder.Body.String()) {
		t.Fatalf("maintenance response leaked sensitive wording: %s", recorder.Body.String())
	}

	var response struct {
		Settings struct {
			Enabled                   bool `json:"enabled"`
			StateRetentionDays        int  `json:"state_retention_days"`
			ProbeRetentionDays        int  `json:"probe_retention_days"`
			NotificationRetentionDays int  `json:"notification_retention_days"`
		} `json:"settings"`
		Candidates struct {
			StateSamples           int64 `json:"state_samples"`
			ProbeRounds            int64 `json:"probe_rounds"`
			ProbeSamples           int64 `json:"probe_samples"`
			NotificationDeliveries int64 `json:"notification_deliveries"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes())).Decode(&response); err != nil {
		t.Fatalf("decode maintenance response: %v", err)
	}
	if response.Settings.Enabled {
		t.Fatalf("default maintenance enabled = true, want false for safe deploy")
	}
	if response.Settings.StateRetentionDays != 30 || response.Settings.ProbeRetentionDays != 30 || response.Settings.NotificationRetentionDays != 90 {
		t.Fatalf("default maintenance settings = %+v, want 30/30/90 day retention", response.Settings)
	}
	if response.Candidates.StateSamples != 1 || response.Candidates.ProbeRounds != 1 || response.Candidates.ProbeSamples != 2 || response.Candidates.NotificationDeliveries != 1 {
		t.Fatalf("cleanup candidates = %+v, want one old state/probe/delivery fixture", response.Candidates)
	}
}

func TestAdminMaintenancePatchAndCleanupRequireExplicitConfirmation(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	seedMaintenanceFixture(t, store)
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	patch := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/maintenance", bytes.NewBufferString(`{
		"enabled": true,
		"state_retention_days": 7,
		"probe_retention_days": 14,
		"notification_retention_days": 21
	}`))
	patchRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(patch, patchRequest)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", patch.Code, patch.Body.String())
	}
	if containsSensitiveMaintenanceText(patch.Body.String()) {
		t.Fatalf("maintenance patch leaked sensitive wording: %s", patch.Body.String())
	}

	badCleanup := httptest.NewRecorder()
	badCleanupRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/maintenance/cleanup", bytes.NewBufferString(`{"dry_run":false}`))
	badCleanupRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(badCleanup, badCleanupRequest)
	if badCleanup.Code != http.StatusBadRequest {
		t.Fatalf("cleanup without confirmation status = %d, want 400; body=%s", badCleanup.Code, badCleanup.Body.String())
	}
	if got := countTableRows(t, store, "state_samples"); got != 2 {
		t.Fatalf("state sample rows after rejected cleanup = %d, want 2", got)
	}

	dryRun := httptest.NewRecorder()
	dryRunRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/maintenance/cleanup", bytes.NewBufferString(`{"dry_run":true}`))
	dryRunRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(dryRun, dryRunRequest)
	if dryRun.Code != http.StatusOK {
		t.Fatalf("dry-run cleanup status = %d, want 200; body=%s", dryRun.Code, dryRun.Body.String())
	}
	if got := countTableRows(t, store, "state_samples"); got != 2 {
		t.Fatalf("state sample rows after dry-run = %d, want 2", got)
	}

	execute := httptest.NewRecorder()
	executeRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/maintenance/cleanup", bytes.NewBufferString(`{"dry_run":false,"confirm":true}`))
	executeRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(execute, executeRequest)
	if execute.Code != http.StatusOK {
		t.Fatalf("confirmed cleanup status = %d, want 200; body=%s", execute.Code, execute.Body.String())
	}
	if containsSensitiveMaintenanceText(execute.Body.String()) {
		t.Fatalf("maintenance cleanup leaked sensitive wording: %s", execute.Body.String())
	}

	if got := countTableRows(t, store, "state_samples"); got != 1 {
		t.Fatalf("state sample rows after cleanup = %d, want only recent row", got)
	}
	if got := countTableRows(t, store, "probe_rounds"); got != 1 {
		t.Fatalf("probe round rows after cleanup = %d, want only recent row", got)
	}
	if got := countTableRows(t, store, "probe_samples"); got != 1 {
		t.Fatalf("probe sample rows after cleanup = %d, want only recent sample", got)
	}
	if got := countTableRows(t, store, "notification_deliveries"); got != 1 {
		t.Fatalf("notification delivery rows after cleanup = %d, want only recent row", got)
	}
}

func TestAutomaticAdminMaintenanceCleanupRespectsEnabledSetting(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	seedMaintenanceFixture(t, store)
	ctx := context.Background()

	cleanup, ran, err := store.RunAutomaticAdminMaintenanceCleanup(ctx)
	if err != nil {
		t.Fatalf("automatic cleanup disabled: %v", err)
	}
	if ran || cleanup.Settings.Enabled {
		t.Fatalf("automatic cleanup ran=%v settings=%+v, want skipped while disabled", ran, cleanup.Settings)
	}
	if got := countTableRows(t, store, "probe_rounds"); got != 2 {
		t.Fatalf("probe round rows after skipped cleanup = %d, want 2", got)
	}

	if _, err := store.UpdateAdminMaintenance(ctx, AdminMaintenanceUpdateRequest{Enabled: adminMaintenanceBoolPtr(true), StateRetentionDays: adminMaintenanceIntPtr(30), ProbeRetentionDays: adminMaintenanceIntPtr(30), NotificationRetentionDays: adminMaintenanceIntPtr(90)}); err != nil {
		t.Fatalf("enable maintenance: %v", err)
	}
	cleanup, ran, err = store.RunAutomaticAdminMaintenanceCleanup(ctx)
	if err != nil {
		t.Fatalf("automatic cleanup enabled: %v", err)
	}
	if !ran || !cleanup.Settings.Enabled {
		t.Fatalf("automatic cleanup ran=%v settings=%+v, want enabled run", ran, cleanup.Settings)
	}
	if cleanup.Deleted.StateSamples != 1 || cleanup.Deleted.ProbeRounds != 1 || cleanup.Deleted.ProbeSamples != 2 || cleanup.Deleted.NotificationDeliveries != 1 {
		t.Fatalf("automatic cleanup deleted = %+v, want old fixture rows", cleanup.Deleted)
	}
	if got := countTableRows(t, store, "probe_rounds"); got != 1 {
		t.Fatalf("probe round rows after automatic cleanup = %d, want 1", got)
	}
}

func adminMaintenanceBoolPtr(value bool) *bool {
	return &value
}

func adminMaintenanceIntPtr(value int) *int {
	return &value
}

func seedMaintenanceFixture(t *testing.T, store *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC().Unix()
	old := time.Now().UTC().Add(-45 * 24 * time.Hour).Unix()
	oldNotification := time.Now().UTC().Add(-120 * 24 * time.Hour).Unix()
	if _, err := store.db.ExecContext(ctx, `
		DELETE FROM state_samples;
		DELETE FROM probe_samples;
		DELETE FROM probe_rounds;
		DELETE FROM notification_deliveries;
	`); err != nil {
		t.Fatalf("reset fixture tables: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES ('hytron', ?, 95), ('hytron', ?, 20);
	`, old, now); err != nil {
		t.Fatalf("insert state samples: %v", err)
	}
	oldRound := insertProbeRoundForMaintenance(t, store, old)
	recentRound := insertProbeRoundForMaintenance(t, store, now)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_samples (round_id, seq, success, latency_ms) VALUES (?, 1, 1, 10), (?, 2, 0, NULL), (?, 1, 1, 11)
	`, oldRound, oldRound, recentRound); err != nil {
		t.Fatalf("insert probe samples: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO notification_deliveries (event_type, label, node_id, node_name, previous_status, status, channel_id, channel_name, channel_type, success, error, created_at)
		VALUES ('probe_unhealthy', '异常', 'hytron', 'Hytron', 'online', 'warning', 'telegram', 'Telegram', 'telegram', 1, '', ?),
		       ('node_online', '上线', 'hytron', 'Hytron', 'offline', 'online', 'telegram', 'Telegram', 'telegram', 1, '', ?)
	`, oldNotification, now); err != nil {
		t.Fatalf("insert notification deliveries: %v", err)
	}
}

func insertProbeRoundForMaintenance(t *testing.T, store *SQLiteStore, ts int64) int64 {
	t.Helper()
	result, err := store.db.ExecContext(context.Background(), `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms, error)
		VALUES ('hytron', 'hytron-local', ?, 'tcping', 2, 2, 0, 10, 11, 11, 12, 1, '')
	`, ts)
	if err != nil {
		t.Fatalf("insert probe round: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func countTableRows(t *testing.T, store *SQLiteStore, table string) int64 {
	t.Helper()
	var count int64
	if err := store.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func containsSensitiveMaintenanceText(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") || strings.Contains(lower, "hash") || strings.Contains(lower, "bearer")
}
