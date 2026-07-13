package api

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	notificationDeliveryBatchSize   = 32
	notificationDeliveryMaxAttempts = 5
	notificationDeliveryLease       = 30 * time.Second
)

type queuedNotificationDelivery struct {
	ID         int64
	Event      notificationEvent
	Channel    notificationDispatchChannel
	Attempts   int
	ClaimToken string
}

type notificationOutboxStore interface {
	QueueNotificationEvent(ctx context.Context, event notificationEvent, channels []notificationDispatchChannel) (bool, error)
	PendingNotificationDeliveries(ctx context.Context, now time.Time, limit int) ([]queuedNotificationDelivery, error)
	RecordNotificationDeliveryAttempt(ctx context.Context, delivery queuedNotificationDelivery, sendErr error, now time.Time) error
}

type notificationOutboxWorker struct {
	wake    chan struct{}
	mu      sync.Mutex
	running bool
}

func (h *handler) notificationOutboxWorker() *notificationOutboxWorker {
	if h == nil {
		return nil
	}
	h.notificationWorkerMu.Lock()
	defer h.notificationWorkerMu.Unlock()
	if h.backgroundCtx == nil {
		h.backgroundCtx, h.backgroundCancel = context.WithCancel(context.Background())
	}
	if h.notificationWorker == nil {
		h.notificationWorker = &notificationOutboxWorker{wake: make(chan struct{}, 1)}
	}
	return h.notificationWorker
}

func (h *handler) wakeNotificationOutbox() {
	worker := h.notificationOutboxWorker()
	if worker == nil {
		return
	}
	select {
	case worker.wake <- struct{}{}:
	default:
	}
	h.ensureNotificationOutboxWorker(0)
}

