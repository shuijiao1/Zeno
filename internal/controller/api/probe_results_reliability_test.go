package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shuijiao1/zeno/internal/shared/probe"
)

type probePostCommitFaultStore struct {
	*SQLiteStore
	mu       sync.Mutex
	failNext bool
}

func (store *probePostCommitFaultStore) InsertAgentProbeResults(ctx context.Context, nodeID string, configVersion int64, rounds []preparedAgentProbeRound) (agentProbeInsertResult, error) {
	result, err := store.SQLiteStore.InsertAgentProbeResults(ctx, nodeID, configVersion, rounds)
	if err != nil {
		return result, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failNext {
		store.failNext = false
		return result, errors.New("injected response-path failure after commit")
	}
	return result, nil
}

type probeBlockingFaultStore struct {
	*SQLiteStore
}

func (store *probeBlockingFaultStore) InsertAgentProbeResults(ctx context.Context, _ string, _ int64, _ []preparedAgentProbeRound) (agentProbeInsertResult, error) {
	<-ctx.Done()
	return agentProbeInsertResult{}, ctx.Err()
}

type probeErrorFaultStore struct {
	*SQLiteStore
}

func (store *probeErrorFaultStore) InsertAgentProbeResults(context.Context, string, int64, []preparedAgentProbeRound) (agentProbeInsertResult, error) {
	return agentProbeInsertResult{}, errors.New("injected pre-commit store failure")
}

func openProbeReliabilityStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{
		NodeID: "example-node-a", DisplayName: "Example Node A", CountryCode: "HK", AgentToken: "test-agent-token",
	}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	return store
}

func postProbeReliabilityRequest(t *testing.T, handler http.Handler, ctx context.Context, payload string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", strings.NewReader(payload)).WithContext(ctx)
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func currentProbeConfigVersion(t *testing.T, store *SQLiteStore) int64 {
	t.Helper()
	var version int64
	if err := store.db.QueryRowContext(context.Background(), `SELECT version FROM probe_config_meta WHERE id = 1`).Scan(&version); err != nil {
		t.Fatalf("read probe config version: %v", err)
	}
	return version
}

func probeReliabilityPayload(version int64, rounds ...string) string {
	return `{"config_version":` + strconv.FormatInt(version, 10) + `,"rounds":[` + strings.Join(rounds, ",") + `]}`
}

func probeReliabilityRound(roundID, targetID string, ts int64, latency float64) string {
	return fmt.Sprintf(`{"round_id":%q,"target_id":%q,"ts":%d,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":%g}]}`, roundID, targetID, ts, latency)
}

func TestAgentProbeResultsCommitThenLostResponseRetriesIdempotently(t *testing.T) {
	store := openProbeReliabilityStore(t)
	faultStore := &probePostCommitFaultStore{SQLiteStore: store, failNext: true}
	handler := NewHandler(HandlerOptions{Store: faultStore, DisableNotifications: true})
	now := time.Now().UTC().Unix()
	payload := probeReliabilityPayload(currentProbeConfigVersion(t, store), probeReliabilityRound("lost-response-round", "google-dns", now, 12.5))

	first := postProbeReliabilityRequest(t, handler, context.Background(), payload)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("injected lost response status=%d body=%s, want 500 after durable commit", first.Code, first.Body.String())
	}
	second := postProbeReliabilityRequest(t, handler, context.Background(), payload)
	if second.Code != http.StatusAccepted || !strings.Contains(second.Body.String(), `"inserted":0`) || !strings.Contains(second.Body.String(), `"idempotent":1`) {
		t.Fatalf("retry status=%d body=%s, want explicit idempotent success", second.Code, second.Body.String())
	}

	var rounds, samples int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM probe_rounds WHERE node_id = 'example-node-a' AND agent_round_id = 'lost-response-round'`).Scan(&rounds); err != nil {
		t.Fatalf("count rounds: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM probe_samples ps JOIN probe_rounds pr ON pr.id = ps.round_id WHERE pr.agent_round_id = 'lost-response-round'`).Scan(&samples); err != nil {
		t.Fatalf("count samples: %v", err)
	}
	if rounds != 1 || samples != 1 {
		t.Fatalf("durable retry rows=%d samples=%d, want 1/1", rounds, samples)
	}
}

