package api

import (
	"context"
	"database/sql"
	"net"
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
	seenAt := nowUnix
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	defer rollbackUnlessCommitted(tx)

	var previous notificationNodeSnapshot
	var storedStatus string
	var offlineIncident int
	if err := tx.QueryRowContext(ctx, `
		SELECT n.id, n.display_name, n.status, COALESCE(n.public_ipv4, ''),
		       CASE WHEN
		         EXISTS (
		           SELECT 1 FROM alert_rule_states ars
		           WHERE ars.node_id = n.id AND ars.rule_id = 'node_offline' AND ars.active = 1
		         ) OR EXISTS (
		           SELECT 1 FROM notification_event_marks nem
		           WHERE nem.event_type = 'node_offline' AND nem.node_id = n.id AND nem.mark = 'status-active:offline'
		         )
		       THEN 1 ELSE 0 END
		FROM nodes n
		WHERE n.id = ? AND n.disabled = 0
	`, nodeID).Scan(&previous.ID, &previous.DisplayName, &storedStatus, &previous.PublicIPv4, &offlineIncident); err != nil {
		if err == sql.ErrNoRows {
			return notificationStatusTransition{}, errNodeNotFound
		}
		return notificationStatusTransition{}, err
	}
	previous.Status = storedNodeStatusForNotification(storedStatus)
	livenessRecovered := offlineIncident != 0 && (status == "online" || status == "warning")
	if livenessRecovered {
		previous.Status = "offline"
	}
	current := notificationNodeSnapshot{ID: previous.ID, DisplayName: previous.DisplayName, PublicIPv4: previous.PublicIPv4}

	nextStatus := status
	if status == "online" && previous.Status == "warning" {
		nextStatus = "warning"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = ?, last_seen_at = ?, updated_at = ?
		WHERE id = ? AND disabled = 0
	`, nextStatus, seenAt, nowUnix, nodeID); err != nil {
		return notificationStatusTransition{}, err
	}
	current.Status = storedNodeStatusForNotification(nextStatus)
	if livenessRecovered {
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert_rule_states
			SET active = 0, last_seen_at = ?, updated_at = ?
			WHERE node_id = ? AND rule_id = 'node_offline'
		`, nowUnix, nowUnix, nodeID); err != nil {
			return notificationStatusTransition{}, err
		}
		// This transition is specifically the liveness recovery. The persisted
		// node may still be warning because of a separate resource incident.
		current.Status = "online"
	}
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

func (s *SQLiteStore) RecordAgentPresenceOnlineTransition(ctx context.Context, nodeID string, ts time.Time) (notificationStatusTransition, error) {
	return s.recordAgentPresenceTransition(ctx, nodeID, ts, "online")
}

func (s *SQLiteStore) RecordAgentPresenceOfflineTransition(ctx context.Context, nodeID string, ts time.Time) (notificationStatusTransition, error) {
	return s.recordAgentPresenceTransition(ctx, nodeID, ts, "offline")
}

