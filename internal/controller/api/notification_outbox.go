package api

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	// One leased delivery is sent at a time. The sender timeout is 5s and the
	// lease is 30s, so serial batch duration cannot consume another row's lease.
	// This also keeps the claim model safe when multiple controller instances are
	// introduced later.
	notificationDeliveryBatchSize   = 1
	notificationDeliveryScanLimit   = 32
	notificationDeliveryMaxAttempts = 5
	notificationDeliveryLease       = 30 * time.Second
)

var errNotificationDeliveryLeaseLost = errors.New("notification delivery lease lost")

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
	now := time.Now().UTC()
	event = notificationEventWithIdentity(event, now)
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

	if err := insertNotificationDeliveriesTx(ctx, tx, event, channels, now.Unix()); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	tx = nil
	return true, nil
}

func insertNotificationDeliveriesTx(ctx context.Context, tx *sql.Tx, event notificationEvent, channels []notificationDispatchChannel, nowUnix int64) error {
	event = notificationEventWithIdentity(event, time.Unix(nowUnix, 0).UTC())
	for _, channel := range channels {
		channel = notificationChannelWithRoutingIdentity(channel)
		predecessorEventID := ""
		if strings.TrimSpace(event.NodeID) != "" {
			err := tx.QueryRowContext(ctx, `
				SELECT event_id
				FROM notification_deliveries
				WHERE channel_id = ? AND channel_version = ?
				  AND destination_fingerprint = ?
				  AND event_type = ? AND node_id = ?
				ORDER BY id DESC
				LIMIT 1
			`, channel.ID, channel.DeliveryVersion, channel.DestinationFingerprint,
				event.EventType, event.NodeID).Scan(&predecessorEventID)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		if notificationEventIsRecovery(event) {
			// A recovery may replace an offline/warning delivery only while that
			// predecessor has never been leased/sent. Once an attempt started, the
			// predecessor must complete first to preserve causal order.
			if _, err := tx.ExecContext(ctx, `
				UPDATE notification_deliveries
				SET state = 'canceled', last_error = 'superseded by recovery',
				    superseded_by_event_id = ?, lease_until = 0, claim_token = '', updated_at = ?
				WHERE channel_id = ? AND channel_version = ?
				  AND destination_fingerprint = ?
				  AND event_type = ? AND node_id = ? AND status = ?
				  AND state IN ('pending', 'paused') AND attempts = 0
			`, event.EventID, nowUnix, channel.ID, channel.DeliveryVersion,
				channel.DestinationFingerprint, event.EventType, event.NodeID,
				event.PreviousStatus); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notification_deliveries (
				event_id, event_type, label, node_id, node_name, node_ip,
				previous_status, status, event_ts, detail,
				channel_id, channel_name, channel_version, destination_fingerprint,
				causal_predecessor_event_id, state, attempts, next_attempt_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?, ?)
		`, event.EventID, event.EventType, event.Label, event.NodeID, event.NodeName, event.NodeIP,
			event.PreviousStatus, event.Status, event.TS, event.Detail, channel.ID, channel.Name,
			channel.DeliveryVersion, channel.DestinationFingerprint, predecessorEventID,
			nowUnix, nowUnix, nowUnix); err != nil {
			return err
		}
	}
	return nil
}

func enabledNotificationChannelsTx(ctx context.Context, tx *sql.Tx) ([]notificationDispatchChannel, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, name, destination, delivery_version, destination_fingerprint
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
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.Destination,
			&channel.DeliveryVersion, &channel.DestinationFingerprint); err != nil {
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
	// Claim a single row so the 5s serial send timeout stays well within the 30s
	// lease. Continue past a bounded number of malformed rows: one bad encrypted
	// credential is terminally isolated and cannot roll back or block the next
	// healthy channel in the claim scan.
	now = now.UTC()
	for scanned := 0; scanned < notificationDeliveryScanLimit; scanned++ {
		delivery, found, quarantined, err := s.claimNextNotificationDelivery(ctx, now)
		if err != nil {
			return nil, err
		}
		if found {
			return []queuedNotificationDelivery{delivery}, nil
		}
		if !quarantined {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("notification delivery quarantine scan limit reached")
}

func (s *SQLiteStore) claimNextNotificationDelivery(ctx context.Context, now time.Time) (queuedNotificationDelivery, bool, bool, error) {
	nowUnix := now.Unix()
	claimToken := notificationClaimToken(now)
	leaseUntil := now.Add(notificationDeliveryLease).Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}
	defer rollbackUnlessCommitted(tx)

	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'pending', lease_until = 0, claim_token = '', updated_at = ?
		WHERE state = 'leased' AND lease_until <= ?
	`, nowUnix, nowUnix); err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}
	// Legacy paused rows may resume only when their immutable routing binding is
	// still the currently enabled channel generation. Disabled, deleted, or
	// changed routes are canceled below and never silently rebound.
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'paused', lease_until = 0, claim_token = '',
		    last_error = 'notification channel disabled', updated_at = ?
		WHERE state = 'pending'
		  AND EXISTS (
		    SELECT 1 FROM notification_channels c
		    WHERE c.id = notification_deliveries.channel_id
		      AND c.enabled = 0
		      AND c.delivery_version = notification_deliveries.channel_version
		      AND c.destination_fingerprint = notification_deliveries.destination_fingerprint
		  )
	`, nowUnix); err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'pending', next_attempt_at = MAX(next_attempt_at, ?),
		    last_error = '', updated_at = ?
		WHERE state = 'paused'
		  AND EXISTS (
		    SELECT 1 FROM notification_channels c
		    WHERE c.id = notification_deliveries.channel_id
		      AND c.enabled = 1
		      AND TRIM(c.destination) <> '' AND TRIM(c.credential) <> ''
		      AND c.delivery_version = notification_deliveries.channel_version
		      AND c.destination_fingerprint = notification_deliveries.destination_fingerprint
		  )
	`, nowUnix, nowUnix); err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'canceled', lease_until = 0, claim_token = '',
		    last_error = 'notification channel changed or unavailable', updated_at = ?
		WHERE state IN ('pending', 'paused', 'failed')
		  AND NOT EXISTS (
		    SELECT 1 FROM notification_channels c
		    WHERE c.id = notification_deliveries.channel_id
		      AND c.delivery_version = notification_deliveries.channel_version
		      AND c.destination_fingerprint = notification_deliveries.destination_fingerprint
		      AND (
		        c.enabled = 0 OR
		        (TRIM(c.destination) <> '' AND TRIM(c.credential) <> '')
		      )
		  )
	`, nowUnix); err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}
	// If a predecessor permanently failed, a later recovery is meaningless to a
	// recipient who never received the incident. Make that recovery terminal
	// rather than allowing it to overtake the failed offline/warning delivery.
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries AS recovery
		SET state = 'canceled', last_error = 'predecessor delivery failed',
		    lease_until = 0, claim_token = '', updated_at = ?
		WHERE recovery.state IN ('pending', 'paused')
		  AND recovery.status = 'online' AND recovery.previous_status <> ''
		  AND TRIM(recovery.causal_predecessor_event_id) <> ''
		  AND EXISTS (
		    SELECT 1 FROM notification_deliveries predecessor
		    WHERE predecessor.channel_id = recovery.channel_id
		      AND predecessor.channel_version = recovery.channel_version
		      AND predecessor.destination_fingerprint = recovery.destination_fingerprint
		      AND predecessor.event_id = recovery.causal_predecessor_event_id
		      AND predecessor.state = 'failed'
		  )
	`, nowUnix); err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}

	var deliveryID int64
	err = tx.QueryRowContext(ctx, `
		SELECT d.id
		FROM notification_deliveries d
		JOIN notification_channels c
		  ON c.id = d.channel_id
		 AND c.delivery_version = d.channel_version
		 AND c.destination_fingerprint = d.destination_fingerprint
		WHERE d.state = 'pending' AND d.next_attempt_at <= ?
		  AND c.enabled = 1
		  AND TRIM(c.destination) <> '' AND TRIM(c.credential) <> ''
		  AND (
		    TRIM(d.causal_predecessor_event_id) = '' OR NOT EXISTS (
		      SELECT 1 FROM notification_deliveries predecessor
		      WHERE predecessor.channel_id = d.channel_id
		        AND predecessor.channel_version = d.channel_version
		        AND predecessor.destination_fingerprint = d.destination_fingerprint
		        AND predecessor.event_id = d.causal_predecessor_event_id
		        AND predecessor.state IN ('pending', 'leased', 'paused', 'failed')
		    )
		  )
		ORDER BY d.next_attempt_at ASC, d.id ASC
		LIMIT 1
	`, nowUnix).Scan(&deliveryID)
	if err == sql.ErrNoRows {
		if err := tx.Commit(); err != nil {
			return queuedNotificationDelivery{}, false, false, err
		}
		tx = nil
		return queuedNotificationDelivery{}, false, false, nil
	}
	if err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'leased', lease_until = ?, claim_token = ?, updated_at = ?
		WHERE id = ? AND state = 'pending'
	`, leaseUntil, claimToken, nowUnix, deliveryID)
	if err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}
	if affected != 1 {
		return queuedNotificationDelivery{}, false, false, errNotificationDeliveryLeaseLost
	}

	var delivery queuedNotificationDelivery
	var storedCredential string
	err = tx.QueryRowContext(ctx, `
		SELECT d.id, d.event_id, d.event_type, d.label, d.node_id, d.node_name,
		       d.node_ip, d.previous_status, d.status, d.event_ts, d.detail,
		       d.channel_id, d.channel_name, d.channel_version,
		       d.destination_fingerprint, d.attempts, c.destination, c.credential
		FROM notification_deliveries d
		JOIN notification_channels c
		  ON c.id = d.channel_id
		 AND c.delivery_version = d.channel_version
		 AND c.destination_fingerprint = d.destination_fingerprint
		WHERE d.id = ? AND d.state = 'leased' AND d.claim_token = ?
		  AND c.enabled = 1
	`, deliveryID, claimToken).Scan(
		&delivery.ID, &delivery.Event.EventID, &delivery.Event.EventType,
		&delivery.Event.Label, &delivery.Event.NodeID, &delivery.Event.NodeName,
		&delivery.Event.NodeIP, &delivery.Event.PreviousStatus, &delivery.Event.Status,
		&delivery.Event.TS, &delivery.Event.Detail, &delivery.Channel.ID,
		&delivery.Channel.Name, &delivery.Channel.DeliveryVersion,
		&delivery.Channel.DestinationFingerprint, &delivery.Attempts,
		&delivery.Channel.Destination, &storedCredential,
	)
	if err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}
	credential, err := s.decryptNotificationCredentialFromStorage(delivery.Channel.ID, "telegram", storedCredential)
	if err != nil {
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE notification_deliveries
			SET state = 'failed', attempts = ?, last_error = 'notification credential unavailable',
			    lease_until = 0, claim_token = '', updated_at = ?
			WHERE id = ? AND state = 'leased' AND claim_token = ?
		`, notificationDeliveryMaxAttempts, nowUnix, delivery.ID, claimToken)
		if updateErr != nil {
			return queuedNotificationDelivery{}, false, false, updateErr
		}
		if updateErr = requireOneNotificationDeliveryRow(result); updateErr != nil {
			return queuedNotificationDelivery{}, false, false, updateErr
		}
		if err := cancelDependentNotificationRecoveriesTx(ctx, tx, delivery, nowUnix); err != nil {
			return queuedNotificationDelivery{}, false, false, err
		}
		if err := tx.Commit(); err != nil {
			return queuedNotificationDelivery{}, false, false, err
		}
		tx = nil
		log.Printf("notification outbox fetch failed-safe quarantine delivery_id=%s event_id=%s event_type=%s node_id=%s channel_id=%s error=credential_unavailable", notificationDeliveryStableID(delivery), notificationEventStableID(delivery.Event), delivery.Event.EventType, delivery.Event.NodeID, delivery.Channel.ID)
		return queuedNotificationDelivery{}, false, true, nil
	}
	delivery.Channel.Type = "telegram"
	delivery.Channel.Credential = credential
	delivery.ClaimToken = claimToken
	if err := tx.Commit(); err != nil {
		return queuedNotificationDelivery{}, false, false, err
	}
	tx = nil
	return delivery, true, false, nil
}

