package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shuijiao1/zeno/internal/shared/probe"
)

func testF64(v float64) *float64 { return &v }

func TestSQLiteBackedHandlerReturnsPersistedLatencyInsteadOfMock(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at, last_seen_at)
		VALUES ('hytron', 'Hytron', 'hash-for-test', 'online', 'HK', ?, ?, ?);
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO host_info (node_id, os_name, cpu_cores, memory_total_bytes, disk_total_bytes, updated_at)
		VALUES ('hytron', 'debian', 2, 4096, 8192, ?);
	`, now.Unix()); err != nil {
		t.Fatalf("insert host info: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_targets (id, name, type, address, count, timeout_ms, interval_sec, enabled, created_at, updated_at)
		VALUES ('google', 'Google', 'ping', '8.8.8.8', 3, 1000, 60, 1, ?, ?);
	`, now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO node_probe_targets (node_id, target_id, enabled)
		VALUES ('hytron', 'google', 1);
	`); err != nil {
		t.Fatalf("insert node target: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
		VALUES ('hytron', 'google', ?, 'ping', 3, 3, 0, 1.1, 1.3, 1.2, 1.6, 0.2);
	`, now.Unix()); err != nil {
		t.Fatalf("insert round: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/hytron/latency?range=1h", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response LatencyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.NodeID != "hytron" {
		t.Fatalf("node_id = %q, want hytron", response.NodeID)
	}
	if response.Range != "1h" {
		t.Fatalf("range = %q, want 1h", response.Range)
	}
	if len(response.Points) != 1 {
		t.Fatalf("points len = %d, want exactly persisted point; points=%+v", len(response.Points), response.Points)
	}
	point := response.Points[0]
	if point.TargetID != "google" || point.TargetName != "Google" {
		t.Fatalf("target = %q/%q, want google/Google", point.TargetID, point.TargetName)
	}
	if point.MedianMS == nil || *point.MedianMS != 1.2 {
		t.Fatalf("median_ms = %v, want 1.2", point.MedianMS)
	}
	if point.LossPercent != 0 {
		t.Fatalf("loss_percent = %v, want 0", point.LossPercent)
	}
}

func TestSQLiteBackedSummaryUsesPersistedNodeAndLatestLatency(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at, last_seen_at)
		VALUES ('hytron', 'Hytron', 'hash-for-test', 'online', 'HK', ?, ?, ?);
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO host_info (node_id, os_name, arch, cpu_cores, memory_total_bytes, disk_total_bytes, updated_at)
		VALUES ('hytron', 'debian', 'aarch64', 2, 4096, 8192, ?);
	`, now.Unix()); err != nil {
		t.Fatalf("insert host info: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_targets (id, name, type, address, count, timeout_ms, interval_sec, enabled, created_at, updated_at)
		VALUES ('google', 'Google', 'ping', '8.8.8.8', 3, 1000, 60, 1, ?, ?);
	`, now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO node_probe_targets (node_id, target_id, enabled)
		VALUES ('hytron', 'google', 1);
	`); err != nil {
		t.Fatalf("insert node target: %v", err)
	}
	for offset, median := range []float64{9.9, 8.8} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
			VALUES ('hytron', 'google', ?, 'ping', 3, 3, ?, 1.1, ?, ?, 9.9, 0.2);
		`, now.Add(time.Duration(offset-1)*time.Minute).Unix(), float64(offset), median, median); err != nil {
			t.Fatalf("insert round: %v", err)
		}
	}

	handler := NewHandler(HandlerOptions{Store: store})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	raw := recorder.Body.String()
	var summary SummaryResponse
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	var publicShape struct {
		Nodes []struct {
			Arch string `json:"arch"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&publicShape); err != nil {
		t.Fatalf("decode public shape: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	node := summary.Nodes[0]
	if node.DisplayName != "Hytron" || node.OS != "debian" || node.CountryCode != "HK" {
		t.Fatalf("node = %+v, want persisted Hytron card", node)
	}
	if publicShape.Nodes[0].Arch != "aarch64" {
		t.Fatalf("node arch = %q, want persisted host architecture", publicShape.Nodes[0].Arch)
	}
	if node.LatencySummary == nil || node.LatencySummary.MedianMS == nil || *node.LatencySummary.MedianMS != 8.8 {
		t.Fatalf("latency summary = %+v, want latest median 8.8", node.LatencySummary)
	}
	if len(summary.LatencyPoints) != 2 {
		t.Fatalf("summary latency points len = %d, want persisted points", len(summary.LatencyPoints))
	}
	if len(summary.Services) != 1 || summary.Services[0].ID != "google" || summary.Services[0].ReportingNodeCount != 1 || summary.Services[0].MedianMS == nil || *summary.Services[0].MedianMS != 8.8 {
		t.Fatalf("summary services = %+v, want persisted google service status", summary.Services)
	}

	serviceRecorder := httptest.NewRecorder()
	handler.ServeHTTP(serviceRecorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/services/google/latency?range=1h", nil))
	if serviceRecorder.Code != http.StatusOK {
		t.Fatalf("service status = %d, want 200; body=%s", serviceRecorder.Code, serviceRecorder.Body.String())
	}
	var serviceResponse ServiceTargetLatencyResponse
	if err := json.NewDecoder(serviceRecorder.Body).Decode(&serviceResponse); err != nil {
		t.Fatalf("decode service response: %v", err)
	}
	if serviceResponse.Target.ID != "google" || len(serviceResponse.Points) != 2 || serviceResponse.Points[0].NodeID != "hytron" {
		t.Fatalf("service response = %+v, want google points by node", serviceResponse)
	}
}

func TestSQLiteBackedSummaryUsesConfiguredHomeLatencyTarget(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, home_probe_target_id, created_at, updated_at, last_seen_at)
		VALUES ('hytron', 'Hytron', 'hash-for-test', 'online', 'HK', 'cloudflare', ?, ?, ?);
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	for _, target := range []struct {
		id   string
		name string
		addr string
	}{
		{id: "google", name: "Google", addr: "8.8.8.8"},
		{id: "cloudflare", name: "Cloudflare", addr: "1.1.1.1"},
	} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO probe_targets (id, name, type, address, count, timeout_ms, interval_sec, enabled, created_at, updated_at)
			VALUES (?, ?, 'ping', ?, 3, 1000, 60, 1, ?, ?);
		`, target.id, target.name, target.addr, now.Unix(), now.Unix()); err != nil {
			t.Fatalf("insert target %s: %v", target.id, err)
		}
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO node_probe_targets (node_id, target_id, enabled)
			VALUES ('hytron', ?, 1);
		`, target.id); err != nil {
			t.Fatalf("insert node target %s: %v", target.id, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
		VALUES ('hytron', 'cloudflare', ?, 'ping', 3, 3, 0, 1.0, 1.1, 1.2, 1.3, 0.1);
	`, now.Add(-time.Minute).Unix()); err != nil {
		t.Fatalf("insert cloudflare round: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO probe_rounds (node_id, target_id, ts, type, sent, received, loss_percent, min_ms, avg_ms, median_ms, max_ms, stddev_ms)
		VALUES ('hytron', 'google', ?, 'ping', 3, 3, 0, 8.0, 8.5, 8.8, 9.0, 0.2);
	`, now.Unix()); err != nil {
		t.Fatalf("insert google round: %v", err)
	}

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0].LatencySummary == nil {
		t.Fatalf("summary nodes = %+v, want latency summary", summary.Nodes)
	}
	latency := summary.Nodes[0].LatencySummary
	if latency.TargetID != "cloudflare" || latency.TargetName != "Cloudflare" || latency.MedianMS == nil || *latency.MedianMS != 1.2 {
		t.Fatalf("latency summary = %+v, want configured home target cloudflare", latency)
	}
}

func TestSQLiteBackedHandlerReturnsPersistedStateHistory(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for offset, cpu := range []float64{12.5, 18.75} {
		if err := store.InsertAgentState(ctx, "hytron", AgentStateRequest{
			TS:               now.Add(time.Duration(offset-1) * time.Minute).Unix(),
			CPUPercent:       cpu,
			MemoryUsedBytes:  int64(3+offset) * 1024,
			MemoryTotalBytes: 8 * 1024,
			DiskUsedBytes:    int64(40+offset) * 1024,
			DiskTotalBytes:   160 * 1024,
			NetInTotalBytes:  int64(1_000_000 + offset*1000),
			NetOutTotalBytes: int64(2_000_000 + offset*2000),
			NetInSpeedBps:    2048.5 + float64(offset),
			NetOutSpeedBps:   1024.25 + float64(offset),
			UptimeSeconds:    int64(3600 + offset),
		}); err != nil {
			t.Fatalf("insert agent state %d: %v", offset, err)
		}
	}

	recorder := httptest.NewRecorder()
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/hytron/state?range=1h", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	bodyLower := strings.ToLower(recorder.Body.String())
	if strings.Contains(bodyLower, "token") || strings.Contains(bodyLower, "secret") {
		t.Fatalf("public state response leaked sensitive wording: %s", recorder.Body.String())
	}

	var response struct {
		NodeID string `json:"node_id"`
		Range  string `json:"range"`
		Points []struct {
			TS               string   `json:"ts"`
			CPUPercent       *float64 `json:"cpu_percent"`
			MemoryUsedBytes  *float64 `json:"memory_used_bytes"`
			MemoryTotalBytes *float64 `json:"memory_total_bytes"`
			DiskUsedBytes    *float64 `json:"disk_used_bytes"`
			DiskTotalBytes   *float64 `json:"disk_total_bytes"`
			NetInTotalBytes  *float64 `json:"net_in_total_bytes"`
			NetOutTotalBytes *float64 `json:"net_out_total_bytes"`
			NetInSpeedBps    *float64 `json:"net_in_speed_bps"`
			NetOutSpeedBps   *float64 `json:"net_out_speed_bps"`
			UptimeSeconds    *float64 `json:"uptime_seconds"`
		} `json:"points"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode state response: %v", err)
	}
	if response.NodeID != "hytron" || response.Range != "1h" {
		t.Fatalf("state response identity = %q/%q, want hytron/1h", response.NodeID, response.Range)
	}
	if len(response.Points) != 2 {
		t.Fatalf("state points len = %d, want 2", len(response.Points))
	}
	latest := response.Points[1]
	if latest.CPUPercent == nil || *latest.CPUPercent != 18.75 || latest.MemoryUsedBytes == nil || *latest.MemoryUsedBytes != 4*1024 || latest.NetOutSpeedBps == nil || *latest.NetOutSpeedBps != 1025.25 || latest.UptimeSeconds == nil || *latest.UptimeSeconds != 3601 {
		t.Fatalf("latest state point = %+v, want persisted agent values", latest)
	}
}

