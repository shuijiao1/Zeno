package api

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
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
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
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
	if _, err := store.NodeLatency(readCtx, "example-node-a", latencyWindow{Name: "1h", Samples: 20, Step: 3 * time.Minute}); err != nil {
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

func TestRecordAgentHeartbeatRetriesAfterSQLiteBusyTimeout(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	store.db.SetMaxOpenConns(2)
	store.db.SetMaxIdleConns(2)

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	blockingConn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open blocking connection: %v", err)
	}
	if _, err := blockingConn.ExecContext(ctx, `PRAGMA busy_timeout = 50`); err != nil {
		blockingConn.Close()
		t.Fatalf("set blocking busy_timeout: %v", err)
	}

	heartbeatConn, err := store.db.Conn(ctx)
	if err != nil {
		blockingConn.Close()
		t.Fatalf("open heartbeat connection: %v", err)
	}
	if _, err := heartbeatConn.ExecContext(ctx, `PRAGMA busy_timeout = 50`); err != nil {
		heartbeatConn.Close()
		blockingConn.Close()
		t.Fatalf("set heartbeat busy_timeout: %v", err)
	}
	if err := heartbeatConn.Close(); err != nil {
		blockingConn.Close()
		t.Fatalf("close heartbeat connection: %v", err)
	}

	blockingTx, err := blockingConn.BeginTx(ctx, nil)
	if err != nil {
		blockingConn.Close()
		t.Fatalf("begin blocking transaction: %v", err)
	}
	if _, err := blockingTx.ExecContext(ctx, `UPDATE nodes SET updated_at = updated_at WHERE id = 'example-node-a'`); err != nil {
		_ = blockingTx.Rollback()
		blockingConn.Close()
		t.Fatalf("reserve blocking writer: %v", err)
	}

	release := make(chan struct{})
	released := make(chan error, 1)
	go func() {
		<-release
		err := blockingTx.Commit()
		if closeErr := blockingConn.Close(); err == nil {
			err = closeErr
		}
		released <- err
	}()
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	timer := time.AfterFunc(150*time.Millisecond, releaseWriter)
	defer func() {
		if !timer.Stop() {
			releaseWriter()
		}
	}()

	result := make(chan error, 1)
	go func() {
		_, err := store.RecordAgentHeartbeatTransition(ctx, "example-node-a", time.Now().UTC(), "online", "agent-test")
		result <- err
	}()

	select {
	case err := <-result:
		releaseWriter()
		if releaseErr := <-released; releaseErr != nil {
			t.Fatalf("release blocking writer: %v", releaseErr)
		}
		if err != nil {
			t.Fatalf("heartbeat should retry after SQLITE_BUSY timeout: %v", err)
		}
	case <-time.After(2 * time.Second):
		releaseWriter()
		if releaseErr := <-released; releaseErr != nil {
			t.Fatalf("release blocking writer: %v", releaseErr)
		}
		t.Fatal("heartbeat did not resume after blocking writer committed")
	}
}