func (s *SQLiteStore) StaleAgentOfflineNodeIDs(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.last_seen_at,
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
		WHERE n.disabled = 0
		  AND n.status IN ('online', 'warning')
		ORDER BY n.display_order ASC, n.id ASC
	`, int64(nodeHeartbeatOfflineAfter/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodeIDs := make([]string, 0)
	nowUnix := now.UTC().Unix()
	for rows.Next() {
		var nodeID string
		var lastSeenAt sql.NullInt64
		var offlineDurationSec sql.NullInt64
		if err := rows.Scan(&nodeID, &lastSeenAt, &offlineDurationSec); err != nil {
			return nil, err
		}
		offlineAfter := normalizeNodeOfflineAfter(nodeOfflineAfterFromSeconds(offlineDurationSec))
		cutoff := nowUnix - int64(offlineAfter/time.Second)
		if !lastSeenAt.Valid || lastSeenAt.Int64 <= cutoff {
			nodeIDs = append(nodeIDs, nodeID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodeIDs, nil
}

func (s *SQLiteStore) RecordStaleAgentOfflineTransition(ctx context.Context, nodeID string, now time.Time) (notificationStatusTransition, bool, error) {
	var offlineDurationSec sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE((
		  SELECT MAX(ar.duration_sec)
		  FROM alert_rules ar
		  WHERE ar.notification_event_type = 'node_offline'
		    AND (
		      NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		      OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ?)
		    )
		), ?) AS offline_duration_sec
	`, nodeID, int64(nodeHeartbeatOfflineAfter/time.Second)).Scan(&offlineDurationSec); err != nil {
		return notificationStatusTransition{}, false, err
	}
	offlineAfter := normalizeNodeOfflineAfter(nodeOfflineAfterFromSeconds(offlineDurationSec))
	cutoff := now.UTC().Add(-offlineAfter).Unix()
	nowUnix := now.UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notificationStatusTransition{}, false, err
	}
	defer rollbackUnlessCommitted(tx)

	var previous notificationNodeSnapshot
	var storedStatus string
	var lastSeenAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT id, display_name, status, last_seen_at, COALESCE(public_ipv4, '')
		FROM nodes
		WHERE id = ? AND disabled = 0
	`, nodeID).Scan(&previous.ID, &previous.DisplayName, &storedStatus, &lastSeenAt, &previous.PublicIPv4); err != nil {
		if err == sql.ErrNoRows {
			return notificationStatusTransition{}, false, errNodeNotFound
		}
		return notificationStatusTransition{}, false, err
	}
	previous.Status = storedNodeStatusForNotification(storedStatus)
	if storedStatus != "online" && storedStatus != "warning" {
		return notificationStatusTransition{}, false, nil
	}
	if lastSeenAt.Valid && lastSeenAt.Int64 > cutoff {
		return notificationStatusTransition{}, false, nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = 'offline', updated_at = ?
		WHERE id = ?
		  AND disabled = 0
		  AND status IN ('online', 'warning')
		  AND (last_seen_at IS NULL OR last_seen_at <= ?)
	`, nowUnix, nodeID, cutoff)
	if err != nil {
		return notificationStatusTransition{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return notificationStatusTransition{}, false, err
	}
	if changed == 0 {
		return notificationStatusTransition{}, false, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO alert_rule_states (node_id, rule_id, active, first_seen_at, last_seen_at, updated_at)
		VALUES (?, 'node_offline', 1, ?, ?, ?)
		ON CONFLICT(node_id, rule_id) DO UPDATE SET
			active = 1,
			first_seen_at = CASE
				WHEN alert_rule_states.active = 1 AND alert_rule_states.first_seen_at IS NOT NULL THEN alert_rule_states.first_seen_at
				ELSE excluded.first_seen_at
			END,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at
	`, nodeID, nowUnix, nowUnix, nowUnix); err != nil {
		return notificationStatusTransition{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return notificationStatusTransition{}, false, err
	}
	tx = nil
	current := notificationNodeSnapshot{ID: previous.ID, DisplayName: previous.DisplayName, Status: "offline", PublicIPv4: previous.PublicIPv4}
	return notificationStatusTransition{Previous: previous, Current: current}, true, nil
}

func (s *SQLiteStore) recordAgentPresenceTransition(ctx context.Context, nodeID string, ts time.Time, status string) (notificationStatusTransition, error) {
	now := time.Now().UTC()
	nowUnix := now.Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	defer rollbackUnlessCommitted(tx)

	var previous notificationNodeSnapshot
	var storedStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT id, display_name, status, COALESCE(public_ipv4, '')
		FROM nodes
		WHERE id = ? AND disabled = 0
	`, nodeID).Scan(&previous.ID, &previous.DisplayName, &storedStatus, &previous.PublicIPv4); err != nil {
		if err == sql.ErrNoRows {
			return notificationStatusTransition{}, errNodeNotFound
		}
		return notificationStatusTransition{}, err
	}
	previous.Status = storedNodeStatusForNotification(storedStatus)
	nextStatus := status
	if status == "online" && storedStatus == "warning" {
		nextStatus = "warning"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = ?, last_seen_at = ?, updated_at = ?
		WHERE id = ? AND disabled = 0
	`, nextStatus, nowUnix, nowUnix, nodeID); err != nil {
		return notificationStatusTransition{}, err
	}
	if status == "online" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert_rule_states
			SET active = 0, last_seen_at = ?, updated_at = ?
			WHERE node_id = ? AND rule_id = 'node_offline'
		`, nowUnix, nowUnix, nodeID); err != nil {
			return notificationStatusTransition{}, err
		}
	} else if status == "offline" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO alert_rule_states (node_id, rule_id, active, first_seen_at, last_seen_at, updated_at)
			VALUES (?, 'node_offline', 1, ?, ?, ?)
			ON CONFLICT(node_id, rule_id) DO UPDATE SET
				active = 1,
				first_seen_at = CASE
					WHEN alert_rule_states.active = 1 AND alert_rule_states.first_seen_at IS NOT NULL THEN alert_rule_states.first_seen_at
					ELSE excluded.first_seen_at
				END,
				last_seen_at = excluded.last_seen_at,
				updated_at = excluded.updated_at
		`, nodeID, nowUnix, nowUnix, nowUnix); err != nil {
			return notificationStatusTransition{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return notificationStatusTransition{}, err
	}
	tx = nil
	current := notificationNodeSnapshot{ID: previous.ID, DisplayName: previous.DisplayName, Status: storedNodeStatusForNotification(nextStatus), PublicIPv4: previous.PublicIPv4}
	return notificationStatusTransition{Previous: previous, Current: current}, nil
}

func storedNodeStatusForNotification(status string) string {
	switch strings.TrimSpace(status) {
	case "online", "warning", "offline":
		return strings.TrimSpace(status)
	default:
		return "offline"
	}
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
	publicIPv4 := normalizeAgentPublicIP(host.PublicIPv4, 4)
	publicIPv6 := normalizeAgentPublicIP(host.PublicIPv6, 6)
	countryCode := normalizeAgentCountryCode(host.CountryCode)
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = CASE WHEN status IN ('warning', 'offline') THEN status ELSE 'online' END,
		    last_seen_at = ?,
		    updated_at = ?,
		    public_ipv4 = CASE WHEN ? <> '' THEN ? ELSE public_ipv4 END,
		    public_ipv6 = CASE WHEN ? <> '' THEN ? ELSE public_ipv6 END,
		    country_code = CASE WHEN ? <> '' THEN ? ELSE country_code END
		WHERE id = ? AND disabled = 0
	`, now, now, publicIPv4, publicIPv4, publicIPv6, publicIPv6, countryCode, countryCode, nodeID); err != nil {
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
			node_id, ts, cpu_percent, load1, load5, load15,
			memory_used_bytes, memory_total_bytes, swap_used_bytes, swap_total_bytes,
			disk_used_bytes, disk_total_bytes, net_in_total_bytes, net_out_total_bytes,
			net_in_speed_bps, net_out_speed_bps, process_count, tcp_connection_count, udp_connection_count, uptime_seconds
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nodeID, state.TS, state.CPUPercent, state.Load1, state.Load5, state.Load15, state.MemoryUsedBytes, state.MemoryTotalBytes, state.SwapUsedBytes, state.SwapTotalBytes, state.DiskUsedBytes, state.DiskTotalBytes, state.NetInTotalBytes, state.NetOutTotalBytes, state.NetInSpeedBps, state.NetOutSpeedBps, state.ProcessCount, state.TCPConnectionCount, state.UDPConnectionCount, state.UptimeSeconds); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = CASE WHEN status IN ('warning', 'offline') THEN status ELSE 'online' END, last_seen_at = ?, updated_at = ?
		WHERE id = ? AND disabled = 0
	`, now, now, nodeID); err != nil {
		return err
	}

	var billingMode string
	var monthlyResetDay int
	if err := tx.QueryRowContext(ctx, `SELECT billing_mode, monthly_reset_day FROM nodes WHERE id = ? AND disabled = 0`, nodeID).Scan(&billingMode, &monthlyResetDay); err != nil {
		return err
	}
	month := billingPeriodKey(sampleTS, monthlyResetDay)
	if err := upsertMonthlyTraffic(ctx, tx, nodeID, month, billingMode, state.NetInTotalBytes, state.NetOutTotalBytes, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func normalizeAgentPublicIP(value string, family int) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return ""
	}
	if family == 4 {
		ipv4 := ip.To4()
		if ipv4 == nil {
			return ""
		}
		return ipv4.String()
	}
	if family == 6 {
		if ip.To4() != nil || ip.To16() == nil {
			return ""
		}
		return ip.String()
	}
	return ""
}

func normalizeAgentCountryCode(value string) string {
	trimmed := strings.ToUpper(strings.TrimSpace(value))
	if len(trimmed) != 2 {
		return ""
	}
	for _, r := range trimmed {
		if r < 'A' || r > 'Z' {
			return ""
		}
	}
	return trimmed
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

func billingPeriodKey(ts time.Time, resetDay int) string {
	return billingPeriodFor(ts, resetDay).Key
}

type billingPeriod struct {
	Key       string
	StartDate string
	EndDate   string
}

func billingPeriodFor(ts time.Time, resetDay int) billingPeriod {
	if resetDay < 1 || resetDay > 31 {
		resetDay = 1
	}
	now := ts.UTC()
	currentReset := resetDate(now.Year(), now.Month(), resetDay)
	start := currentReset
	if now.Before(currentReset) {
		previousYear, previousMonth := monthOffset(now.Year(), now.Month(), -1)
		start = resetDate(previousYear, previousMonth, resetDay)
	}
	nextYear, nextMonth := monthOffset(start.Year(), start.Month(), 1)
	nextReset := resetDate(nextYear, nextMonth, resetDay)
	return billingPeriod{
		Key:       start.Format("2006-01"),
		StartDate: start.Format("2006-01-02"),
		EndDate:   nextReset.AddDate(0, 0, -1).Format("2006-01-02"),
	}
}

func resetDate(year int, month time.Month, resetDay int) time.Time {
	day := resetDay
	if maxDay := daysInMonth(year, month); day > maxDay {
		day = maxDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func monthOffset(year int, month time.Month, offset int) (int, time.Month) {
	shifted := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).AddDate(0, offset, 0)
	return shifted.Year(), shifted.Month()
}

func nullableUnix(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}
