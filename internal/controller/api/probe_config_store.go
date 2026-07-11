package api

import (
	"context"
	"database/sql"
	"time"
)

type probeConfigVersionStore interface {
	BumpProbeConfigVersion(ctx context.Context) (int64, error)
	ProbeConfigVersion(ctx context.Context) (int64, error)
}

func (s *SQLiteStore) BumpProbeConfigVersion(ctx context.Context) (int64, error) {
	if _, err := s.db.ExecContext(ctx, bumpProbeConfigVersionSQL, time.Now().UTC().Unix()); err != nil {
		return 0, err
	}
	return s.ProbeConfigVersion(ctx)
}

const bumpProbeConfigVersionSQL = `
	INSERT INTO probe_config_meta (id, version, updated_at)
	VALUES (1, 1, ?)
	ON CONFLICT(id) DO UPDATE SET
		version = version + 1,
		updated_at = excluded.updated_at
`

func bumpProbeConfigVersionTx(ctx context.Context, tx *sql.Tx) error {
	now := time.Now().UTC().Unix()
	_, err := tx.ExecContext(ctx, bumpProbeConfigVersionSQL, now)
	return err
}

func (s *SQLiteStore) ProbeConfigVersion(ctx context.Context) (int64, error) {
	var version int64
	err := s.db.QueryRowContext(ctx, `SELECT version FROM probe_config_meta WHERE id = 1`).Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}
