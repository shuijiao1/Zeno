package api

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestProbeIdempotencyMigrationRepairsDuplicateLegacyAgentRoundIDs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "duplicate-agent-rounds.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE probe_rounds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			ts INTEGER NOT NULL,
			type TEXT NOT NULL,
			idempotency_key TEXT NOT NULL DEFAULT '',
			sent INTEGER NOT NULL,
			received INTEGER NOT NULL,
			loss_percent REAL NOT NULL,
			min_ms REAL, avg_ms REAL, median_ms REAL, max_ms REAL, stddev_ms REAL,
			error TEXT
		);
		CREATE TABLE probe_samples (
			round_id INTEGER NOT NULL REFERENCES probe_rounds(id) ON DELETE CASCADE,
			seq INTEGER NOT NULL,
			success INTEGER NOT NULL,
			latency_ms REAL,
			error TEXT,
			PRIMARY KEY (round_id, seq)
		);
		CREATE UNIQUE INDEX idx_probe_rounds_idempotency
		ON probe_rounds(node_id, target_id, ts, type);
		INSERT INTO probe_rounds (
			node_id, target_id, ts, type, idempotency_key,
			sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms
		) VALUES
			('legacy-node', 'target-a', 1700000000, 'ping', 'agent:duplicate-round', 1, 1, 0, 1, 1, 1, 1, 0),
			('legacy-node', 'target-b', 1700000001, 'ping', 'agent:duplicate-round', 1, 1, 0, 2, 2, 2, 2, 0),
			('legacy-node', 'target-c', 1700000002, 'ping', 'agent:', 1, 1, 0, 3, 3, 3, 3, 0);
		INSERT INTO probe_samples (round_id, seq, success, latency_ms) VALUES
			(1, 1, 1, 1), (2, 1, 1, 2), (3, 1, 1, 3);
	`); err != nil {
		_ = db.Close()
		t.Fatalf("create duplicate legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open and repair duplicate legacy Agent ids: %v", err)
	}
	ctx := context.Background()
	var canonical int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM probe_rounds
		WHERE node_id = 'legacy-node' AND agent_round_id = 'duplicate-round'
	`).Scan(&canonical); err != nil {
		_ = store.Close()
		t.Fatalf("count canonical Agent ids: %v", err)
	}
	if canonical != 1 {
		_ = store.Close()
		t.Fatalf("canonical duplicate Agent ids=%d, want 1", canonical)
	}
	var demoted int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM probe_rounds
		WHERE agent_round_id IS NULL AND idempotency_key GLOB 'legacy:*'
	`).Scan(&demoted); err != nil {
		_ = store.Close()
		t.Fatalf("count demoted legacy rows: %v", err)
	}
	if demoted != 2 {
		_ = store.Close()
		t.Fatalf("demoted rows=%d, want duplicate and malformed Agent ids", demoted)
	}
	unique, err := sqliteIndexUnique(ctx, store.db, "idx_probe_rounds_agent_id")
	if err != nil {
		_ = store.Close()
		t.Fatalf("inspect Agent id index: %v", err)
	}
	if !unique {
		_ = store.Close()
		t.Fatal("Agent id index is not unique after repair")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close repaired store: %v", err)
	}

	// The repair is durable and restarting cannot promote a demoted duplicate.
	reopened, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen repaired store: %v", err)
	}
	defer reopened.Close()
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds WHERE agent_round_id = 'duplicate-round'`).Scan(&canonical); err != nil {
		t.Fatalf("count canonical Agent id after reopen: %v", err)
	}
	if canonical != 1 {
		t.Fatalf("canonical Agent ids after reopen=%d, want 1", canonical)
	}
}

