package api

import (
	"context"
	"database/sql"
	"log"
	"strings"
	"time"
)

const (
	notificationDeliveryBatchSize   = 32
	notificationDeliveryMaxAttempts = 5
)

type queuedNotificationDelivery struct {
	ID       int64
	Event    notificationEvent
	Channel  notificationDispatchChannel
	Attempts int
}

type notificationOutboxStore interface {
	QueueNotificationEvent(ctx context.Context, event notificationEvent, channels []notificationDispatchChannel) (bool, error)
	PendingNotificationDeliveries(ctx context.Context, now time.Time, limit int) ([]queuedNotificationDelivery, error)
	RecordNotificationDeliveryAttempt(ctx context.Context, delivery queuedNotificationDelivery, sendErr error, now time.Time) error
}

// QueueNotificationEvent persists both the incident claim and its deliveries in
// one transaction. A controller crash after this point cannot silently lose the
// notification; the outbox worker resumes it after restart.
func (s *SQLiteStore) QueueNotificationEvent(ctx context.Context, event notificationEvent, channels []notificationDispatchChannel) (bool, error) {
	if len(channels) == 0 {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer rollbackUnlessCommitted(tx)

	if shouldClaimStatusNotification(event) {
		claimed, err := claimStatusNotificationTx(ctx, tx, event)
		if err != nil || !claimed {
			return false, err
		}
	}

	now := time.Now().UTC().Unix()
	if err := insertNotificationDeliveriesTx(ctx, tx, event, channels, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	tx = nil
	return true, nil
}

func insertNotificationDeliveriesTx(ctx context.Context, tx *sql.Tx, event notificationEvent, channels []notificationDispatchChannel, nowUnix int64) error {
	for _, channel := range channels {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notification_deliveries (
				event_type, label, node_id, node_name, previous_status, status, detail,
				channel_id, channel_name, state, attempts, next_attempt_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?, ?)
		`, event.EventType, event.Label, event.NodeID, event.NodeName, event.PreviousStatus, event.Status,
			event.Detail, channel.ID, channel.Name, nowUnix, nowUnix, nowUnix); err != nil {
			return err
		}
	}
	return nil
}

func enabledNotificationChannelsTx(ctx context.Context, tx *sql.Tx) ([]notificationDispatchChannel, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, name, destination, credential
		FROM notification_channels
		WHERE enabled = 1 AND TRIM(destination) <> '' AND TRIM(credential) <> ''
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := make([]notificationDispatchChannel, 0)
	for rows.Next() {
		var channel notificationDispatchChannel
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.Destination, &channel.Credential); err != nil {
			return nil, err
		}
		channel.Type = "telegram"
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return channels, nil
}

func (s *SQLiteStore) PendingNotificationDeliveries(ctx context.Context, now time.Time, limit int) ([]queuedNotificationDelivery, error) {
	if limit <= 0 || limit > notificationDeliveryBatchSize {
		limit = notificationDeliveryBatchSize
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.event_type, d.label, d.node_id, d.node_name, d.previous_status,
		       d.status, d.detail, d.channel_id, d.channel_name, d.attempts,
		       COALESCE(c.destination, ''), COALESCE(c.credential, ''), COALESCE(c.enabled, 0)
		FROM notification_deliveries d
		LEFT JOIN notification_channels c ON c.id = d.channel_id
		WHERE d.state = 'pending' AND d.next_attempt_at <= ?
		ORDER BY d.next_attempt_at ASC, d.id ASC
		LIMIT ?
	`, now.UTC().Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	deliveries := make([]queuedNotificationDelivery, 0)
	for rows.Next() {
		var delivery queuedNotificationDelivery
		var enabled int
		if err := rows.Scan(&delivery.ID, &delivery.Event.EventType, &delivery.Event.Label,
			&delivery.Event.NodeID, &delivery.Event.NodeName, &delivery.Event.PreviousStatus,
			&delivery.Event.Status, &delivery.Event.Detail, &delivery.Channel.ID,
			&delivery.Channel.Name, &delivery.Attempts, &delivery.Channel.Destination,
			&delivery.Channel.Credential, &enabled); err != nil {
			return nil, err
		}
		delivery.Channel.Type = "telegram"
		if enabled == 0 {
			delivery.Channel.Credential = ""
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return deliveries, nil
}

func (s *SQLiteStore) RecordNotificationDeliveryAttempt(ctx context.Context, delivery queuedNotificationDelivery, sendErr error, now time.Time) error {
	nowUnix := now.UTC().Unix()
	if sendErr == nil {
		_, err := s.db.ExecContext(ctx, `
			UPDATE notification_deliveries
			SET state = 'delivered', attempts = attempts + 1, last_error = '',
			    updated_at = ?, delivered_at = ?
			WHERE id = ? AND state = 'pending'
		`, nowUnix, nowUnix, delivery.ID)
		return err
	}

	attempts := delivery.Attempts + 1
	state := "pending"
	if attempts >= notificationDeliveryMaxAttempts {
		state = "failed"
	}
	delay := notificationRetryDelay(attempts)
	_, err := s.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = ?, attempts = ?, next_attempt_at = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND state = 'pending'
	`, state, attempts, now.Add(delay).UTC().Unix(), sanitizeNotificationDeliveryError(sendErr), nowUnix, delivery.ID)
	return err
}

func notificationRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Second
	case 2:
		return 5 * time.Second
	case 3:
		return 30 * time.Second
	default:
		return 2 * time.Minute
	}
}

func claimStatusNotificationTx(ctx context.Context, tx *sql.Tx, event notificationEvent) (bool, error) {
	eventType := strings.TrimSpace(event.EventType)
	nodeID := strings.TrimSpace(event.NodeID)
	status := strings.TrimSpace(event.Status)
	previousStatus := strings.TrimSpace(event.PreviousStatus)
	if eventType == "" || nodeID == "" || status == "" || previousStatus == status {
		return false, nil
	}
	mark := activeStatusNotificationMark(status)
	clearMark := recoveredStatusNotificationMark(status)
	if status == "online" && previousStatus != "" {
		mark = recoveredStatusNotificationMark(previousStatus)
		clearMark = activeStatusNotificationMark(previousStatus)
		if clearMark == "" {
			return false, nil
		}
		var activeIncident int
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM notification_event_marks
				WHERE event_type = ? AND node_id = ? AND mark = ?
			)
		`, eventType, nodeID, clearMark).Scan(&activeIncident); err != nil {
			return false, err
		}
		if activeIncident == 0 {
			return false, nil
		}
	}
	if mark == "" {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO notification_event_marks (event_type, node_id, mark, created_at)
		VALUES (?, ?, ?, ?)
	`, eventType, nodeID, mark, time.Now().UTC().Unix())
	if err != nil {
		return false, err
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if claimed > 0 && clearMark != "" {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM notification_event_marks
			WHERE event_type = ? AND node_id = ? AND mark = ?
		`, eventType, nodeID, clearMark); err != nil {
			return false, err
		}
	}
	return claimed > 0, nil
}

func (h *handler) runNotificationOutbox(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	h.dispatchPendingNotificationDeliveries(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.dispatchPendingNotificationDeliveries(ctx)
		}
	}
}

func (h *handler) dispatchPendingNotificationDeliveries(ctx context.Context) {
	store, ok := h.store.(notificationOutboxStore)
	if !ok || h.notificationSender == nil {
		return
	}
	h.notificationDrainMu.Lock()
	defer h.notificationDrainMu.Unlock()

	deliveries, err := store.PendingNotificationDeliveries(ctx, time.Now().UTC(), notificationDeliveryBatchSize)
	if err != nil {
		log.Printf("notification outbox fetch failed: %v", err)
		return
	}
	for _, delivery := range deliveries {
		if ctx.Err() != nil {
			return
		}
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var sendErr error
		if delivery.Channel.Credential == "" || delivery.Channel.Destination == "" {
			sendErr = context.Canceled
		} else {
			sendErr = h.notificationSender.Send(sendCtx, delivery.Channel, delivery.Event)
		}
		cancel()
		if err := store.RecordNotificationDeliveryAttempt(ctx, delivery, sendErr, time.Now().UTC()); err != nil {
			log.Printf("notification outbox update failed for delivery %d: %v", delivery.ID, err)
		}
	}
}
