package api

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneRawHistoryDeletesInBatchesAndKeepsRecentRows(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	oldTS := time.Now().UTC().Add(-rawHistoryRetention - time.Hour).Unix()
	recentTS := time.Now().UTC().Unix()
	for i := 0; i < historyRetentionBatchSize+17; i++ {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES ('hytron', ?, 1)`, oldTS-int64(i)); err != nil {
			t.Fatalf("insert old state %d: %v", i, err)
		}
		if _, err := store.db.ExecContext(ctx, `INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent) VALUES ('hytron', 'google-dns', ?, 'ping', 1, 1, 0)`, oldTS-int64(i)); err != nil {
			t.Fatalf("insert old probe round %d: %v", i, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO state_samples (node_id, ts, cpu_percent) VALUES ('hytron', ?, 2)`, recentTS); err != nil {
		t.Fatalf("insert recent state: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent) VALUES ('hytron', 'google-dns', ?, 'ping', 1, 1, 0)`, recentTS); err != nil {
		t.Fatalf("insert recent probe round: %v", err)
	}
	for i := 0; i < historyRetentionBatchSize+3; i++ {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO notification_deliveries (event_type, channel_id, state, next_attempt_at, created_at, updated_at)
			VALUES ('node_offline', ?, 'delivered', 0, ?, ?)
		`, fmt.Sprintf("channel-%d", i), oldTS, oldTS); err != nil {
			t.Fatalf("insert old notification %d: %v", i, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO notification_deliveries (event_type, channel_id, state, next_attempt_at, created_at, updated_at)
		VALUES ('node_offline', 'recent-channel', 'delivered', 0, ?, ?)
	`, recentTS, recentTS); err != nil {
		t.Fatalf("insert recent notification: %v", err)
	}

	if err := store.PruneRawHistory(ctx, time.Now().UTC().Add(-rawHistoryRetention)); err != nil {
		t.Fatalf("prune raw history: %v", err)
	}
	for _, check := range []struct {
		name  string
		query string
	}{
		{name: "old state", query: `SELECT COUNT(*) FROM state_samples WHERE ts < ?`},
		{name: "old probe", query: `SELECT COUNT(*) FROM probe_rounds WHERE ts < ?`},
		{name: "old delivered notifications", query: `SELECT COUNT(*) FROM notification_deliveries WHERE state = 'delivered' AND updated_at < ?`},
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, check.query, time.Now().UTC().Add(-rawHistoryRetention).Unix()).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("%s remaining = %d, want 0", check.name, count)
		}
	}
	for _, check := range []struct {
		name  string
		query string
	}{
		{name: "recent state", query: `SELECT COUNT(*) FROM state_samples WHERE ts = ?`},
		{name: "recent probe", query: `SELECT COUNT(*) FROM probe_rounds WHERE ts = ?`},
		{name: "recent notification", query: `SELECT COUNT(*) FROM notification_deliveries WHERE channel_id = 'recent-channel' AND updated_at = ?`},
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, check.query, recentTS).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", check.name, count)
		}
	}
}