func (h *handler) ensureNotificationOutboxWorker(interval time.Duration) {
	worker := h.notificationOutboxWorker()
	if worker == nil || h.backgroundContext().Err() != nil {
		return
	}
	worker.mu.Lock()
	if worker.running {
		worker.mu.Unlock()
		return
	}
	worker.running = true
	worker.mu.Unlock()
	h.startBackground(func(ctx context.Context) { h.runNotificationOutboxLoop(ctx, interval, worker) })
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
		SELECT id, name, destination
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
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.Destination); err != nil {
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

func enabledNotificationChannelsForEventTx(ctx context.Context, tx *sql.Tx, eventType, nodeID string) (string, []notificationDispatchChannel, error) {
	label, ok := adminNotificationTypeLabel(eventType)
	if !ok {
		return "", nil, errNotificationTypeNotFound
	}
	enabled, err := notificationEventEnabledTx(ctx, tx, eventType, nodeID)
	if err != nil || !enabled {
		return label, nil, err
	}
	channels, err := enabledNotificationChannelsTx(ctx, tx)
	if err != nil {
		return "", nil, err
	}
	return label, channels, nil
}

func notificationEventEnabledTx(ctx context.Context, tx *sql.Tx, eventType, nodeID string) (bool, error) {
	var enabledRuleCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM alert_rules ar
		WHERE ar.notification_event_type = ?
		  AND ar.enabled = 1
		  AND (
		    ? = ''
		    OR NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		    OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ?)
		  )
	`, eventType, strings.TrimSpace(nodeID), strings.TrimSpace(nodeID)).Scan(&enabledRuleCount); err != nil {
		return false, err
	}
	return enabledRuleCount > 0, nil
}

func (s *SQLiteStore) PendingNotificationDeliveries(ctx context.Context, now time.Time, limit int) ([]queuedNotificationDelivery, error) {
	if limit <= 0 || limit > notificationDeliveryBatchSize {
		limit = notificationDeliveryBatchSize
	}
	now = now.UTC()
	nowUnix := now.Unix()
	claimToken := notificationClaimToken(now)
	leaseUntil := now.Add(notificationDeliveryLease).Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackUnlessCommitted(tx)

	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'pending', lease_until = 0, claim_token = '', updated_at = ?
		WHERE state = 'leased' AND lease_until <= ?
	`, nowUnix, nowUnix); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'pending', next_attempt_at = CASE WHEN next_attempt_at > ? THEN next_attempt_at ELSE ? END,
		    last_error = '', updated_at = ?
		WHERE state = 'paused'
		  AND EXISTS (
		    SELECT 1 FROM notification_channels c
		    WHERE c.id = notification_deliveries.channel_id
		      AND c.enabled = 1
		      AND TRIM(c.destination) <> ''
		      AND TRIM(c.credential) <> ''
		  )
	`, nowUnix, nowUnix, nowUnix); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'paused', lease_until = 0, claim_token = '', last_error = 'notification channel disabled', updated_at = ?
		WHERE state = 'pending'
		  AND NOT EXISTS (
		    SELECT 1 FROM notification_channels c
		    WHERE c.id = notification_deliveries.channel_id
		      AND c.enabled = 1
		      AND TRIM(c.destination) <> ''
		      AND TRIM(c.credential) <> ''
		  )
	`, nowUnix); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT d.id
		FROM notification_deliveries d
		JOIN notification_channels c ON c.id = d.channel_id
		WHERE d.state = 'pending'
		  AND d.next_attempt_at <= ?
		  AND c.enabled = 1
		  AND TRIM(c.destination) <> ''
		  AND TRIM(c.credential) <> ''
		ORDER BY d.next_attempt_at ASC, d.id ASC
		LIMIT ?
	`, nowUnix, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return nil, nil
	}
	placeholders := sqlitePlaceholders(len(ids))
	args := make([]any, 0, len(ids)+4)
	args = append(args, leaseUntil, claimToken, nowUnix)
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'leased', lease_until = ?, claim_token = ?, updated_at = ?
		WHERE state = 'pending' AND id IN (`+placeholders+`)
	`, args...); err != nil {
		return nil, err
	}

	rows, err = tx.QueryContext(ctx, `
		SELECT d.id, d.event_type, d.label, d.node_id, d.node_name, d.previous_status,
		       d.status, d.detail, d.channel_id, d.channel_name, d.attempts,
		       COALESCE(c.destination, ''), COALESCE(c.credential, '')
		FROM notification_deliveries d
		JOIN notification_channels c ON c.id = d.channel_id
		WHERE d.state = 'leased' AND d.claim_token = ?
		  AND c.enabled = 1
		  AND TRIM(c.destination) <> ''
		  AND TRIM(c.credential) <> ''
		ORDER BY d.next_attempt_at ASC, d.id ASC
	`, claimToken)
	if err != nil {
		return nil, err
	}

	deliveries := make([]queuedNotificationDelivery, 0)
	for rows.Next() {
		var delivery queuedNotificationDelivery
		var storedCredential string
		if err := rows.Scan(&delivery.ID, &delivery.Event.EventType, &delivery.Event.Label,
			&delivery.Event.NodeID, &delivery.Event.NodeName, &delivery.Event.PreviousStatus,
			&delivery.Event.Status, &delivery.Event.Detail, &delivery.Channel.ID,
			&delivery.Channel.Name, &delivery.Attempts, &delivery.Channel.Destination,
			&storedCredential); err != nil {
			_ = rows.Close()
			return nil, err
		}
		credential, err := s.decryptNotificationCredentialFromStorage(delivery.Channel.ID, "telegram", storedCredential)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		delivery.Channel.Type = "telegram"
		delivery.Channel.Credential = credential
		delivery.ClaimToken = claimToken
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return deliveries, nil
}

