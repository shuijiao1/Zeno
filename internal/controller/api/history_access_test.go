package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestExtendedHistoryRequiresAdminToken(t *testing.T) {
	const adminToken = "history-admin-token"
	handler := NewHandler(HandlerOptions{AdminTokenHash: HashAdminToken(adminToken)})
	paths := []string{
		"/api/public/v1/nodes/hytron/latency?range=7d",
		"/api/public/v1/nodes/hytron/state?range=30d",
		"/api/public/v1/services/google/latency?range=7d",
	}
	for _, path := range paths {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", path, recorder.Code)
		}

		recorder = httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Admin-Token", adminToken)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("authorized %s status = %d, want 200; body=%s", path, recorder.Code, recorder.Body.String())
		}
	}

	for _, path := range []string{
		"/api/public/v1/nodes/hytron/latency?range=1d",
		"/api/public/v1/nodes/hytron/state?range=1d",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("public %s status = %d, want 200", path, recorder.Code)
		}
	}
}

func TestPruneRawHistoryKeepsExactlyThirtyDayWindow(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, created_at, updated_at)
		VALUES ('node-1', 'Node 1', 'hash', 'online', ?, ?);
		INSERT INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, enabled, created_at, updated_at)
		VALUES ('target-1', 'Target 1', 'tcp', '127.0.0.1', 443, 1, 1000, 30, 1, ?, ?);
	`, now.Unix(), now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("seed retention rows: %v", err)
	}

	oldTS := now.Add(-31 * 24 * time.Hour).Unix()
	boundaryTS := now.Add(-30 * 24 * time.Hour).Unix()
	newTS := now.Add(-29 * 24 * time.Hour).Unix()
	for _, ts := range []int64{oldTS, boundaryTS, newTS} {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES ('node-1', ?, 10)`, ts); err != nil {
			t.Fatalf("insert state sample: %v", err)
		}
		result, err := store.db.ExecContext(ctx, `
			INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent)
			VALUES ('node-1', 'target-1', ?, 'tcp', 1, 1, 0)
		`, ts)
		if err != nil {
			t.Fatalf("insert probe round: %v", err)
		}
		roundID, _ := result.LastInsertId()
		if _, err := store.db.ExecContext(ctx, `INSERT INTO probe_samples (round_id, seq, success, latency_ms) VALUES (?, 1, 1, 10)`, roundID); err != nil {
			t.Fatalf("insert probe sample: %v", err)
		}
	}

	if err := store.PruneRawHistory(ctx, now.Add(-rawHistoryRetention)); err != nil {
		t.Fatalf("prune raw history: %v", err)
	}
	for table, want := range map[string]int{"state_samples": 2, "probe_rounds": 2, "probe_samples": 2} {
		var got int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
}
