package api

import (
	"context"
	"database/sql"
	"time"
)

type PreviewSeedOptions struct {
	NodeID      string
	DisplayName string
	CountryCode string
	AgentToken  string
}

type ProbeTarget struct {
	ID          string
	Name        string
	Type        string
	Address     string
	Port        *int
	Count       int
	TimeoutMS   int
	IntervalSec int
}

func (s *SQLiteStore) SeedPreviewData(ctx context.Context, options PreviewSeedOptions) error {
	nodeID := options.NodeID
	if nodeID == "" {
		nodeID = "hytron"
	}
	displayName := options.DisplayName
	if displayName == "" {
		displayName = "Hytron"
	}
	countryCode := options.CountryCode
	if countryCode == "" {
		countryCode = "HK"
	}

	now := time.Now().UTC().Unix()
	tokenHash := "preview-local-collector-token-hash"
	if options.AgentToken != "" {
		tokenHash = hashAgentToken(options.AgentToken)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, billing_mode, monthly_quota_bytes, monthly_reset_day, disabled, created_at, updated_at, last_seen_at)
		VALUES (?, ?, ?, 'no_data', ?, 'both', ?, 1, 0, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			display_name = excluded.display_name,
			token_hash = excluded.token_hash,
			country_code = excluded.country_code,
			monthly_quota_bytes = excluded.monthly_quota_bytes,
			updated_at = excluded.updated_at
	`, nodeID, displayName, tokenHash, countryCode, int64(10*1024*1024*1024*1024), now, now); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO host_info (node_id, hostname, os_name, os_version, kernel, arch, virtualization, cpu_model, cpu_cores, memory_total_bytes, disk_total_bytes, agent_version, updated_at)
		VALUES (?, 'hytron', 'debian', '13', '', 'x86_64', 'kvm', '', 2, ?, ?, 'controller-local-preview', ?)
		ON CONFLICT(node_id) DO UPDATE SET
			hostname = excluded.hostname,
			os_name = excluded.os_name,
			os_version = excluded.os_version,
			arch = excluded.arch,
			virtualization = excluded.virtualization,
			cpu_cores = excluded.cpu_cores,
			memory_total_bytes = excluded.memory_total_bytes,
			disk_total_bytes = excluded.disk_total_bytes,
			agent_version = excluded.agent_version,
			updated_at = excluded.updated_at
	`, nodeID, int64(4112953344), int64(107374182400), now); err != nil {
		return err
	}

	for _, target := range DefaultPreviewProbeTargets() {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				name = excluded.name,
				type = excluded.type,
				address = excluded.address,
				port = excluded.port,
				count = excluded.count,
				timeout_ms = excluded.timeout_ms,
				interval_sec = excluded.interval_sec,
				enabled = 1,
				updated_at = excluded.updated_at
		`, target.ID, target.Name, target.Type, target.Address, nullablePort(target.Port), target.Count, target.TimeoutMS, target.IntervalSec, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO node_probe_targets (node_id, target_id, enabled)
			VALUES (?, ?, 1)
			ON CONFLICT(node_id, target_id) DO UPDATE SET enabled = 1
		`, nodeID, target.ID); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func DefaultPreviewProbeTargets() []ProbeTarget {
	return []ProbeTarget{
		previewTarget("hytron-local", "Hytron", "127.0.0.1", 18980),
		previewTarget("sharon", "Sharon", "157.254.53.89", 53580),
		previewTarget("alibaba", "Alibaba", "47.83.203.128", 53580),
		previewTarget("datawave-hk", "DataWave HK", "103.97.175.136", 53580),
		previewTarget("datawave-jp", "DataWave JP", "82.108.198.81", 53580),
		previewTarget("zouter", "Zouter", "14.137.230.209", 53580),
		previewTarget("datawave-tw", "DataWave TW", "78.105.182.19", 53580),
		previewTarget("hostdzire", "HostDZire", "23.80.89.188", 53580),
		previewTarget("hostishere", "Hostishere", "92.119.167.19", 53580),
		previewTarget("bage", "BAGE", "167.253.97.158", 53580),
		previewTarget("google-dns", "Google DNS", "8.8.8.8", 53),
		previewTarget("cloudflare-dns", "Cloudflare DNS", "1.1.1.1", 53),
		previewTarget("telegram-dc5", "Telegram DC5", "91.108.56.130", 443),
	}
}

func previewTarget(id, name, address string, port int) ProbeTarget {
	return ProbeTarget{
		ID:          id,
		Name:        name,
		Type:        "tcping",
		Address:     address,
		Port:        &port,
		Count:       3,
		TimeoutMS:   1200,
		IntervalSec: 60,
	}
}

func (s *SQLiteStore) EnabledProbeTargets(ctx context.Context, nodeID string) ([]ProbeTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pt.id, pt.name, pt.type, pt.address, pt.port, pt.count, pt.timeout_ms, pt.interval_sec
		FROM probe_targets pt
		JOIN node_probe_targets npt ON npt.target_id = pt.id
		WHERE npt.node_id = ?
		  AND pt.enabled = 1
		  AND npt.enabled = 1
		ORDER BY pt.id ASC
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ProbeTarget
	for rows.Next() {
		var target ProbeTarget
		var port sql.NullInt64
		if err := rows.Scan(&target.ID, &target.Name, &target.Type, &target.Address, &port, &target.Count, &target.TimeoutMS, &target.IntervalSec); err != nil {
			return nil, err
		}
		if port.Valid {
			p := int(port.Int64)
			target.Port = &p
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func nullablePort(port *int) any {
	if port == nil {
		return nil
	}
	return *port
}

func rollbackUnlessCommitted(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}