func (s *SQLiteStore) RecordNotificationDeliveryAttempt(ctx context.Context, delivery queuedNotificationDelivery, sendErr error, now time.Time) error {
	nowUnix := now.UTC().Unix()
	if sendErr == nil {
		result, err := s.db.ExecContext(ctx, `
			UPDATE notification_deliveries
			SET state = 'delivered', attempts = attempts + 1, last_error = '',
			    lease_until = 0, claim_token = '', updated_at = ?, delivered_at = ?
			WHERE id = ? AND state = 'leased' AND claim_token = ?
		`, nowUnix, nowUnix, delivery.ID, delivery.ClaimToken)
		if err != nil {
			return err
		}
		return requireOneNotificationDeliveryRow(result)
	}

	attempts := delivery.Attempts + 1
	state := "pending"
	if attempts >= notificationDeliveryMaxAttempts {
		state = "failed"
	}
	delay := notificationRetryDelay(attempts)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	result, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = ?, attempts = ?, next_attempt_at = ?, last_error = ?, lease_until = 0, claim_token = '', updated_at = ?
		WHERE id = ? AND state = 'leased' AND claim_token = ?
	`, state, attempts, now.Add(delay).UTC().Unix(), sanitizeNotificationDeliveryError(sendErr), nowUnix, delivery.ID, delivery.ClaimToken)
	if err != nil {
		return err
	}
	if err := requireOneNotificationDeliveryRow(result); err != nil {
		return err
	}
	if state == "failed" {
		if err := cancelDependentNotificationRecoveriesTx(ctx, tx, delivery, nowUnix); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func requireOneNotificationDeliveryRow(result sql.Result) error {
	if result == nil {
		return errNotificationDeliveryLeaseLost
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errNotificationDeliveryLeaseLost
	}
	return nil
}

func cancelDependentNotificationRecoveriesTx(ctx context.Context, tx *sql.Tx, delivery queuedNotificationDelivery, nowUnix int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'canceled', last_error = 'predecessor delivery failed',
		    lease_until = 0, claim_token = '', updated_at = ?
		WHERE id > ? AND channel_id = ? AND channel_version = ?
		  AND destination_fingerprint = ? AND event_type = ? AND node_id = ?
		  AND causal_predecessor_event_id = ?
		  AND status = 'online' AND previous_status = ?
		  AND state IN ('pending', 'paused')
	`, nowUnix, delivery.ID, delivery.Channel.ID, delivery.Channel.DeliveryVersion,
		delivery.Channel.DestinationFingerprint, delivery.Event.EventType,
		delivery.Event.NodeID, delivery.Event.EventID, delivery.Event.Status)
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
			JOIN notification_channels c
			  ON c.id = d.channel_id
			 AND c.delivery_version = d.channel_version
			 AND c.destination_fingerprint = d.destination_fingerprint
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