func TestSQLiteBackedSummaryMarksStaleAgentOffline(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	staleSeenAt := now.Add(-10 * time.Minute).Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO nodes (id, display_name, token_hash, status, country_code, created_at, updated_at, last_seen_at)
		VALUES ('hytron', 'Hytron', 'hash-for-test', 'online', 'HK', ?, ?, ?);
	`, now.Unix(), now.Unix(), staleSeenAt); err != nil {
		t.Fatalf("insert stale node: %v", err)
	}

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	if summary.Nodes[0].Status != "offline" {
		t.Fatalf("stale node status = %q, want offline", summary.Nodes[0].Status)
	}
}

func TestSeedPreviewDataDoesNotFakeAgentOnlineStatus(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(summary.Nodes))
	}
	if summary.Nodes[0].Status != "no_data" {
		t.Fatalf("seed-only node status = %q, want no_data until an agent reports", summary.Nodes[0].Status)
	}

	if err := store.RecordAgentHeartbeat(ctx, "hytron", time.Now().UTC(), "online", "test-agent"); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK"}); err != nil {
		t.Fatalf("seed preview data after heartbeat: %v", err)
	}
	summary, err = store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary after heartbeat: %v", err)
	}
	if summary.Nodes[0].Status != "online" {
		t.Fatalf("node status after heartbeat and reseed = %q, want online", summary.Nodes[0].Status)
	}
}

func TestSeedPreviewDataIsIdempotentAndWiresHytronTargets(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK"}); err != nil {
			t.Fatalf("seed preview data run %d: %v", i+1, err)
		}
	}

	summary, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1 seeded hytron node", len(summary.Nodes))
	}
	if summary.Nodes[0].ID != "hytron" || summary.Nodes[0].DisplayName != "Hytron" || summary.Nodes[0].CountryCode != "HK" {
		t.Fatalf("seeded node = %+v, want hytron/HK", summary.Nodes[0])
	}

	targets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled probe targets: %v", err)
	}
	if len(targets) < 10 {
		t.Fatalf("targets len = %d, want a useful preview target set", len(targets))
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target.ID] {
			t.Fatalf("duplicate target id after idempotent seed: %s", target.ID)
		}
		seen[target.ID] = true
		if target.Type != "tcping" {
			t.Fatalf("target %s type = %q, want tcping for controller-local preview collector", target.ID, target.Type)
		}
		if target.Port == nil || *target.Port <= 0 {
			t.Fatalf("target %s port = %v, want tcp port", target.ID, target.Port)
		}
		if target.Count <= 0 || target.TimeoutMS <= 0 || target.IntervalSec <= 0 {
			t.Fatalf("target %s has invalid probe settings: %+v", target.ID, target)
		}
	}
	for _, id := range []string{"hytron-local", "google-dns", "telegram-dc5", "sharon", "hostdzire", "bage"} {
		if !seen[id] {
			t.Fatalf("missing seeded preview target %q; seen=%v", id, seen)
		}
	}
}

func TestLocalProbeCollectorCollectOnceWritesRealRoundShape(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	collector := NewLocalProbeCollector(store, LocalProbeCollectorOptions{
		NodeID: "hytron",
		Now: func() time.Time {
			return now
		},
		ProbeRunner: func(ctx context.Context, target ProbeTarget) ([]probe.Sample, error) {
			return []probe.Sample{
				{Seq: 1, Success: true, LatencyMS: testF64(10)},
				{Seq: 2, Success: false, Error: "timeout"},
				{Seq: 3, Success: true, LatencyMS: testF64(20)},
			}, nil
		},
	})
	if err := collector.CollectOnce(ctx); err != nil {
		t.Fatalf("collect once: %v", err)
	}

	targets, err := store.EnabledProbeTargets(ctx, "hytron")
	if err != nil {
		t.Fatalf("enabled targets: %v", err)
	}
	latency, err := store.NodeLatency(ctx, "hytron", latencyWindow{Name: "1h", Samples: 36, Step: 2 * time.Minute})
	if err != nil {
		t.Fatalf("node latency: %v", err)
	}
	if len(latency.Points) != len(targets) {
		t.Fatalf("latency points len = %d, want one point per target (%d)", len(latency.Points), len(targets))
	}
	for _, point := range latency.Points {
		if point.MedianMS == nil || *point.MedianMS != 15 {
			t.Fatalf("point %s median = %v, want 15", point.TargetID, point.MedianMS)
		}
		if math.Abs(point.LossPercent-100.0/3.0) > 0.000001 {
			t.Fatalf("point %s loss = %v, want 33.333", point.TargetID, point.LossPercent)
		}
	}

	var sampleRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&sampleRows); err != nil {
		t.Fatalf("count samples: %v", err)
	}
	if sampleRows != len(targets)*3 {
		t.Fatalf("probe sample rows = %d, want %d", sampleRows, len(targets)*3)
	}
}