func (s *SQLiteStore) RecordNotificationDeliveryAttempt(ctx context.Context, delivery queuedNotificationDelivery, sendErr error, now time.Time) error {
	nowUnix := now.UTC().Unix()
	if sendErr == nil {
		_, err := s.db.ExecContext(ctx, `
			UPDATE notification_deliveries
			SET state = 'delivered', attempts = attempts + 1, last_error = '',
			    lease_until = 0, claim_token = '', updated_at = ?, delivered_at = ?
			WHERE id = ? AND state = 'leased' AND claim_token = ?
		`, nowUnix, nowUnix, delivery.ID, delivery.ClaimToken)
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
		SET state = ?, attempts = ?, next_attempt_at = ?, last_error = ?, lease_until = 0, claim_token = '', updated_at = ?
		WHERE id = ? AND state = 'leased' AND claim_token = ?
	`, state, attempts, now.Add(delay).UTC().Unix(), sanitizeNotificationDeliveryError(sendErr), nowUnix, delivery.ID, delivery.ClaimToken)
	return err
}

func (s *SQLiteStore) ReplayFailedNotificationDeliveries(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > notificationDeliveryBatchSize {
		limit = notificationDeliveryBatchSize
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'pending', next_attempt_at = ?, lease_until = 0, claim_token = '', last_error = '', updated_at = ?
		WHERE id IN (
			SELECT d.id
			FROM notification_deliveries d
			JOIN notification_channels c ON c.id = d.channel_id
			WHERE d.state = 'failed'
			  AND c.enabled = 1
			  AND TRIM(c.destination) <> ''
			  AND TRIM(c.credential) <> ''
			ORDER BY d.updated_at ASC, d.id ASC
			LIMIT ?
		)
	`, now.UTC().Unix(), now.UTC().Unix(), limit)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

func notificationClaimToken(now time.Time) string {
	var random [16]byte
	if _, err := cryptorand.Read(random[:]); err == nil {
		return fmt.Sprintf("claim:%d:%s", now.UnixNano(), hex.EncodeToString(random[:]))
	}
	return fmt.Sprintf("claim:%d", now.UnixNano())
}

func sqlitePlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
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

func (h *handler) runNotificationOutboxLoop(ctx context.Context, interval time.Duration, worker *notificationOutboxWorker) {
	defer func() {
		worker.mu.Lock()
		worker.running = false
		pending := len(worker.wake) > 0
		worker.mu.Unlock()
		if pending && h.backgroundContext().Err() == nil {
			h.ensureNotificationOutboxWorker(interval)
		}
	}()
	if interval <= 0 {
		for {
			// The wake that created this worker is already represented by the
			// dispatch below. Drain it first so one event does not cause two scans.
			select {
			case <-worker.wake:
			default:
			}
			h.dispatchPendingNotificationDeliveries(ctx)
			idle := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				if !idle.Stop() {
					<-idle.C
				}
				return
			case <-worker.wake:
				if !idle.Stop() {
					<-idle.C
				}
				continue
			case <-idle.C:
				return
			}
		}
	}
	h.dispatchPendingNotificationDeliveries(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-worker.wake:
			h.dispatchPendingNotificationDeliveries(ctx)
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
	if ctx.Err() != nil || !h.automaticNotificationsAllowed() {
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
			log.Printf("notification outbox update failed delivery_id=%s event_id=%s event_type=%s node_id=%s channel_id=%s: %v", notificationDeliveryStableID(delivery), notificationEventStableID(delivery.Event), delivery.Event.EventType, delivery.Event.NodeID, delivery.Channel.ID, err)
			continue
		}
		if sendErr != nil {
			log.Printf("notification delivery failed delivery_id=%s event_id=%s event_type=%s node_id=%s channel_id=%s attempt=%d error=%s", notificationDeliveryStableID(delivery), notificationEventStableID(delivery.Event), delivery.Event.EventType, delivery.Event.NodeID, delivery.Channel.ID, delivery.Attempts+1, sanitizeNotificationDeliveryError(sendErr))
		}
	}
}

func notificationEventStableID(event notificationEvent) string {
	parts := []string{
		strings.TrimSpace(event.EventType),
		strings.TrimSpace(event.NodeID),
		strings.TrimSpace(event.PreviousStatus),
		strings.TrimSpace(event.Status),
		strings.TrimSpace(event.Detail),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func notificationDeliveryStableID(delivery queuedNotificationDelivery) string {
	if delivery.ID > 0 {
		return fmt.Sprintf("outbox:%d", delivery.ID)
	}
	parts := []string{notificationEventStableID(delivery.Event), strings.TrimSpace(delivery.Channel.ID)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "outbox:" + hex.EncodeToString(sum[:8])
}
