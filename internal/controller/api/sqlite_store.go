package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

const nodeHeartbeatOfflineAfter = 3 * time.Minute

func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &SQLiteStore{db: db}
	if err := store.ensureSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) ensureSchema(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'no_data',
			country_code TEXT,
			region TEXT,
			expiry_date TEXT,
			billing_cycle TEXT,
			display_order INTEGER NOT NULL DEFAULT 0,
			public_ipv4 TEXT,
			public_ipv6 TEXT,
			billing_mode TEXT NOT NULL DEFAULT 'both',
			monthly_quota_bytes INTEGER,
			monthly_reset_day INTEGER NOT NULL DEFAULT 1,
			hide_for_guest INTEGER NOT NULL DEFAULT 0,
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
			uptime_seconds INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_state_samples_node_ts ON state_samples(node_id, ts);`,
		`CREATE TABLE IF NOT EXISTS traffic_monthly (
			node_id TEXT NOT NULL REFERENCES nodes(id),
			month TEXT NOT NULL,
			in_bytes INTEGER NOT NULL DEFAULT 0,
			out_bytes INTEGER NOT NULL DEFAULT 0,
			billable_bytes INTEGER NOT NULL DEFAULT 0,
			last_in_total_bytes INTEGER,
			last_out_total_bytes INTEGER,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (node_id, month)
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
			enabled INTEGER NOT NULL DEFAULT 1,
			hide_for_guest INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS node_probe_targets (
			node_id TEXT NOT NULL REFERENCES nodes(id),
			target_id TEXT NOT NULL REFERENCES probe_targets(id),
			enabled INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (node_id, target_id)
		);`,
		`CREATE TABLE IF NOT EXISTS probe_rounds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL REFERENCES nodes(id),
			target_id TEXT NOT NULL REFERENCES probe_targets(id),
			ts INTEGER NOT NULL,
			type TEXT NOT NULL,
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
			type TEXT NOT NULL,
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
		`CREATE TABLE IF NOT EXISTS notification_deliveries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			label TEXT NOT NULL,
			node_id TEXT NOT NULL,
			node_name TEXT NOT NULL,
			previous_status TEXT NOT NULL,
			status TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			channel_name TEXT NOT NULL,
			channel_type TEXT NOT NULL,
			success INTEGER NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_notification_deliveries_created_at ON notification_deliveries(created_at DESC, id DESC);`,
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
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	stateSampleColumns := map[string]string{
		"load1":                "REAL",
		"load5":                "REAL",
		"load15":               "REAL",
		"swap_used_bytes":      "INTEGER",
		"swap_total_bytes":     "INTEGER",
		"process_count":        "INTEGER",
		"tcp_connection_count": "INTEGER",
	}
	for column, columnType := range stateSampleColumns {
		if err := s.ensureColumn(ctx, "state_samples", column, columnType); err != nil {
			return err
		}
	}
	nodeColumns := map[string]string{
		"expiry_date":       "TEXT",
		"billing_cycle":     "TEXT",
		"display_order":     "INTEGER NOT NULL DEFAULT 0",
		"public_ipv4":       "TEXT",
		"public_ipv6":       "TEXT",
		"hide_for_guest":    "INTEGER NOT NULL DEFAULT 0",
		"billing_mode":      "TEXT NOT NULL DEFAULT 'both'",
		"monthly_reset_day": "INTEGER NOT NULL DEFAULT 1",
	}
	for column, columnType := range nodeColumns {
		if err := s.ensureColumn(ctx, "nodes", column, columnType); err != nil {
			return err
		}
	}
	probeTargetColumns := map[string]string{
		"hide_for_guest": "INTEGER NOT NULL DEFAULT 0",
	}
	for column, columnType := range probeTargetColumns {
		if err := s.ensureColumn(ctx, "probe_targets", column, columnType); err != nil {
			return err
		}
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
	return nil
}

func (s *SQLiteStore) ensureColumn(ctx context.Context, table, column, columnType string) error {
	if !safeSQLIdentifier(table) || !safeSQLIdentifier(column) || strings.TrimSpace(columnType) == "" {
		return fmt.Errorf("invalid schema identifier")
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, columnType))
	return err
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
	for index := range nodes {
		summary, err := s.latestLatencySummary(ctx, nodes[index].ID)
		if err != nil {
			return SummaryResponse{}, err
		}
		nodes[index].LatencySummary = summary
	}

	var points []LatencyPoint
	if len(nodes) > 0 {
		nodeID := preferredSummaryNodeID(nodes)
		window, _ := resolveLatencyWindow("1h")
		points, err = s.latencyPoints(ctx, nodeID, window)
		if err != nil {
			return SummaryResponse{}, err
		}
	}
	return SummaryResponse{Nodes: nodes, LatencyPoints: points}, nil
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
		       n.billing_mode, n.monthly_reset_day, n.expiry_date, n.billing_cycle, n.display_order, n.public_ipv4, n.public_ipv6,
		       n.monthly_quota_bytes, n.hide_for_guest, n.last_seen_at, n.created_at, n.updated_at,
		       h.hostname, h.os_name, h.os_version, h.kernel, h.arch, h.virtualization,
		       h.cpu_model, h.cpu_cores, h.memory_total_bytes, h.disk_total_bytes,
		       h.boot_time, h.agent_version
		FROM nodes n
		LEFT JOIN host_info h ON h.node_id = n.id
		ORDER BY n.display_order ASC, n.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []AdminNode
	now := time.Now().UTC()
	for rows.Next() {
		var node AdminNode
		var status string
		var countryCode, region, billingMode, expiryDate, billingCycle, publicIPv4, publicIPv6 sql.NullString
		var disabled, hideForGuest int
		var monthlyResetDay int
		var displayOrder int
		var quota, lastSeenAt, createdAt, updatedAt sql.NullInt64
		var hostname, osName, osVersion, kernel, arch, virtualization, cpuModel, agentVersion sql.NullString
		var cpuCores, memoryTotal, diskTotal, bootTime sql.NullInt64
		if err := rows.Scan(
			&node.ID, &node.DisplayName, &status, &countryCode, &region, &disabled,
			&billingMode, &monthlyResetDay, &expiryDate, &billingCycle, &displayOrder, &publicIPv4, &publicIPv6,
			&quota, &hideForGuest, &lastSeenAt, &createdAt, &updatedAt,
			&hostname, &osName, &osVersion, &kernel, &arch, &virtualization,
			&cpuModel, &cpuCores, &memoryTotal, &diskTotal,
			&bootTime, &agentVersion,
		); err != nil {
			return nil, err
		}
		node.Disabled = disabled != 0
		node.HideForGuest = hideForGuest != 0
		node.Status = publicNodeStatus(status, lastSeenAt, now)
		if node.Disabled {
			node.Status = "disabled"
		}
		node.CountryCode = nullStringOr(countryCode, "")
		node.Region = nullStringOr(region, "")
		node.BillingMode = nullStringOr(billingMode, "both")
		if monthlyResetDay <= 0 {
			monthlyResetDay = 1
		}
		node.MonthlyResetDay = monthlyResetDay
		node.ExpiryDate = nullStringOr(expiryDate, "")
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
	return nodes, nil
}

func (s *SQLiteStore) AdminProbeTargets(ctx context.Context) ([]AdminProbeTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pt.id, pt.name, pt.type, pt.address, pt.port, pt.count, pt.timeout_ms, pt.interval_sec, pt.enabled, pt.hide_for_guest,
		       npt.node_id, n.display_name, npt.enabled
		FROM probe_targets pt
		LEFT JOIN node_probe_targets npt ON npt.target_id = pt.id
		LEFT JOIN nodes n ON n.id = npt.node_id
		ORDER BY pt.id ASC, npt.node_id ASC
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
		var targetEnabled, hideForGuest int
		var nodeID, nodeDisplayName sql.NullString
		var assignmentEnabled sql.NullInt64
		if err := rows.Scan(&target.ID, &target.Name, &target.Type, &target.Address, &port, &target.Count, &target.TimeoutMS, &target.IntervalSec, &targetEnabled, &hideForGuest, &nodeID, &nodeDisplayName, &assignmentEnabled); err != nil {
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
		target.HideForGuest = hideForGuest != 0
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
	hideForGuest := 0
	if create.HideForGuest {
		hideForGuest = 1
	}
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminProbeTarget{}, err
	}
	defer rollbackUnlessCommitted(tx)
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, enabled, hide_for_guest, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, targetID, create.Name, create.Type, create.Address, adminOptionalInt64SQLValue(create.Port), create.Count, create.TimeoutMS, create.IntervalSec, enabled, hideForGuest, now, now)
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
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO node_probe_targets (node_id, target_id, enabled)
		SELECT id, ?, 1 FROM nodes
	`, targetID); err != nil {
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
	if err := s.db.QueryRowContext(ctx, `SELECT type, address, port FROM probe_targets WHERE id = ?`, targetID).Scan(&currentType, &currentAddress, &currentPort); err != nil {
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
	sets := make([]string, 0, 8)
	args := make([]any, 0, 9)
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
	if update.Enabled != nil {
		sets = append(sets, "enabled = ?")
		if *update.Enabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if update.HideForGuest != nil {
		sets = append(sets, "hide_for_guest = ?")
		if *update.HideForGuest {
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
		}
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
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
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
		return !port.Valid
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

	sets := make([]string, 0, 11)
	args := make([]any, 0, 12)
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
	if update.ExpiryDate != nil {
		sets = append(sets, "expiry_date = ?")
		args = append(args, nullIfEmpty(*update.ExpiryDate))
	}
	if update.BillingCycle != nil {
		sets = append(sets, "billing_cycle = ?")
		args = append(args, nullIfEmpty(*update.BillingCycle))
	}
	if update.BillingMode != nil {
		sets = append(sets, "billing_mode = ?")
		args = append(args, *update.BillingMode)
	}
	if update.MonthlyResetDay != nil {
		sets = append(sets, "monthly_reset_day = ?")
		args = append(args, *update.MonthlyResetDay)
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
	if update.HideForGuest != nil {
		sets = append(sets, "hide_for_guest = ?")
		if *update.HideForGuest {
			args = append(args, 1)
		} else {
			args = append(args, 0)
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
		SELECT n.id, n.display_name, n.status, n.country_code, n.expiry_date, n.last_seen_at,
		       h.os_name, h.os_version, h.kernel, h.arch, h.virtualization, h.cpu_model, h.cpu_cores, h.memory_total_bytes, h.disk_total_bytes,
		       ss.cpu_percent, ss.memory_used_bytes, ss.disk_used_bytes,
		       ss.net_in_speed_bps, ss.net_out_speed_bps, ss.net_in_total_bytes, ss.net_out_total_bytes,
		       (
		         SELECT tm.billable_bytes
		         FROM traffic_monthly tm
		         WHERE tm.node_id = n.id
		           AND tm.month = CASE
		             WHEN CAST(strftime('%d', 'now') AS INTEGER) < n.monthly_reset_day THEN strftime('%Y-%m', 'now', '-1 month')
		             ELSE strftime('%Y-%m', 'now')
		           END
		       ) AS billable_bytes,
		       n.monthly_quota_bytes
		FROM nodes n
		LEFT JOIN host_info h ON h.node_id = n.id
		LEFT JOIN state_samples ss ON ss.id = (
			SELECT id FROM state_samples WHERE node_id = n.id ORDER BY ts DESC, id DESC LIMIT 1
		)
		WHERE n.disabled = 0
		  AND n.hide_for_guest = 0
		ORDER BY n.display_order ASC, n.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	now := time.Now().UTC()
	for rows.Next() {
		var id, displayName, status string
		var countryCode, expiryDate, osName, osVersion, kernel, arch, virtualization, cpuModel sql.NullString
		var cpuCores, memoryTotal, diskTotal, lastSeenAt sql.NullInt64
		var cpuPercent, netInSpeed, netOutSpeed sql.NullFloat64
		var memoryUsed, diskUsed, netInTotal, netOutTotal, billable, quota sql.NullInt64
		if err := rows.Scan(&id, &displayName, &status, &countryCode, &expiryDate, &lastSeenAt, &osName, &osVersion, &kernel, &arch, &virtualization, &cpuModel, &cpuCores, &memoryTotal, &diskTotal, &cpuPercent, &memoryUsed, &diskUsed, &netInSpeed, &netOutSpeed, &netInTotal, &netOutTotal, &billable, &quota); err != nil {
			return nil, err
		}
		node := Node{
			ID:                   id,
			DisplayName:          displayName,
			Status:               publicNodeStatus(status, lastSeenAt, now),
			OS:                   nullStringOr(osName, "linux"),
			OSVersion:            nullStringOr(osVersion, ""),
			Kernel:               nullStringOr(kernel, ""),
			Arch:                 nullStringOr(arch, ""),
			Virtualization:       nullStringOr(virtualization, ""),
			CPUModel:             nullStringOr(cpuModel, ""),
			CountryCode:          nullStringOr(countryCode, ""),
			ExpiryLabel:          nullStringOr(expiryDate, ""),
			CPUCores:             intPtr(cpuCores),
			CPUPercent:           floatPtr(cpuPercent),
			MemoryUsedBytes:      intPtr(memoryUsed),
			MemoryTotalBytes:     intPtr(memoryTotal),
			DiskUsedBytes:        intPtr(diskUsed),
			DiskTotalBytes:       intPtr(diskTotal),
			NetInSpeedBps:        floatPtr(netInSpeed),
			NetOutSpeedBps:       floatPtr(netOutSpeed),
			NetInTotalBytes:      intPtr(netInTotal),
			NetOutTotalBytes:     intPtr(netOutTotal),
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
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ? AND disabled = 0 AND hide_for_guest = 0`, nodeID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) latestLatencySummary(ctx context.Context, nodeID string) (*LatencySummary, error) {
	var targetID, targetName string
	var median, avg sql.NullFloat64
	var loss float64
	var ts int64
	err := s.db.QueryRowContext(ctx, `
		SELECT pr.target_id, pt.name, pr.median_ms, pr.avg_ms, pr.loss_percent, pr.ts
		FROM probe_rounds pr
		JOIN probe_targets pt ON pt.id = pr.target_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.node_id = ?
		  AND pt.enabled = 1
		  AND pt.hide_for_guest = 0
		  AND COALESCE(npt.enabled, 1) = 1
		ORDER BY pr.ts DESC, pr.id DESC
		LIMIT 1
	`, nodeID).Scan(&targetID, &targetName, &median, &avg, &loss, &ts)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	lossPtr := loss
	return &LatencySummary{
		TargetID:    targetID,
		TargetName:  targetName,
		MedianMS:    floatPtr(median),
		AvgMS:       floatPtr(avg),
		LossPercent: &lossPtr,
		UpdatedAt:   time.Unix(ts, 0).UTC().Format(time.RFC3339),
	}, nil
}

func (s *SQLiteStore) latencyPoints(ctx context.Context, nodeID string, window latencyWindow) ([]LatencyPoint, error) {
	since := time.Now().UTC().Add(-time.Duration(window.Samples) * window.Step).Unix()
	rows, err := s.db.QueryContext(ctx, `
		SELECT pr.ts, pr.target_id, pt.name, pr.median_ms, pr.loss_percent
		FROM probe_rounds pr
		JOIN probe_targets pt ON pt.id = pr.target_id
		LEFT JOIN node_probe_targets npt ON npt.node_id = pr.node_id AND npt.target_id = pr.target_id
		WHERE pr.node_id = ?
		  AND pr.ts >= ?
		  AND pt.enabled = 1
		  AND pt.hide_for_guest = 0
		  AND COALESCE(npt.enabled, 1) = 1
		ORDER BY pr.ts ASC, pt.name ASC, pr.id ASC
	`, nodeID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []LatencyPoint
	for rows.Next() {
		var ts int64
		var targetID, targetName string
		var median sql.NullFloat64
		var loss float64
		if err := rows.Scan(&ts, &targetID, &targetName, &median, &loss); err != nil {
			return nil, err
		}
		points = append(points, LatencyPoint{
			TS:          time.Unix(ts, 0).UTC().Format(time.RFC3339),
			TargetID:    targetID,
			TargetName:  targetName,
			MedianMS:    floatPtr(median),
			LossPercent: loss,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}

func (s *SQLiteStore) statePoints(ctx context.Context, nodeID string, window latencyWindow) ([]StatePoint, error) {
	since := time.Now().UTC().Add(-time.Duration(window.Samples) * window.Step).Unix()
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, cpu_percent, load1, load5, load15,
		       memory_used_bytes, memory_total_bytes, swap_used_bytes, swap_total_bytes,
		       disk_used_bytes, disk_total_bytes, net_in_total_bytes, net_out_total_bytes,
		       net_in_speed_bps, net_out_speed_bps, process_count, tcp_connection_count, uptime_seconds
		FROM state_samples
		WHERE node_id = ?
		  AND ts >= ?
		ORDER BY ts ASC, id ASC
	`, nodeID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []StatePoint
	for rows.Next() {
		var ts int64
		var cpuPercent, load1, load5, load15, netInSpeed, netOutSpeed sql.NullFloat64
		var memoryUsed, memoryTotal, swapUsed, swapTotal, diskUsed, diskTotal, netInTotal, netOutTotal, processCount, tcpConnectionCount, uptimeSeconds sql.NullInt64
		if err := rows.Scan(&ts, &cpuPercent, &load1, &load5, &load15, &memoryUsed, &memoryTotal, &swapUsed, &swapTotal, &diskUsed, &diskTotal, &netInTotal, &netOutTotal, &netInSpeed, &netOutSpeed, &processCount, &tcpConnectionCount, &uptimeSeconds); err != nil {
			return nil, err
		}
		points = append(points, StatePoint{
			TS:                 time.Unix(ts, 0).UTC().Format(time.RFC3339),
			CPUPercent:         floatPtr(cpuPercent),
			Load1:              floatPtr(load1),
			Load5:              floatPtr(load5),
			Load15:             floatPtr(load15),
			MemoryUsedBytes:    intPtr(memoryUsed),
			MemoryTotalBytes:   intPtr(memoryTotal),
			SwapUsedBytes:      intPtr(swapUsed),
			SwapTotalBytes:     intPtr(swapTotal),
			DiskUsedBytes:      intPtr(diskUsed),
			DiskTotalBytes:     intPtr(diskTotal),
			NetInTotalBytes:    intPtr(netInTotal),
			NetOutTotalBytes:   intPtr(netOutTotal),
			NetInSpeedBps:      floatPtr(netInSpeed),
			NetOutSpeedBps:     floatPtr(netOutSpeed),
			ProcessCount:       intPtr(processCount),
			TCPConnectionCount: intPtr(tcpConnectionCount),
			UptimeSeconds:      intPtr(uptimeSeconds),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}

func preferredSummaryNodeID(nodes []Node) string {
	for _, node := range nodes {
		if node.ID == "hytron" {
			return node.ID
		}
	}
	return nodes[0].ID
}

func publicNodeStatus(status string, lastSeenAt sql.NullInt64, now time.Time) string {
	if status == "" {
		status = "no_data"
	}
	if (status == "online" || status == "warning") && (!lastSeenAt.Valid || now.Sub(time.Unix(lastSeenAt.Int64, 0).UTC()) > nodeHeartbeatOfflineAfter) {
		return "offline"
	}
	return status
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

func (s *SQLiteStore) String() string {
	return fmt.Sprintf("SQLiteStore(%p)", s)
}
