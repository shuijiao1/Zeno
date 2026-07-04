package api

import (
	"context"
	"database/sql"
	"time"
)

const maxAdminNotificationDeliveryLimit = 100

func (s *SQLiteStore) RecordNotificationDelivery(ctx context.Context, event notificationEvent, channel notificationDispatchChannel, success bool, deliveryError string) (AdminNotificationDelivery, error) {
	successValue := 0
	if success {
		successValue = 1
	}
	channelType := channel.Type
	if channelType == "" {
		channelType = "telegram"
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_deliveries (
			event_type, label, node_id, node_name, previous_status, status,
			channel_id, channel_name, channel_type, success, error, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.EventType, event.Label, event.NodeID, event.NodeName, event.PreviousStatus, event.Status, channel.ID, channel.Name, channelType, successValue, deliveryError, time.Now().UTC().Unix())
	if err != nil {
		return AdminNotificationDelivery{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AdminNotificationDelivery{}, err
	}
	return s.adminNotificationDeliveryByID(ctx, id)
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
		delivery, err := scanAdminNotificationDelivery(rows)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return deliveries, nil
}

func (s *SQLiteStore) adminNotificationDeliveryByID(ctx context.Context, id int64) (AdminNotificationDelivery, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, event_type, label, node_id, node_name, previous_status, status,
		       channel_id, channel_name, channel_type, success, error, created_at
		FROM notification_deliveries
		WHERE id = ?
	`, id)
	return scanAdminNotificationDelivery(row)
}

type adminNotificationDeliveryScanner interface {
	Scan(dest ...any) error
}

func scanAdminNotificationDelivery(scanner adminNotificationDeliveryScanner) (AdminNotificationDelivery, error) {
	var delivery AdminNotificationDelivery
	var channelType string
	var success int
	var createdAt sql.NullInt64
	if err := scanner.Scan(
		&delivery.ID, &delivery.EventType, &delivery.Label, &delivery.NodeID, &delivery.NodeName,
		&delivery.PreviousStatus, &delivery.Status, &delivery.ChannelID, &delivery.ChannelName,
		&channelType, &success, &delivery.Error, &createdAt,
	); err != nil {
		return AdminNotificationDelivery{}, err
	}
	delivery.Success = success != 0
	delivery.CreatedAt = unixStringOr(createdAt, time.Now().UTC())
	return delivery, nil
}