func TestProbeIdempotencyMigrationBatchesLargeLegacySchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE probe_rounds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			ts INTEGER NOT NULL,
			type TEXT NOT NULL,
			idempotency_key TEXT NOT NULL DEFAULT '',
			sent INTEGER NOT NULL,
			received INTEGER NOT NULL,
			loss_percent REAL NOT NULL,
			min_ms REAL, avg_ms REAL, median_ms REAL, max_ms REAL, stddev_ms REAL,
			error TEXT
		);
		CREATE TABLE probe_samples (
			round_id INTEGER NOT NULL REFERENCES probe_rounds(id) ON DELETE CASCADE,
			seq INTEGER NOT NULL,
			success INTEGER NOT NULL,
			latency_ms REAL,
			error TEXT,
			PRIMARY KEY (round_id, seq)
		);
		CREATE UNIQUE INDEX idx_probe_rounds_idempotency
		ON probe_rounds(node_id, target_id, ts, type);
	`); err != nil {
		_ = db.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		_ = db.Close()
		t.Fatalf("begin legacy seed: %v", err)
	}
	const roundCount = probeRoundIdempotencyMigrationBatchSize*2 + 37
	for index := 0; index < roundCount; index++ {
		idempotencyKey := ""
		if index%113 == 0 {
			idempotencyKey = fmt.Sprintf("agent:round-%d", index)
		}
		result, err := tx.Exec(`
			INSERT INTO probe_rounds (
				node_id, target_id, ts, type, idempotency_key,
				sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms
			) VALUES ('legacy-node', 'legacy-target', ?, 'ping', ?, 2, 1, 50, 1, 1, 1, 1, 0)
		`, 1_700_000_000+index, idempotencyKey)
		if err != nil {
			_ = tx.Rollback()
			_ = db.Close()
			t.Fatalf("insert round %d: %v", index, err)
		}
		roundID, err := result.LastInsertId()
		if err != nil {
			_ = tx.Rollback()
			_ = db.Close()
			t.Fatalf("round id %d: %v", index, err)
		}
		if _, err := tx.Exec(`
			INSERT INTO probe_samples (round_id, seq, success, latency_ms, error)
			VALUES (?, 1, 1, 1.25, NULL), (?, 2, 0, NULL, 'timeout')
		`, roundID, roundID); err != nil {
			_ = tx.Rollback()
			_ = db.Close()
			t.Fatalf("insert samples %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		_ = db.Close()
		t.Fatalf("commit legacy seed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open and migrate legacy db: %v", err)
	}
	ctx := context.Background()
	var incomplete int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM probe_rounds
		WHERE idempotency_key = '' OR payload_hash = ''
		   OR (idempotency_key GLOB 'agent:*' AND (agent_round_id IS NULL OR agent_round_id = ''))
	`).Scan(&incomplete); err != nil {
		_ = store.Close()
		t.Fatalf("count incomplete rows: %v", err)
	}
	if incomplete != 0 {
		_ = store.Close()
		t.Fatalf("incomplete migrated rounds=%d", incomplete)
	}
	var migrated int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds WHERE idempotency_key GLOB 'legacy:*'`).Scan(&migrated); err != nil {
		_ = store.Close()
		t.Fatalf("count legacy keys: %v", err)
	}
	agentRoundCount := (roundCount-1)/113 + 1
	if migrated != roundCount-agentRoundCount {
		_ = store.Close()
		t.Fatalf("legacy keys=%d, want %d", migrated, roundCount-agentRoundCount)
	}
	columns, err := sqliteIndexColumns(ctx, store.db, "idx_probe_rounds_idempotency")
	if err != nil {
		_ = store.Close()
		t.Fatalf("read migrated index: %v", err)
	}
	if !stringSlicesEqual(columns, []string{"node_id", "target_id", "ts", "type", "idempotency_key"}) {
		_ = store.Close()
		t.Fatalf("migrated index columns=%v", columns)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}

	// Every batch is durable and the migration is idempotent on restart.
	reopened, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen migrated db: %v", err)
	}
	defer reopened.Close()
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&migrated); err != nil {
		t.Fatalf("count after reopen: %v", err)
	}
	if migrated != roundCount {
		t.Fatalf("round count after reopen=%d, want %d", migrated, roundCount)
	}
}
