package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/shuijiao1/zeno/internal/shared/probe"
)

func TestAdminNodeDeleteReturnsImmediatelyHidesAndRejectsNewReports(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	store.stopAdminDeletionWorker()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if _, err := store.CreateAdminNode(ctx, AdminNodeCreateRequest{ID: "large-node", DisplayName: "Large node", CountryCode: "US"}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET token_hash = ? WHERE id = 'large-node'`, hashAgentToken("live-token")); err != nil {
		t.Fatalf("set known Agent token: %v", err)
	}
	if authorized, err := store.AuthorizeAgent(ctx, "large-node", "live-token"); err != nil || !authorized {
		t.Fatalf("Agent authorization before delete = %v, err=%v; want true", authorized, err)
	}
	rowCount := adminDeleteBatchSize*2 + 37
	seedAdminDeleteHistory(t, store, "large-node", "hytron-local", rowCount, 2)

	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/admin/v1/nodes/large-node", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	started := time.Now()
	handler.ServeHTTP(recorder, request)
	elapsed := time.Since(started)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	if elapsed >= time.Second {
		t.Fatalf("delete took %s, want sub-second durable tombstone", elapsed)
	}

	// The worker is stopped, proving the request did not synchronously consume
	// the large history and that visibility comes from the durable tombstone.
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM nodes WHERE id = 'large-node'`, 1)
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM state_samples WHERE node_id = 'large-node'`, rowCount)
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM admin_deletion_jobs WHERE entity_kind = 'node' AND entity_id = 'large-node' AND state = 'pending'`, 1)
	assertAdminNodeAbsent(t, store, "large-node")
	if exists, err := store.nodeExists(ctx, "large-node"); err != nil || exists {
		t.Fatalf("public node existence after tombstone = %v, err=%v; want false", exists, err)
	}
	if authorized, err := store.AuthorizeAgent(ctx, "large-node", "live-token"); err != nil || authorized {
		t.Fatalf("agent authorization after tombstone = %v, err=%v; want false", authorized, err)
	}
	if err := store.RecordAgentHeartbeat(ctx, "large-node", time.Now(), "online", "test"); !errors.Is(err, errNodeNotFound) {
		t.Fatalf("heartbeat after tombstone error = %v, want node not found", err)
	}
	if targets, err := store.EnabledProbeTargets(ctx, "large-node"); err != nil || len(targets) != 0 {
		t.Fatalf("probe config after tombstone = %+v, err=%v; want empty", targets, err)
	}

	store.startAdminDeletionWorker()
	waitForAdminDeletionCompleted(t, store, "node", "large-node", 10*time.Second)
	for name, query := range map[string]string{
		"node":          `SELECT COUNT(*) FROM nodes WHERE id = 'large-node'`,
		"state samples": `SELECT COUNT(*) FROM state_samples WHERE node_id = 'large-node'`,
		"probe rounds":  `SELECT COUNT(*) FROM probe_rounds WHERE node_id = 'large-node'`,
		"probe samples": `SELECT COUNT(*) FROM probe_samples ps JOIN probe_rounds pr ON pr.id = ps.round_id WHERE pr.node_id = 'large-node'`,
	} {
		t.Run(name, func(t *testing.T) { assertAdminDeleteCount(t, store, query, 0) })
	}
	assertSQLiteForeignKeysClean(t, store)
}

func TestAdminProbeTargetDeleteImmediatelyHidesAndRejectsNewRounds(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	store.stopAdminDeletionWorker()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	rowCount := adminDeleteBatchSize*2 + 19
	seedAdminDeleteHistory(t, store, "hytron", "hytron-local", rowCount, 2)

	if err := store.DeleteAdminProbeTarget(ctx, "hytron-local"); err != nil {
		t.Fatalf("delete target: %v", err)
	}
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM probe_targets WHERE id = 'hytron-local'`, 1)
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM probe_rounds WHERE target_id = 'hytron-local'`, rowCount)
	assertAdminProbeTargetAbsent(t, store, "hytron-local")
	if targets, err := store.EnabledProbeTargets(ctx, "hytron"); err != nil {
		t.Fatalf("enabled targets after tombstone: %v", err)
	} else {
		for _, target := range targets {
			if target.ID == "hytron-local" {
				t.Fatalf("tombstoned target remains in Agent config: %+v", target)
			}
		}
	}
	latency := 1.0
	err = store.InsertProbeRound(ctx, "hytron", ProbeTarget{ID: "hytron-local", Name: "deleted", Type: "tcping", Address: "127.0.0.1", Port: intPtrValue(80), Count: 1, TimeoutMS: 1000, IntervalSec: 30}, time.Now(), []probe.Sample{{Seq: 1, Success: true, LatencyMS: &latency}})
	if !errors.Is(err, errInvalidAgentProbeResults) {
		t.Fatalf("new round for tombstoned target error = %v, want invalid probe results", err)
	}

	store.startAdminDeletionWorker()
	waitForAdminDeletionCompleted(t, store, "probe_target", "hytron-local", 10*time.Second)
	for _, query := range []string{
		`SELECT COUNT(*) FROM probe_targets WHERE id = 'hytron-local'`,
		`SELECT COUNT(*) FROM node_probe_targets WHERE target_id = 'hytron-local'`,
		`SELECT COUNT(*) FROM probe_rounds WHERE target_id = 'hytron-local'`,
	} {
		assertAdminDeleteCount(t, store, query, 0)
	}
	assertSQLiteForeignKeysClean(t, store)
}

