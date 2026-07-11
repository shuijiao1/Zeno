package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenSQLiteStoreAllowsWALReadsWhileWriterTransactionIsOpen(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	if got := store.db.Stats().MaxOpenConnections; got < 2 {
		t.Fatalf("max open connections = %d, want concurrent WAL readers", got)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin writer transaction: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		t.Fatalf("acquire writer transaction: %v", err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if _, err := store.NodeLatency(readCtx, "hytron", latencyWindow{Name: "1h", Samples: 20, Step: 3 * time.Minute}); err != nil {
		t.Fatalf("history read blocked behind writer transaction: %v", err)
	}

	for index := 0; index < 3; index++ {
		conn, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatalf("open pooled connection %d: %v", index, err)
		}
		var foreignKeys int
		if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			conn.Close()
			t.Fatalf("read foreign_keys on connection %d: %v", index, err)
		}
		conn.Close()
		if foreignKeys != 1 {
			t.Fatalf("foreign_keys on pooled connection %d = %d, want 1", index, foreignKeys)
		}
	}
}
