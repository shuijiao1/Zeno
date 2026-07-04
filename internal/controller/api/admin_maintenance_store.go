package api

import (
	"context"
	"database/sql"
	"strconv"
	"time"
)

const (
	settingKeyMaintenanceEnabled                   = "maintenance_enabled"
	settingKeyMaintenanceStateRetentionDays        = "maintenance_state_retention_days"
	settingKeyMaintenanceProbeRetentionDays        = "maintenance_probe_retention_days"
	settingKeyMaintenanceNotificationRetentionDays = "maintenance_notification_retention_days"
)

func defaultAdminMaintenanceSettings() AdminMaintenanceSettings {
	return AdminMaintenanceSettings{
		Enabled:                   false,
		StateRetentionDays:        30,
		ProbeRetentionDays:        30,
		NotificationRetentionDays: 90,
	}
}

func (s *SQLiteStore) AdminMaintenance(ctx context.Context) (AdminMaintenanceResponse, error) {
	settings, err := s.adminMaintenanceSettings(ctx)
	if err != nil {
		return AdminMaintenanceResponse{}, err
	}
	candidates, err := s.adminMaintenanceCandidates(ctx, settings)
	if err != nil {
		return AdminMaintenanceResponse{}, err
	}
	return AdminMaintenanceResponse{Settings: settings, Candidates: candidates}, nil
}

func (s *SQLiteStore) UpdateAdminMaintenance(ctx context.Context, update AdminMaintenanceUpdateRequest) (AdminMaintenanceResponse, error) {
	if err := update.normalize(); err != nil {
		return AdminMaintenanceResponse{}, err
	}
	settings, err := s.adminMaintenanceSettings(ctx)
	if err != nil {
		return AdminMaintenanceResponse{}, err
	}
	if update.Enabled != nil {
		settings.Enabled = *update.Enabled
	}
	if update.StateRetentionDays != nil {
		settings.StateRetentionDays = *update.StateRetentionDays
	}
	if update.ProbeRetentionDays != nil {
		settings.ProbeRetentionDays = *update.ProbeRetentionDays
	}
	if update.NotificationRetentionDays != nil {
		settings.NotificationRetentionDays = *update.NotificationRetentionDays
	}
	if err := s.writeAdminMaintenanceSettings(ctx, settings); err != nil {
		return AdminMaintenanceResponse{}, err
	}
	return s.AdminMaintenance(ctx)
}

func (s *SQLiteStore) RunAdminMaintenanceCleanup(ctx context.Context, request AdminMaintenanceCleanupRequest) (AdminMaintenanceCleanupResponse, error) {
	settings, err := s.adminMaintenanceSettings(ctx)
	if err != nil {
		return AdminMaintenanceCleanupResponse{}, err
	}
	candidates, err := s.adminMaintenanceCandidates(ctx, settings)
	if err != nil {
		return AdminMaintenanceCleanupResponse{}, err
	}
	if request.DryRun {
		return AdminMaintenanceCleanupResponse{Settings: settings, Deleted: candidates, Candidates: candidates, DryRun: true}, nil
	}
	if !request.Confirm {
		return AdminMaintenanceCleanupResponse{}, errInvalidAdminMaintenanceCleanup
	}
	deleted, err := s.deleteAdminMaintenanceCandidates(ctx, settings)
	if err != nil {
		return AdminMaintenanceCleanupResponse{}, err
	}
	after, err := s.adminMaintenanceCandidates(ctx, settings)
	if err != nil {
		return AdminMaintenanceCleanupResponse{}, err
	}
	return AdminMaintenanceCleanupResponse{Settings: settings, Deleted: deleted, Candidates: after, DryRun: false}, nil
}

func (s *SQLiteStore) RunAutomaticAdminMaintenanceCleanup(ctx context.Context) (AdminMaintenanceCleanupResponse, bool, error) {
	settings, err := s.adminMaintenanceSettings(ctx)
	if err != nil {
		return AdminMaintenanceCleanupResponse{}, false, err
	}
	if !settings.Enabled {
		return AdminMaintenanceCleanupResponse{Settings: settings, DryRun: false}, false, nil
	}
	if _, err := s.adminMaintenanceCandidates(ctx, settings); err != nil {
		return AdminMaintenanceCleanupResponse{}, true, err
	}
	deleted, err := s.deleteAdminMaintenanceCandidates(ctx, settings)
	if err != nil {
		return AdminMaintenanceCleanupResponse{}, true, err
	}
	after, err := s.adminMaintenanceCandidates(ctx, settings)
	if err != nil {
		return AdminMaintenanceCleanupResponse{}, true, err
	}
	return AdminMaintenanceCleanupResponse{Settings: settings, Deleted: deleted, Candidates: after, DryRun: false}, true, nil
}

