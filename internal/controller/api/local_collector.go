package api

import (
	"context"
	"errors"
	"fmt"
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
	if timeout < localDrawableLatencyCap {
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
	stats, err := probe.ComputeStats(samples)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	result, err := tx.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nodeID, target.ID, ts.UTC().Unix(), target.Type, stats.Sent, stats.Received, stats.LossPercent, nullableFloat(stats.MinMS), nullableFloat(stats.AvgMS), nullableFloat(stats.MedianMS), nullableFloat(stats.MaxMS), nullableFloat(stats.StddevMS), roundError(samples))
	if err != nil {
		return err
	}
	roundID, err := result.LastInsertId()
	if err != nil {
		return err
	}

	for index, sample := range samples {
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

	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = 'online', last_seen_at = ?, updated_at = ?
		WHERE id = ?
	`, ts.UTC().Unix(), now, nodeID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
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
