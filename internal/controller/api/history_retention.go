package api

import (
	"context"
	"time"
)

const rawHistoryRetention = 30 * 24 * time.Hour

type historyRetentionStore interface {
	PruneRawHistory(ctx context.Context, before time.Time) error
}

// PruneRawHistory removes raw high-frequency samples older than the longest
// supported history range. Probe samples are removed through the round
// foreign-key cascade, preserving current configuration and aggregate state.
func (s *SQLiteStore) PruneRawHistory(ctx context.Context, before time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	cutoff := before.UTC().Unix()
	if _, err := tx.ExecContext(ctx, `DELETE FROM probe_rounds WHERE ts < ?`, cutoff); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM state_samples WHERE ts < ?`, cutoff); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (h *handler) runHistoryRetention(ctx context.Context, interval time.Duration) {
	store, ok := h.store.(historyRetentionStore)
	if !ok || interval <= 0 {
		return
	}
	prune := func() {
		pruneCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_ = store.PruneRawHistory(pruneCtx, time.Now().UTC().Add(-rawHistoryRetention))
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
