package api

import (
	"context"
	"time"
)

type probeConfigVersionStore interface {
	BumpProbeConfigVersion(ctx context.Context) (int64, error)
	ProbeConfigVersion(ctx context.Context) (int64, error)
}

func (s *SQLiteStore) BumpProbeConfigVersion(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Unix()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO probe_config_meta (id, version, updated_at)
		VALUES (1, 1, ?)
		ON CONFLICT(id) DO UPDATE SET
			version = version + 1,
			updated_at = excluded.updated_at
	`, now); err != nil {
		return 0, err
	}
	return s.ProbeConfigVersion(ctx)
}

func (s *SQLiteStore) ProbeConfigVersion(ctx context.Context) (int64, error) {
	var version int64
	err := s.db.QueryRowContext(ctx, `SELECT version FROM probe_config_meta WHERE id = 1`).Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}