func TestAgentProbeResultsReplayPrecedesMutableConfigAndBatchRemainsAtomic(t *testing.T) {
	store := openProbeReliabilityStore(t)
	// Keep the durable deletion job tombstoned but do not let the asynchronous
	// worker erase the historical round while this ordering test runs.
	store.stopAdminDeletionWorker()
	handler := NewHandler(HandlerOptions{Store: store, DisableNotifications: true})
	ctx := context.Background()
	now := time.Now().UTC().Unix()
	oldVersion := currentProbeConfigVersion(t, store)
	originalRound := probeReliabilityRound("old-config-round", "google-dns", now, 11.5)
	original := probeReliabilityPayload(oldVersion, originalRound)
	if response := postProbeReliabilityRequest(t, handler, ctx, original); response.Code != http.StatusAccepted {
		t.Fatalf("initial insert status=%d body=%s", response.Code, response.Body.String())
	}
	if err := store.enqueueAdminProbeTargetDeletion(ctx, "google-dns"); err != nil {
		t.Fatalf("tombstone target: %v", err)
	}
	newVersion := currentProbeConfigVersion(t, store)
	if newVersion == oldVersion {
		t.Fatal("target deletion did not change config version")
	}

	replay := postProbeReliabilityRequest(t, handler, ctx, original)
	if replay.Code != http.StatusAccepted || !strings.Contains(replay.Body.String(), `"idempotent":1`) {
		t.Fatalf("old exact replay status=%d body=%s, want idempotent 202", replay.Code, replay.Body.String())
	}
	conflict := postProbeReliabilityRequest(t, handler, ctx, probeReliabilityPayload(oldVersion, probeReliabilityRound("old-config-round", "google-dns", now, 99)))
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "probe_round_conflict") {
		t.Fatalf("conflicting old id status=%d body=%s, want explicit 409 conflict", conflict.Code, conflict.Body.String())
	}
	staleNew := postProbeReliabilityRequest(t, handler, ctx, probeReliabilityPayload(oldVersion, probeReliabilityRound("stale-new-round", "cloudflare-dns", now, 12)))
	if staleNew.Code != http.StatusConflict || !strings.Contains(staleNew.Body.String(), "stale_probe_config") {
		t.Fatalf("uncommitted stale round status=%d body=%s, want stale 409", staleNew.Code, staleNew.Body.String())
	}

	mixed := postProbeReliabilityRequest(t, handler, ctx, probeReliabilityPayload(newVersion,
		originalRound,
		probeReliabilityRound("mixed-new-round", "cloudflare-dns", now, 13),
	))
	if mixed.Code != http.StatusAccepted || !strings.Contains(mixed.Body.String(), `"inserted":1`) || !strings.Contains(mixed.Body.String(), `"idempotent":1`) {
		t.Fatalf("mixed retry/new status=%d body=%s, want atomic 1 inserted/1 idempotent", mixed.Code, mixed.Body.String())
	}
	invalidMixed := postProbeReliabilityRequest(t, handler, ctx, probeReliabilityPayload(newVersion,
		originalRound,
		probeReliabilityRound("mixed-unknown-round", "missing-target", now, 13),
	))
	if invalidMixed.Code != http.StatusBadRequest {
		t.Fatalf("mixed retry/unknown status=%d body=%s, want 400", invalidMixed.Code, invalidMixed.Body.String())
	}
	var invalidWrites int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM probe_rounds WHERE agent_round_id = 'mixed-unknown-round'`).Scan(&invalidWrites); err != nil {
		t.Fatalf("count invalid mixed writes: %v", err)
	}
	if invalidWrites != 0 {
		t.Fatalf("invalid mixed batch wrote %d new rounds, want zero", invalidWrites)
	}
}

func TestAgentProbeResultsOldCommittedRoundBypassesTimestampAdmission(t *testing.T) {
	store := openProbeReliabilityStore(t)
	ctx := context.Background()
	targets, err := store.EnabledProbeTargets(ctx, "example-node-a")
	if err != nil {
		t.Fatalf("load targets: %v", err)
	}
	var target ProbeTarget
	for _, candidate := range targets {
		if candidate.ID == "google-dns" {
			target = candidate
			break
		}
	}
	latency := 8.5
	oldTS := time.Now().UTC().Add(-maxAgentTimestampPastSkew - time.Minute).Truncate(time.Second)
	round := preparedAgentProbeRound{
		targetID: "google-dns", targetType: "tcping", target: target, ts: oldTS,
		agentRoundID: "old-timestamp-round", samples: []probe.Sample{{Seq: 1, Success: true, LatencyMS: &latency}},
	}
	round.payloadHash = agentProbeRoundPayloadHash(round)
	round.idempotencyKey = "agent:" + round.agentRoundID
	round.samples = agentProbeSamplesForTarget(round.samples, target)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin historical fixture: %v", err)
	}
	if err := insertProbeRoundTx(ctx, tx, "example-node-a", round); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert historical fixture: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit historical fixture: %v", err)
	}

	payload := probeReliabilityPayload(currentProbeConfigVersion(t, store), probeReliabilityRound("old-timestamp-round", "google-dns", oldTS.Unix(), latency))
	response := postProbeReliabilityRequest(t, NewHandler(HandlerOptions{Store: store, DisableNotifications: true}), ctx, payload)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"idempotent":1`) {
		t.Fatalf("historical exact replay status=%d body=%s, want idempotent 202", response.Code, response.Body.String())
	}
}

