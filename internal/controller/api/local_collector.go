package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/shuijiao1/zeno/internal/shared/probe"
)

type ProbeRunner func(ctx context.Context, target ProbeTarget) ([]probe.Sample, error)

const localDrawableLatencyCap = 5 * time.Second

type LocalProbeCollectorOptions struct {
	NodeID      string
	Now         func() time.Time
	ProbeRunner ProbeRunner
}

type LocalProbeCollector struct {
	store       *SQLiteStore
	nodeID      string
	now         func() time.Time
	probeRunner ProbeRunner
}

func NewLocalProbeCollector(store *SQLiteStore, options LocalProbeCollectorOptions) *LocalProbeCollector {
	nodeID := options.NodeID
	if nodeID == "" {
		nodeID = "hytron"
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	probeRunner := options.ProbeRunner
	if probeRunner == nil {
		probeRunner = RunTCPProbe
	}
	return &LocalProbeCollector{store: store, nodeID: nodeID, now: now, probeRunner: probeRunner}
}

func (c *LocalProbeCollector) CollectOnce(ctx context.Context) error {
	if c.store == nil {
		return errors.New("local probe collector requires a SQLite store")
	}
	targets, err := c.store.EnabledProbeTargets(ctx, c.nodeID)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	ts := c.now().UTC()
	var errs []error
	for _, target := range targets {
		samples, err := c.probeRunner(ctx, target)
		if err != nil {
			errs = append(errs, fmt.Errorf("probe %s: %w", target.ID, err))
		}
		if len(samples) == 0 {
			samples = failedProbeSamples(target.Count, "probe_error")
		}
		if err := c.store.InsertProbeRound(ctx, c.nodeID, target, ts, samples); err != nil {
			errs = append(errs, fmt.Errorf("store %s: %w", target.ID, err))
		}
	}
	return errors.Join(errs...)
}

func RunTCPProbe(ctx context.Context, target ProbeTarget) ([]probe.Sample, error) {
	if target.Port == nil {
		return nil, fmt.Errorf("target %s has no TCP port", target.ID)
	}
	target = normalizeProbeTargetForExecution(target)
	count := target.Count
	if count <= 0 {
		count = 1
	}
	timeout := normalizedLocalProbeTimeout(target.TimeoutMS)
	observationTimeout := localLatencyObservationTimeout(timeout)

	address := net.JoinHostPort(target.Address, strconv.Itoa(*target.Port))
	samples := make([]probe.Sample, 0, count)
	for seq := 1; seq <= count; seq++ {
		select {
		case <-ctx.Done():
			return samples, ctx.Err()
		default:
		}

		dialCtx, cancel := context.WithTimeout(ctx, observationTimeout)
		start := time.Now()
		conn, err := (&net.Dialer{Timeout: observationTimeout}).DialContext(dialCtx, "tcp", address)
		elapsedMS := float64(time.Since(start).Microseconds()) / 1000
		cancel()
		if err != nil {
			samples = append(samples, failedMeasuredLocalProbeSample(seq, elapsedMS, classifyProbeError(err)))
			continue
		}
		_ = conn.Close()
		samples = append(samples, measuredLocalProbeSample(seq, elapsedMS, timeout))
	}
	return samples, nil
}

func normalizedLocalProbeTimeout(timeoutMS int) time.Duration {
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout <= 0 {
		return time.Second
	}
	return timeout
}

func localLatencyObservationTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return time.Second
	}
	if timeout > localDrawableLatencyCap {
		return localDrawableLatencyCap
	}
	return timeout
}

func measuredLocalProbeSample(seq int, elapsedMS float64, timeout time.Duration) probe.Sample {
	latency := cappedLocalDrawableLatencyMS(elapsedMS)
	if time.Duration(elapsedMS*float64(time.Millisecond)) > timeout {
		return probe.Sample{Seq: seq, Success: false, LatencyMS: &latency, Error: "timeout"}
	}
	return probe.Sample{Seq: seq, Success: true, LatencyMS: &latency}
}

func failedMeasuredLocalProbeSample(seq int, elapsedMS float64, errText string) probe.Sample {
	if errText != "timeout" {
		return probe.Sample{Seq: seq, Success: false, Error: errText}
	}
	latency := cappedLocalDrawableLatencyMS(elapsedMS)
	return probe.Sample{Seq: seq, Success: false, LatencyMS: &latency, Error: errText}
}

func cappedLocalDrawableLatencyMS(elapsedMS float64) float64 {
	if elapsedMS < 0 {
		return 0
	}
	capMS := float64(localDrawableLatencyCap / time.Millisecond)
	if elapsedMS > capMS {
		return capMS
	}
	return elapsedMS
}