func (s *SQLiteStore) adminMaintenanceSettings(ctx context.Context) (AdminMaintenanceSettings, error) {
	settings := defaultAdminMaintenanceSettings()
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, value, updated_at
		FROM settings
		WHERE key IN (?, ?, ?, ?)
	`, settingKeyMaintenanceEnabled, settingKeyMaintenanceStateRetentionDays, settingKeyMaintenanceProbeRetentionDays, settingKeyMaintenanceNotificationRetentionDays)
	if err != nil {
		return AdminMaintenanceSettings{}, err
	}
	defer rows.Close()
	var latest sql.NullInt64
	for rows.Next() {
		var key, value string
		var updatedAt sql.NullInt64
		if err := rows.Scan(&key, &value, &updatedAt); err != nil {
			return AdminMaintenanceSettings{}, err
		}
		switch key {
		case settingKeyMaintenanceEnabled:
			settings.Enabled = value == "true" || value == "1"
		case settingKeyMaintenanceStateRetentionDays:
			settings.StateRetentionDays = parseMaintenanceDays(value, settings.StateRetentionDays)
		case settingKeyMaintenanceProbeRetentionDays:
			settings.ProbeRetentionDays = parseMaintenanceDays(value, settings.ProbeRetentionDays)
		case settingKeyMaintenanceNotificationRetentionDays:
			settings.NotificationRetentionDays = parseMaintenanceDays(value, settings.NotificationRetentionDays)
		}
		if updatedAt.Valid && (!latest.Valid || updatedAt.Int64 > latest.Int64) {
			latest = updatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return AdminMaintenanceSettings{}, err
	}
	if latest.Valid && latest.Int64 > 0 {
		settings.UpdatedAt = time.Unix(latest.Int64, 0).UTC().Format(time.RFC3339)
	}
	return settings, nil
}

func parseMaintenanceDays(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || !validMaintenanceRetentionDays(parsed) {
		return fallback
	}
	return parsed
}

func (s *SQLiteStore) writeAdminMaintenanceSettings(ctx context.Context, settings AdminMaintenanceSettings) error {
	now := time.Now().UTC().Unix()
	values := map[string]string{
		settingKeyMaintenanceEnabled:                   strconv.FormatBool(settings.Enabled),
		settingKeyMaintenanceStateRetentionDays:        strconv.Itoa(settings.StateRetentionDays),
		settingKeyMaintenanceProbeRetentionDays:        strconv.Itoa(settings.ProbeRetentionDays),
		settingKeyMaintenanceNotificationRetentionDays: strconv.Itoa(settings.NotificationRetentionDays),
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, key, value, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *SQLiteStore) adminMaintenanceCandidates(ctx context.Context, settings AdminMaintenanceSettings) (AdminMaintenanceStats, error) {
	stateCutoff := maintenanceCutoff(settings.StateRetentionDays)
	probeCutoff := maintenanceCutoff(settings.ProbeRetentionDays)
	notificationCutoff := maintenanceCutoff(settings.NotificationRetentionDays)
	var stats AdminMaintenanceStats
	queries := []struct {
		destination *int64
		query       string
		arg         int64
	}{
		{&stats.StateSamples, `SELECT COUNT(*) FROM state_samples WHERE ts < ?`, stateCutoff},
		{&stats.ProbeRounds, `SELECT COUNT(*) FROM probe_rounds WHERE ts < ?`, probeCutoff},
		{&stats.ProbeSamples, `SELECT COUNT(*) FROM probe_samples WHERE round_id IN (SELECT id FROM probe_rounds WHERE ts < ?)`, probeCutoff},
		{&stats.NotificationDeliveries, `SELECT COUNT(*) FROM notification_deliveries WHERE created_at < ?`, notificationCutoff},
	}
	for _, item := range queries {
		if err := s.db.QueryRowContext(ctx, item.query, item.arg).Scan(item.destination); err != nil {
			return AdminMaintenanceStats{}, err
		}
	}
	return stats, nil
}

func (s *SQLiteStore) deleteAdminMaintenanceCandidates(ctx context.Context, settings AdminMaintenanceSettings) (AdminMaintenanceStats, error) {
	stateCutoff := maintenanceCutoff(settings.StateRetentionDays)
	probeCutoff := maintenanceCutoff(settings.ProbeRetentionDays)
	notificationCutoff := maintenanceCutoff(settings.NotificationRetentionDays)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminMaintenanceStats{}, err
	}
	defer rollbackUnlessCommitted(tx)
	var deleted AdminMaintenanceStats
	var affected int64
	if affected, err = execRowsAffected(ctx, tx, `DELETE FROM state_samples WHERE ts < ?`, stateCutoff); err != nil {
		return AdminMaintenanceStats{}, err
	}
	deleted.StateSamples = affected
	if affected, err = execRowsAffected(ctx, tx, `DELETE FROM probe_samples WHERE round_id IN (SELECT id FROM probe_rounds WHERE ts < ?)`, probeCutoff); err != nil {
		return AdminMaintenanceStats{}, err
	}
	deleted.ProbeSamples = affected
	if affected, err = execRowsAffected(ctx, tx, `DELETE FROM probe_rounds WHERE ts < ?`, probeCutoff); err != nil {
		return AdminMaintenanceStats{}, err
	}
	deleted.ProbeRounds = affected
	if affected, err = execRowsAffected(ctx, tx, `DELETE FROM notification_deliveries WHERE created_at < ?`, notificationCutoff); err != nil {
		return AdminMaintenanceStats{}, err
	}
	deleted.NotificationDeliveries = affected
	if err := tx.Commit(); err != nil {
		return AdminMaintenanceStats{}, err
	}
	tx = nil
	return deleted, nil
}

func execRowsAffected(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func maintenanceCutoff(days int) int64 {
	return time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Unix()
}