func TestAgentProbeResultsNewRoundUsesAgentBacklogWindowAndKeepsFutureGuard(t *testing.T) {
	store := openProbeReliabilityStore(t)
	handler := NewHandler(HandlerOptions{Store: store, DisableNotifications: true})
	version := currentProbeConfigVersion(t, store)
	now := time.Now().UTC()

	withinBacklog := probeReliabilityPayload(version, probeReliabilityRound(
		"within-backlog-round", "google-dns", now.Add(-maxAgentProbeTimestampPastSkew+time.Minute).Unix(), 6,
	))
	if response := postProbeReliabilityRequest(t, handler, context.Background(), withinBacklog); response.Code != http.StatusAccepted {
		t.Fatalf("within-backlog status=%d body=%s, want 202", response.Code, response.Body.String())
	}

	tooOld := probeReliabilityPayload(version, probeReliabilityRound(
		"beyond-backlog-round", "cloudflare-dns", now.Add(-maxAgentProbeTimestampPastSkew-time.Minute).Unix(), 7,
	))
	if response := postProbeReliabilityRequest(t, handler, context.Background(), tooOld); response.Code != http.StatusBadRequest {
		t.Fatalf("beyond-backlog status=%d body=%s, want 400", response.Code, response.Body.String())
	}

	tooFarFuture := probeReliabilityPayload(version, probeReliabilityRound(
		"future-round", "cloudflare-dns", now.Add(maxAgentTimestampFutureSkew+time.Minute).Unix(), 8,
	))
	if response := postProbeReliabilityRequest(t, handler, context.Background(), tooFarFuture); response.Code != http.StatusBadRequest {
		t.Fatalf("future status=%d body=%s, want 400", response.Code, response.Body.String())
	}

	var accepted, rejected int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM probe_rounds WHERE agent_round_id = 'within-backlog-round'`).Scan(&accepted); err != nil {
		t.Fatalf("count accepted backlog round: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM probe_rounds WHERE agent_round_id IN ('beyond-backlog-round', 'future-round')`).Scan(&rejected); err != nil {
		t.Fatalf("count rejected backlog rounds: %v", err)
	}
	if accepted != 1 || rejected != 0 {
		t.Fatalf("backlog writes accepted=%d rejected=%d, want 1/0", accepted, rejected)
	}
}

func TestAgentProbeRoundV2HashBindsMetadataAndFullSamplePayload(t *testing.T) {
	latency := 12.5
	base := preparedAgentProbeRound{
		targetID: "google-dns", targetType: "tcping", ts: time.Unix(1700000000, 0).UTC(),
		agentRoundID: "hash-round", samples: []probe.Sample{{Seq: 1, Success: true, LatencyMS: &latency}},
	}
	baseHash := agentProbeRoundPayloadHash(base)
	if !strings.HasPrefix(baseHash, "v2:") {
		t.Fatalf("payload hash=%q, want v2 prefix", baseHash)
	}
	mutations := map[string]func(*preparedAgentProbeRound){
		"target":  func(round *preparedAgentProbeRound) { round.targetID = "cloudflare-dns" },
		"ts":      func(round *preparedAgentProbeRound) { round.ts = round.ts.Add(time.Second) },
		"type":    func(round *preparedAgentProbeRound) { round.targetType = "ping" },
		"seq":     func(round *preparedAgentProbeRound) { round.samples[0].Seq = 2 },
		"success": func(round *preparedAgentProbeRound) { round.samples[0].Success = false },
		"latency": func(round *preparedAgentProbeRound) { value := 13.5; round.samples[0].LatencyMS = &value },
		"error":   func(round *preparedAgentProbeRound) { round.samples[0].Error = "connect_error" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.samples = append([]probe.Sample(nil), base.samples...)
			mutate(&candidate)
			if hash := agentProbeRoundPayloadHash(candidate); hash == baseHash {
				t.Fatalf("mutation %s retained payload hash %q", name, hash)
			}
		})
	}
}