func TestAgentHighFrequencyWritersSerializeAndRetrySQLiteBusy(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	const pooledConnections = 12
	store.db.SetMaxOpenConns(pooledConnections)
	store.db.SetMaxIdleConns(pooledConnections)
	setSQLiteBusyTimeoutForPool(t, store, pooledConnections, 50)

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	probeConfigVersion, err := store.ProbeConfigVersion(ctx)
	if err != nil {
		t.Fatalf("read probe config version: %v", err)
	}

	blockingConn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open blocking connection: %v", err)
	}
	blockingTx, err := blockingConn.BeginTx(ctx, nil)
	if err != nil {
		blockingConn.Close()
		t.Fatalf("begin blocking transaction: %v", err)
	}
	if _, err := blockingTx.ExecContext(ctx, `UPDATE nodes SET updated_at = updated_at WHERE id = 'example-node-a'`); err != nil {
		_ = blockingTx.Rollback()
		blockingConn.Close()
		t.Fatalf("reserve blocking writer: %v", err)
	}

	release := make(chan struct{})
	released := make(chan error, 1)
	go func() {
		<-release
		err := blockingTx.Commit()
		if closeErr := blockingConn.Close(); err == nil {
			err = closeErr
		}
		released <- err
	}()
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	timer := time.AfterFunc(150*time.Millisecond, releaseWriter)
	defer func() {
		if !timer.Stop() {
			releaseWriter()
		}
	}()

	baseTS := time.Now().UTC().Unix()
	writers := []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "heartbeat-online", run: func(ctx context.Context) error {
			_, err := store.RecordAgentHeartbeatTransition(ctx, "example-node-a", time.Unix(baseTS+1, 0).UTC(), "online", "agent-test")
			return err
		}},
		{name: "heartbeat-warning", run: func(ctx context.Context) error {
			_, err := store.RecordAgentHeartbeatTransition(ctx, "example-node-a", time.Unix(baseTS+2, 0).UTC(), "warning", "agent-test")
			return err
		}},
		{name: "host", run: func(ctx context.Context) error {
			return store.UpsertAgentHost(ctx, "example-node-a", AgentHostRequest{Hostname: "example-node-a", OSName: "Linux", Arch: "amd64", AgentVersion: "agent-test", CPUCores: 2, MemoryTotalBytes: 1024, DiskTotalBytes: 4096})
		}},
		{name: "state-report", run: func(ctx context.Context) error {
			_, _, err := store.RecordAgentStateReport(ctx, "example-node-a", sqliteConcurrencyAgentState(baseTS+3, "busy-state-report"))
			return err
		}},
		{name: "state-insert", run: func(ctx context.Context) error {
			return store.InsertAgentState(ctx, "example-node-a", sqliteConcurrencyAgentState(baseTS+4, "busy-state-insert"))
		}},
		{name: "state-alert", run: func(ctx context.Context) error {
			_, err := store.RecordAgentStateAlertRuleTransition(ctx, "example-node-a", time.Unix(baseTS+5, 0).UTC(), sqliteConcurrencyAgentState(baseTS+5, "busy-state-alert"))
			return err
		}},
		{name: "presence-online", run: func(ctx context.Context) error {
			_, err := store.RecordAgentPresenceOnlineTransition(ctx, "example-node-a", time.Unix(baseTS+6, 0).UTC())
			return err
		}},
		{name: "presence-offline", run: func(ctx context.Context) error {
			_, err := store.RecordAgentPresenceOfflineTransition(ctx, "example-node-a", time.Unix(baseTS+7, 0).UTC())
			return err
		}},
		{name: "stale-offline", run: func(ctx context.Context) error {
			_, _, err := store.RecordStaleAgentOfflineTransition(ctx, "example-node-a", time.Unix(baseTS+7200, 0).UTC())
			return err
		}},
		{name: "presence-config-applied", run: func(ctx context.Context) error {
			return store.RecordProbeConfigApplied(ctx, "example-node-a", probeConfigVersion, time.Unix(baseTS+8, 0).UTC())
		}},
	}

	start := make(chan struct{})
	errs := make(chan error, len(writers))
	var wg sync.WaitGroup
	for _, writer := range writers {
		writer := writer
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := writer.run(writeCtx); err != nil {
				errs <- fmt.Errorf("%s: %w", writer.name, err)
			}
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		releaseWriter()
		if releaseErr := <-released; releaseErr != nil {
			t.Fatalf("release blocking writer: %v", releaseErr)
		}
	case <-time.After(6 * time.Second):
		releaseWriter()
		if releaseErr := <-released; releaseErr != nil {
			t.Fatalf("release blocking writer: %v", releaseErr)
		}
		t.Fatal("agent writers did not drain after blocking writer committed")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("agent writer returned error: %v", err)
		}
	}

	var samples int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_samples WHERE node_id = 'example-node-a'`).Scan(&samples); err != nil {
		t.Fatalf("count state samples: %v", err)
	}
	if samples == 0 {
		t.Fatal("agent state writers did not persist any samples")
	}
}

func TestAgentSQLiteBusyRetryBudgetStaysBelowAgentClientTimeout(t *testing.T) {
	const agentClientTimeout = 30 * time.Second
	const configuredSQLiteBusyTimeout = 1 * time.Second
	if sqliteAgentWriteTimeout >= agentClientTimeout {
		t.Fatalf("agent write timeout %s must stay below agent client timeout %s", sqliteAgentWriteTimeout, agentClientTimeout)
	}
	if sqliteBusyRetryFor+configuredSQLiteBusyTimeout >= agentClientTimeout {
		t.Fatalf("busy retry budget %s plus SQLite busy_timeout %s must stay below agent client timeout %s", sqliteBusyRetryFor, configuredSQLiteBusyTimeout, agentClientTimeout)
	}
	if sqliteBusyRetryFor >= sqliteAgentWriteTimeout {
		t.Fatalf("busy retry budget %s must be lower than agent write timeout %s", sqliteBusyRetryFor, sqliteAgentWriteTimeout)
	}
}

func setSQLiteBusyTimeoutForPool(t *testing.T, store *SQLiteStore, connections int, timeoutMS int) {
	t.Helper()
	ctx := context.Background()
	conns := make([]*sql.Conn, 0, connections)
	defer func() {
		for _, conn := range conns {
			if err := conn.Close(); err != nil {
				t.Fatalf("close primed sqlite connection: %v", err)
			}
		}
	}()
	for index := 0; index < connections; index++ {
		conn, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatalf("open sqlite connection %d: %v", index, err)
		}
		conns = append(conns, conn)
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", timeoutMS)); err != nil {
			t.Fatalf("set sqlite busy_timeout on connection %d: %v", index, err)
		}
	}
}

func sqliteConcurrencyAgentState(ts int64, sampleID string) AgentStateRequest {
	return AgentStateRequest{
		SampleID:         sampleID,
		TS:               ts,
		CPUPercent:       12.5,
		MemoryUsedBytes:  512,
		MemoryTotalBytes: 2048,
		DiskUsedBytes:    1024,
		DiskTotalBytes:   8192,
		NetInTotalBytes:  ts * 10,
		NetOutTotalBytes: ts * 20,
		NetInSpeedBps:    128,
		NetOutSpeedBps:   256,
		UptimeSeconds:    3600,
	}
}
