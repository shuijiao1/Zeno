package api

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/shuijiao1/zeno/internal/shared/probe"
)

func TestAuditProbeErrorPayloadIsBounded(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"rounds": []map[string]any{{
		"target_id": "google-dns", "ts": time.Now().UTC().Unix(), "type": "tcping",
		"samples": []map[string]any{{"seq": 1, "success": false, "error": strings.Repeat("E", 900_000)}},
	}}}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer token")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized error status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
	}
	var rounds, samples int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&rounds); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if rounds != 0 || samples != 0 {
		t.Fatalf("oversized error produced writes: rounds=%d samples=%d", rounds, samples)
	}
}

func TestProbeStoreDefensivelyBoundsErrorAndPreservesUTF8(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatal(err)
	}
	errorText := strings.Repeat("界", 400)
	if err := store.InsertProbeRound(ctx, "example-node-a", ProbeTarget{ID: "google-dns", Type: "tcping", Count: 1}, time.Now().UTC(), []probe.Sample{{Seq: 1, Error: errorText}}); err != nil {
		t.Fatalf("insert direct probe round: %v", err)
	}
	var roundError, sampleError string
	if err := store.db.QueryRowContext(ctx, `SELECT error FROM probe_rounds LIMIT 1`).Scan(&roundError); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT error FROM probe_samples LIMIT 1`).Scan(&sampleError); err != nil {
		t.Fatal(err)
	}
	for label, value := range map[string]string{"round": roundError, "sample": sampleError} {
		if len(value) > maxProbeErrorBytes || !utf8.ValidString(value) {
			t.Fatalf("%s error not safely bounded: bytes=%d valid_utf8=%v", label, len(value), utf8.ValidString(value))
		}
	}
}

func TestAuditAuthAdmissionCannotDrainGlobalBucketFromOneIP(t *testing.T) {
	manager := newAgentAuthAdmissionManager()
	fixedNow := time.Now().UTC()
	manager.now = func() time.Time { return fixedNow }
	accepted := 0
	for i := 0; i < int(agentAuthGlobalBucketSpec.burst); i++ {
		release, _, ok := manager.admit("198.51.100.1")
		if ok {
			accepted++
			release()
		}
	}
	if accepted != int(agentAuthPerIPBucketSpec.burst) {
		t.Fatalf("accepted=%d, want per-IP burst=%d", accepted, int(agentAuthPerIPBucketSpec.burst))
	}
	if release, _, ok := manager.admit("203.0.113.2"); !ok {
		t.Fatal("one abusive IP drained the global authentication bucket")
	} else {
		release()
	}
}

func TestAuditMonthlyTrafficArithmeticStaysNonNegativeInteger(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	state := AgentStateRequest{CPUPercent: 1, MemoryUsedBytes: 1, MemoryTotalBytes: 2, DiskUsedBytes: 1, DiskTotalBytes: 2, UptimeSeconds: 1}
	totals := []int64{0, math.MaxInt64, 0, math.MaxInt64}
	for i, total := range totals {
		state.TS = now.Add(time.Duration(i) * time.Second).Unix()
		state.NetInTotalBytes, state.NetOutTotalBytes = total, total
		if err := store.InsertAgentState(ctx, "example-node-a", state); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	var inType, outType, billableType string
	var inNonNegative, outNonNegative, billableNonNegative int
	if err := store.db.QueryRowContext(ctx, `SELECT typeof(in_bytes), typeof(out_bytes), typeof(billable_bytes), in_bytes >= 0, out_bytes >= 0, billable_bytes >= 0 FROM traffic_monthly WHERE node_id = ?`, "example-node-a").Scan(&inType, &outType, &billableType, &inNonNegative, &outNonNegative, &billableNonNegative); err != nil {
		t.Fatal(err)
	}
	if inType != "integer" || outType != "integer" || billableType != "integer" || inNonNegative != 1 || outNonNegative != 1 || billableNonNegative != 1 {
		t.Fatalf("monthly ledger corrupted: types=%s/%s/%s nonnegative=%d/%d/%d", inType, outType, billableType, inNonNegative, outNonNegative, billableNonNegative)
	}
	if _, err := store.Summary(ctx); err != nil {
		t.Fatalf("summary poisoned by one node: %v", err)
	}
}