func TestAgentProbeRoundConflictRetainsPreStorageLatencyPayload(t *testing.T) {
	store := openProbeReliabilityStore(t)
	handler := NewHandler(HandlerOptions{Store: store, DisableNotifications: true})
	now := time.Now().UTC().Unix()
	version := currentProbeConfigVersion(t, store)
	first := probeReliabilityPayload(version, probeReliabilityRound("over-cap-payload", "google-dns", now, 6000))
	if response := postProbeReliabilityRequest(t, handler, context.Background(), first); response.Code != http.StatusAccepted {
		t.Fatalf("first over-cap payload status=%d body=%s", response.Code, response.Body.String())
	}
	conflict := probeReliabilityPayload(version, probeReliabilityRound("over-cap-payload", "google-dns", now, 7000))
	if response := postProbeReliabilityRequest(t, handler, context.Background(), conflict); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "probe_round_conflict") {
		t.Fatalf("distinct over-cap payload status=%d body=%s, want typed 409", response.Code, response.Body.String())
	}
}

func TestAgentProbeResultsReadsLegacySampleHashButChecksStoredMetadata(t *testing.T) {
	store := openProbeReliabilityStore(t)
	ctx := context.Background()
	targets, err := store.EnabledProbeTargets(ctx, "example-node-a")
	if err != nil {
		t.Fatalf("load targets: %v", err)
	}
	var target ProbeTarget
	for _, candidate := range targets {
		if candidate.ID == "google-dns" {
			target = candidate
			break
		}
	}
	latency := 9.5
	round := preparedAgentProbeRound{
		targetID: target.ID, targetType: target.Type, target: target, ts: time.Now().UTC().Truncate(time.Second),
		agentRoundID: "legacy-v1-round", samples: []probe.Sample{{Seq: 1, Success: true, LatencyMS: &latency}},
	}
	round.samples = agentProbeSamplesForTarget(round.samples, target)
	round.payloadHash = probeRoundIdempotencyKey(round.samples)
	round.idempotencyKey = "agent:" + round.agentRoundID
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin legacy fixture: %v", err)
	}
	if err := insertProbeRoundTx(ctx, tx, "example-node-a", round); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert legacy fixture: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit legacy fixture: %v", err)
	}

	handler := NewHandler(HandlerOptions{Store: store, DisableNotifications: true})
	replay := probeReliabilityPayload(currentProbeConfigVersion(t, store), probeReliabilityRound("legacy-v1-round", "google-dns", round.ts.Unix(), latency))
	if response := postProbeReliabilityRequest(t, handler, ctx, replay); response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"idempotent":1`) {
		t.Fatalf("legacy v1 replay status=%d body=%s, want idempotent 202", response.Code, response.Body.String())
	}
	metadataConflict := probeReliabilityPayload(currentProbeConfigVersion(t, store), probeReliabilityRound("legacy-v1-round", "cloudflare-dns", round.ts.Unix(), latency))
	if response := postProbeReliabilityRequest(t, handler, ctx, metadataConflict); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "probe_round_conflict") {
		t.Fatalf("legacy v1 metadata reuse status=%d body=%s, want typed 409", response.Code, response.Body.String())
	}
}

func TestAgentProbeResultsFaultStatusesAreControllableOnlyInHTTPTest(t *testing.T) {
	store := openProbeReliabilityStore(t)
	now := time.Now().UTC().Unix()
	version := currentProbeConfigVersion(t, store)
	payload := probeReliabilityPayload(version,
		probeReliabilityRound("fault-round-a", "google-dns", now, 10),
		probeReliabilityRound("fault-round-b", "cloudflare-dns", now, 11),
	)

	t.Run("429", func(t *testing.T) {
		h := NewHandler(HandlerOptions{Store: store, DisableNotifications: true}).(*handler)
		h.agentQuotas.mu.Lock()
		quota := h.agentQuotas.nodeLocked("example-node-a", time.Now())
		quota.buckets[agentQuotaProbeResults] = &agentTokenBucket{tokens: 0, updatedAt: time.Now()}
		h.agentQuotas.mu.Unlock()
		response := postProbeReliabilityRequest(t, h, context.Background(), payload)
		if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
			t.Fatalf("status=%d retry-after=%q, want controlled 429", response.Code, response.Header().Get("Retry-After"))
		}
	})

	t.Run("507", func(t *testing.T) {
		store.telemetryStorage.mu.Lock()
		store.telemetryStorage.minFreeBytes = 1
		store.telemetryStorage.checkedAt = time.Time{}
		store.telemetryStorage.blocked = false
		store.telemetryStorage.freeBytes = func(string) (uint64, error) { return 0, nil }
		store.telemetryStorage.mu.Unlock()
		response := postProbeReliabilityRequest(t, NewHandler(HandlerOptions{Store: store, DisableNotifications: true}), context.Background(), payload)
		if response.Code != http.StatusInsufficientStorage || response.Header().Get("Retry-After") != "60" {
			t.Fatalf("status=%d retry-after=%q body=%s, want controlled 507", response.Code, response.Header().Get("Retry-After"), response.Body.String())
		}
		var writes int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM probe_rounds WHERE agent_round_id IN ('fault-round-a','fault-round-b')`).Scan(&writes); err != nil || writes != 0 {
			t.Fatalf("507 writes=%d err=%v, want zero", writes, err)
		}
		store.telemetryStorage.mu.Lock()
		store.telemetryStorage.checkedAt = time.Time{}
		store.telemetryStorage.blocked = false
		store.telemetryStorage.freeBytes = func(string) (uint64, error) { return ^uint64(0), nil }
		store.telemetryStorage.mu.Unlock()
	})

	t.Run("500", func(t *testing.T) {
		response := postProbeReliabilityRequest(t, NewHandler(HandlerOptions{Store: &probeErrorFaultStore{SQLiteStore: store}, DisableNotifications: true}), context.Background(), payload)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s, want controlled pre-commit 500", response.Code, response.Body.String())
		}
		var writes int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM probe_rounds WHERE agent_round_id IN ('fault-round-a','fault-round-b')`).Scan(&writes); err != nil || writes != 0 {
			t.Fatalf("500 writes=%d err=%v, want zero", writes, err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		response := postProbeReliabilityRequest(t, NewHandler(HandlerOptions{Store: &probeBlockingFaultStore{SQLiteStore: store}, DisableNotifications: true}), ctx, payload)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s, want controlled timeout mapped to 500", response.Code, response.Body.String())
		}
		var writes int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM probe_rounds WHERE agent_round_id IN ('fault-round-a','fault-round-b')`).Scan(&writes); err != nil || writes != 0 {
			t.Fatalf("timeout writes=%d err=%v, want zero", writes, err)
		}
	})
}