func (s *SQLiteStore) InsertProbeRound(ctx context.Context, nodeID string, target ProbeTarget, ts time.Time, samples []probe.Sample) error {
	return s.InsertProbeRounds(ctx, nodeID, []preparedAgentProbeRound{{target: target, ts: ts, samples: samples}})
}

func (s *SQLiteStore) InsertProbeRounds(ctx context.Context, nodeID string, rounds []preparedAgentProbeRound) error {
	if len(rounds) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	for _, round := range rounds {
		if err := insertProbeRoundTx(ctx, tx, nodeID, round); err != nil {
			return err
		}
	}

	// Probe rounds may be delayed, batched, or use an Agent-provided timestamp.
	// They are service measurements, not authoritative node-liveness updates;
	// heartbeat/state/host reports own last_seen_at and status transitions.

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *SQLiteStore) InsertAgentProbeResults(ctx context.Context, nodeID string, configVersion int64, rounds []preparedAgentProbeRound) error {
	if configVersion < 0 {
		return errInvalidAgentProbeResults
	}
	if len(rounds) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	// Acquire the SQLite writer lock before reading the probe config version and
	// target set. This keeps the non-zero config_version comparison and the full
	// batch insert in one serialized transaction boundary instead of accepting a
	// handler-level pre-read that could race with an admin config change.
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return err
	}
	// configVersion == 0 is the legacy/unknown snapshot value used by older
	// Agents. It intentionally skips stale-version comparison, but the current
	// enabled target set below still validates the whole batch atomically.
	if configVersion > 0 {
		var currentVersion int64
		if err := tx.QueryRowContext(ctx, `SELECT version FROM probe_config_meta WHERE id = 1`).Scan(&currentVersion); err != nil {
			return err
		}
		if currentVersion != configVersion {
			return errAgentProbeConfigStale
		}
	}

	targets, err := enabledProbeTargetsTx(ctx, tx, nodeID)
	if err != nil {
		return err
	}
	targetsByID := make(map[string]ProbeTarget, len(targets))
	for _, target := range targets {
		targetsByID[target.ID] = target
	}
	for _, round := range rounds {
		target, ok := targetsByID[round.targetID]
		if !ok {
			return errInvalidAgentProbeResults
		}
		if round.targetType != "" && round.targetType != target.Type {
			return errInvalidAgentProbeResults
		}
		if len(round.samples) == 0 || len(round.samples) > target.Count || len(round.samples) > maxAgentProbeSamplesPerRound {
			return errInvalidAgentProbeResults
		}
		round.target = target
		round.samples = agentProbeSamplesForTarget(round.samples, target)
		round.payloadHash = probeRoundIdempotencyKey(round.samples)
		round.idempotencyKey = "legacy:" + round.payloadHash
		if strings.TrimSpace(round.agentRoundID) != "" {
			round.idempotencyKey = "agent:" + strings.TrimSpace(round.agentRoundID)
		}
		if err := insertProbeRoundTx(ctx, tx, nodeID, round); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func agentProbeSamplesForTarget(samples []probe.Sample, target ProbeTarget) []probe.Sample {
	normalized := make([]probe.Sample, 0, len(samples))
	effectiveTimeoutMS := target.TimeoutMS
	if effectiveTimeoutMS <= 0 || effectiveTimeoutMS > int(localDrawableLatencyCap/time.Millisecond) {
		effectiveTimeoutMS = int(localDrawableLatencyCap / time.Millisecond)
	}
	for _, sample := range samples {
		copy := sample
		if copy.LatencyMS != nil && *copy.LatencyMS > float64(effectiveTimeoutMS) {
			copy.Success = false
			copy.Error = "timeout"
		}
		normalized = append(normalized, copy)
	}
	return normalized
}

func insertProbeRoundTx(ctx context.Context, tx *sql.Tx, nodeID string, round preparedAgentProbeRound) error {
	stats, err := probe.ComputeStats(round.samples)
	if err != nil {
		return err
	}
	ts := round.ts.UTC().Unix()
	idempotencyKey := strings.TrimSpace(round.idempotencyKey)
	payloadHash := strings.TrimSpace(round.payloadHash)
	if payloadHash == "" {
		payloadHash = probeRoundIdempotencyKey(round.samples)
	}
	if idempotencyKey == "" {
		idempotencyKey = "legacy:" + payloadHash
	}
	agentRoundID := strings.TrimSpace(round.agentRoundID)
	if agentRoundID != "" {
		var existingTargetID, existingType, existingPayloadHash string
		var existingTS int64
		err := tx.QueryRowContext(ctx, agentProbeRoundLookupSQL,
			nodeID, agentRoundID,
		).Scan(&existingTargetID, &existingTS, &existingType, &existingPayloadHash)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			if existingTargetID == round.target.ID && existingTS == ts && existingType == round.target.Type && existingPayloadHash == payloadHash {
				return nil
			}
			return fmt.Errorf("probe round id conflict for node %q", nodeID)
		}
	}
	var existingRoundID int64
	query := `
		SELECT id
		FROM probe_rounds
		WHERE node_id = ? AND target_id = ? AND ts = ? AND type = ? AND idempotency_key = ?
		LIMIT 1
	`
	args := []any{nodeID, round.target.ID, ts, round.target.Type, idempotencyKey}
	if legacyPattern, ok := migratedProbeRoundLegacyRetryPattern(idempotencyKey); ok {
		query = `
			SELECT id
			FROM probe_rounds
			WHERE node_id = ? AND target_id = ? AND ts = ? AND type = ?
			  AND (idempotency_key = ? OR idempotency_key GLOB ?)
			LIMIT 1
		`
		args = append(args, legacyPattern)
	}
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&existingRoundID); err != nil && err != sql.ErrNoRows {
		return err
	} else if err == nil {
		return nil
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, idempotency_key, agent_round_id, payload_hash, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms, error)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nodeID, round.target.ID, ts, round.target.Type, idempotencyKey, agentRoundID, payloadHash, stats.Sent, stats.Received, stats.LossPercent, nullableFloat(stats.MinMS), nullableFloat(stats.AvgMS), nullableFloat(stats.MedianMS), nullableFloat(stats.MaxMS), nullableFloat(stats.StddevMS), roundError(round.samples))
	if err != nil {
		return err
	}
	roundID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for index, sample := range round.samples {
		seq := sample.Seq
		if seq == 0 {
			seq = index + 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO probe_samples (round_id, seq, success, latency_ms, error)
			VALUES (?, ?, ?, ?, ?)
		`, roundID, seq, boolInt(sample.Success), nullableFloat(sample.LatencyMS), nullableString(sample.Error)); err != nil {
			return err
		}
	}
	return nil
}

const agentProbeRoundLookupSQL = `
			SELECT target_id, ts, type, payload_hash
			FROM probe_rounds
			WHERE node_id = ?
			  AND agent_round_id = ?
			  AND agent_round_id IS NOT NULL
			  AND agent_round_id <> ''
			LIMIT 1
`

func probeRoundIdempotencyKey(samples []probe.Sample) string {
	digest := sha256.New()
	for index, sample := range samples {
		seq := sample.Seq
		if seq == 0 {
			seq = index + 1
		}
		writeProbeDigestUint64(digest, uint64(seq))
		if sample.Success {
			writeProbeDigestUint64(digest, 1)
		} else {
			writeProbeDigestUint64(digest, 0)
		}
		if sample.LatencyMS == nil {
			writeProbeDigestUint64(digest, 0)
		} else {
			writeProbeDigestUint64(digest, 1)
			writeProbeDigestUint64(digest, math.Float64bits(*sample.LatencyMS))
		}
		errorText := []byte(sample.Error)
		writeProbeDigestUint64(digest, uint64(len(errorText)))
		_, _ = digest.Write(errorText)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func migratedProbeRoundLegacyKey(roundID int64, samples []probe.Sample) string {
	return fmt.Sprintf("legacy:%d:%s", roundID, probeRoundIdempotencyKey(samples))
}

func migratedProbeRoundLegacyRetryPattern(idempotencyKey string) (string, bool) {
	const legacyPrefix = "legacy:"
	if !strings.HasPrefix(idempotencyKey, legacyPrefix) {
		return "", false
	}
	digest := strings.TrimPrefix(idempotencyKey, legacyPrefix)
	if len(digest) != sha256.Size*2 || !isLowerHex(digest) {
		return "", false
	}
	return legacyPrefix + "[0-9]*:" + digest, true
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') {
			continue
		}
		return false
	}
	return true
}

func writeProbeDigestUint64(digest hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = digest.Write(encoded[:])
}

func (s *SQLiteStore) migrateProbeRoundIdempotency(ctx context.Context) error {
	indexColumns, err := sqliteIndexColumns(ctx, s.db, "idx_probe_rounds_idempotency")
	if err != nil {
		return err
	}
	agentIndexColumns, err := sqliteIndexColumns(ctx, s.db, "idx_probe_rounds_agent_id")
	if err != nil {
		return err
	}
	var rowsNeedingBackfill int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM probe_rounds
		WHERE idempotency_key = '' OR payload_hash = ''
		   OR (agent_round_id IS NULL AND idempotency_key GLOB 'agent:*')
	`).Scan(&rowsNeedingBackfill); err != nil {
		return err
	}
	wantColumns := []string{"node_id", "target_id", "ts", "type", "idempotency_key"}
	wantAgentColumns := []string{"node_id", "agent_round_id"}
	if rowsNeedingBackfill == 0 && stringSlicesEqual(indexColumns, wantColumns) && stringSlicesEqual(agentIndexColumns, wantAgentColumns) {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	rows, err := tx.QueryContext(ctx, `
		SELECT id, idempotency_key, COALESCE(agent_round_id, '')
		FROM probe_rounds
		WHERE idempotency_key = '' OR payload_hash = ''
		   OR (agent_round_id IS NULL AND idempotency_key GLOB 'agent:*')
		ORDER BY id ASC
	`)
	if err != nil {
		return err
	}
	type probeRoundBackfill struct {
		id             int64
		idempotencyKey string
		agentRoundID   string
	}
	var roundsToBackfill []probeRoundBackfill
	for rows.Next() {
		var round probeRoundBackfill
		if err := rows.Scan(&round.id, &round.idempotencyKey, &round.agentRoundID); err != nil {
			_ = rows.Close()
			return err
		}
		roundsToBackfill = append(roundsToBackfill, round)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, round := range roundsToBackfill {
		sampleRows, err := tx.QueryContext(ctx, `
			SELECT seq, success, latency_ms, error
			FROM probe_samples
			WHERE round_id = ?
			ORDER BY seq ASC
		`, round.id)
		if err != nil {
			return err
		}
		var samples []probe.Sample
		for sampleRows.Next() {
			var seq, success int
			var latency sql.NullFloat64
			var errorText sql.NullString
			if err := sampleRows.Scan(&seq, &success, &latency, &errorText); err != nil {
				_ = sampleRows.Close()
				return err
			}
			var latencyMS *float64
			if latency.Valid {
				value := latency.Float64
				latencyMS = &value
			}
			samples = append(samples, probe.Sample{Seq: seq, Success: success != 0, LatencyMS: latencyMS, Error: errorText.String})
		}
		if err := sampleRows.Close(); err != nil {
			return err
		}
		if err := sampleRows.Err(); err != nil {
			return err
		}
		payloadHash := probeRoundIdempotencyKey(samples)
		idempotencyKey := round.idempotencyKey
		if idempotencyKey == "" {
			idempotencyKey = migratedProbeRoundLegacyKey(round.id, samples)
		}
		agentRoundID := round.agentRoundID
		if agentRoundID == "" && strings.HasPrefix(idempotencyKey, "agent:") {
			agentRoundID = strings.TrimPrefix(idempotencyKey, "agent:")
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE probe_rounds
			SET idempotency_key = ?, agent_round_id = NULLIF(?, ''), payload_hash = ?
			WHERE id = ?
		`, idempotencyKey, agentRoundID, payloadHash, round.id); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_probe_rounds_idempotency`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE UNIQUE INDEX idx_probe_rounds_idempotency
		ON probe_rounds(node_id, target_id, ts, type, idempotency_key)
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_probe_rounds_agent_id`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE UNIQUE INDEX idx_probe_rounds_agent_id
		ON probe_rounds(node_id, agent_round_id)
		WHERE agent_round_id IS NOT NULL AND agent_round_id <> ''
	`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func sqliteIndexColumns(ctx context.Context, db *sql.DB, indexName string) ([]string, error) {
	var statement string
	switch indexName {
	case "idx_probe_rounds_idempotency":
		statement = `PRAGMA index_info('idx_probe_rounds_idempotency')`
	case "idx_probe_rounds_agent_id":
		statement = `PRAGMA index_info('idx_probe_rounds_agent_id')`
	default:
		return nil, fmt.Errorf("unsupported sqlite index %q", indexName)
	}
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var sequence, columnID int
		var name string
		if err := rows.Scan(&sequence, &columnID, &name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func failedProbeSamples(count int, errText string) []probe.Sample {
	if count <= 0 {
		count = 1
	}
	samples := make([]probe.Sample, 0, count)
	for seq := 1; seq <= count; seq++ {
		samples = append(samples, probe.Sample{Seq: seq, Success: false, Error: errText})
	}
	return samples
}

func classifyProbeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return "timeout"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "timeout") || strings.Contains(message, "deadline") || strings.Contains(message, "i/o timeout") {
		return "timeout"
	}
	if strings.Contains(message, "no such host") {
		return "dns_error"
	}
	return "connect_error"
}

func roundError(samples []probe.Sample) any {
	for _, sample := range samples {
		if !sample.Success && sample.Error != "" {
			return sample.Error
		}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
