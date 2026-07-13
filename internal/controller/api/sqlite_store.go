package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	moderncsqlite "modernc.org/sqlite"
)

type SQLiteStore struct {
	db                           *sql.DB
	agentWriteMu                 sync.Mutex
	renewalMu                    sync.Mutex
	adminSessionPruneMu          sync.Mutex
	adminSessionLastPruned       time.Time
	notificationCredentialMu     sync.RWMutex
	notificationCredentialCipher *notificationCredentialCipher
	summaryAggregateMu           sync.Mutex
	summaryAggregateUpdated      time.Time
	summaryAggregateHome         map[string]*LatencySummary
	summaryAggregateServices     []ServiceTarget
	summaryAggregateNodeLatency  map[string][]LatencySummary
}

const (
	nodeHeartbeatOfflineAfter = 30 * time.Second
	// Node state remains live at the Agent cadence, while the expensive rolling
	// 24-hour loss/reporting aggregates are reused briefly. Probe targets update
	// on a much slower cadence, so rebuilding these scans every three seconds
	// only burns CPU without materially improving freshness.
	summaryAggregateFreshFor = 30 * time.Second
	// Agent HTTP clients use a 30s total timeout. Keep server-side SQLite busy
	// recovery below that budget even when an individual SQLite call consumes the
	// configured 5s busy_timeout before returning SQLITE_BUSY.
	sqliteAgentWriteTimeout  = 25 * time.Second
	sqliteBusyRetryFor       = 20 * time.Second
	sqliteBusyRetryInitial   = 25 * time.Millisecond
	sqliteBusyRetryMax       = 250 * time.Millisecond
	sqliteAgentWriteLockPoll = 10 * time.Millisecond
)

func (s *SQLiteStore) withAgentWrite(ctx context.Context, operation func(context.Context) error) error {
	writeCtx, cancel := context.WithTimeout(ctx, sqliteAgentWriteTimeout)
	defer cancel()

	unlock, err := s.lockAgentWrite(writeCtx)
	if err != nil {
		return err
	}
	defer unlock()

	return retrySQLiteBusy(writeCtx, func() error {
		if err := writeCtx.Err(); err != nil {
			return err
		}
		return operation(writeCtx)
	})
}

func (s *SQLiteStore) lockAgentWrite(ctx context.Context) (func(), error) {
	if s.agentWriteMu.TryLock() {
		return s.agentWriteMu.Unlock, nil
	}

	ticker := time.NewTicker(sqliteAgentWriteLockPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if s.agentWriteMu.TryLock() {
				return s.agentWriteMu.Unlock, nil
			}
		}
	}
}

