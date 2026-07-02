package api

import (
	"context"
	"database/sql"
	"fmt"
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
			billing_mode TEXT NOT NULL DEFAULT 'both',
			monthly_quota_bytes INTEGER,
			monthly_reset_day INTEGER NOT NULL DEFAULT 1,
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
			memory_used_bytes INTEGER,
			memory_total_bytes INTEGER,
			disk_used_bytes INTEGER,
			disk_total_bytes INTEGER,
			net_in_total_bytes INTEGER,
			net_out_total_bytes INTEGER,
			net_in_speed_bps REAL,
			net_out_speed_bps REAL,
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
	return nil
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

func (s *SQLiteStore) nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.display_name, n.status, n.country_code, n.last_seen_at,
		       h.os_name, h.cpu_cores, h.memory_total_bytes, h.disk_total_bytes,
		       ss.cpu_percent, ss.memory_used_bytes, ss.disk_used_bytes,
		       ss.net_in_speed_bps, ss.net_out_speed_bps, ss.net_in_total_bytes, ss.net_out_total_bytes,
		       tm.billable_bytes, n.monthly_quota_bytes
		FROM nodes n
		LEFT JOIN host_info h ON h.node_id = n.id
		LEFT JOIN state_samples ss ON ss.id = (
			SELECT id FROM state_samples WHERE node_id = n.id ORDER BY ts DESC, id DESC LIMIT 1
		)
		LEFT JOIN traffic_monthly tm ON tm.node_id = n.id AND tm.month = strftime('%Y-%m', 'now')
		WHERE n.disabled = 0
		ORDER BY n.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	now := time.Now().UTC()
	for rows.Next() {
		var id, displayName, status string
		var countryCode, osName sql.NullString
		var cpuCores, memoryTotal, diskTotal, lastSeenAt sql.NullInt64
		var cpuPercent, netInSpeed, netOutSpeed sql.NullFloat64
		var memoryUsed, diskUsed, netInTotal, netOutTotal, billable, quota sql.NullInt64
		if err := rows.Scan(&id, &displayName, &status, &countryCode, &lastSeenAt, &osName, &cpuCores, &memoryTotal, &diskTotal, &cpuPercent, &memoryUsed, &diskUsed, &netInSpeed, &netOutSpeed, &netInTotal, &netOutTotal, &billable, &quota); err != nil {
			return nil, err
		}
		node := Node{
			ID:                   id,
			DisplayName:          displayName,
			Status:               publicNodeStatus(status, lastSeenAt, now),
			OS:                   nullStringOr(osName, "linux"),
			CountryCode:          nullStringOr(countryCode, ""),
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
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ? AND disabled = 0`, nodeID).Scan(&exists); err != nil {
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
		WHERE pr.node_id = ?
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
		SELECT ts, cpu_percent, memory_used_bytes, memory_total_bytes,
		       disk_used_bytes, disk_total_bytes, net_in_total_bytes, net_out_total_bytes,
		       net_in_speed_bps, net_out_speed_bps, uptime_seconds
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
		var cpuPercent, netInSpeed, netOutSpeed sql.NullFloat64
		var memoryUsed, memoryTotal, diskUsed, diskTotal, netInTotal, netOutTotal, uptimeSeconds sql.NullInt64
		if err := rows.Scan(&ts, &cpuPercent, &memoryUsed, &memoryTotal, &diskUsed, &diskTotal, &netInTotal, &netOutTotal, &netInSpeed, &netOutSpeed, &uptimeSeconds); err != nil {
			return nil, err
		}
		points = append(points, StatePoint{
			TS:               time.Unix(ts, 0).UTC().Format(time.RFC3339),
			CPUPercent:       floatPtr(cpuPercent),
			MemoryUsedBytes:  intPtr(memoryUsed),
			MemoryTotalBytes: intPtr(memoryTotal),
			DiskUsedBytes:    intPtr(diskUsed),
			DiskTotalBytes:   intPtr(diskTotal),
			NetInTotalBytes:  intPtr(netInTotal),
			NetOutTotalBytes: intPtr(netOutTotal),
			NetInSpeedBps:    floatPtr(netInSpeed),
			NetOutSpeedBps:   floatPtr(netOutSpeed),
			UptimeSeconds:    intPtr(uptimeSeconds),
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
	if status == "online" && (!lastSeenAt.Valid || now.Sub(time.Unix(lastSeenAt.Int64, 0).UTC()) > nodeHeartbeatOfflineAfter) {
		return "offline"
	}
	return status
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
