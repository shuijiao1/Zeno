package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResidualProbeErrorBatchAmplificationIsRejected(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatal(err)
	}
	samples := make([]map[string]any, 9)
	for j := range samples {
		samples[j] = map[string]any{"seq": j + 1, "success": false, "error": strings.Repeat("E", maxProbeErrorBytes)}
	}
	rounds := []map[string]any{{"round_id": "audit-error-budget", "target_id": "google-dns", "ts": time.Now().UTC().Unix(), "type": "tcping", "samples": samples}}
	payload, err := json.Marshal(map[string]any{"rounds": rounds})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer token")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s payload=%d, want 400", recorder.Code, recorder.Body.String(), len(payload))
	}
	var roundsWritten, samplesWritten int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_rounds`).Scan(&roundsWritten); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_samples`).Scan(&samplesWritten); err != nil {
		t.Fatal(err)
	}
	if roundsWritten != 0 || samplesWritten != 0 {
		t.Fatalf("rejected batch produced writes: rounds=%d samples=%d", roundsWritten, samplesWritten)
	}
}

func TestProbeBatchRejectsDuplicateTargetEvenWithShortErrors(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "example-node-a", DisplayName: "Example Node A", AgentToken: "token"}); err != nil {
		t.Fatal(err)
	}
	rounds := make([]map[string]any, 2)
	for i := range rounds {
		samples := make([]map[string]any, 3)
		for j := range samples {
			samples[j] = map[string]any{"seq": j + 1, "success": false, "error": "connect_error"}
		}
		rounds[i] = map[string]any{"round_id": fmt.Sprintf("normal-%d", i), "target_id": "google-dns", "ts": time.Now().UTC().Unix(), "type": "tcping", "samples": samples}
	}
	payload, err := json.Marshal(map[string]any{"rounds": rounds})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/probe-results", bytes.NewReader(payload))
	request.Header.Set("X-Node-ID", "example-node-a")
	request.Header.Set("Authorization", "Bearer token")
	NewHandler(HandlerOptions{Store: store}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("duplicate-target batch status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
	}
}