func retrySQLiteBusy(ctx context.Context, operation func() error) error {
	started := time.Now()
	delay := sqliteBusyRetryInitial
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		if err == nil || !isSQLiteBusyError(err) {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		remaining := sqliteBusyRetryFor - time.Since(started)
		if remaining <= 0 {
			return err
		}
		sleepFor := delay
		if sleepFor > remaining {
			sleepFor = remaining
		}
		timer := time.NewTimer(sleepFor)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
		if delay < sqliteBusyRetryMax {
			delay *= 2
			if delay > sqliteBusyRetryMax {
				delay = sqliteBusyRetryMax
			}
		}
	}
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *moderncsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY or SQLITE_LOCKED, including extended result codes.
			return true
		}
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// WAL supports concurrent readers while SQLite still serializes writes.
	// Keeping a small pool prevents history/chart reads from queueing behind
	// Agent writes and summary refreshes on one shared connection.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &SQLiteStore{db: db}
	if err := store.ensureSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func sqliteDSN(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("sqlite path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	values := url.Values{}
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", "busy_timeout(5000)")
	return (&url.URL{Scheme: "file", Path: absolutePath, RawQuery: values.Encode()}).String(), nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// AuthorizeNotificationAuthority binds notification delivery to an external
// secret that is deliberately not stored in SQLite. Copying the production DB
// therefore cannot copy permission to send through its Telegram channels.
func (s *SQLiteStore) AuthorizeNotificationAuthority(ctx context.Context, authorityKey string) (bool, error) {
	authorityKey = strings.TrimSpace(authorityKey)
	if authorityKey == "" {
		return false, nil
	}
	sum := sha256.Sum256([]byte(authorityKey))
	fingerprint := hex.EncodeToString(sum[:])
	nowUnix := time.Now().UTC().Unix()
	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO settings (key, value, updated_at)
		VALUES ('internal.notification_authority_fingerprint', ?, ?)
	`, fingerprint, nowUnix); err != nil {
		return false, err
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'internal.notification_authority_fingerprint'`).Scan(&stored); err != nil {
		return false, err
	}
	if len(stored) != len(fingerprint) {
		return false, nil
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(fingerprint)) == 1, nil
}

func (s *SQLiteStore) Ready(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store is closed")
	}
	var one int
	if err := s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		return err
	}
	if one != 1 {
		return fmt.Errorf("sqlite readiness probe returned %d", one)
	}
	var tableName string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'nodes'`).Scan(&tableName); err != nil {
		return err
	}
	if tableName != "nodes" {
		return fmt.Errorf("sqlite readiness schema missing nodes table")
	}
	return nil
}

func (s *SQLiteStore) QuickCheck(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store is closed")
	}
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("sqlite quick_check: %s", result)
	}
	return nil
}

func (s *SQLiteStore) ensureSchema(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`CREATE TABLE IF NOT EXISTS readiness_probe (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			checked_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			install_token TEXT,
			status TEXT NOT NULL DEFAULT 'no_data',
			country_code TEXT,
			region TEXT,
			home_probe_target_id TEXT,
			expiry_date TEXT,
			expiry_permanent INTEGER NOT NULL DEFAULT 0,
			billing_cycle TEXT,
			display_order INTEGER NOT NULL DEFAULT 0,
			public_ipv4 TEXT,
			public_ipv6 TEXT,
			billing_mode TEXT NOT NULL DEFAULT 'both',
			monthly_quota_bytes INTEGER,
			monthly_reset_day INTEGER NOT NULL DEFAULT 1,
			billing_traffic_epoch INTEGER NOT NULL DEFAULT 0,
			probe_config_applied_version INTEGER NOT NULL DEFAULT 0,
			probe_config_applied_at INTEGER,
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			last_seen_at INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS host_info (
			node_id TEXT PRIMARY KEY REFERENCES nodes(id),
			hostname TEXT,
			os_name TEXT,
			os_version TEXT,
			kernel TEXT,
			arch TEXT,
			virtualization TEXT,
			cpu_model TEXT,
			cpu_cores INTEGER,
			memory_total_bytes INTEGER,
			disk_total_bytes INTEGER,
			boot_time INTEGER,
			agent_version TEXT,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS state_samples (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL REFERENCES nodes(id),
			sample_id TEXT,
			payload_hash TEXT NOT NULL DEFAULT '',
			received_at INTEGER NOT NULL DEFAULT 0,
			ts INTEGER NOT NULL,
			cpu_percent REAL,
			load1 REAL,
			load5 REAL,
			load15 REAL,
			memory_used_bytes INTEGER,
			memory_total_bytes INTEGER,
			swap_used_bytes INTEGER,
			swap_total_bytes INTEGER,
			disk_used_bytes INTEGER,
			disk_total_bytes INTEGER,
			net_in_total_bytes INTEGER,
			net_out_total_bytes INTEGER,
			net_in_speed_bps REAL,
			net_out_speed_bps REAL,
			process_count INTEGER,
			tcp_connection_count INTEGER,
			udp_connection_count INTEGER,
			uptime_seconds INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_state_samples_node_ts ON state_samples(node_id, ts);`,
		`CREATE TABLE IF NOT EXISTS traffic_monthly (
			node_id TEXT NOT NULL REFERENCES nodes(id),
			month TEXT NOT NULL,
			billing_epoch INTEGER NOT NULL DEFAULT 0,
			reset_day INTEGER NOT NULL DEFAULT 1,
			billing_mode TEXT NOT NULL DEFAULT 'both',
			in_bytes INTEGER NOT NULL DEFAULT 0,
			out_bytes INTEGER NOT NULL DEFAULT 0,
			billable_bytes INTEGER NOT NULL DEFAULT 0,
			last_in_total_bytes INTEGER,
			last_out_total_bytes INTEGER,
			last_sample_ts INTEGER,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (node_id, month, billing_epoch)
		);`,
		`CREATE TABLE IF NOT EXISTS probe_targets (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			address TEXT NOT NULL,
			port INTEGER,
			count INTEGER NOT NULL,
			timeout_ms INTEGER NOT NULL,
			interval_sec INTEGER NOT NULL,
			display_order INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS node_probe_targets (
			node_id TEXT NOT NULL REFERENCES nodes(id),
			target_id TEXT NOT NULL REFERENCES probe_targets(id),
			enabled INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (node_id, target_id)
		);`,
		`CREATE TABLE IF NOT EXISTS probe_config_meta (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL DEFAULT 1,
			updated_at INTEGER NOT NULL
		);`,
		`INSERT OR IGNORE INTO probe_config_meta (id, version, updated_at) VALUES (1, 1, strftime('%s', 'now'));`,
		`CREATE TABLE IF NOT EXISTS probe_rounds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL REFERENCES nodes(id),
			target_id TEXT NOT NULL REFERENCES probe_targets(id),
			ts INTEGER NOT NULL,
			type TEXT NOT NULL,
			idempotency_key TEXT NOT NULL DEFAULT '',
			agent_round_id TEXT,
			payload_hash TEXT NOT NULL DEFAULT '',
			sent INTEGER NOT NULL,
			received INTEGER NOT NULL,
			loss_percent REAL NOT NULL,
			min_ms REAL,
			avg_ms REAL,
			median_ms REAL,
			max_ms REAL,
			stddev_ms REAL,
			error TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_probe_rounds_node_target_ts ON probe_rounds(node_id, target_id, ts);`,
		`CREATE INDEX IF NOT EXISTS idx_probe_rounds_node_ts ON probe_rounds(node_id, ts);`,
		`CREATE INDEX IF NOT EXISTS idx_probe_rounds_target_ts ON probe_rounds(target_id, ts);`,
		`CREATE INDEX IF NOT EXISTS idx_probe_rounds_ts_target_node ON probe_rounds(ts, target_id, node_id);`,
		`CREATE TABLE IF NOT EXISTS probe_samples (
			round_id INTEGER NOT NULL REFERENCES probe_rounds(id) ON DELETE CASCADE,
			seq INTEGER NOT NULL,
			success INTEGER NOT NULL,
			latency_ms REAL,
			error TEXT,
			PRIMARY KEY (round_id, seq)
		);`,
		`CREATE TABLE IF NOT EXISTS notification_channels (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			destination TEXT NOT NULL,
			credential TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS notification_types (
			event_type TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS alert_rules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			category TEXT NOT NULL,
			metric TEXT NOT NULL,
			comparator TEXT NOT NULL,
			threshold REAL NOT NULL,
			threshold_unit TEXT NOT NULL,
			duration_sec INTEGER NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			notification_event_type TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_alert_rules_sort_order ON alert_rules(sort_order ASC, id ASC);`,
		`CREATE TABLE IF NOT EXISTS alert_rule_node_scopes (
			rule_id TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
			node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (rule_id, node_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_alert_rule_node_scopes_node ON alert_rule_node_scopes(node_id, rule_id);`,
		`CREATE TABLE IF NOT EXISTS alert_rule_states (
			node_id TEXT NOT NULL REFERENCES nodes(id),
			rule_id TEXT NOT NULL REFERENCES alert_rules(id),
			active INTEGER NOT NULL DEFAULT 0,
			first_seen_at INTEGER,
			last_seen_at INTEGER,
			last_value REAL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (node_id, rule_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_alert_rule_states_node_active ON alert_rule_states(node_id, active);`,
		`CREATE TABLE IF NOT EXISTS notification_event_marks (
			event_type TEXT NOT NULL,
			node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			mark TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (event_type, node_id, mark)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_notification_event_marks_event_node ON notification_event_marks(event_type, node_id);`,
		`CREATE TABLE IF NOT EXISTS notification_deliveries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			node_id TEXT NOT NULL DEFAULT '',
			node_name TEXT NOT NULL DEFAULT '',
			previous_status TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			channel_id TEXT NOT NULL,
			channel_name TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			next_attempt_at INTEGER NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			lease_until INTEGER NOT NULL DEFAULT 0,
			claim_token TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			delivered_at INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_notification_deliveries_pending ON notification_deliveries(state, next_attempt_at, id);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS admin_sessions (
			token_hash TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL
		);`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := s.pruneRetiredTables(ctx); err != nil {
		return err
	}
	if err := s.migrateNotificationChannels(ctx); err != nil {
		return err
	}
	stateSampleColumns := map[string]string{
		"sample_id":            "TEXT",
		"payload_hash":         "TEXT NOT NULL DEFAULT ''",
		"received_at":          "INTEGER NOT NULL DEFAULT 0",
		"load1":                "REAL",
		"load5":                "REAL",
		"load15":               "REAL",
		"swap_used_bytes":      "INTEGER",
		"swap_total_bytes":     "INTEGER",
		"process_count":        "INTEGER",
		"tcp_connection_count": "INTEGER",
		"udp_connection_count": "INTEGER",
	}
	for column, columnType := range stateSampleColumns {
		if err := s.ensureColumn(ctx, "state_samples", column, columnType); err != nil {
			return err
		}
	}
	if err := s.ensureStateSampleIdempotency(ctx); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "probe_rounds", "idempotency_key", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "probe_rounds", "agent_round_id", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "probe_rounds", "payload_hash", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.migrateProbeRoundIdempotency(ctx); err != nil {
		return err
	}
	nodeColumns := map[string]string{
		"install_token":                "TEXT",
		"home_probe_target_id":         "TEXT",
		"expiry_date":                  "TEXT",
		"expiry_permanent":             "INTEGER NOT NULL DEFAULT 0",
		"billing_cycle":                "TEXT",
		"billing_mode":                 "TEXT NOT NULL DEFAULT 'both'",
		"monthly_quota_bytes":          "INTEGER",
		"monthly_reset_day":            "INTEGER NOT NULL DEFAULT 1",
		"billing_traffic_epoch":        "INTEGER NOT NULL DEFAULT 0",
		"probe_config_applied_version": "INTEGER NOT NULL DEFAULT 0",
		"probe_config_applied_at":      "INTEGER",
		"disabled":                     "INTEGER NOT NULL DEFAULT 0",
		"display_order":                "INTEGER NOT NULL DEFAULT 0",
		"public_ipv4":                  "TEXT",
		"public_ipv6":                  "TEXT",
		"last_seen_at":                 "INTEGER",
	}
	for column, columnType := range nodeColumns {
		if err := s.ensureColumn(ctx, "nodes", column, columnType); err != nil {
			return err
		}
	}
	notificationDeliveryColumns := map[string]string{
		"lease_until": "INTEGER NOT NULL DEFAULT 0",
		"claim_token": "TEXT NOT NULL DEFAULT ''",
	}
	for column, columnType := range notificationDeliveryColumns {
		if err := s.ensureColumn(ctx, "notification_deliveries", column, columnType); err != nil {
			return err
		}
	}
	// Existing databases predate the lease columns. Build the claim index only
	// after both columns have been added; otherwise CREATE INDEX aborts startup
	// before the migration can run.
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_notification_deliveries_claim
		ON notification_deliveries(state, next_attempt_at, lease_until, id)
	`); err != nil {
		return err
	}
	if err := s.migrateTrafficMonthlySchema(ctx); err != nil {
		return err
	}
	probeTargetColumns := map[string]string{
		"display_order": "INTEGER NOT NULL DEFAULT 0",
	}
	for column, columnType := range probeTargetColumns {
		if err := s.ensureColumn(ctx, "probe_targets", column, columnType); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE traffic_monthly
		SET last_sample_ts = COALESCE(
			(SELECT MAX(ss.ts)
			 FROM state_samples ss
			 WHERE ss.node_id = traffic_monthly.node_id
			   AND ss.ts <= CAST(strftime('%s', 'now') AS INTEGER) + 300),
			updated_at
		)
		WHERE last_sample_ts IS NULL
	`); err != nil {
		return err
	}
	alertRuleStateColumns := map[string]string{
		"last_value": "REAL",
	}
	for column, columnType := range alertRuleStateColumns {
		if err := s.ensureColumn(ctx, "alert_rule_states", column, columnType); err != nil {
			return err
		}
	}
	if err := s.ensureDefaultAlertRules(ctx); err != nil {
		return err
	}
	if err := s.pruneRetiredNotificationConfig(ctx); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) pruneRetiredTables(ctx context.Context) error {
	return nil
}

func (s *SQLiteStore) migrateNotificationChannels(ctx context.Context) error {
	hasType, err := s.columnExists(ctx, "notification_channels", "type")
	if err != nil {
		return err
	}
	if !hasType {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE notification_channels_new (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			destination TEXT NOT NULL,
			credential TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO notification_channels_new (id, name, destination, credential, enabled, created_at, updated_at)
		SELECT id, name, destination, credential, enabled, created_at, updated_at
		FROM notification_channels
		WHERE type = 'telegram' OR TRIM(COALESCE(type, '')) = ''
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE notification_channels`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE notification_channels_new RENAME TO notification_channels`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *SQLiteStore) ensureColumn(ctx context.Context, table, column, columnType string) error {
	if !safeSQLIdentifier(table) || !safeSQLIdentifier(column) || strings.TrimSpace(columnType) == "" {
		return fmt.Errorf("invalid schema identifier")
	}
	exists, err := s.columnExists(ctx, table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, columnType))
	return err
}

func (s *SQLiteStore) ensureStateSampleIdempotency(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_state_samples_node_sample_id
		ON state_samples(node_id, sample_id)
		WHERE sample_id IS NOT NULL AND sample_id <> ''
	`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_state_samples_node_received
		ON state_samples(node_id, received_at DESC, id DESC)
	`); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) migrateTrafficMonthlySchema(ctx context.Context) error {
	columns, err := s.tableColumns(ctx, "traffic_monthly")
	if err != nil {
		return err
	}
	pkIncludesEpoch, err := s.primaryKeyIncludes(ctx, "traffic_monthly", "billing_epoch")
	if err != nil {
		return err
	}
	requiresRebuild := !pkIncludesEpoch || !columns["billing_epoch"] || !columns["reset_day"] || !columns["billing_mode"] || !columns["last_sample_ts"]
	if !requiresRebuild {
		return nil
	}

	billingEpochExpr := "0"
	if columns["billing_epoch"] {
		billingEpochExpr = "COALESCE(billing_epoch, 0)"
	}
	resetDayExpr := "COALESCE((SELECT n.monthly_reset_day FROM nodes n WHERE n.id = traffic_monthly.node_id), 1)"
	if columns["reset_day"] {
		resetDayExpr = "COALESCE(reset_day, " + resetDayExpr + ")"
	}
	billingModeExpr := "COALESCE((SELECT n.billing_mode FROM nodes n WHERE n.id = traffic_monthly.node_id), 'both')"
	if columns["billing_mode"] {
		billingModeExpr = "COALESCE(NULLIF(TRIM(billing_mode), ''), " + billingModeExpr + ")"
	}
	lastSampleExpr := "NULL"
	if columns["last_sample_ts"] {
		lastSampleExpr = "last_sample_ts"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS traffic_monthly_new`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE traffic_monthly_new (
			node_id TEXT NOT NULL REFERENCES nodes(id),
			month TEXT NOT NULL,
			billing_epoch INTEGER NOT NULL DEFAULT 0,
			reset_day INTEGER NOT NULL DEFAULT 1,
			billing_mode TEXT NOT NULL DEFAULT 'both',
			in_bytes INTEGER NOT NULL DEFAULT 0,
			out_bytes INTEGER NOT NULL DEFAULT 0,
			billable_bytes INTEGER NOT NULL DEFAULT 0,
			last_in_total_bytes INTEGER,
			last_out_total_bytes INTEGER,
			last_sample_ts INTEGER,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (node_id, month, billing_epoch)
		)
	`); err != nil {
		return err
	}
	insertSQL := fmt.Sprintf(`
		INSERT OR REPLACE INTO traffic_monthly_new (
			node_id, month, billing_epoch, reset_day, billing_mode,
			in_bytes, out_bytes, billable_bytes, last_in_total_bytes,
			last_out_total_bytes, last_sample_ts, updated_at
		)
		SELECT node_id, month, %s, %s, %s,
		       in_bytes, out_bytes, billable_bytes, last_in_total_bytes,
		       last_out_total_bytes, %s, updated_at
		FROM traffic_monthly
	`, billingEpochExpr, resetDayExpr, billingModeExpr, lastSampleExpr)
	if _, err := tx.ExecContext(ctx, insertSQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE traffic_monthly`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE traffic_monthly_new RENAME TO traffic_monthly`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *SQLiteStore) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	if !safeSQLIdentifier(table) {
		return nil, fmt.Errorf("invalid schema identifier")
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func (s *SQLiteStore) primaryKeyIncludes(ctx context.Context, table, column string) (bool, error) {
	if !safeSQLIdentifier(table) || !safeSQLIdentifier(column) {
		return false, fmt.Errorf("invalid schema identifier")
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column && primaryKey > 0 {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *SQLiteStore) columnExists(ctx context.Context, table, column string) (bool, error) {
	if !safeSQLIdentifier(table) || !safeSQLIdentifier(column) {
		return false, fmt.Errorf("invalid schema identifier")
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func safeSQLIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func (s *SQLiteStore) Summary(ctx context.Context) (SummaryResponse, error) {
	nodes, err := s.nodes(ctx)
	if err != nil {
		return SummaryResponse{}, err
	}
	homeSummaries, services, latencySummaries, err := s.summaryAggregates(ctx)
	if err != nil {
		return SummaryResponse{}, err
	}
	for index := range nodes {
		nodes[index].LatencySummary = homeSummaries[nodes[index].ID]
		if summaries, ok := latencySummaries[nodes[index].ID]; ok {
			nodes[index].LatencySummaries = summaries
		} else {
			nodes[index].LatencySummaries = []LatencySummary{}
		}
	}
	return SummaryResponse{Nodes: nodes, Services: services, LatencyPoints: []LatencyPoint{}}, nil
}

func (s *SQLiteStore) summaryAggregates(ctx context.Context) (map[string]*LatencySummary, []ServiceTarget, map[string][]LatencySummary, error) {
	s.summaryAggregateMu.Lock()
	defer s.summaryAggregateMu.Unlock()
	if !s.summaryAggregateUpdated.IsZero() && time.Since(s.summaryAggregateUpdated) < summaryAggregateFreshFor {
		return s.summaryAggregateHome, s.summaryAggregateServices, s.summaryAggregateNodeLatency, nil
	}
	homeSummaries, err := s.latestHomeLatencySummaries(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	services, err := s.serviceTargets(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	latencySummaries, err := s.latestLatencySummariesByNode(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	s.summaryAggregateHome = homeSummaries
	s.summaryAggregateServices = services
	s.summaryAggregateNodeLatency = latencySummaries
	s.summaryAggregateUpdated = time.Now()
	return homeSummaries, services, latencySummaries, nil
}

func (s *SQLiteStore) invalidateSummaryAggregates() {
	s.summaryAggregateMu.Lock()
	s.summaryAggregateUpdated = time.Time{}
	s.summaryAggregateHome = nil
	s.summaryAggregateServices = nil
	s.summaryAggregateNodeLatency = nil
	s.summaryAggregateMu.Unlock()
}

func (s *SQLiteStore) NodeLatency(ctx context.Context, nodeID string, window latencyWindow) (LatencyResponse, error) {
	exists, err := s.nodeExists(ctx, nodeID)
	if err != nil {
		return LatencyResponse{}, err
	}
	if !exists {
		return LatencyResponse{}, errNodeNotFound
	}
	points, err := s.latencyPoints(ctx, nodeID, window)
	if err != nil {
		return LatencyResponse{}, err
	}
	return LatencyResponse{NodeID: nodeID, Range: window.Name, Points: points}, nil
}

func (s *SQLiteStore) ServiceTargetLatency(ctx context.Context, targetID string, window latencyWindow) (ServiceTargetLatencyResponse, error) {
	target, err := s.serviceTargetByID(ctx, targetID)
	if err != nil {
		return ServiceTargetLatencyResponse{}, err
	}
	points, err := s.serviceLatencyPoints(ctx, targetID, window)
	if err != nil {
		return ServiceTargetLatencyResponse{}, err
	}
	return ServiceTargetLatencyResponse{Target: target, Range: window.Name, Points: points}, nil
}

func (s *SQLiteStore) NodeState(ctx context.Context, nodeID string, window latencyWindow) (StateResponse, error) {
	exists, err := s.nodeExists(ctx, nodeID)
	if err != nil {
		return StateResponse{}, err
	}
	if !exists {
		return StateResponse{}, errNodeNotFound
	}
	points, err := s.statePoints(ctx, nodeID, window)
	if err != nil {
		return StateResponse{}, err
	}
	return StateResponse{NodeID: nodeID, Range: window.Name, Points: points}, nil
}

func (s *SQLiteStore) AdminNodes(ctx context.Context) ([]AdminNode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.display_name, n.status, n.country_code, n.region, n.disabled,
		       n.home_probe_target_id, n.billing_mode, n.monthly_reset_day, n.expiry_date, n.expiry_permanent, n.billing_cycle, n.display_order, n.public_ipv4, n.public_ipv6,
		       n.monthly_quota_bytes, n.last_seen_at, n.created_at, n.updated_at,
		       COALESCE((
		         SELECT MAX(ar.duration_sec)
		         FROM alert_rules ar
		         WHERE ar.notification_event_type = 'node_offline'
		           AND (
		             NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		             OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = n.id)
		           )
		       ), ?) AS offline_duration_sec,
		       h.hostname, h.os_name, h.os_version, h.kernel, h.arch, h.virtualization,
		       h.cpu_model, h.cpu_cores, h.memory_total_bytes, h.disk_total_bytes,
		       h.boot_time, h.agent_version
		FROM nodes n
		LEFT JOIN host_info h ON h.node_id = n.id
		ORDER BY n.display_order ASC, n.id ASC
	`, int64(nodeHeartbeatOfflineAfter/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []AdminNode
	now := time.Now()
	for rows.Next() {
		var node AdminNode
		var status string
		var countryCode, region, homeProbeTargetID, billingMode, expiryDate, billingCycle, publicIPv4, publicIPv6 sql.NullString
		var disabled int
		var expiryPermanent int
		var monthlyResetDay int
		var displayOrder int
		var quota, lastSeenAt, createdAt, updatedAt, offlineDurationSec sql.NullInt64
		var hostname, osName, osVersion, kernel, arch, virtualization, cpuModel, agentVersion sql.NullString
		var cpuCores, memoryTotal, diskTotal, bootTime sql.NullInt64
		if err := rows.Scan(
			&node.ID, &node.DisplayName, &status, &countryCode, &region, &disabled,
			&homeProbeTargetID, &billingMode, &monthlyResetDay, &expiryDate, &expiryPermanent, &billingCycle, &displayOrder, &publicIPv4, &publicIPv6,
			&quota, &lastSeenAt, &createdAt, &updatedAt, &offlineDurationSec,
			&hostname, &osName, &osVersion, &kernel, &arch, &virtualization,
			&cpuModel, &cpuCores, &memoryTotal, &diskTotal,
			&bootTime, &agentVersion,
		); err != nil {
			return nil, err
		}
		node.Disabled = disabled != 0
		node.Status = publicNodeStatusAfter(status, lastSeenAt, now, nodeOfflineAfterFromSeconds(offlineDurationSec))
		if node.Disabled {
			node.Status = "disabled"
		}
		node.CountryCode = nullStringOr(countryCode, "")
		node.Region = nullStringOr(region, "")
		node.HomeProbeTargetID = nullStringOr(homeProbeTargetID, "")
		node.BillingMode = nullStringOr(billingMode, "both")
		if monthlyResetDay <= 0 {
			monthlyResetDay = 1
		}
		node.MonthlyResetDay = monthlyResetDay
		node.ExpiryDate = nullStringOr(expiryDate, "")
		node.ExpiryPermanent = expiryPermanent != 0
		node.BillingCycle = nullStringOr(billingCycle, "")
		node.DisplayOrder = displayOrder
		node.PublicIPv4 = nullStringOr(publicIPv4, "")
		node.PublicIPv6 = nullStringOr(publicIPv6, "")
		node.MonthlyQuotaBytes = int64Ptr(quota)
		node.LastSeenAt = unixStringPtr(lastSeenAt)
		node.CreatedAt = unixStringOr(createdAt, now)
		node.UpdatedAt = unixStringOr(updatedAt, now)
		node.Hostname = nullStringOr(hostname, "")
		node.OSName = nullStringOr(osName, "")
		node.OSVersion = nullStringOr(osVersion, "")
		node.Kernel = nullStringOr(kernel, "")
		node.Arch = nullStringOr(arch, "")
		node.Virtualization = nullStringOr(virtualization, "")
		node.CPUModel = nullStringOr(cpuModel, "")
		node.CPUCores = intSQLPtr(cpuCores)
		node.MemoryTotalBytes = int64Ptr(memoryTotal)
		node.DiskTotalBytes = int64Ptr(diskTotal)
		node.BootTime = unixStringPtr(bootTime)
		node.AgentVersion = nullStringOr(agentVersion, "")
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if nodes == nil {
		nodes = []AdminNode{}
	}
	return nodes, nil
}

func (s *SQLiteStore) AdminProbeTargets(ctx context.Context) ([]AdminProbeTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pt.id, pt.name, pt.type, pt.address, pt.port, pt.count, pt.timeout_ms, pt.interval_sec, pt.display_order, pt.enabled,
		       npt.node_id, n.display_name, npt.enabled
		FROM probe_targets pt
		LEFT JOIN node_probe_targets npt ON npt.target_id = pt.id
		LEFT JOIN nodes n ON n.id = npt.node_id
		ORDER BY pt.display_order ASC, pt.id ASC, npt.node_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	targets := make([]AdminProbeTarget, 0)
	indexByID := map[string]int{}
	for rows.Next() {
		var target AdminProbeTarget
		var port sql.NullInt64
		var targetEnabled int
		var nodeID, nodeDisplayName sql.NullString
		var assignmentEnabled sql.NullInt64
		if err := rows.Scan(&target.ID, &target.Name, &target.Type, &target.Address, &port, &target.Count, &target.TimeoutMS, &target.IntervalSec, &target.DisplayOrder, &targetEnabled, &nodeID, &nodeDisplayName, &assignmentEnabled); err != nil {
			return nil, err
		}
		if existingIndex, exists := indexByID[target.ID]; exists {
			if nodeID.Valid {
				targets[existingIndex].Assignments = append(targets[existingIndex].Assignments, AdminProbeTargetAssignment{NodeID: nodeID.String, NodeDisplayName: nullStringOr(nodeDisplayName, ""), Enabled: assignmentEnabled.Valid && assignmentEnabled.Int64 != 0})
			}
			continue
		}
		if port.Valid {
			converted := int(port.Int64)
			target.Port = &converted
		}
		target.Enabled = targetEnabled != 0
		target.Assignments = []AdminProbeTargetAssignment{}
		if nodeID.Valid {
			target.Assignments = append(target.Assignments, AdminProbeTargetAssignment{NodeID: nodeID.String, NodeDisplayName: nullStringOr(nodeDisplayName, ""), Enabled: assignmentEnabled.Valid && assignmentEnabled.Int64 != 0})
		}
		indexByID[target.ID] = len(targets)
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *SQLiteStore) CreateAdminProbeTarget(ctx context.Context, create AdminProbeTargetCreateRequest) (AdminProbeTarget, error) {
	if err := create.normalize(); err != nil {
		return AdminProbeTarget{}, err
	}
	targetID := create.ID
	if targetID == "" {
		generated, err := generatedAdminNodeID(create.Name)
		if err != nil {
			return AdminProbeTarget{}, err
		}
		targetID = generated
	}
	enabled := 1
	if create.Enabled != nil && !*create.Enabled {
		enabled = 0
	}
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return AdminProbeTarget{}, err
	}
	usageBefore, err := probeNodeUsagesTx(ctx, tx)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, display_order, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, targetID, create.Name, create.Type, create.Address, adminOptionalInt64SQLValue(create.Port), create.Count, create.TimeoutMS, create.IntervalSec, create.DisplayOrder, enabled, now, now)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AdminProbeTarget{}, err
	}
	if affected == 0 {
		return AdminProbeTarget{}, errProbeTargetAlreadyExists
	}
	for _, assignment := range create.Assignments {
		var nodeExists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ?`, assignment.NodeID).Scan(&nodeExists); err != nil {
			if err == sql.ErrNoRows {
				return AdminProbeTarget{}, errInvalidAdminTargetWrite
			}
			return AdminProbeTarget{}, err
		}
		enabled := 0
		if assignment.Enabled {
			enabled = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO node_probe_targets (node_id, target_id, enabled)
			VALUES (?, ?, ?)
			ON CONFLICT(node_id, target_id) DO UPDATE SET enabled = excluded.enabled
		`, assignment.NodeID, targetID, enabled); err != nil {
			return AdminProbeTarget{}, err
		}
	}
	usageAfter, err := probeNodeUsagesTx(ctx, tx)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	if err := validateProbeNodeUsageTransition(usageBefore, usageAfter); err != nil {
		return AdminProbeTarget{}, err
	}
	if err := bumpProbeConfigVersionTx(ctx, tx); err != nil {
		return AdminProbeTarget{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminProbeTarget{}, err
	}
	tx = nil
	return s.adminProbeTargetByID(ctx, targetID)
}

func (s *SQLiteStore) UpdateAdminProbeTarget(ctx context.Context, targetID string, update AdminProbeTargetUpdateRequest) (AdminProbeTarget, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return AdminProbeTarget{}, errProbeTargetNotFound
	}
	if err := update.normalize(); err != nil {
		return AdminProbeTarget{}, err
	}
	var currentType, currentAddress string
	var currentPort sql.NullInt64
	var currentCount, currentTimeoutMS, currentIntervalSec int
	if err := s.db.QueryRowContext(ctx, `SELECT type, address, port, count, timeout_ms, interval_sec FROM probe_targets WHERE id = ?`, targetID).Scan(&currentType, &currentAddress, &currentPort, &currentCount, &currentTimeoutMS, &currentIntervalSec); err != nil {
		if err == sql.ErrNoRows {
			return AdminProbeTarget{}, errProbeTargetNotFound
		}
		return AdminProbeTarget{}, err
	}
	finalType := currentType
	if update.Type != nil {
		finalType = *update.Type
	}
	finalAddress := currentAddress
	if update.Address != nil {
		finalAddress = *update.Address
	}
	finalPort := currentPort
	if update.Port.Set {
		finalPort = sql.NullInt64{Valid: update.Port.Valid, Int64: update.Port.Value}
	}
	if !validAdminProbeTargetForType(finalType, finalAddress, finalPort) {
		return AdminProbeTarget{}, errInvalidAdminTargetWrite
	}
	finalCount := currentCount
	if update.Count != nil {
		finalCount = *update.Count
	}
	finalTimeoutMS := currentTimeoutMS
	if update.TimeoutMS != nil {
		finalTimeoutMS = *update.TimeoutMS
	}
	finalIntervalSec := currentIntervalSec
	if update.IntervalSec != nil {
		finalIntervalSec = *update.IntervalSec
	}
	validateResourceConfig := update.Count != nil || update.TimeoutMS != nil || update.IntervalSec != nil || (update.Enabled != nil && *update.Enabled)
	if !validateResourceConfig && update.Assignments != nil {
		for _, assignment := range update.Assignments {
			if assignment.Enabled {
				validateResourceConfig = true
				break
			}
		}
	}
	if validateResourceConfig && !validProbeTargetResourceConfig(finalCount, finalTimeoutMS, finalIntervalSec) {
		return AdminProbeTarget{}, errInvalidAdminTargetWrite
	}
	sets := make([]string, 0, 9)
	args := make([]any, 0, 10)
	if update.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *update.Name)
	}
	if update.Type != nil {
		sets = append(sets, "type = ?")
		args = append(args, *update.Type)
	}
	if update.Address != nil {
		sets = append(sets, "address = ?")
		args = append(args, *update.Address)
	}
	if update.Port.Set {
		sets = append(sets, "port = ?")
		args = append(args, adminOptionalInt64SQLValue(update.Port))
	}
	if update.Count != nil {
		sets = append(sets, "count = ?")
		args = append(args, *update.Count)
	}
	if update.TimeoutMS != nil {
		sets = append(sets, "timeout_ms = ?")
		args = append(args, *update.TimeoutMS)
	}
	if update.IntervalSec != nil {
		sets = append(sets, "interval_sec = ?")
		args = append(args, *update.IntervalSec)
	}
	if update.DisplayOrder != nil {
		sets = append(sets, "display_order = ?")
		args = append(args, *update.DisplayOrder)
	}
	if update.Enabled != nil {
		sets = append(sets, "enabled = ?")
		if *update.Enabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return AdminProbeTarget{}, err
	}
	usageBefore, err := probeNodeUsagesTx(ctx, tx)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, time.Now().UTC().Unix(), targetID)
		if _, err := tx.ExecContext(ctx, "UPDATE probe_targets SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
			return AdminProbeTarget{}, err
		}
	}
	if update.Assignments != nil {
		for _, assignment := range update.Assignments {
			var nodeExists int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ?`, assignment.NodeID).Scan(&nodeExists); err != nil {
				if err == sql.ErrNoRows {
					return AdminProbeTarget{}, errInvalidAdminTargetWrite
				}
				return AdminProbeTarget{}, err
			}
			enabled := 0
			if assignment.Enabled {
				enabled = 1
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO node_probe_targets (node_id, target_id, enabled)
				VALUES (?, ?, ?)
				ON CONFLICT(node_id, target_id) DO UPDATE SET enabled = excluded.enabled
			`, assignment.NodeID, targetID, enabled); err != nil {
				return AdminProbeTarget{}, err
			}
			if !assignment.Enabled {
				if _, err := tx.ExecContext(ctx, `UPDATE nodes SET home_probe_target_id = NULL WHERE id = ? AND home_probe_target_id = ?`, assignment.NodeID, targetID); err != nil {
					return AdminProbeTarget{}, err
				}
			}
		}
	}
	if update.Enabled != nil && !*update.Enabled {
		if _, err := tx.ExecContext(ctx, `UPDATE nodes SET home_probe_target_id = NULL WHERE home_probe_target_id = ?`, targetID); err != nil {
			return AdminProbeTarget{}, err
		}
	}
	usageAfter, err := probeNodeUsagesTx(ctx, tx)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	if err := validateProbeNodeUsageTransition(usageBefore, usageAfter); err != nil {
		return AdminProbeTarget{}, err
	}
	if err := bumpProbeConfigVersionTx(ctx, tx); err != nil {
		return AdminProbeTarget{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminProbeTarget{}, err
	}
	tx = nil
	return s.adminProbeTargetByID(ctx, targetID)
}

func (s *SQLiteStore) DeleteAdminProbeTarget(ctx context.Context, targetID string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return errProbeTargetNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `DELETE FROM probe_samples WHERE round_id IN (SELECT id FROM probe_rounds WHERE target_id = ?)`, targetID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM probe_rounds WHERE target_id = ?`, targetID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_probe_targets WHERE target_id = ?`, targetID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET home_probe_target_id = NULL WHERE home_probe_target_id = ?`, targetID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM probe_targets WHERE id = ?`, targetID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errProbeTargetNotFound
	}
	if err := bumpProbeConfigVersionTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

type probeNodeUsage struct {
	targetCount   int
	roundBudgetMS int64
}

func probeNodeUsagesTx(ctx context.Context, tx *sql.Tx) (map[string]probeNodeUsage, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT npt.node_id, COUNT(*) AS target_count, COALESCE(SUM(pt.count * pt.timeout_ms), 0) AS round_budget_ms
		FROM node_probe_targets npt
		JOIN probe_targets pt ON pt.id = npt.target_id
		WHERE npt.enabled = 1
		  AND pt.enabled = 1
		GROUP BY npt.node_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	usages := make(map[string]probeNodeUsage)
	for rows.Next() {
		var nodeID string
		var usage probeNodeUsage
		if err := rows.Scan(&nodeID, &usage.targetCount, &usage.roundBudgetMS); err != nil {
			return nil, err
		}
		usages[nodeID] = usage
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return usages, nil
}

func validateProbeNodeUsageTransition(before, after map[string]probeNodeUsage) error {
	for nodeID, current := range after {
		if current.targetCount <= maxProbeTargetsPerNode && current.roundBudgetMS <= maxProbeNodeRoundBudgetMS {
			continue
		}
		previous := before[nodeID]
		if current.targetCount > previous.targetCount || current.roundBudgetMS > previous.roundBudgetMS {
			return errInvalidAdminTargetWrite
		}
	}
	return nil
}

func (s *SQLiteStore) adminProbeTargetByID(ctx context.Context, targetID string) (AdminProbeTarget, error) {
	targets, err := s.AdminProbeTargets(ctx)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	for _, target := range targets {
		if target.ID == targetID {
			return target, nil
		}
	}
	return AdminProbeTarget{}, errProbeTargetNotFound
}

func adminOptionalInt64SQLValue(value adminOptionalInt64) any {
	if !value.Set || !value.Valid {
		return nil
	}
	return value.Value
}

func validAdminProbeTargetForType(targetType string, address string, port sql.NullInt64) bool {
	switch targetType {
	case "tcping":
		return port.Valid && validPort(port.Int64)
	case "ping":
		return !port.Valid && validPingTargetAddress(address)
	case "http_get":
		return !port.Valid && validHTTPGetTargetAddress(address)
	default:
		return false
	}
}

func (s *SQLiteStore) UpdateAdminNode(ctx context.Context, nodeID string, update AdminNodeUpdateRequest) (AdminNode, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return AdminNode{}, errNodeNotFound
	}
	if err := update.normalize(); err != nil {
		return AdminNode{}, err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ?`, nodeID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return AdminNode{}, errNodeNotFound
		}
		return AdminNode{}, err
	}
	if update.HomeProbeTargetID != nil && *update.HomeProbeTargetID != "" {
		var targetExists int
		if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM probe_targets WHERE id = ?`, *update.HomeProbeTargetID).Scan(&targetExists); err != nil {
			if err == sql.ErrNoRows {
				return AdminNode{}, errInvalidAdminNodeUpdate
			}
			return AdminNode{}, err
		}
	}

	sets := make([]string, 0, 12)
	args := make([]any, 0, 13)
	billingRebaseConditions := make([]string, 0, 2)
	billingRebaseArgs := make([]any, 0, 2)
	if update.DisplayName != nil {
		sets = append(sets, "display_name = ?")
		args = append(args, *update.DisplayName)
	}
	if update.CountryCode != nil {
		sets = append(sets, "country_code = ?")
		args = append(args, nullIfEmpty(*update.CountryCode))
	}
	if update.Region != nil {
		sets = append(sets, "region = ?")
		args = append(args, nullIfEmpty(*update.Region))
	}
	if update.HomeProbeTargetID != nil {
		sets = append(sets, "home_probe_target_id = ?")
		args = append(args, nullIfEmpty(*update.HomeProbeTargetID))
	}
	if update.ExpiryDate != nil {
		sets = append(sets, "expiry_date = ?")
		args = append(args, nullIfEmpty(*update.ExpiryDate))
	}
	if update.ExpiryPermanent != nil {
		sets = append(sets, "expiry_permanent = ?")
		args = append(args, sqliteBoolInt(*update.ExpiryPermanent))
	}
	if update.BillingCycle != nil {
		sets = append(sets, "billing_cycle = ?")
		args = append(args, nullIfEmpty(*update.BillingCycle))
	}
	if update.BillingMode != nil {
		sets = append(sets, "billing_mode = ?")
		args = append(args, *update.BillingMode)
		billingRebaseConditions = append(billingRebaseConditions, "COALESCE(billing_mode, '') <> ?")
		billingRebaseArgs = append(billingRebaseArgs, *update.BillingMode)
	}
	if update.MonthlyResetDay != nil {
		sets = append(sets, "monthly_reset_day = ?")
		args = append(args, *update.MonthlyResetDay)
		billingRebaseConditions = append(billingRebaseConditions, "COALESCE(monthly_reset_day, 1) <> ?")
		billingRebaseArgs = append(billingRebaseArgs, *update.MonthlyResetDay)
	}
	if update.DisplayOrder != nil {
		sets = append(sets, "display_order = ?")
		args = append(args, *update.DisplayOrder)
	}
	if update.PublicIPv4 != nil {
		sets = append(sets, "public_ipv4 = ?")
		args = append(args, nullIfEmpty(*update.PublicIPv4))
	}
	if update.PublicIPv6 != nil {
		sets = append(sets, "public_ipv6 = ?")
		args = append(args, nullIfEmpty(*update.PublicIPv6))
	}
	if update.MonthlyQuotaBytes.Set {
		sets = append(sets, "monthly_quota_bytes = ?")
		if update.MonthlyQuotaBytes.Valid {
			args = append(args, update.MonthlyQuotaBytes.Value)
		} else {
			args = append(args, nil)
		}
	}
	if update.Disabled != nil {
		sets = append(sets, "disabled = ?")
		if *update.Disabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if len(billingRebaseConditions) > 0 {
		sets = append(sets, "billing_traffic_epoch = billing_traffic_epoch + CASE WHEN "+strings.Join(billingRebaseConditions, " OR ")+" THEN 1 ELSE 0 END")
		args = append(args, billingRebaseArgs...)
	}
	if len(sets) == 0 {
		return AdminNode{}, errInvalidAdminNodeUpdate
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Unix(), nodeID)
	if _, err := s.db.ExecContext(ctx, "UPDATE nodes SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		return AdminNode{}, err
	}
	nodes, err := s.AdminNodes(ctx)
	if err != nil {
		return AdminNode{}, err
	}
	for _, node := range nodes {
		if node.ID == nodeID {
			return node, nil
		}
	}
	return AdminNode{}, errNodeNotFound
}

func (s *SQLiteStore) nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.display_name, n.status, n.country_code, n.expiry_date, n.expiry_permanent, n.billing_cycle, n.billing_mode, n.monthly_reset_day, n.last_seen_at,
		       h.os_name, h.os_version, h.kernel, h.arch, h.virtualization, h.cpu_model, h.cpu_cores, h.memory_total_bytes, h.disk_total_bytes, h.boot_time,
		       ss.cpu_percent, ss.load1, ss.load5, ss.load15, ss.uptime_seconds, ss.memory_used_bytes, ss.disk_used_bytes,
		       ss.net_in_speed_bps, ss.net_out_speed_bps, ss.net_in_total_bytes, ss.net_out_total_bytes,
		       (
		         SELECT tm.billable_bytes
		         FROM traffic_monthly tm
		         WHERE tm.node_id = n.id
		           AND tm.billing_epoch = COALESCE(n.billing_traffic_epoch, 0)
		           AND tm.month = CASE
		             WHEN CAST(strftime('%d', 'now') AS INTEGER) < CASE
		               WHEN (CASE WHEN COALESCE(n.monthly_reset_day, 1) BETWEEN 1 AND 31 THEN n.monthly_reset_day ELSE 1 END) > CAST(strftime('%d', date('now', 'start of month', '+1 month', '-1 day')) AS INTEGER)
		               THEN CAST(strftime('%d', date('now', 'start of month', '+1 month', '-1 day')) AS INTEGER)
		               ELSE (CASE WHEN COALESCE(n.monthly_reset_day, 1) BETWEEN 1 AND 31 THEN n.monthly_reset_day ELSE 1 END)
		             END THEN strftime('%Y-%m', date('now', 'start of month', '-1 day'))
		             ELSE strftime('%Y-%m', 'now')
		           END
		       ) AS billable_bytes,
		       n.monthly_quota_bytes,
		       COALESCE((
		         SELECT MAX(ar.duration_sec)
		         FROM alert_rules ar
		         WHERE ar.notification_event_type = 'node_offline'
		           AND (
		             NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		             OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = n.id)
		           )
		       ), ?) AS offline_duration_sec
		FROM nodes n
		LEFT JOIN host_info h ON h.node_id = n.id
		LEFT JOIN state_samples ss ON ss.id = (
			SELECT id FROM state_samples WHERE node_id = n.id ORDER BY ts DESC, id DESC LIMIT 1
		)
		WHERE n.disabled = 0
		ORDER BY n.display_order ASC, n.id ASC
	`, int64(nodeHeartbeatOfflineAfter/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	now := time.Now()
	for rows.Next() {
		var id, displayName, status string
		var countryCode, expiryDate, billingCycle, billingMode, osName, osVersion, kernel, arch, virtualization, cpuModel sql.NullString
		var expiryPermanent int
		var monthlyResetDay, cpuCores, memoryTotal, diskTotal, bootTime, lastSeenAt, uptimeSeconds sql.NullInt64
		var cpuPercent, load1, load5, load15, netInSpeed, netOutSpeed sql.NullFloat64
		var memoryUsed, diskUsed, netInTotal, netOutTotal, billable, quota, offlineDurationSec sql.NullInt64
		if err := rows.Scan(&id, &displayName, &status, &countryCode, &expiryDate, &expiryPermanent, &billingCycle, &billingMode, &monthlyResetDay, &lastSeenAt, &osName, &osVersion, &kernel, &arch, &virtualization, &cpuModel, &cpuCores, &memoryTotal, &diskTotal, &bootTime, &cpuPercent, &load1, &load5, &load15, &uptimeSeconds, &memoryUsed, &diskUsed, &netInSpeed, &netOutSpeed, &netInTotal, &netOutTotal, &billable, &quota, &offlineDurationSec); err != nil {
			return nil, err
		}
		resetDay := 1
		if monthlyResetDay.Valid && monthlyResetDay.Int64 >= 1 && monthlyResetDay.Int64 <= 31 {
			resetDay = int(monthlyResetDay.Int64)
		}
		period := billingPeriodFor(now, resetDay)
		node := Node{
			ID:                   id,
			DisplayName:          displayName,
			Status:               publicNodeStatusAfter(status, lastSeenAt, now, nodeOfflineAfterFromSeconds(offlineDurationSec)),
			OS:                   nullStringOr(osName, "linux"),
			OSVersion:            nullStringOr(osVersion, ""),
			Kernel:               nullStringOr(kernel, ""),
			Arch:                 nullStringOr(arch, ""),
			Virtualization:       nullStringOr(virtualization, ""),
			CPUModel:             nullStringOr(cpuModel, ""),
			CountryCode:          nullStringOr(countryCode, ""),
			ExpiryLabel:          expiryLabelValue(expiryDate, billingCycle, expiryPermanent != 0, now),
			CPUCores:             intPtr(cpuCores),
			CPUPercent:           floatPtr(cpuPercent),
			MemoryUsedBytes:      intPtr(memoryUsed),
			MemoryTotalBytes:     intPtr(memoryTotal),
			DiskUsedBytes:        intPtr(diskUsed),
			DiskTotalBytes:       intPtr(diskTotal),
			BootTime:             unixStringPtr(bootTime),
			Load1:                floatPtr(load1),
			Load5:                floatPtr(load5),
			Load15:               floatPtr(load15),
			UptimeSeconds:        intPtr(uptimeSeconds),
			NetInSpeedBps:        floatPtr(netInSpeed),
			NetOutSpeedBps:       floatPtr(netOutSpeed),
			NetInTotalBytes:      intPtr(netInTotal),
			NetOutTotalBytes:     intPtr(netOutTotal),
			BillingMode:          nullStringOr(billingMode, "both"),
			MonthlyResetDay:      resetDay,
			MonthlyPeriodStart:   period.StartDate,
			MonthlyPeriodEnd:     period.EndDate,
			MonthlyBillableBytes: intPtr(billable),
			MonthlyQuotaBytes:    intPtr(quota),
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *SQLiteStore) nodeExists(ctx context.Context, nodeID string) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ? AND disabled = 0`, nodeID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) latestLatencySummary(ctx context.Context, nodeID string) (*LatencySummary, error) {
	var preferredTarget sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT home_probe_target_id FROM nodes WHERE id = ?`, nodeID).Scan(&preferredTarget); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if preferredTarget.Valid && strings.TrimSpace(preferredTarget.String) != "" {
		return s.latestLatencySummaryForTarget(ctx, nodeID, strings.TrimSpace(preferredTarget.String))
	}
	return nil, nil
}

func (s *SQLiteStore) latestLatencySummaryForTarget(ctx context.Context, nodeID, preferredTargetID string) (*LatencySummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pr.target_id, pt.name, pr.median_ms, pr.avg_ms, pr.loss_percent, pr.ts
		FROM probe_rounds pr
		JOIN probe_targets pt ON pt.id = pr.target_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.node_id = ?
		  AND pr.target_id = ?
		  AND pt.enabled = 1
		  AND COALESCE(npt.enabled, 0) = 1
		  AND pr.ts >= ?
		ORDER BY pr.ts DESC, pr.id DESC
	`, nodeID, strings.TrimSpace(preferredTargetID), time.Now().UTC().Add(-24*time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaryTargetID, targetName string
	var latestMedian, latestAvg *float64
	var latestTS int64
	var lossTotal float64
	var lossCount int
	for rows.Next() {
		var rowTargetID, rowTargetName string
		var median, avg sql.NullFloat64
		var loss float64
		var ts int64
		if err := rows.Scan(&rowTargetID, &rowTargetName, &median, &avg, &loss, &ts); err != nil {
			return nil, err
		}
		if summaryTargetID == "" {
			summaryTargetID = rowTargetID
			targetName = rowTargetName
			latestTS = ts
		}
		if latestAvg == nil && (avg.Valid || median.Valid) {
			latestAvg = floatPtr(avg)
			latestMedian = floatPtr(median)
			if latestAvg == nil {
				latestAvg = latestMedian
			}
		}
		lossTotal += loss
		lossCount++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if summaryTargetID == "" {
		return nil, nil
	}
	loss := 0.0
	if lossCount > 0 {
		loss = lossTotal / float64(lossCount)
	}
	return &LatencySummary{
		TargetID:    summaryTargetID,
		TargetName:  targetName,
		MedianMS:    latestMedian,
		AvgMS:       latestAvg,
		LossPercent: &loss,
		UpdatedAt:   time.Unix(latestTS, 0).UTC().Format(time.RFC3339),
	}, nil

}

func (s *SQLiteStore) latestLatencySummaries(ctx context.Context, nodeID string) ([]LatencySummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pt.id, pt.name, pr.median_ms, pr.avg_ms, pr.loss_percent, pr.ts
		FROM node_probe_targets npt
		JOIN probe_targets pt ON pt.id = npt.target_id
		LEFT JOIN probe_rounds pr ON pr.id = (
			SELECT pr2.id
			FROM probe_rounds pr2
			WHERE pr2.node_id = npt.node_id AND pr2.target_id = npt.target_id
			ORDER BY pr2.ts DESC, pr2.id DESC
			LIMIT 1
		)
		WHERE npt.node_id = ?
		  AND npt.enabled = 1
		  AND pt.enabled = 1
		ORDER BY pt.display_order ASC, pt.name ASC, pt.id ASC
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := []LatencySummary{}
	for rows.Next() {
		var targetID, targetName string
		var median, avg, loss sql.NullFloat64
		var ts sql.NullInt64
		if err := rows.Scan(&targetID, &targetName, &median, &avg, &loss, &ts); err != nil {
			return nil, err
		}
		if !ts.Valid {
			continue
		}
		summaries = append(summaries, LatencySummary{
			TargetID:    targetID,
			TargetName:  targetName,
			MedianMS:    floatPtr(median),
			AvgMS:       floatPtr(avg),
			LossPercent: floatPtr(loss),
			UpdatedAt:   time.Unix(ts.Int64, 0).UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (s *SQLiteStore) latestHomeLatencySummaries(ctx context.Context) (map[string]*LatencySummary, error) {
	since := time.Now().UTC().Add(-24 * time.Hour).Unix()
	rows, err := s.db.QueryContext(ctx, `
		WITH eligible_nodes AS (
			SELECT n.id AS node_id, TRIM(n.home_probe_target_id) AS target_id
			FROM nodes n
			JOIN probe_targets pt ON pt.id = TRIM(n.home_probe_target_id)
			JOIN node_probe_targets npt ON npt.node_id = n.id AND npt.target_id = pt.id
			WHERE n.disabled = 0
			  AND TRIM(COALESCE(n.home_probe_target_id, '')) <> ''
			  AND pt.enabled = 1
			  AND npt.enabled = 1
		),
		loss_by_node AS (
			SELECT eligible.node_id, AVG(pr.loss_percent) AS loss_percent
			FROM eligible_nodes eligible
			JOIN probe_rounds pr ON pr.node_id = eligible.node_id AND pr.target_id = eligible.target_id
			WHERE pr.ts >= ?
			GROUP BY eligible.node_id
		)
		SELECT eligible.node_id, eligible.target_id, pt.name,
		       value.median_ms, value.avg_ms, loss_by_node.loss_percent, latest.ts
		FROM eligible_nodes eligible
		JOIN probe_targets pt ON pt.id = eligible.target_id
		JOIN probe_rounds latest ON latest.id = (
			SELECT candidate.id
			FROM probe_rounds candidate
			WHERE candidate.node_id = eligible.node_id
			  AND candidate.target_id = eligible.target_id
			  AND candidate.ts >= ?
			ORDER BY candidate.ts DESC, candidate.id DESC
			LIMIT 1
		)
		LEFT JOIN probe_rounds value ON value.id = (
			SELECT candidate.id
			FROM probe_rounds candidate
			WHERE candidate.node_id = eligible.node_id
			  AND candidate.target_id = eligible.target_id
			  AND candidate.ts >= ?
			  AND (candidate.avg_ms IS NOT NULL OR candidate.median_ms IS NOT NULL)
			ORDER BY candidate.ts DESC, candidate.id DESC
			LIMIT 1
		)
		JOIN loss_by_node ON loss_by_node.node_id = eligible.node_id
	`, since, since, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := map[string]*LatencySummary{}
	for rows.Next() {
		var nodeID, targetID, targetName string
		var median, avg, loss sql.NullFloat64
		var ts int64
		if err := rows.Scan(&nodeID, &targetID, &targetName, &median, &avg, &loss, &ts); err != nil {
			return nil, err
		}
		medianPtr := floatPtr(median)
		avgPtr := floatPtr(avg)
		if avgPtr == nil {
			avgPtr = medianPtr
		}
		summaries[nodeID] = &LatencySummary{
			TargetID:    targetID,
			TargetName:  targetName,
			MedianMS:    medianPtr,
			AvgMS:       avgPtr,
			LossPercent: floatPtr(loss),
			UpdatedAt:   time.Unix(ts, 0).UTC().Format(time.RFC3339),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (s *SQLiteStore) latestLatencySummariesByNode(ctx context.Context) (map[string][]LatencySummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT npt.node_id, pt.id AS target_id, pt.name AS target_name,
		       pr.median_ms, pr.avg_ms, pr.loss_percent, pr.ts
		FROM node_probe_targets npt
		JOIN nodes n ON n.id = npt.node_id
		JOIN probe_targets pt ON pt.id = npt.target_id
		JOIN probe_rounds pr ON pr.id = (
			SELECT candidate.id
			FROM probe_rounds candidate
			WHERE candidate.node_id = npt.node_id
			  AND candidate.target_id = npt.target_id
			ORDER BY candidate.ts DESC, candidate.id DESC
			LIMIT 1
		)
		WHERE n.disabled = 0
		  AND npt.enabled = 1
		  AND pt.enabled = 1
		ORDER BY npt.node_id ASC, pt.display_order ASC, pt.name ASC, pt.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := map[string][]LatencySummary{}
	for rows.Next() {
		var nodeID, targetID, targetName string
		var median, avg, loss sql.NullFloat64
		var ts int64
		if err := rows.Scan(&nodeID, &targetID, &targetName, &median, &avg, &loss, &ts); err != nil {
			return nil, err
		}
		summaries[nodeID] = append(summaries[nodeID], LatencySummary{
			TargetID:    targetID,
			TargetName:  targetName,
			MedianMS:    floatPtr(median),
			AvgMS:       floatPtr(avg),
			LossPercent: floatPtr(loss),
			UpdatedAt:   time.Unix(ts, 0).UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (s *SQLiteStore) serviceTargets(ctx context.Context) ([]ServiceTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pt.id, pt.name, pt.type, pt.address, pt.port,
		       COUNT(DISTINCT CASE WHEN n.id IS NOT NULL AND COALESCE(npt.enabled, 0) = 1 AND n.disabled = 0 THEN n.id END) AS assigned_nodes
		FROM probe_targets pt
		LEFT JOIN node_probe_targets npt ON npt.target_id = pt.id
		LEFT JOIN nodes n ON n.id = npt.node_id
		WHERE pt.enabled = 1
		GROUP BY pt.id, pt.name, pt.type, pt.address, pt.port, pt.display_order
		ORDER BY pt.display_order ASC, pt.name ASC, pt.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	targets := []ServiceTarget{}
	for rows.Next() {
		var target ServiceTarget
		var port sql.NullInt64
		if err := rows.Scan(&target.ID, &target.Name, &target.Type, &target.Address, &port, &target.AssignedNodeCount); err != nil {
			return nil, err
		}
		target.Port = intSQLPtr(port)
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.populateServiceTargetLatencySummaries(ctx, targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *SQLiteStore) serviceTargetByID(ctx context.Context, targetID string) (ServiceTarget, error) {
	var target ServiceTarget
	var port sql.NullInt64
	var assigned int
	err := s.db.QueryRowContext(ctx, `
		SELECT pt.id, pt.name, pt.type, pt.address, pt.port,
		       COUNT(DISTINCT CASE WHEN n.id IS NOT NULL AND COALESCE(npt.enabled, 0) = 1 AND n.disabled = 0 THEN n.id END) AS assigned_nodes
		FROM probe_targets pt
		LEFT JOIN node_probe_targets npt ON npt.target_id = pt.id
		LEFT JOIN nodes n ON n.id = npt.node_id
		WHERE pt.enabled = 1 AND pt.id = ?
		GROUP BY pt.id, pt.name, pt.type, pt.address, pt.port
	`, targetID).Scan(&target.ID, &target.Name, &target.Type, &target.Address, &port, &assigned)
	if err != nil {
		if err == sql.ErrNoRows {
			return ServiceTarget{}, errProbeTargetNotFound
		}
		return ServiceTarget{}, err
	}
	target.Port = intSQLPtr(port)
	target.AssignedNodeCount = assigned
	if err := s.populateServiceTargetLatencySummary(ctx, &target); err != nil {
		return ServiceTarget{}, err
	}
	return target, nil
}

func (s *SQLiteStore) populateServiceTargetLatencySummaries(ctx context.Context, targets []ServiceTarget) error {
	if len(targets) == 0 {
		return nil
	}
	since := time.Now().UTC().Add(-24 * time.Hour).Unix()
	rows, err := s.db.QueryContext(ctx, `
		WITH reporting AS (
			SELECT pr.target_id, COUNT(DISTINCT pr.node_id) AS reporting_node_count
			FROM probe_rounds pr
			JOIN nodes n ON n.id = pr.node_id
			JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
			WHERE pr.ts >= ?
			  AND n.disabled = 0
			  AND npt.enabled = 1
			GROUP BY pr.target_id
		)
		SELECT pt.id, COALESCE(reporting.reporting_node_count, 0),
		       latest.median_ms, latest.avg_ms, latest.loss_percent, latest.ts
		FROM probe_targets pt
		JOIN probe_rounds latest ON latest.id = (
			SELECT candidate.id
			FROM probe_rounds candidate
			JOIN nodes candidate_node ON candidate_node.id = candidate.node_id
			JOIN node_probe_targets candidate_assignment
			  ON candidate_assignment.node_id = candidate.node_id
			 AND candidate_assignment.target_id = candidate.target_id
			WHERE candidate.target_id = pt.id
			  AND candidate_node.disabled = 0
			  AND candidate_assignment.enabled = 1
			ORDER BY candidate.ts DESC, candidate.id DESC
			LIMIT 1
		)
		LEFT JOIN reporting ON reporting.target_id = pt.id
		WHERE pt.enabled = 1
	`, since)
	if err != nil {
		return err
	}
	defer rows.Close()

	indexByID := make(map[string]int, len(targets))
	for index := range targets {
		indexByID[targets[index].ID] = index
	}
	for rows.Next() {
		var targetID string
		var reportingNodeCount int
		var median, avg, loss sql.NullFloat64
		var ts sql.NullInt64
		if err := rows.Scan(&targetID, &reportingNodeCount, &median, &avg, &loss, &ts); err != nil {
			return err
		}
		index, ok := indexByID[targetID]
		if !ok {
			continue
		}
		targets[index].ReportingNodeCount = reportingNodeCount
		targets[index].MedianMS = floatPtr(median)
		targets[index].AvgMS = floatPtr(avg)
		targets[index].LossPercent = floatPtr(loss)
		if ts.Valid {
			targets[index].UpdatedAt = time.Unix(ts.Int64, 0).UTC().Format(time.RFC3339)
		}
	}
	return rows.Err()
}

func (s *SQLiteStore) populateServiceTargetLatencySummary(ctx context.Context, target *ServiceTarget) error {
	since := time.Now().UTC().Add(-24 * time.Hour).Unix()
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT pr.node_id)
		FROM probe_rounds pr
		JOIN nodes n ON n.id = pr.node_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.target_id = ?
		  AND pr.ts >= ?
		  AND n.disabled = 0
		  AND COALESCE(npt.enabled, 0) = 1
	`, target.ID, since).Scan(&target.ReportingNodeCount); err != nil {
		return err
	}
	var median, avg sql.NullFloat64
	var loss sql.NullFloat64
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT pr.median_ms, pr.avg_ms, pr.loss_percent, pr.ts
		FROM probe_rounds pr
		JOIN nodes n ON n.id = pr.node_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.target_id = ?
		  AND n.disabled = 0
		  AND COALESCE(npt.enabled, 0) = 1
		ORDER BY pr.ts DESC, pr.id DESC
		LIMIT 1
	`, target.ID).Scan(&median, &avg, &loss, &ts)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	target.MedianMS = floatPtr(median)
	target.AvgMS = floatPtr(avg)
	target.LossPercent = floatPtr(loss)
	if ts.Valid {
		target.UpdatedAt = time.Unix(ts.Int64, 0).UTC().Format(time.RFC3339)
	}
	return nil
}

func (s *SQLiteStore) serviceLatencyPoints(ctx context.Context, targetID string, window latencyWindow) ([]ServiceLatencyPoint, error) {
	if useLatencyGrid(window) {
		return s.serviceLatencyGridPoints(ctx, targetID, window)
	}
	since := time.Now().UTC().Add(-time.Duration(window.Samples) * window.Step).Unix()
	rows, err := s.db.QueryContext(ctx, `
		SELECT pr.ts, pr.node_id, n.display_name, pr.median_ms, pr.avg_ms, pr.loss_percent
		FROM probe_rounds pr
		JOIN nodes n ON n.id = pr.node_id
		JOIN probe_targets pt ON pt.id = pr.target_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.target_id = ?
		  AND pr.ts >= ?
		  AND pt.enabled = 1
		  AND n.disabled = 0
		  AND COALESCE(npt.enabled, 0) = 1
		ORDER BY pr.ts ASC, n.display_order ASC, n.display_name ASC, pr.id ASC
	`, targetID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := []ServiceLatencyPoint{}
	for rows.Next() {
		var ts int64
		var nodeID, nodeName string
		var median, avg sql.NullFloat64
		var loss float64
		if err := rows.Scan(&ts, &nodeID, &nodeName, &median, &avg, &loss); err != nil {
			return nil, err
		}
		points = append(points, ServiceLatencyPoint{TS: time.Unix(ts, 0).UTC().Format(time.RFC3339), NodeID: nodeID, NodeName: nodeName, MedianMS: floatPtr(median), AvgMS: floatPtr(avg), LossPercent: loss})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}

func (s *SQLiteStore) latencyPoints(ctx context.Context, nodeID string, window latencyWindow) ([]LatencyPoint, error) {
	if useLatencyGrid(window) {
		return s.latencyGridPoints(ctx, nodeID, window)
	}
	since := time.Now().UTC().Add(-time.Duration(window.Samples) * window.Step).Unix()
	rows, err := s.db.QueryContext(ctx, `
		SELECT pr.ts, pr.target_id, pt.name, pr.median_ms, pr.avg_ms, pr.loss_percent
		FROM probe_rounds pr
		JOIN probe_targets pt ON pt.id = pr.target_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.node_id = ?
		  AND pr.ts >= ?
		  AND pt.enabled = 1
		  AND COALESCE(npt.enabled, 0) = 1
		ORDER BY pr.ts ASC, pt.display_order ASC, pt.name ASC, pr.id ASC
	`, nodeID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []LatencyPoint
	for rows.Next() {
		var ts int64
		var targetID, targetName string
		var median, avg sql.NullFloat64
		var loss float64
		if err := rows.Scan(&ts, &targetID, &targetName, &median, &avg, &loss); err != nil {
			return nil, err
		}
		points = append(points, LatencyPoint{
			TS:          time.Unix(ts, 0).UTC().Format(time.RFC3339),
			TargetID:    targetID,
			TargetName:  targetName,
			MedianMS:    floatPtr(median),
			AvgMS:       floatPtr(avg),
			LossPercent: loss,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}

func useLatencyGrid(window latencyWindow) bool {
	gridWindow, ok := resolveLatencyGridWindow(window.Name)
	if !ok {
		return false
	}
	// Some unit tests pass a custom 1h latencyWindow directly to the store to
	// assert raw round storage. Public 1h requests use resolveLatencyWindow's
	// canonical 20 × 3m realtime grid and should stay bucketed for fast initial
	// chart paint.
	if window.Name == "1h" && (window.Samples != gridWindow.Samples || window.Step != gridWindow.Step) {
		return false
	}
	return true
}

type latencyGridTarget struct {
	ID   string
	Name string
}

type latencyGridBucket struct {
	medianSum   float64
	medianCount int
	avgSum      float64
	avgCount    int
	lossSum     float64
	lossCount   int
}

func (bucket *latencyGridBucket) add(median, avg sql.NullFloat64, loss float64) {
	if median.Valid {
		bucket.medianSum += median.Float64
		bucket.medianCount++
	}
	if avg.Valid {
		bucket.avgSum += avg.Float64
		bucket.avgCount++
	}
	bucket.lossSum += loss
	bucket.lossCount++
}

func (bucket latencyGridBucket) medianPtr() *float64 {
	if bucket.medianCount == 0 {
		return nil
	}
	value := bucket.medianSum / float64(bucket.medianCount)
	return &value
}

func (bucket latencyGridBucket) avgPtr() *float64 {
	if bucket.avgCount > 0 {
		value := bucket.avgSum / float64(bucket.avgCount)
		return &value
	}
	if bucket.medianCount > 0 {
		value := bucket.medianSum / float64(bucket.medianCount)
		return &value
	}
	return nil
}

func (bucket latencyGridBucket) lossPercent() float64 {
	if bucket.lossCount == 0 {
		return 0
	}
	return bucket.lossSum / float64(bucket.lossCount)
}

func (s *SQLiteStore) latencyGridPoints(ctx context.Context, nodeID string, window latencyWindow) ([]LatencyPoint, error) {
	gridWindow, ok := resolveLatencyGridWindow(window.Name)
	if !ok {
		return nil, nil
	}
	targets, err := s.enabledLatencyTargetsForNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return []LatencyPoint{}, nil
	}

	start, end, stepSeconds := latencyGridBounds(gridWindow)
	buckets := make(map[string]map[int64]*latencyGridBucket, len(targets))
	rows, err := s.db.QueryContext(ctx, `
		SELECT (pr.ts / ?) * ? AS bucket_ts, pr.target_id,
		       AVG(pr.median_ms), AVG(pr.avg_ms), AVG(pr.loss_percent)
		FROM probe_rounds pr
		JOIN probe_targets pt ON pt.id = pr.target_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.node_id = ?
		  AND pr.ts >= ?
		  AND pr.ts < ?
		  AND pt.enabled = 1
		  AND COALESCE(npt.enabled, 0) = 1
		GROUP BY bucket_ts, pr.target_id
	`, stepSeconds, stepSeconds, nodeID, start.Unix(), end.Add(gridWindow.Step).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var bucketTS int64
		var targetID string
		var median, avg, loss sql.NullFloat64
		if err := rows.Scan(&bucketTS, &targetID, &median, &avg, &loss); err != nil {
			return nil, err
		}
		if bucketTS < start.Unix() || bucketTS > end.Unix() {
			continue
		}
		if buckets[targetID] == nil {
			buckets[targetID] = map[int64]*latencyGridBucket{}
		}
		if buckets[targetID][bucketTS] == nil {
			buckets[targetID][bucketTS] = &latencyGridBucket{}
		}
		buckets[targetID][bucketTS].add(median, avg, nullFloatOr(loss, 0))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	points := make([]LatencyPoint, 0, gridWindow.Samples*len(targets))
	for index := 0; index < gridWindow.Samples; index++ {
		bucketTS := start.Add(time.Duration(index) * gridWindow.Step).Unix()
		ts := time.Unix(bucketTS, 0).UTC().Format(time.RFC3339)
		for _, target := range targets {
			bucket := buckets[target.ID][bucketTS]
			point := LatencyPoint{TS: ts, TargetID: target.ID, TargetName: target.Name, LossPercent: 0}
			if bucket != nil {
				point.MedianMS = bucket.medianPtr()
				point.AvgMS = bucket.avgPtr()
				point.LossPercent = bucket.lossPercent()
			}
			points = append(points, point)
		}
	}
	return points, nil
}

func (s *SQLiteStore) serviceLatencyGridPoints(ctx context.Context, targetID string, window latencyWindow) ([]ServiceLatencyPoint, error) {
	gridWindow, ok := resolveLatencyGridWindow(window.Name)
	if !ok {
		return nil, nil
	}
	nodes, err := s.enabledLatencyNodesForTarget(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return []ServiceLatencyPoint{}, nil
	}

	start, end, stepSeconds := latencyGridBounds(gridWindow)
	buckets := make(map[string]map[int64]*latencyGridBucket, len(nodes))
	rows, err := s.db.QueryContext(ctx, `
		SELECT (pr.ts / ?) * ? AS bucket_ts, pr.node_id,
		       AVG(pr.median_ms), AVG(pr.avg_ms), AVG(pr.loss_percent)
		FROM probe_rounds pr
		JOIN nodes n ON n.id = pr.node_id
		JOIN probe_targets pt ON pt.id = pr.target_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.target_id = ?
		  AND pr.ts >= ?
		  AND pr.ts < ?
		  AND pt.enabled = 1
		  AND n.disabled = 0
		  AND COALESCE(npt.enabled, 0) = 1
		GROUP BY bucket_ts, pr.node_id
	`, stepSeconds, stepSeconds, targetID, start.Unix(), end.Add(gridWindow.Step).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var bucketTS int64
		var nodeID string
		var median, avg, loss sql.NullFloat64
		if err := rows.Scan(&bucketTS, &nodeID, &median, &avg, &loss); err != nil {
			return nil, err
		}
		if bucketTS < start.Unix() || bucketTS > end.Unix() {
			continue
		}
		if buckets[nodeID] == nil {
			buckets[nodeID] = map[int64]*latencyGridBucket{}
		}
		if buckets[nodeID][bucketTS] == nil {
			buckets[nodeID][bucketTS] = &latencyGridBucket{}
		}
		buckets[nodeID][bucketTS].add(median, avg, nullFloatOr(loss, 0))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	points := make([]ServiceLatencyPoint, 0, gridWindow.Samples*len(nodes))
	for index := 0; index < gridWindow.Samples; index++ {
		bucketTS := start.Add(time.Duration(index) * gridWindow.Step).Unix()
		ts := time.Unix(bucketTS, 0).UTC().Format(time.RFC3339)
		for _, node := range nodes {
			bucket := buckets[node.ID][bucketTS]
			point := ServiceLatencyPoint{TS: ts, NodeID: node.ID, NodeName: node.Name, LossPercent: 0}
			if bucket != nil {
				point.MedianMS = bucket.medianPtr()
				point.AvgMS = bucket.avgPtr()
				point.LossPercent = bucket.lossPercent()
			}
			points = append(points, point)
		}
	}
	return points, nil
}

func (s *SQLiteStore) enabledLatencyTargetsForNode(ctx context.Context, nodeID string) ([]latencyGridTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pt.id, pt.name
		FROM probe_targets pt
		LEFT JOIN node_probe_targets npt ON npt.target_id = pt.id AND npt.node_id = ?
		WHERE pt.enabled = 1 AND COALESCE(npt.enabled, 0) = 1
		ORDER BY pt.display_order ASC, pt.name ASC, pt.id ASC
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []latencyGridTarget
	for rows.Next() {
		var target latencyGridTarget
		if err := rows.Scan(&target.ID, &target.Name); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *SQLiteStore) enabledLatencyNodesForTarget(ctx context.Context, targetID string) ([]latencyGridTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.display_name
		FROM nodes n
		LEFT JOIN node_probe_targets npt ON npt.node_id = n.id AND npt.target_id = ?
		WHERE n.disabled = 0 AND COALESCE(npt.enabled, 0) = 1
		ORDER BY n.display_order ASC, n.display_name ASC, n.id ASC
	`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []latencyGridTarget
	for rows.Next() {
		var node latencyGridTarget
		if err := rows.Scan(&node.ID, &node.Name); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func latencyGridBounds(window latencyWindow) (time.Time, time.Time, int64) {
	step := window.Step
	if step <= 0 {
		step = time.Minute
	}
	stepSeconds := int64(step.Seconds())
	if stepSeconds <= 0 {
		stepSeconds = 1
	}
	endUnix := (time.Now().UTC().Unix() / stepSeconds) * stepSeconds
	end := time.Unix(endUnix, 0).UTC()
	start := end.Add(-time.Duration(window.Samples-1) * step)
	return start, end, stepSeconds
}

func (s *SQLiteStore) statePoints(ctx context.Context, nodeID string, window latencyWindow) ([]StatePoint, error) {
	since := time.Now().UTC().Add(-time.Duration(window.Samples) * window.Step).Unix()
	stepSeconds := int64(window.Step.Seconds())
	if stepSeconds <= 0 {
		stepSeconds = 1
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT (ts / ?) * ? AS bucket_ts, AVG(cpu_percent), AVG(load1), AVG(load5), AVG(load15),
		       AVG(memory_used_bytes), AVG(memory_total_bytes), AVG(swap_used_bytes), AVG(swap_total_bytes),
		       AVG(disk_used_bytes), AVG(disk_total_bytes), AVG(net_in_total_bytes), AVG(net_out_total_bytes),
		       AVG(net_in_speed_bps), AVG(net_out_speed_bps), AVG(process_count), AVG(tcp_connection_count), AVG(udp_connection_count), AVG(uptime_seconds)
		FROM state_samples
		WHERE node_id = ?
		  AND ts >= ?
		GROUP BY bucket_ts
		ORDER BY bucket_ts ASC
	`, stepSeconds, stepSeconds, nodeID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []StatePoint
	for rows.Next() {
		var ts int64
		var cpuPercent, load1, load5, load15, memoryUsed, memoryTotal, swapUsed, swapTotal, diskUsed, diskTotal, netInTotal, netOutTotal, netInSpeed, netOutSpeed, processCount, tcpConnectionCount, udpConnectionCount, uptimeSeconds sql.NullFloat64
		if err := rows.Scan(&ts, &cpuPercent, &load1, &load5, &load15, &memoryUsed, &memoryTotal, &swapUsed, &swapTotal, &diskUsed, &diskTotal, &netInTotal, &netOutTotal, &netInSpeed, &netOutSpeed, &processCount, &tcpConnectionCount, &udpConnectionCount, &uptimeSeconds); err != nil {
			return nil, err
		}
		points = append(points, StatePoint{
			TS:                 time.Unix(ts, 0).UTC().Format(time.RFC3339),
			CPUPercent:         floatPtr(cpuPercent),
			Load1:              floatPtr(load1),
			Load5:              floatPtr(load5),
			Load15:             floatPtr(load15),
			MemoryUsedBytes:    floatPtr(memoryUsed),
			MemoryTotalBytes:   floatPtr(memoryTotal),
			SwapUsedBytes:      floatPtr(swapUsed),
			SwapTotalBytes:     floatPtr(swapTotal),
			DiskUsedBytes:      floatPtr(diskUsed),
			DiskTotalBytes:     floatPtr(diskTotal),
			NetInTotalBytes:    floatPtr(netInTotal),
			NetOutTotalBytes:   floatPtr(netOutTotal),
			NetInSpeedBps:      floatPtr(netInSpeed),
			NetOutSpeedBps:     floatPtr(netOutSpeed),
			ProcessCount:       floatPtr(processCount),
			TCPConnectionCount: floatPtr(tcpConnectionCount),
			UDPConnectionCount: floatPtr(udpConnectionCount),
			UptimeSeconds:      floatPtr(uptimeSeconds),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}

func publicNodeStatus(status string, lastSeenAt sql.NullInt64, now time.Time) string {
	return publicNodeStatusAfter(status, lastSeenAt, now, nodeHeartbeatOfflineAfter)
}

func publicNodeStatusAfter(status string, lastSeenAt sql.NullInt64, now time.Time, offlineAfter time.Duration) string {
	if status == "" {
		status = "no_data"
	}
	offlineAfter = normalizeNodeOfflineAfter(offlineAfter)
	if (status == "online" || status == "warning") && (!lastSeenAt.Valid || now.Sub(time.Unix(lastSeenAt.Int64, 0).UTC()) >= offlineAfter) {
		return "offline"
	}
	return status
}

func normalizeNodeOfflineAfter(offlineAfter time.Duration) time.Duration {
	if offlineAfter <= 0 {
		return nodeHeartbeatOfflineAfter
	}
	return offlineAfter
}

func nodeOfflineAfterFromSeconds(seconds sql.NullInt64) time.Duration {
	if !seconds.Valid || seconds.Int64 <= 0 {
		return nodeHeartbeatOfflineAfter
	}
	return time.Duration(seconds.Int64) * time.Second
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullStringOr(value sql.NullString, fallback string) string {
	if value.Valid {
		return value.String
	}
	return fallback
}

func expiryLabelValue(expiryDate, billingCycle sql.NullString, permanent bool, now time.Time) string {
	if permanent {
		return "永久"
	}
	rawDate := strings.TrimSpace(nullStringOr(expiryDate, ""))
	if rawDate == "" {
		return ""
	}
	cycleMonths := billingCycleMonths(billingCycle)
	if cycleMonths <= 0 {
		return rawDate
	}
	nextDate, ok := nextBillingCycleDate(rawDate, cycleMonths, now)
	if !ok {
		return rawDate
	}
	return formatExpiryDaysLabel(nextDate, now)
}

func billingCycleMonths(value sql.NullString) int {
	if !value.Valid {
		return 0
	}
	cycle := strings.TrimSpace(value.String)
	if cycle == "" {
		return 0
	}
	if strings.Contains(cycle, "五年") || strings.Contains(cycle, "5年") || strings.Contains(cycle, "5 年") {
		return 60
	}
	if strings.Contains(cycle, "三年") || strings.Contains(cycle, "3年") || strings.Contains(cycle, "3 年") {
		return 36
	}
	if strings.Contains(cycle, "两年") || strings.Contains(cycle, "二年") || strings.Contains(cycle, "2年") || strings.Contains(cycle, "2 年") {
		return 24
	}
	if strings.Contains(cycle, "半年") || strings.Contains(cycle, "半 年") {
		return 6
	}
	if strings.Contains(cycle, "季") {
		return 3
	}
	if strings.Contains(cycle, "年") {
		return 12
	}
	if strings.Contains(cycle, "月") {
		return 1
	}
	return 0
}

func nextBillingCycleDate(rawDate string, cycleMonths int, now time.Time) (time.Time, bool) {
	finalDate, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(rawDate), time.UTC)
	if err != nil || cycleMonths <= 0 {
		return time.Time{}, false
	}
	today := dateOnlyUTC(now)
	finalDate = dateOnlyUTC(finalDate)
	if finalDate.Before(today) {
		return finalDate, true
	}
	nextDate := finalDate
	offsetMonths := 0
	for {
		previous := addMonthsFromAnchorClampedUTC(finalDate, offsetMonths-cycleMonths)
		if previous.Before(today) {
			break
		}
		nextDate = previous
		offsetMonths -= cycleMonths
	}
	return nextDate, true
}

func formatExpiryDaysLabel(date, now time.Time) string {
	today := dateOnlyUTC(now)
	due := dateOnlyUTC(date)
	days := int(due.Sub(today).Hours() / 24)
	if days < 0 {
		return "已过期"
	}
	if days == 0 {
		return "今天到期"
	}
	return fmt.Sprintf("余 %d 天", days)
}

func dateOnlyUTC(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func addMonthsClampedUTC(value time.Time, months int) time.Time {
	value = dateOnlyUTC(value)
	return addMonthsFromAnchorClampedUTC(value, months)
}

func addMonthsFromAnchorClampedUTC(anchor time.Time, months int) time.Time {
	anchor = dateOnlyUTC(anchor)
	year, month, day := anchor.Date()
	totalMonths := year*12 + int(month) - 1 + months
	newYear := totalMonths / 12
	newMonth := time.Month(totalMonths%12 + 1)
	if totalMonths < 0 && totalMonths%12 != 0 {
		newYear--
		newMonth = time.Month(totalMonths%12 + 13)
	}
	if maxDay := daysInMonth(newYear, newMonth); day > maxDay {
		day = maxDay
	}
	return time.Date(newYear, newMonth, day, 0, 0, 0, 0, time.UTC)
}

func sqliteBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func intPtr(value sql.NullInt64) *float64 {
	if !value.Valid {
		return nil
	}
	converted := float64(value.Int64)
	return &converted
}

func intSQLPtr(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}

func int64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	converted := value.Int64
	return &converted
}

func unixStringPtr(value sql.NullInt64) *string {
	if !value.Valid || value.Int64 <= 0 {
		return nil
	}
	formatted := time.Unix(value.Int64, 0).UTC().Format(time.RFC3339)
	return &formatted
}

func unixStringOr(value sql.NullInt64, fallback time.Time) string {
	if !value.Valid || value.Int64 <= 0 {
		return fallback.UTC().Format(time.RFC3339)
	}
	return time.Unix(value.Int64, 0).UTC().Format(time.RFC3339)
}

func floatPtr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	converted := value.Float64
	return &converted
}

func nullFloatOr(value sql.NullFloat64, fallback float64) float64 {
	if !value.Valid {
		return fallback
	}
	return value.Float64
}

func (s *SQLiteStore) String() string {
	return fmt.Sprintf("SQLiteStore(%p)", s)
}
