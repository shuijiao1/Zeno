package api

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

var adminNotificationTypeCatalog = []AdminNotificationType{
	{EventType: "node_offline", Label: "离线"},
	{EventType: "probe_unhealthy", Label: "异常"},
	{EventType: "renewal_due", Label: "续费"},
}

func (s *SQLiteStore) AdminNotificationChannels(ctx context.Context) ([]AdminNotificationChannel, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, destination, credential, enabled, created_at, updated_at
		FROM notification_channels
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	channels := make([]AdminNotificationChannel, 0)
	for rows.Next() {
		var channel AdminNotificationChannel
		var credential string
		var enabled int
		var createdAt, updatedAt sql.NullInt64
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.Destination, &credential, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		channel.CredentialSet = strings.TrimSpace(credential) != ""
		channel.Enabled = enabled != 0
		channel.CreatedAt = unixStringOr(createdAt, time.Now().UTC())
		channel.UpdatedAt = unixStringOr(updatedAt, time.Now().UTC())
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return channels, nil
}

func (s *SQLiteStore) CreateAdminNotificationChannel(ctx context.Context, create AdminNotificationChannelCreateRequest) (AdminNotificationChannel, error) {
	if err := create.normalize(); err != nil {
		return AdminNotificationChannel{}, err
	}
	channelID := create.ID
	if channelID == "" {
		generated, err := generatedAdminNodeID(create.Name)
		if err != nil {
			return AdminNotificationChannel{}, err
		}
		channelID = generated
	}
	enabled := 1
	if create.Enabled != nil && !*create.Enabled {
		enabled = 0
	}
	now := time.Now().UTC().Unix()
	credential, err := s.encryptNotificationCredentialForStorage(channelID, "telegram", create.Credential)
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO notification_channels (id, name, destination, credential, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, channelID, create.Name, create.Destination, credential, enabled, now, now)
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	if affected == 0 {
		return AdminNotificationChannel{}, errNotificationChannelAlreadyExists
	}
	return s.adminNotificationChannelByID(ctx, channelID)
}

func (s *SQLiteStore) UpdateAdminNotificationChannel(ctx context.Context, channelID string, update AdminNotificationChannelUpdateRequest) (AdminNotificationChannel, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return AdminNotificationChannel{}, errNotificationChannelNotFound
	}
	if err := update.normalize(); err != nil {
		return AdminNotificationChannel{}, err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM notification_channels WHERE id = ?`, channelID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return AdminNotificationChannel{}, errNotificationChannelNotFound
		}
		return AdminNotificationChannel{}, err
	}
	sets := make([]string, 0, 6)
	args := make([]any, 0, 7)
	if update.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *update.Name)
	}
	if update.Destination != nil {
		sets = append(sets, "destination = ?")
		args = append(args, *update.Destination)
	}
	if update.Credential != nil {
		credential, err := s.encryptNotificationCredentialForStorage(channelID, "telegram", *update.Credential)
		if err != nil {
			return AdminNotificationChannel{}, err
		}
		sets = append(sets, "credential = ?")
		args = append(args, credential)
	}
	if update.Enabled != nil {
		sets = append(sets, "enabled = ?")
		if *update.Enabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if len(sets) == 0 {
		return AdminNotificationChannel{}, errInvalidAdminNotificationChannelWrite
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Unix(), channelID)
	if _, err := s.db.ExecContext(ctx, "UPDATE notification_channels SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		return AdminNotificationChannel{}, err
	}
	return s.adminNotificationChannelByID(ctx, channelID)
}

func (s *SQLiteStore) DeleteAdminNotificationChannel(ctx context.Context, channelID string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return errNotificationChannelNotFound
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM notification_channels WHERE id = ?`, channelID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errNotificationChannelNotFound
	}
	return nil
}

func (s *SQLiteStore) adminNotificationChannelByID(ctx context.Context, channelID string) (AdminNotificationChannel, error) {
	channels, err := s.AdminNotificationChannels(ctx)
	if err != nil {
		return AdminNotificationChannel{}, err
	}
	for _, channel := range channels {
		if channel.ID == channelID {
			return channel, nil
		}
	}
	return AdminNotificationChannel{}, errNotificationChannelNotFound
}

func (s *SQLiteStore) AdminNotificationDispatchChannel(ctx context.Context, channelID string) (notificationDispatchChannel, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return notificationDispatchChannel{}, errNotificationChannelNotFound
	}
	var channel notificationDispatchChannel
	var storedCredential string
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, name, destination, credential
		FROM notification_channels
		WHERE id = ? AND enabled = 1
	`, channelID).Scan(&channel.ID, &channel.Name, &channel.Destination, &storedCredential); err != nil {
		if err == sql.ErrNoRows {
			return notificationDispatchChannel{}, errNotificationChannelNotFound
		}
		return notificationDispatchChannel{}, err
	}
	credential, err := s.decryptNotificationCredentialFromStorage(channel.ID, "telegram", storedCredential)
	if err != nil {
		return notificationDispatchChannel{}, err
	}
	if strings.TrimSpace(credential) == "" {
		return notificationDispatchChannel{}, errInvalidAdminNotificationChannelWrite
	}
	channel.Type = "telegram"
	channel.Credential = credential
	return channel, nil
}

func (s *SQLiteStore) UpdateAdminNotificationType(ctx context.Context, eventType string, update AdminNotificationTypeUpdateRequest) (AdminNotificationType, error) {
	eventType = strings.TrimSpace(eventType)
	if err := update.normalize(); err != nil {
		return AdminNotificationType{}, err
	}
	label, ok := adminNotificationTypeLabel(eventType)
	if !ok {
		return AdminNotificationType{}, errNotificationTypeNotFound
	}
	if eventType != "node_offline" && eventType != "renewal_due" {
		// Resource warnings share probe_unhealthy but are independently managed
		// alert rules. Pretending this legacy endpoint updated that shared event
		// type would be a successful no-op, so require the alert-rules API.
		return AdminNotificationType{}, errNotificationTypeGone
	}
	enabled := 0
	if *update.Enabled {
		enabled = 1
	}
	now := time.Now().UTC().Unix()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_types (event_type, enabled, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(event_type) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at
	`, eventType, enabled, now); err != nil {
		return AdminNotificationType{}, err
	}
	// Compatibility endpoint: only event types that have a one-to-one default
	// alert rule (currently node_offline and renewal_due) update that rule. Do
	// not fan out by notification_event_type; resource rules share
	// probe_unhealthy and must remain independently configurable.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE alert_rules
		SET enabled = ?, updated_at = ?
		WHERE id = ?
	`, enabled, now, eventType); err != nil {
		return AdminNotificationType{}, err
	}
	return AdminNotificationType{EventType: eventType, Label: label, Enabled: *update.Enabled, UpdatedAt: time.Unix(now, 0).UTC().Format(time.RFC3339)}, nil
}

func adminNotificationTypeLabel(eventType string) (string, bool) {
	for _, catalogType := range adminNotificationTypeCatalog {
		if catalogType.EventType == eventType {
			return catalogType.Label, true
		}
	}
	return "", false
}