func TestAdminDeletionWorkerResumesRunningTargetJobAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	store.stopAdminDeletionWorker()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	seedAdminDeleteHistory(t, store, "hytron", "hytron-local", adminDeleteBatchSize+31, 2)
	if err := store.DeleteAdminProbeTarget(ctx, "hytron-local"); err != nil {
		t.Fatalf("enqueue target deletion: %v", err)
	}
	processed, err := store.processNextAdminDeletionBatch(ctx)
	if err != nil || !processed {
		t.Fatalf("process first batch = %v, err=%v", processed, err)
	}
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM admin_deletion_jobs WHERE entity_kind = 'probe_target' AND entity_id = 'hytron-local' AND state = 'running'`, 1)
	if err := store.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	reopened, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer reopened.Close()
	waitForAdminDeletionCompleted(t, reopened, "probe_target", "hytron-local", 10*time.Second)
	assertAdminDeleteCount(t, reopened, `SELECT COUNT(*) FROM probe_targets WHERE id = 'hytron-local'`, 0)
	assertAdminDeleteCount(t, reopened, `SELECT COUNT(*) FROM probe_rounds WHERE target_id = 'hytron-local'`, 0)
	assertSQLiteForeignKeysClean(t, reopened)
}

func TestAdminDeletionFailureAndCancellationReturnJobToRetryQueue(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	store.stopAdminDeletionWorker()
	ctx := context.Background()
	if _, err := store.CreateAdminNode(ctx, AdminNodeCreateRequest{ID: "retry-node", DisplayName: "Retry node", CountryCode: "US"}); err != nil {
		t.Fatalf("create retry node: %v", err)
	}
	for index := 0; index < 10; index++ {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO state_samples (node_id, ts) VALUES ('retry-node', ?)`, time.Now().Unix()+int64(index)); err != nil {
			t.Fatalf("seed retry state: %v", err)
		}
	}
	if err := store.DeleteAdminNode(ctx, "retry-node"); err != nil {
		t.Fatalf("enqueue node deletion: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		CREATE TRIGGER fail_admin_state_delete
		BEFORE DELETE ON state_samples
		WHEN OLD.node_id = 'retry-node'
		BEGIN SELECT RAISE(ABORT, 'injected deletion failure'); END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	if _, err := store.processNextAdminDeletionBatch(ctx); err == nil {
		t.Fatal("deletion batch unexpectedly succeeded with failure trigger")
	}
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM admin_deletion_jobs WHERE entity_kind = 'node' AND entity_id = 'retry-node' AND state = 'pending' AND last_error <> ''`, 1)
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER fail_admin_state_delete`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}

	job := adminDeletionJob{kind: "node", id: "retry-node"}
	if err := store.markAdminDeletionJobRunning(ctx, job); err != nil {
		t.Fatalf("mark retry job running: %v", err)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.processAdminNodeDeletionBatch(canceledCtx, "retry-node"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled batch error = %v, want context canceled", err)
	} else {
		store.recordAdminDeletionError(job, err)
	}
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM admin_deletion_jobs WHERE entity_kind = 'node' AND entity_id = 'retry-node' AND state = 'pending' AND last_error <> ''`, 1)

	drainAdminDeletions(t, store, 10*time.Second)
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM nodes WHERE id = 'retry-node'`, 0)
	assertAdminDeleteCount(t, store, `SELECT COUNT(*) FROM state_samples WHERE node_id = 'retry-node'`, 0)
	assertSQLiteForeignKeysClean(t, store)
}

