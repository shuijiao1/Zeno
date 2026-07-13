package api

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestBillingPeriodResetDayClampsAcrossMonthLengths(t *testing.T) {
	tests := []struct {
		name      string
		ts        time.Time
		resetDay  int
		wantKey   string
		wantStart string
		wantEnd   string
	}{
		{
			name:      "reset 29 before short February clamp",
			ts:        time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC),
			resetDay:  29,
			wantKey:   "2026-01",
			wantStart: "2026-01-29",
			wantEnd:   "2026-02-27",
		},
		{
			name:      "reset 29 on short February clamp",
			ts:        time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC),
			resetDay:  29,
			wantKey:   "2026-02",
			wantStart: "2026-02-28",
			wantEnd:   "2026-03-28",
		},
		{
			name:      "reset 29 leap day",
			ts:        time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),
			resetDay:  29,
			wantKey:   "2024-02",
			wantStart: "2024-02-29",
			wantEnd:   "2024-03-28",
		},
		{
			name:      "reset 30 short February clamp",
			ts:        time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC),
			resetDay:  30,
			wantKey:   "2026-02",
			wantStart: "2026-02-28",
			wantEnd:   "2026-03-29",
		},
		{
			name:      "reset 31 short February clamp",
			ts:        time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC),
			resetDay:  31,
			wantKey:   "2026-02",
			wantStart: "2026-02-28",
			wantEnd:   "2026-03-30",
		},
		{
			name:      "reset 31 thirty day month clamp",
			ts:        time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
			resetDay:  31,
			wantKey:   "2026-04",
			wantStart: "2026-04-30",
			wantEnd:   "2026-05-30",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			period := billingPeriodFor(tt.ts, tt.resetDay)
			if period.Key != tt.wantKey || period.StartDate != tt.wantStart || period.EndDate != tt.wantEnd {
				t.Fatalf("period = %+v, want key=%s start=%s end=%s", period, tt.wantKey, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestBillingPeriodSQLiteClampExpressionMatchesGo(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	tests := []struct {
		day      string
		resetDay int
	}{
		{day: "2026-02-27", resetDay: 29},
		{day: "2026-02-28", resetDay: 29},
		{day: "2024-02-29", resetDay: 29},
		{day: "2026-02-28", resetDay: 30},
		{day: "2026-02-28", resetDay: 31},
		{day: "2026-04-30", resetDay: 31},
		{day: "2026-03-30", resetDay: 31},
		{day: "2026-03-31", resetDay: 31},
		{day: "2026-07-01", resetDay: 0},
		{day: "2026-07-01", resetDay: 32},
	}
	for _, tt := range tests {
		var got string
		if err := db.QueryRow(`
			SELECT CASE
			  WHEN CAST(strftime('%d', ?) AS INTEGER) < CASE
			    WHEN (CASE WHEN COALESCE(?, 1) BETWEEN 1 AND 31 THEN ? ELSE 1 END) > CAST(strftime('%d', date(?, 'start of month', '+1 month', '-1 day')) AS INTEGER)
			    THEN CAST(strftime('%d', date(?, 'start of month', '+1 month', '-1 day')) AS INTEGER)
			    ELSE (CASE WHEN COALESCE(?, 1) BETWEEN 1 AND 31 THEN ? ELSE 1 END)
			  END THEN strftime('%Y-%m', date(?, 'start of month', '-1 day'))
			  ELSE strftime('%Y-%m', ?)
			END
		`, tt.day, tt.resetDay, tt.resetDay, tt.day, tt.day, tt.resetDay, tt.resetDay, tt.day, tt.day).Scan(&got); err != nil {
			t.Fatalf("sqlite period key for %s/%d: %v", tt.day, tt.resetDay, err)
		}
		parsed, err := time.ParseInLocation("2006-01-02", tt.day, time.UTC)
		if err != nil {
			t.Fatalf("parse day: %v", err)
		}
		if want := billingPeriodKey(parsed, tt.resetDay); got != want {
			t.Fatalf("sqlite key for %s reset %d = %s, want Go key %s", tt.day, tt.resetDay, got, want)
		}
	}
}

func TestBillingTrafficEpochRebasesOnModeAndResetDayChange(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	state := AgentStateRequest{TS: now.Unix(), CPUPercent: 1, MemoryUsedBytes: 1, MemoryTotalBytes: 2, DiskUsedBytes: 1, DiskTotalBytes: 2, NetInSpeedBps: 1, NetOutSpeedBps: 1, UptimeSeconds: 1}
	state.NetInTotalBytes, state.NetOutTotalBytes = 100, 50
	if err := store.InsertAgentState(ctx, "hytron", state); err != nil {
		t.Fatalf("insert baseline state: %v", err)
	}
	state.TS = now.Add(time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 200, 100
	if err := store.InsertAgentState(ctx, "hytron", state); err != nil {
		t.Fatalf("insert epoch 0 delta: %v", err)
	}

	modeOut := "out"
	if _, err := store.UpdateAdminNode(ctx, "hytron", AdminNodeUpdateRequest{BillingMode: &modeOut}); err != nil {
		t.Fatalf("update billing mode: %v", err)
	}
	state.TS = now.Add(2 * time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 500, 400
	if err := store.InsertAgentState(ctx, "hytron", state); err != nil {
		t.Fatalf("insert epoch 1 baseline: %v", err)
	}
	state.TS = now.Add(3 * time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 800, 900
	if err := store.InsertAgentState(ctx, "hytron", state); err != nil {
		t.Fatalf("insert epoch 1 delta: %v", err)
	}

	resetDay := 15
	if _, err := store.UpdateAdminNode(ctx, "hytron", AdminNodeUpdateRequest{MonthlyResetDay: &resetDay}); err != nil {
		t.Fatalf("update monthly reset day: %v", err)
	}
	state.TS = now.Add(4 * time.Second).Unix()
	state.NetInTotalBytes, state.NetOutTotalBytes = 1000, 1200
	if err := store.InsertAgentState(ctx, "hytron", state); err != nil {
		t.Fatalf("insert epoch 2 baseline: %v", err)
	}

	wantBillable := map[int64]int64{0: 150, 1: 500, 2: 0}
	wantMode := map[int64]string{0: "both", 1: "out", 2: "out"}
	rows, err := store.db.QueryContext(ctx, `
		SELECT billing_epoch, billing_mode, billable_bytes
		FROM traffic_monthly
		WHERE node_id = 'hytron'
		ORDER BY billing_epoch
	`)
	if err != nil {
		t.Fatalf("query traffic epochs: %v", err)
	}
	defer rows.Close()
	seen := map[int64]bool{}
	for rows.Next() {
		var epoch, billable int64
		var mode string
		if err := rows.Scan(&epoch, &mode, &billable); err != nil {
			t.Fatalf("scan epoch row: %v", err)
		}
		seen[epoch] = true
		if billable != wantBillable[epoch] || mode != wantMode[epoch] {
			t.Fatalf("epoch %d row mode=%s billable=%d, want mode=%s billable=%d", epoch, mode, billable, wantMode[epoch], wantBillable[epoch])
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate epoch rows: %v", err)
	}
	for epoch := range wantBillable {
		if !seen[epoch] {
			t.Fatalf("missing traffic epoch %d", epoch)
		}
	}

	var currentEpoch int64
	if err := store.db.QueryRowContext(ctx, `SELECT billing_traffic_epoch FROM nodes WHERE id = 'hytron'`).Scan(&currentEpoch); err != nil {
		t.Fatalf("read current epoch: %v", err)
	}
	if currentEpoch != 2 {
		t.Fatalf("current billing epoch = %d, want 2", currentEpoch)
	}
	nodes, err := store.nodes(ctx)
	if err != nil {
		t.Fatalf("read public nodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].MonthlyBillableBytes == nil || *nodes[0].MonthlyBillableBytes != 0 {
		t.Fatalf("current public monthly billable = %+v, want current epoch baseline 0", nodes)
	}
}

func TestTrafficMonthlyEpochMigrationPreservesLegacyRows(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "zeno.db")
	db, err := sql.Open("sqlite", sqliteURLForTest(t, databasePath))
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'no_data',
			country_code TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE traffic_monthly (
			node_id TEXT NOT NULL REFERENCES nodes(id),
			month TEXT NOT NULL,
			in_bytes INTEGER NOT NULL DEFAULT 0,
			out_bytes INTEGER NOT NULL DEFAULT 0,
			billable_bytes INTEGER NOT NULL DEFAULT 0,
			last_in_total_bytes INTEGER,
			last_out_total_bytes INTEGER,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (node_id, month)
		);
	`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at) VALUES ('hytron', 'Hytron', 'hash', 'online', 'HK', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert legacy node: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO traffic_monthly (node_id, month, in_bytes, out_bytes, billable_bytes, last_in_total_bytes, last_out_total_bytes, updated_at) VALUES ('hytron', '2026-06', 10, 20, 30, 100, 200, ?)`, now); err != nil {
		t.Fatalf("insert legacy traffic: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := OpenSQLiteStore(databasePath)
	if err != nil {
		t.Fatalf("open migrated sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	pkIncludesEpoch, err := store.primaryKeyIncludes(ctx, "traffic_monthly", "billing_epoch")
	if err != nil {
		t.Fatalf("inspect migrated primary key: %v", err)
	}
	if !pkIncludesEpoch {
		t.Fatal("traffic_monthly primary key does not include billing_epoch after migration")
	}
	var epoch, resetDay, inBytes, outBytes, billable int64
	var mode string
	if err := store.db.QueryRowContext(ctx, `
		SELECT billing_epoch, reset_day, billing_mode, in_bytes, out_bytes, billable_bytes
		FROM traffic_monthly
		WHERE node_id = 'hytron' AND month = '2026-06'
	`).Scan(&epoch, &resetDay, &mode, &inBytes, &outBytes, &billable); err != nil {
		t.Fatalf("read migrated legacy row: %v", err)
	}
	if epoch != 0 || resetDay != 1 || mode != "both" || inBytes != 10 || outBytes != 20 || billable != 30 {
		t.Fatalf("migrated row = epoch %d reset %d mode %s in %d out %d billable %d", epoch, resetDay, mode, inBytes, outBytes, billable)
	}
}

func sqliteURLForTest(t *testing.T, path string) string {
	t.Helper()
	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatalf("build sqlite dsn: %v", err)
	}
	return dsn
}
