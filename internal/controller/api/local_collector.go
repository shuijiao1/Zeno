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
	"io"
	"math"
	"net"
	"net/http"
	"os/exec"
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
		probeRunner = RunLocalProbe
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

func RunLocalProbe(ctx context.Context, target ProbeTarget) ([]probe.Sample, error) {
	switch normalizeProbeTargetForExecution(target).Type {
	case "tcping":
		return RunTCPProbe(ctx, target)
	case "ping":
		return RunPingProbe(ctx, target)
	case "http_get":
		return RunHTTPProbe(ctx, target)
	default:
		return nil, fmt.Errorf("unsupported probe target type %q", target.Type)
	}
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

func RunHTTPProbe(ctx context.Context, target ProbeTarget) ([]probe.Sample, error) {
	target = normalizeProbeTargetForExecution(target)
	if target.Type != "http_get" {
		return nil, fmt.Errorf("target %s is not http_get", target.ID)
	}
	count := target.Count
	if count <= 0 {
		count = 1
	}
	timeout := normalizedLocalProbeTimeout(target.TimeoutMS)
	observationTimeout := localLatencyObservationTimeout(timeout)
	client := &http.Client{Timeout: observationTimeout}
	samples := make([]probe.Sample, 0, count)
	for seq := 1; seq <= count; seq++ {
		select {
		case <-ctx.Done():
			return samples, ctx.Err()
		default:
		}
		requestCtx, cancel := context.WithTimeout(ctx, observationTimeout)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target.Address, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		request.Header.Set("User-Agent", "Zeno-Controller")
		start := time.Now()
		response, err := client.Do(request)
		elapsedMS := float64(time.Since(start).Microseconds()) / 1000
		cancel()
		if err != nil {
			samples = append(samples, failedMeasuredLocalProbeSample(seq, elapsedMS, classifyProbeError(err)))
			continue
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 400 {
			samples = append(samples, failedMeasuredLocalProbeSample(seq, elapsedMS, "http_status"))
			continue
		}
		samples = append(samples, measuredLocalProbeSample(seq, elapsedMS, timeout))
	}
	return samples, nil
}

func RunPingProbe(ctx context.Context, target ProbeTarget) ([]probe.Sample, error) {
	target = normalizeProbeTargetForExecution(target)
	if target.Type != "ping" {
		return nil, fmt.Errorf("target %s is not ping", target.ID)
	}
	count := target.Count
	if count <= 0 {
		count = 1
	}
	timeout := normalizedLocalProbeTimeout(target.TimeoutMS)
	observationTimeout := localLatencyObservationTimeout(timeout)
	samples := make([]probe.Sample, 0, count)
	for seq := 1; seq <= count; seq++ {
		select {
		case <-ctx.Done():
			return samples, ctx.Err()
		default:
		}
		pingCtx, cancel := context.WithTimeout(ctx, observationTimeout)
		start := time.Now()
		output, err := exec.CommandContext(pingCtx, "ping", "-n", "-c", "1", "-W", pingTimeoutSeconds(observationTimeout), "--", target.Address).CombinedOutput()
		elapsedMS := float64(time.Since(start).Microseconds()) / 1000
		cancel()
		if err != nil {
			samples = append(samples, failedMeasuredLocalProbeSample(seq, elapsedMS, classifyProbeError(err)))
			continue
		}
		latency := parsePingLatencyMS(string(output))
		if latency == nil {
			latencyValue := cappedLocalDrawableLatencyMS(elapsedMS)
			latency = &latencyValue
		}
		if time.Duration(*latency*float64(time.Millisecond)) > timeout {
			capped := cappedLocalDrawableLatencyMS(*latency)
			samples = append(samples, probe.Sample{Seq: seq, Success: false, LatencyMS: &capped, Error: "timeout"})
			continue
		}
		capped := cappedLocalDrawableLatencyMS(*latency)
		samples = append(samples, probe.Sample{Seq: seq, Success: true, LatencyMS: &capped})
	}
	return samples, nil
}

func pingTimeoutSeconds(timeout time.Duration) string {
	seconds := int(math.Ceil(timeout.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func parsePingLatencyMS(output string) *float64 {
	marker := "time="
	index := strings.Index(output, marker)
	if index < 0 {
		return nil
	}
	valueStart := index + len(marker)
	valueEnd := valueStart
	for valueEnd < len(output) {
		c := output[valueEnd]
		if (c >= '0' && c <= '9') || c == '.' {
			valueEnd++
			continue
		}
		break
	}
	if valueEnd == valueStart {
		return nil
	}
	value, err := strconv.ParseFloat(output[valueStart:valueEnd], 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return nil
	}
	return &value
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
	return s.withAgentWrite(ctx, func(ctx context.Context) error {
		return s.insertProbeRoundsOnce(ctx, nodeID, rounds)
	})
}

func (s *SQLiteStore) insertProbeRoundsOnce(ctx context.Context, nodeID string, rounds []preparedAgentProbeRound) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	// Acquire SQLite's writer reservation before insertProbeRoundTx performs
	// idempotency reads. Otherwise a concurrent writer can commit between the
	// read and INSERT and make the deferred transaction fail with BUSY_SNAPSHOT.
	if _, err := tx.ExecContext(ctx, `UPDATE probe_config_meta SET version = version WHERE id = 1`); err != nil {
		return err
	}

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
	return s.withAgentWrite(ctx, func(ctx context.Context) error {
		return s.insertAgentProbeResultsOnce(ctx, nodeID, configVersion, rounds)
	})
}

func (s *SQLiteStore) insertAgentProbeResultsOnce(ctx context.Context, nodeID string, configVersion int64, rounds []preparedAgentProbeRound) error {
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

// Four bind parameters are used per row by the set-based VALUES update. Keep
// the batch below SQLite's conservative 999-variable compatibility ceiling.
const probeRoundIdempotencyMigrationBatchSize = 200

type probeRoundIdempotencyBackfill struct {
	id             int64
	idempotencyKey string
	agentRoundID   string
	payloadHash    string
}

func (s *SQLiteStore) migrateProbeRoundIdempotency(ctx context.Context) error {
	indexColumns, err := sqliteIndexColumns(ctx, s.db, "idx_probe_rounds_idempotency")
	if err != nil {
		return err
	}
	indexUnique, err := sqliteIndexUnique(ctx, s.db, "idx_probe_rounds_idempotency")
	if err != nil {
		return err
	}
	agentIndexColumns, err := sqliteIndexColumns(ctx, s.db, "idx_probe_rounds_agent_id")
	if err != nil {
		return err
	}
	agentIndexUnique, err := sqliteIndexUnique(ctx, s.db, "idx_probe_rounds_agent_id")
	if err != nil {
		return err
	}
	var rowsNeedBackfill int
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM probe_rounds
			WHERE idempotency_key = '' OR payload_hash = ''
			   OR (COALESCE(agent_round_id, '') = '' AND idempotency_key GLOB 'agent:*')
			LIMIT 1
		)
	`).Scan(&rowsNeedBackfill); err != nil {
		return err
	}
	duplicateAgentIDs, err := s.probeRoundDuplicateAgentIDsExist(ctx)
	if err != nil {
		return err
	}
	wantColumns := []string{"node_id", "target_id", "ts", "type", "idempotency_key"}
	wantAgentColumns := []string{"node_id", "agent_round_id"}
	if rowsNeedBackfill == 0 && !duplicateAgentIDs && stringSlicesEqual(indexColumns, wantColumns) && indexUnique && stringSlicesEqual(agentIndexColumns, wantAgentColumns) && agentIndexUnique {
		return nil
	}

	// A partially upgraded database can already have the final unique Agent-id
	// index while still containing legacy agent:<id> keys that have not been
	// copied into agent_round_id. Drop the index before bounded data backfill so
	// two conflicting legacy keys can be repaired deterministically instead of
	// making startup fail halfway through. Startup has not exposed the store to
	// request handlers yet, and a restart safely resumes before recreating it.
	if rowsNeedBackfill != 0 && len(agentIndexColumns) != 0 {
		if _, err := s.db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_probe_rounds_agent_id`); err != nil {
			return err
		}
		agentIndexColumns = nil
		agentIndexUnique = false
	}

	// Backfill in bounded batches. Each batch streams one JOIN over its rounds
	// and samples, computes hashes without retaining every sample, then applies a
	// single set-based UPDATE in a short transaction. This avoids the legacy
	// all-rows allocation, one giant write transaction, and per-round N+1 query.
	for rowsNeedBackfill != 0 {
		backfills, err := s.loadProbeRoundIdempotencyBackfillBatch(ctx, probeRoundIdempotencyMigrationBatchSize)
		if err != nil {
			return err
		}
		if len(backfills) == 0 {
			break
		}
		if err := s.applyProbeRoundIdempotencyBackfillBatch(ctx, backfills); err != nil {
			return err
		}
		if err := s.db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM probe_rounds
				WHERE idempotency_key = '' OR payload_hash = ''
				   OR (COALESCE(agent_round_id, '') = '' AND idempotency_key GLOB 'agent:*')
				LIMIT 1
			)
		`).Scan(&rowsNeedBackfill); err != nil {
			return err
		}
	}

	// Older schemas scoped idempotency to target/timestamp and could therefore
	// contain the same Agent round id more than once for a node. Preserve every
	// historical measurement, but keep the oldest row as the canonical Agent-id
	// binding and demote later rows to stable legacy keys. This makes the new
	// node-wide uniqueness rule recoverable and idempotent without deleting data.
	for {
		repaired, err := s.repairProbeRoundDuplicateAgentIDBatch(ctx, probeRoundIdempotencyMigrationBatchSize)
		if err != nil {
			return err
		}
		if repaired == 0 {
			break
		}
	}

	// Index replacement is a separate, short schema transaction after all data
	// batches are durable. A restart can resume the idempotent backfill safely.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if !stringSlicesEqual(indexColumns, wantColumns) || !indexUnique {
		if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_probe_rounds_idempotency`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			CREATE UNIQUE INDEX idx_probe_rounds_idempotency
			ON probe_rounds(node_id, target_id, ts, type, idempotency_key)
		`); err != nil {
			return err
		}
	}
	if !stringSlicesEqual(agentIndexColumns, wantAgentColumns) || !agentIndexUnique {
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
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *SQLiteStore) probeRoundDuplicateAgentIDsExist(ctx context.Context) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM probe_rounds
			WHERE COALESCE(agent_round_id, '') <> ''
			GROUP BY node_id, agent_round_id
			HAVING COUNT(*) > 1
			LIMIT 1
		)
	`).Scan(&exists)
	return exists != 0, err
}

func (s *SQLiteStore) repairProbeRoundDuplicateAgentIDBatch(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH ranked AS (
			SELECT id, payload_hash,
			       ROW_NUMBER() OVER (
			         PARTITION BY node_id, agent_round_id
			         ORDER BY id ASC
			       ) AS duplicate_rank
			FROM probe_rounds
			WHERE COALESCE(agent_round_id, '') <> ''
		)
		SELECT id, payload_hash
		FROM ranked
		WHERE duplicate_rank > 1
		ORDER BY id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return 0, err
	}
	type duplicate struct {
		id          int64
		payloadHash string
	}
	duplicates := make([]duplicate, 0, limit)
	for rows.Next() {
		var candidate duplicate
		if err := rows.Scan(&candidate.id, &candidate.payloadHash); err != nil {
			_ = rows.Close()
			return 0, err
		}
		duplicates = append(duplicates, candidate)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(duplicates) == 0 {
		return 0, nil
	}

	values := make([]string, 0, len(duplicates))
	args := make([]any, 0, len(duplicates)*3)
	for _, candidate := range duplicates {
		values = append(values, "(?, ?, ?)")
		args = append(args, candidate.id, fmt.Sprintf("legacy:%d:%s", candidate.id, candidate.payloadHash), candidate.payloadHash)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackUnlessCommitted(tx)
	statement := `
		WITH repairs(id, idempotency_key, payload_hash) AS (
			VALUES ` + strings.Join(values, ",") + `
		)
		UPDATE probe_rounds
		SET idempotency_key = (SELECT r.idempotency_key FROM repairs r WHERE r.id = probe_rounds.id),
		    agent_round_id = NULL,
		    payload_hash = (SELECT r.payload_hash FROM repairs r WHERE r.id = probe_rounds.id)
		WHERE id IN (SELECT id FROM repairs)
	`
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	tx = nil
	return int(affected), nil
}

func (s *SQLiteStore) loadProbeRoundIdempotencyBackfillBatch(ctx context.Context, limit int) ([]probeRoundIdempotencyBackfill, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH candidates AS (
		SELECT id, idempotency_key, COALESCE(agent_round_id, '') AS agent_round_id
		FROM probe_rounds
		WHERE idempotency_key = '' OR payload_hash = ''
		   OR (COALESCE(agent_round_id, '') = '' AND idempotency_key GLOB 'agent:*')
			ORDER BY id ASC
			LIMIT ?
		)
		SELECT c.id, c.idempotency_key, c.agent_round_id,
		       ps.seq, ps.success, ps.latency_ms, ps.error
		FROM candidates c
		LEFT JOIN probe_samples ps ON ps.round_id = c.id
		ORDER BY c.id ASC, ps.seq ASC
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	backfills := make([]probeRoundIdempotencyBackfill, 0, limit)
	var current probeRoundIdempotencyBackfill
	var digest hash.Hash
	var sampleIndex int
	flush := func() {
		if digest == nil {
			return
		}
		current.payloadHash = hex.EncodeToString(digest.Sum(nil))
		if current.idempotencyKey == "" {
			current.idempotencyKey = fmt.Sprintf("legacy:%d:%s", current.id, current.payloadHash)
		}
		if current.agentRoundID == "" && strings.HasPrefix(current.idempotencyKey, "agent:") {
			candidateAgentRoundID := strings.TrimPrefix(current.idempotencyKey, "agent:")
			if validAgentProbeRoundID(candidateAgentRoundID) {
				current.agentRoundID = candidateAgentRoundID
			} else {
				current.idempotencyKey = fmt.Sprintf("legacy:%d:%s", current.id, current.payloadHash)
			}
		}
		backfills = append(backfills, current)
	}
	for rows.Next() {
		var id int64
		var idempotencyKey, agentRoundID string
		var seq, success sql.NullInt64
		var latency sql.NullFloat64
		var errorText sql.NullString
		if err := rows.Scan(&id, &idempotencyKey, &agentRoundID, &seq, &success, &latency, &errorText); err != nil {
			return nil, err
		}
		if digest == nil || current.id != id {
			flush()
			current = probeRoundIdempotencyBackfill{id: id, idempotencyKey: idempotencyKey, agentRoundID: agentRoundID}
			digest = sha256.New()
			sampleIndex = 0
		}
		if !seq.Valid {
			continue
		}
		sampleIndex++
		sequence := seq.Int64
		if sequence == 0 {
			sequence = int64(sampleIndex)
		}
		writeProbeDigestUint64(digest, uint64(sequence))
		if success.Valid && success.Int64 != 0 {
			writeProbeDigestUint64(digest, 1)
		} else {
			writeProbeDigestUint64(digest, 0)
		}
		if latency.Valid {
			writeProbeDigestUint64(digest, 1)
			writeProbeDigestUint64(digest, math.Float64bits(latency.Float64))
		} else {
			writeProbeDigestUint64(digest, 0)
		}
		errorBytes := []byte(errorText.String)
		writeProbeDigestUint64(digest, uint64(len(errorBytes)))
		_, _ = digest.Write(errorBytes)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	flush()
	return backfills, nil
}

func (s *SQLiteStore) applyProbeRoundIdempotencyBackfillBatch(ctx context.Context, backfills []probeRoundIdempotencyBackfill) error {
	if len(backfills) == 0 {
		return nil
	}
	values := make([]string, 0, len(backfills))
	args := make([]any, 0, len(backfills)*4)
	for _, backfill := range backfills {
		values = append(values, "(?, ?, ?, ?)")
		args = append(args, backfill.id, backfill.idempotencyKey, backfill.agentRoundID, backfill.payloadHash)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	statement := `
		WITH backfill(id, idempotency_key, agent_round_id, payload_hash) AS (
			VALUES ` + strings.Join(values, ",") + `
		)
		UPDATE probe_rounds
		SET idempotency_key = (SELECT b.idempotency_key FROM backfill b WHERE b.id = probe_rounds.id),
		    agent_round_id = NULLIF((SELECT b.agent_round_id FROM backfill b WHERE b.id = probe_rounds.id), ''),
		    payload_hash = (SELECT b.payload_hash FROM backfill b WHERE b.id = probe_rounds.id)
		WHERE id IN (SELECT id FROM backfill)
	`
	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
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

func sqliteIndexUnique(ctx context.Context, db *sql.DB, indexName string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA index_list('probe_rounds')`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			return false, err
		}
		if name == indexName {
			return unique != 0, nil
		}
	}
	return false, rows.Err()
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