func seedAdminDeleteHistory(t *testing.T, store *SQLiteStore, nodeID, targetID string, rounds, samplesPerRound int) {
	t.Helper()
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin history seed: %v", err)
	}
	defer rollbackUnlessCommitted(tx)

	stateStmt, err := tx.PrepareContext(ctx, `INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES (?, ?, 12.5)`)
	if err != nil {
		t.Fatalf("prepare state seed: %v", err)
	}
	defer stateStmt.Close()
	roundStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent)
		VALUES (?, ?, ?, 'tcping', ?, ?, 0)
	`)
	if err != nil {
		t.Fatalf("prepare round seed: %v", err)
	}
	defer roundStmt.Close()
	sampleStmt, err := tx.PrepareContext(ctx, `INSERT INTO probe_samples (round_id, seq, success, latency_ms) VALUES (?, ?, 1, ?)`)
	if err != nil {
		t.Fatalf("prepare sample seed: %v", err)
	}
	defer sampleStmt.Close()

	baseTS := time.Now().UTC().Unix() - int64(rounds)
	for index := 0; index < rounds; index++ {
		if _, err := stateStmt.ExecContext(ctx, nodeID, baseTS+int64(index)); err != nil {
			t.Fatalf("seed state %d: %v", index, err)
		}
		result, err := roundStmt.ExecContext(ctx, nodeID, targetID, baseTS+int64(index), samplesPerRound, samplesPerRound)
		if err != nil {
			t.Fatalf("seed round %d: %v", index, err)
		}
		roundID, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("round %d id: %v", index, err)
		}
		for seq := 1; seq <= samplesPerRound; seq++ {
			if _, err := sampleStmt.ExecContext(ctx, roundID, seq, float64(seq)); err != nil {
				t.Fatalf("seed round %d sample %d: %v", index, seq, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit history seed: %v", err)
	}
	tx = nil
}

func waitForAdminDeletionCompleted(t *testing.T, store *SQLiteStore, kind, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var state string
		err := store.db.QueryRowContext(context.Background(), `
			SELECT state FROM admin_deletion_jobs WHERE entity_kind = ? AND entity_id = ?
		`, kind, id).Scan(&state)
		if err == nil && state == "completed" {
			return
		}
		if err != nil {
			t.Fatalf("query deletion job %s/%s: %v", kind, id, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var state, lastError string
	_ = store.db.QueryRowContext(context.Background(), `
		SELECT state, last_error FROM admin_deletion_jobs WHERE entity_kind = ? AND entity_id = ?
	`, kind, id).Scan(&state, &lastError)
	t.Fatalf("deletion job %s/%s did not complete: state=%q error=%q", kind, id, state, lastError)
}

func drainAdminDeletions(t *testing.T, store *SQLiteStore, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		processed, err := store.processNextAdminDeletionBatch(context.Background())
		if err != nil {
			t.Fatalf("process deletion batch: %v", err)
		}
		if !processed {
			return
		}
	}
	t.Fatal("timed out draining admin deletion jobs")
}

func assertAdminNodeAbsent(t *testing.T, store *SQLiteStore, nodeID string) {
	t.Helper()
	nodes, err := store.AdminNodes(context.Background())
	if err != nil {
		t.Fatalf("admin nodes: %v", err)
	}
	for _, node := range nodes {
		if node.ID == nodeID {
			t.Fatalf("tombstoned node remains visible: %+v", node)
		}
	}
}

func assertAdminProbeTargetAbsent(t *testing.T, store *SQLiteStore, targetID string) {
	t.Helper()
	targets, err := store.AdminProbeTargets(context.Background())
	if err != nil {
		t.Fatalf("admin targets: %v", err)
	}
	for _, target := range targets {
		if target.ID == targetID {
			t.Fatalf("tombstoned target remains visible: %+v", target)
		}
	}
}

func assertAdminDeleteCount(t *testing.T, store *SQLiteStore, query string, want int) {
	t.Helper()
	var got int
	if err := store.db.QueryRowContext(context.Background(), query).Scan(&got); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("count query %q = %d, want %d", query, got, want)
	}
}

func assertSQLiteForeignKeysClean(t *testing.T, store *SQLiteStore) {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(), `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign key check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID any
		var parent string
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			t.Fatalf("scan foreign key violation: %v", err)
		}
		t.Fatalf("foreign key violation: %s", fmt.Sprint([]any{table, rowID, parent, foreignKeyID}))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign key check rows: %v", err)
	}
}

func intPtrValue(value int) *int { return &value }
