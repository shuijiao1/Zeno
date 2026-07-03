package api

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s *SQLiteStore) RecordAgentHeartbeat(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) error {
	_, err := s.RecordAgentHeartbeatTransition(ctx, nodeID, ts, status, agentVersion)
	return err
}

func (s *SQLiteStore) RecordAgentHeartbeatTransition(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) (notificationStatusTransition, error) {
	now := time.Now().UTC()
	nowUnix := now.Unix()
	seenAt := ts.UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	defer rollbackUnlessCommitted(tx)

	var previous notificationNodeSnapshot
	var storedStatus string
	var lastSeenAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT id, display_name, status, last_seen_at
		FROM nodes
		WHERE id = ? AND disabled = 0
	`, nodeID).Scan(&previous.ID, &previous.DisplayName, &storedStatus, &lastSeenAt); err != nil {
		if err == sql.ErrNoRows {
			return notificationStatusTransition{}, errNodeNotFound
		}
		return notificationStatusTransition{}, err
	}
	previous.Status = publicNodeStatus(storedStatus, lastSeenAt, now)
	current := notificationNodeSnapshot{ID: previous.ID, DisplayName: previous.DisplayName}

	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = ?, last_seen_at = ?, updated_at = ?
		WHERE id = ? AND disabled = 0
	`, status, seenAt, nowUnix, nodeID); err != nil {
		return notificationStatusTransition{}, err
	}
	current.Status = publicNodeStatus(status, sql.NullInt64{Int64: seenAt, Valid: true}, now)
	if agentVersion != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO host_info (node_id, agent_version, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(node_id) DO UPDATE SET
				agent_version = excluded.agent_version,
				updated_at = excluded.updated_at
		`, nodeID, agentVersion, nowUnix); err != nil {
			return notificationStatusTransition{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return notificationStatusTransition{}, err
	}
	tx = nil
	return notificationStatusTransition{Previous: previous, Current: current}, nil
}

func (s *SQLiteStore) UpsertAgentHost(ctx context.Context, nodeID string, host AgentHostRequest) error {
	now := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO host_info (
			node_id, hostname, os_name, os_version, kernel, arch, virtualization,
			cpu_model, cpu_cores, memory_total_bytes, disk_total_bytes, boot_time,
			agent_version, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			hostname = excluded.hostname,
			os_name = excluded.os_name,
			os_version = excluded.os_version,
			kernel = excluded.kernel,
			arch = excluded.arch,
			virtualization = excluded.virtualization,
			cpu_model = excluded.cpu_model,
			cpu_cores = excluded.cpu_cores,
			memory_total_bytes = excluded.memory_total_bytes,
			disk_total_bytes = excluded.disk_total_bytes,
			boot_time = excluded.boot_time,
			agent_version = excluded.agent_version,
			updated_at = excluded.updated_at
	`, nodeID, strings.TrimSpace(host.Hostname), strings.TrimSpace(host.OSName), strings.TrimSpace(host.OSVersion), strings.TrimSpace(host.Kernel), strings.TrimSpace(host.Arch), strings.TrimSpace(host.Virtualization), strings.TrimSpace(host.CPUModel), host.CPUCores, host.MemoryTotalBytes, host.DiskTotalBytes, nullableUnix(host.BootTime), strings.TrimSpace(host.AgentVersion), now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = 'online', last_seen_at = ?, updated_at = ?
		WHERE id = ? AND disabled = 0
	`, now, now, nodeID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *SQLiteStore) InsertAgentState(ctx context.Context, nodeID string, state AgentStateRequest) error {
	now := time.Now().UTC().Unix()
	sampleTS := time.Unix(state.TS, 0).UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO state_samples (
			node_id, ts, cpu_percent, memory_used_bytes, memory_total_bytes,
			disk_used_bytes, disk_total_bytes, net_in_total_bytes, net_out_total_bytes,
			net_in_speed_bps, net_out_speed_bps, uptime_seconds
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nodeID, state.TS, state.CPUPercent, state.MemoryUsedBytes, state.MemoryTotalBytes, state.DiskUsedBytes, state.DiskTotalBytes, state.NetInTotalBytes, state.NetOutTotalBytes, state.NetInSpeedBps, state.NetOutSpeedBps, state.UptimeSeconds); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = 'online', last_seen_at = ?, updated_at = ?
		WHERE id = ? AND disabled = 0
	`, state.TS, now, nodeID); err != nil {
		return err
	}

	var billingMode string
	if err := tx.QueryRowContext(ctx, `SELECT billing_mode FROM nodes WHERE id = ? AND disabled = 0`, nodeID).Scan(&billingMode); err != nil {
		return err
	}
	month := sampleTS.Format("2006-01")
	if err := upsertMonthlyTraffic(ctx, tx, nodeID, month, billingMode, state.NetInTotalBytes, state.NetOutTotalBytes, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func upsertMonthlyTraffic(ctx context.Context, tx *sql.Tx, nodeID, month, billingMode string, inTotal, outTotal int64, now int64) error {
	var previousIn, previousOut sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT last_in_total_bytes, last_out_total_bytes
		FROM traffic_monthly
		WHERE node_id = ? AND month = ?
	`, nodeID, month).Scan(&previousIn, &previousOut)
	if err == sql.ErrNoRows {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO traffic_monthly (node_id, month, in_bytes, out_bytes, billable_bytes, last_in_total_bytes, last_out_total_bytes, updated_at)
			VALUES (?, ?, 0, 0, 0, ?, ?, ?)
		`, nodeID, month, inTotal, outTotal, now)
		return err
	}
	if err != nil {
		return err
	}

	deltaIn := nonNegativeDelta(previousIn, inTotal)
	deltaOut := nonNegativeDelta(previousOut, outTotal)
	billable := billableTrafficDelta(billingMode, deltaIn, deltaOut)
	_, err = tx.ExecContext(ctx, `
		UPDATE traffic_monthly
		SET in_bytes = in_bytes + ?,
		    out_bytes = out_bytes + ?,
		    billable_bytes = billable_bytes + ?,
		    last_in_total_bytes = ?,
		    last_out_total_bytes = ?,
		    updated_at = ?
		WHERE node_id = ? AND month = ?
	`, deltaIn, deltaOut, billable, inTotal, outTotal, now, nodeID, month)
	return err
}

func nonNegativeDelta(previous sql.NullInt64, current int64) int64 {
	if !previous.Valid || current < previous.Int64 {
		return 0
	}
	return current - previous.Int64
}

func billableTrafficDelta(mode string, deltaIn, deltaOut int64) int64 {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "in", "download", "inbound":
		return deltaIn
	case "out", "upload", "outbound":
		return deltaOut
	case "max", "higher":
		if deltaIn > deltaOut {
			return deltaIn
		}
		return deltaOut
	default:
		return deltaIn + deltaOut
	}
}

func nullableUnix(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}