func TestAgentProbeResultsConcurrentExactWritesAreSingleRound(t *testing.T) {
	store := openProbeReliabilityStore(t)
	targets, err := store.EnabledProbeTargets(context.Background(), "example-node-a")
	if err != nil {
		t.Fatalf("load targets: %v", err)
	}
	var target ProbeTarget
	for _, candidate := range targets {
		if candidate.ID == "google-dns" {
			target = candidate
		}
	}
	latency := 12.5
	round := preparedAgentProbeRound{
		targetID: "google-dns", targetType: "tcping", ts: time.Now().UTC(), agentRoundID: "concurrent-exact-round",
		samples: []probe.Sample{{Seq: 1, Success: true, LatencyMS: &latency}}, target: target,
	}
	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.InsertAgentProbeResults(context.Background(), "example-node-a", 0, []preparedAgentProbeRound{round})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent exact insert: %v", err)
		}
	}
	var rounds, samples int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM probe_rounds WHERE agent_round_id = 'concurrent-exact-round'`).Scan(&rounds)
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM probe_samples ps JOIN probe_rounds pr ON pr.id = ps.round_id WHERE pr.agent_round_id = 'concurrent-exact-round'`).Scan(&samples)
	if rounds != 1 || samples != 1 {
		t.Fatalf("concurrent exact writes rows=%d samples=%d, want 1/1", rounds, samples)
	}
}

