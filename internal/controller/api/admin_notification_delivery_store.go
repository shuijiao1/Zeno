package api

import (
	"context"
	"database/sql"
	"time"
)

const maxAdminNotificationDeliveryLimit = 100

func (s *SQLiteStore) RecordNotificationDelivery(ctx context.Context, event notificationEvent, channel notificationDispatchChannel, success bool, deliveryError string) error {
	successValue := 0
	if success {
		successValue = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_deliveries (
			event_type, label, node_id, node_name, previous_status, status,
			channel_id, channel_name, channel_type, success, error, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.EventType, event.Label, event.NodeID, event.NodeName, event.PreviousStatus, event.Status, channel.ID, channel.Name, channel.Type, successValue, deliveryError, time.Now().UTC().Unix())
	return err
}

func (s *SQLiteStore) AdminNotificationDeliveries(ctx context.Context, limit int) ([]AdminNotificationDelivery, error) {
	if limit <= 0 || limit > maxAdminNotificationDeliveryLimit {
		limit = maxAdminNotificationDeliveryLimit
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_type, label, node_id, node_name, previous_status, status,
		       channel_id, channel_name, channel_type, success, error, created_at
		FROM notification_deliveries
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	deliveries := make([]AdminNotificationDelivery, 0)
	for rows.Next() {
		var delivery AdminNotificationDelivery
		var success int
		var createdAt sql.NullInt64
		if err := rows.Scan(
			&delivery.ID, &delivery.EventType, &delivery.Label, &delivery.NodeID, &delivery.NodeName,
			&delivery.PreviousStatus, &delivery.Status, &delivery.ChannelID, &delivery.ChannelName,
			&delivery.ChannelType, &success, &delivery.Error, &createdAt,
		); err != nil {
			return nil, err
		}
		delivery.Success = success != 0
		delivery.CreatedAt = unixStringOr(createdAt, time.Now().UTC())
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return deliveries, nil
}
