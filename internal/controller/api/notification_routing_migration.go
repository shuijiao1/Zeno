package api

import (
	"context"
	"fmt"
	"strings"
)

// migrateNotificationRoutingBindings gives new rows immutable routing metadata.
// A legacy row never recorded its original destination or credential generation;
// binding it to today's mutable channel would invent evidence and could disclose
// an old notification to a new recipient. Active legacy rows are therefore
// canceled fail-closed while their historical metadata is backfilled.
func (s *SQLiteStore) migrateNotificationRoutingBindings(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store is closed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	rows, err := tx.QueryContext(ctx, `
		SELECT id, destination, delivery_version, destination_fingerprint
		FROM notification_channels
		WHERE delivery_version < 1 OR TRIM(destination_fingerprint) = ''
		ORDER BY id ASC
	`)
	if err != nil {
		return err
	}
	type channelBinding struct {
		id          string
		destination string
		version     int64
		fingerprint string
	}
	bindings := make([]channelBinding, 0)
	for rows.Next() {
		var binding channelBinding
		if err := rows.Scan(&binding.id, &binding.destination, &binding.version, &binding.fingerprint); err != nil {
			_ = rows.Close()
			return err
		}
		if binding.version < 1 {
			binding.version = 1
		}
		if strings.TrimSpace(binding.fingerprint) == "" {
			binding.fingerprint = notificationDestinationFingerprint("telegram", binding.destination)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, binding := range bindings {
		result, err := tx.ExecContext(ctx, `
			UPDATE notification_channels
			SET delivery_version = ?, destination_fingerprint = ?
			WHERE id = ? AND (delivery_version < 1 OR TRIM(destination_fingerprint) = '')
		`, binding.version, binding.fingerprint, binding.id)
		if err != nil {
			return err
		}
		if _, err := result.RowsAffected(); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'canceled',
		    last_error = 'legacy notification route unverifiable',
		    lease_until = 0,
		    claim_token = ''
		WHERE state IN ('pending', 'paused', 'failed', 'leased')
		  AND (channel_version < 1 OR TRIM(destination_fingerprint) = '')
	`); err != nil {
		return err
	}

	// A legacy row had no event timestamp. created_at is the closest durable
	// event-time evidence available and is retained rather than replaced with the
	// upgrade time. The id-derived value remains stable across retries/restarts.
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET event_id = CASE
				WHEN TRIM(event_id) = '' THEN 'legacy:' || printf('%016x', id)
				ELSE event_id
			END,
			event_ts = CASE
				WHEN TRIM(event_ts) = '' THEN strftime('%Y-%m-%dT%H:%M:%SZ', created_at, 'unixepoch')
				ELSE event_ts
			END,
			channel_version = CASE
				WHEN channel_version < 1 THEN COALESCE((
					SELECT c.delivery_version FROM notification_channels c
					WHERE c.id = notification_deliveries.channel_id
				), 1)
				ELSE channel_version
			END,
			destination_fingerprint = CASE
				WHEN TRIM(destination_fingerprint) = '' THEN COALESCE((
					SELECT c.destination_fingerprint FROM notification_channels c
					WHERE c.id = notification_deliveries.channel_id
				), '')
				ELSE destination_fingerprint
			END
		WHERE TRIM(event_id) = '' OR TRIM(event_ts) = '' OR channel_version < 1
		   OR TRIM(destination_fingerprint) = ''
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		WITH ordered AS (
			SELECT id,
			       LAG(event_id) OVER (
			         PARTITION BY channel_id, channel_version, destination_fingerprint,
			                      node_id, event_type
			         ORDER BY id ASC
			       ) AS predecessor_event_id
			FROM notification_deliveries
			WHERE TRIM(node_id) <> ''
		)
		UPDATE notification_deliveries
		SET causal_predecessor_event_id = COALESCE((
			SELECT ordered.predecessor_event_id FROM ordered
			WHERE ordered.id = notification_deliveries.id
		), '')
		WHERE TRIM(causal_predecessor_event_id) = ''
		  AND id IN (SELECT id FROM ordered WHERE predecessor_event_id IS NOT NULL)
	`); err != nil {
		return err
	}

	// Rows whose channel disappeared before the upgrade must be terminal rather
	// than silently becoming eligible if a new channel later reuses the same id.
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'canceled', last_error = 'notification channel unavailable',
		    lease_until = 0, claim_token = ''
		WHERE state IN ('pending', 'paused', 'failed', 'leased')
		  AND NOT EXISTS (
			SELECT 1 FROM notification_channels c
			WHERE c.id = notification_deliveries.channel_id
		  )
	`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}