func TestAgentProbeRoundLookupAndMaximumBatchAtScale(t *testing.T) {
	store := openProbeReliabilityStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `
		WITH RECURSIVE scale(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM scale WHERE n < 100000)
		INSERT INTO probe_rounds
			(node_id, target_id, ts, type, idempotency_key, agent_round_id, payload_hash, sent, received, loss_percent)
		SELECT 'example-node-a', 'google-dns', ?, 'tcping', 'agent:scale-' || n, 'scale-' || n, 'v2:scale-' || n, 1, 1, 0
		FROM scale
	`, now-1); err != nil {
		t.Fatalf("seed scale rounds: %v", err)
	}

	rows, err := store.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT agent_round_id, target_id, ts, type, payload_hash FROM probe_rounds WHERE node_id = ? AND agent_round_id IN (?, ?) AND agent_round_id IS NOT NULL AND agent_round_id <> ''`, "example-node-a", "scale-1", "scale-100000")
	if err != nil {
		t.Fatalf("explain round lookup: %v", err)
	}
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan.WriteString(detail)
	}
	_ = rows.Close()
	if !strings.Contains(plan.String(), "idx_probe_rounds_agent_id") {
		t.Fatalf("round lookup plan=%q, want node/round unique index", plan.String())
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin target fixture tx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE node_probe_targets SET enabled = 0 WHERE node_id = 'example-node-a'`); err != nil {
		_ = tx.Rollback()
		t.Fatalf("disable preview target assignments: %v", err)
	}
	for i := 0; i < maxAgentProbeRounds; i++ {
		id := fmt.Sprintf("scale-target-%02d", i)
		if _, err := tx.ExecContext(ctx, `INSERT INTO probe_targets (id, name, type, address, port, count, timeout_ms, interval_sec, display_order, enabled, created_at, updated_at) VALUES (?, ?, 'tcping', '127.0.0.1', 80, 1, 1000, 5, ?, 1, ?, ?)`, id, id, i, now, now); err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert scale target: %v", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_probe_targets (node_id, target_id, enabled) VALUES ('example-node-a', ?, 1)`, id); err != nil {
			_ = tx.Rollback()
			t.Fatalf("assign scale target: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit target fixtures: %v", err)
	}

	latency := 1.5
	batch := make([]preparedAgentProbeRound, 0, maxAgentProbeRounds)
	for i := 0; i < maxAgentProbeRounds; i++ {
		batch = append(batch, preparedAgentProbeRound{
			targetID: fmt.Sprintf("scale-target-%02d", i), targetType: "tcping", ts: time.Now().UTC(),
			agentRoundID: fmt.Sprintf("scale-batch-%02d", i), samples: []probe.Sample{{Seq: 1, Success: true, LatencyMS: &latency}},
		})
	}
	result, err := store.InsertAgentProbeResults(ctx, "example-node-a", 0, batch)
	if err != nil {
		t.Fatalf("insert maximum batch over production-scale history: %v", err)
	}
	if result.inserted != maxAgentProbeRounds || result.idempotent != 0 {
		t.Fatalf("maximum batch result=%+v, want %d new", result, maxAgentProbeRounds)
	}
}

func TestAgentProbeRetriesDoNotCreateNotificationCandidates(t *testing.T) {
	store := openProbeReliabilityStore(t)
	handler := NewHandler(HandlerOptions{Store: store, DisableNotifications: true})
	now := time.Now().UTC().Unix()
	payload := probeReliabilityPayload(currentProbeConfigVersion(t, store), probeReliabilityRound("notification-free-round", "google-dns", now, 12))
	var marksBefore, deliveriesBefore int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM notification_event_marks`).Scan(&marksBefore)
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM notification_deliveries`).Scan(&deliveriesBefore)
	for i := 0; i < 2; i++ {
		response := postProbeReliabilityRequest(t, handler, context.Background(), payload)
		if response.Code != http.StatusAccepted {
			t.Fatalf("attempt %d status=%d body=%s", i+1, response.Code, response.Body.String())
		}
	}
	var marksAfter, deliveriesAfter int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM notification_event_marks`).Scan(&marksAfter)
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM notification_deliveries`).Scan(&deliveriesAfter)
	if marksAfter != marksBefore || deliveriesAfter != deliveriesBefore {
		t.Fatalf("probe retry changed notification candidates marks %d->%d deliveries %d->%d", marksBefore, marksAfter, deliveriesBefore, deliveriesAfter)
	}
}

