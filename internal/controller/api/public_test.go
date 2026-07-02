package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthEndpoint(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var body map[string]bool
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if !body["ok"] {
		t.Fatalf("health ok = false, want true")
	}
}

func TestSummaryEndpointReturnsMockHomeCardsWithoutSecrets(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	raw := recorder.Body.String()
	var summary SummaryResponse
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if len(summary.Nodes) < 3 {
		t.Fatalf("nodes len = %d, want at least 3", len(summary.Nodes))
	}
	if summary.Nodes[0].DisplayName != "Hytron" {
		t.Fatalf("first node = %q, want Hytron", summary.Nodes[0].DisplayName)
	}
	if len(summary.LatencyPoints) == 0 {
		t.Fatal("summary should include latency points for the mock chart")
	}

	if strings.Contains(strings.ToLower(raw), "token") || strings.Contains(strings.ToLower(raw), "secret") {
		t.Fatalf("public summary leaked token/secret wording: %s", raw)
	}
}

func TestNodeLatencyEndpointPreservesLossOnlyPointsAsNullLatency(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/hytron/latency?range=1h", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response LatencyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode latency response: %v", err)
	}
	if response.NodeID != "hytron" {
		t.Fatalf("node_id = %q, want hytron", response.NodeID)
	}
	if response.Range != "1h" {
		t.Fatalf("range = %q, want 1h", response.Range)
	}

	var sawLossOnly bool
	for _, point := range response.Points {
		if point.TargetID == "telegram-dc1" && point.LossPercent == 100 {
			sawLossOnly = true
			if point.MedianMS != nil {
				t.Fatalf("100%% loss point median = %v, want nil", *point.MedianMS)
			}
		}
	}
	if !sawLossOnly {
		t.Fatal("expected at least one telegram-dc1 100% loss point")
	}
}
