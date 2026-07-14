package api

import (
	"context"
	"log"
	"time"
)

const rawHistoryRetention = 30 * 24 * time.Hour

const stalePendingNotificationDeliveryAfter = 7 * 24 * time.Hour

const historyRetentionBatchSize = 1000

type historyRetentionStore interface {
	PruneRawHistory(ctx context.Context, before time.Time) error
}

// PruneRawHistory removes raw high-frequency samples older than the longest
// supported history range. Probe samples are removed through the round
// foreign-key cascade, preserving current configuration and aggregate state.
func (s *SQLiteStore) PruneRawHistory(ctx context.Context, before time.Time) error {
	cutoff := before.UTC().Unix()
	if err := s.pruneRowsInBatches(ctx, `DELETE FROM probe_rounds WHERE id IN (SELECT id FROM probe_rounds WHERE ts < ? ORDER BY id LIMIT ?)`, cutoff); err != nil {
		return err
	}
	if err := s.pruneRowsInBatches(ctx, `DELETE FROM state_samples WHERE id IN (SELECT id FROM state_samples WHERE ts < ? ORDER BY id LIMIT ?)`, cutoff); err != nil {
		return err
	}
	if err := s.pruneRowsInBatches(ctx, `DELETE FROM notification_deliveries WHERE id IN (SELECT id FROM notification_deliveries WHERE state IN ('delivered', 'failed', 'canceled') AND updated_at < ? ORDER BY id LIMIT ?)`, cutoff); err != nil {
		return err
	}
	stalePendingCutoff := time.Now().UTC().Add(-stalePendingNotificationDeliveryAfter).Unix()
	if err := s.expirePendingNotificationDeliveriesInBatches(ctx, stalePendingCutoff, time.Now().UTC().Unix()); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) pruneRowsInBatches(ctx context.Context, query string, cutoff int64) error {
	for {
		result, err := s.db.ExecContext(ctx, query, cutoff, historyRetentionBatchSize)
		if err != nil {
			return err
		}
		removed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if removed < historyRetentionBatchSize {
			return nil
		}
	}
}

func (s *SQLiteStore) expirePendingNotificationDeliveriesInBatches(ctx context.Context, stalePendingCutoff, now int64) error {
	for {
		result, err := s.db.ExecContext(ctx, `
			UPDATE notification_deliveries
			SET state = 'failed', last_error = 'expired before delivery', lease_until = 0, claim_token = '', updated_at = ?
			WHERE id IN (
				SELECT id FROM notification_deliveries
				WHERE state IN ('pending', 'leased') AND created_at < ?
				ORDER BY id
				LIMIT ?
			)
		`, now, stalePendingCutoff, historyRetentionBatchSize)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated < historyRetentionBatchSize {
			return nil
		}
	}
}

func (h *handler) runHistoryRetention(ctx context.Context, interval time.Duration) {
	store, ok := h.store.(historyRetentionStore)
	if !ok || interval <= 0 {
		return
	}
	prune := func() {
		pruneCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := store.PruneRawHistory(pruneCtx, time.Now().UTC().Add(-rawHistoryRetention)); err != nil {
			log.Printf("history retention cleanup failed: %v", err)
		}
	}
	prune()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}