func TestAgentProbeResultsRejectsDuplicateTargetsAndBudgetAtomically(t *testing.T) {
	store := openProbeReliabilityStore(t)
	handler := NewHandler(HandlerOptions{Store: store, DisableNotifications: true})
	now := time.Now().UTC().Unix()
	version := currentProbeConfigVersion(t, store)
	duplicateTarget := probeReliabilityPayload(version,
		probeReliabilityRound("dup-target-a", "google-dns", now, 1),
		probeReliabilityRound("dup-target-b", "google-dns", now, 2),
	)
	if response := postProbeReliabilityRequest(t, handler, context.Background(), duplicateTarget); response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate target status=%d body=%s, want 400", response.Code, response.Body.String())
	}
	tooLong := strings.Repeat("x", maxProbeErrorBytes+1)
	budget := fmt.Sprintf(`{"config_version":%d,"rounds":[{"round_id":"budget-valid-first","target_id":"google-dns","ts":%d,"type":"tcping","samples":[{"seq":1,"success":true,"latency_ms":1}]},{"round_id":"budget-invalid-second","target_id":"cloudflare-dns","ts":%d,"type":"tcping","samples":[{"seq":1,"success":false,"error":%q}]}]}`, version, now, now, tooLong)
	if response := postProbeReliabilityRequest(t, handler, context.Background(), budget); response.Code != http.StatusBadRequest {
		t.Fatalf("budget status=%d body=%s, want 400", response.Code, response.Body.String())
	}
	var writes int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM probe_rounds WHERE agent_round_id IN ('dup-target-a','dup-target-b','budget-valid-first','budget-invalid-second')`).Scan(&writes); err != nil {
		t.Fatalf("count rejected writes: %v", err)
	}
	if writes != 0 {
		t.Fatalf("invalid batches wrote %d rounds, want zero", writes)
	}
}

func TestAgentProbeExactReplayBypassesStoragePressureButMixedBatchDoesNot(t *testing.T) {
	store := openProbeReliabilityStore(t)
	handler := NewHandler(HandlerOptions{Store: store, DisableNotifications: true})
	now := time.Now().UTC().Unix()
	version := currentProbeConfigVersion(t, store)
	oldRound := probeReliabilityRound("pressure-committed", "google-dns", now, 4)
	payload := probeReliabilityPayload(version, oldRound)
	if response := postProbeReliabilityRequest(t, handler, context.Background(), payload); response.Code != http.StatusAccepted {
		t.Fatalf("initial pressure fixture status=%d body=%s", response.Code, response.Body.String())
	}
	store.telemetryStorage.mu.Lock()
	store.telemetryStorage.minFreeBytes = 1
	store.telemetryStorage.checkedAt = time.Time{}
	store.telemetryStorage.blocked = false
	store.telemetryStorage.freeBytes = func(string) (uint64, error) { return 0, nil }
	store.telemetryStorage.mu.Unlock()
	if response := postProbeReliabilityRequest(t, handler, context.Background(), payload); response.Code != http.StatusAccepted {
		t.Fatalf("exact replay under pressure status=%d body=%s, want 202", response.Code, response.Body.String())
	}
	mixed := probeReliabilityPayload(version, oldRound, probeReliabilityRound("pressure-new", "cloudflare-dns", now, 5))
	if response := postProbeReliabilityRequest(t, handler, context.Background(), mixed); response.Code != http.StatusInsufficientStorage {
		t.Fatalf("mixed batch under pressure status=%d body=%s, want 507", response.Code, response.Body.String())
	}
	var writes int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM probe_rounds WHERE agent_round_id = 'pressure-new'`).Scan(&writes); err != nil || writes != 0 {
		t.Fatalf("mixed 507 writes=%d err=%v, want zero", writes, err)
	}
}

// Compile-time checks keep the test-only fault stores aligned with the
// production capability interface without exposing an injection endpoint.
var _ agentProbeBatchStore = (*probePostCommitFaultStore)(nil)
var _ agentProbeBatchStore = (*probeBlockingFaultStore)(nil)
var _ agentProbeBatchStore = (*probeErrorFaultStore)(nil)