func notificationDestinationFingerprint(channelType, destination string) string {
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if channelType == "" {
		channelType = "telegram"
	}
	sum := sha256.Sum256([]byte(channelType + "\x00" + strings.TrimSpace(destination)))
	return hex.EncodeToString(sum[:])
}

func notificationChannelWithRoutingIdentity(channel notificationDispatchChannel) notificationDispatchChannel {
	if channel.DeliveryVersion < 1 {
		channel.DeliveryVersion = 1
	}
	channel.DestinationFingerprint = notificationDestinationFingerprint(channel.Type, channel.Destination)
	return channel
}

func notificationEventWithIdentity(event notificationEvent, fallback time.Time) notificationEvent {
	if strings.TrimSpace(event.TS) == "" {
		event.TS = fallback.UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(event.EventID) == "" {
		parts := []string{
			strings.TrimSpace(event.EventType),
			strings.TrimSpace(event.NodeID),
			strings.TrimSpace(event.NodeName),
			strings.TrimSpace(event.NodeIP),
			strings.TrimSpace(event.PreviousStatus),
			strings.TrimSpace(event.Status),
			strings.TrimSpace(event.TS),
			strings.TrimSpace(event.Detail),
		}
		sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
		event.EventID = hex.EncodeToString(sum[:16])
	}
	return event
}

func notificationEventIsRecovery(event notificationEvent) bool {
	return strings.TrimSpace(event.NodeID) != "" &&
		strings.TrimSpace(event.Status) == "online" &&
		strings.TrimSpace(event.PreviousStatus) != "" &&
		strings.TrimSpace(event.PreviousStatus) != "online"
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
	for drained := 0; drained < notificationDeliveryScanLimit; drained++ {
		deliveries, err := store.PendingNotificationDeliveries(ctx, time.Now().UTC(), notificationDeliveryBatchSize)
		if err != nil {
			log.Printf("notification outbox fetch failed: %v", err)
			return
		}
		if len(deliveries) == 0 {
			return
		}
		// The SQLite implementation returns one row because that is the lease
		// safety contract. Iterate defensively for test/alternate stores that may
		// return more than the requested limit.
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
			// Recording the outcome is the acknowledgement boundary. Delivery is
			// intentionally at-least-once: a process crash after the remote accepted
			// the stable event_id but before this update can cause a retry. Receivers
			// should deduplicate on event_id when their protocol supports it.
			if err := store.RecordNotificationDeliveryAttempt(ctx, delivery, sendErr, time.Now().UTC()); err != nil {
				log.Printf("notification outbox update failed delivery_id=%s event_id=%s event_type=%s node_id=%s channel_id=%s: %v", notificationDeliveryStableID(delivery), notificationEventStableID(delivery.Event), delivery.Event.EventType, delivery.Event.NodeID, delivery.Channel.ID, err)
				// Do not tight-loop a delivery whose acknowledgement failed. Its lease
				// provides the retry boundary and prevents an immediate duplicate.
				return
			}
			if sendErr != nil {
				log.Printf("notification delivery failed delivery_id=%s event_id=%s event_type=%s node_id=%s channel_id=%s attempt=%d error=%s", notificationDeliveryStableID(delivery), notificationEventStableID(delivery.Event), delivery.Event.EventType, delivery.Event.NodeID, delivery.Channel.ID, delivery.Attempts+1, sanitizeNotificationDeliveryError(sendErr))
			}
		}
	}
	// Keep draining without leasing a serial batch whose tail could expire.
	h.wakeNotificationOutbox()
}

func notificationEventStableID(event notificationEvent) string {
	if eventID := strings.TrimSpace(event.EventID); eventID != "" {
		return eventID
	}
	parts := []string{
		strings.TrimSpace(event.EventType),
		strings.TrimSpace(event.NodeID),
		strings.TrimSpace(event.TS),
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
