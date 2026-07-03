package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunPingProbeMeasuresLoopbackLatency(t *testing.T) {
	samples := RunPingProbe(context.Background(), ProbeTarget{ID: "loopback", Type: "ping", Address: "127.0.0.1", Count: 1, TimeoutMS: 1000})

	if len(samples) != 1 {
		t.Fatalf("samples len = %d, want 1", len(samples))
	}
	if !samples[0].Success || samples[0].LatencyMS == nil || *samples[0].LatencyMS < 0 || samples[0].Error != "" {
		t.Fatalf("ping sample = %+v, want successful latency sample", samples[0])
	}
}

func TestProbeTargetsRunsPingTargetsInsteadOfMarkingUnsupported(t *testing.T) {
	rounds := ProbeTargets(context.Background(), []ProbeTarget{{ID: "loopback", Type: "ping", Address: "127.0.0.1", Count: 1, TimeoutMS: 1000}}, time.Unix(1782990000, 0))

	if len(rounds) != 1 {
		t.Fatalf("rounds len = %d, want 1", len(rounds))
	}
	if rounds[0].TargetID != "loopback" || rounds[0].Type != "ping" || len(rounds[0].Samples) != 1 {
		t.Fatalf("round = %+v, want one ping round", rounds[0])
	}
	if rounds[0].Samples[0].Error == "unsupported_ping" {
		t.Fatalf("ping target should run real ping, got unsupported sample: %+v", rounds[0].Samples[0])
	}
	if !rounds[0].Samples[0].Success || rounds[0].Samples[0].LatencyMS == nil {
		t.Fatalf("ping sample = %+v, want successful latency sample", rounds[0].Samples[0])
	}
}

func TestRunHTTPProbeMeasuresSuccessfulGETLatency(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	samples := RunHTTPProbe(context.Background(), ProbeTarget{ID: "http-health", Type: "http_get", Address: server.URL, Count: 2, TimeoutMS: 1000})

	if len(samples) != 2 {
		t.Fatalf("samples len = %d, want 2", len(samples))
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	for _, sample := range samples {
		if !sample.Success || sample.LatencyMS == nil || *sample.LatencyMS < 0 || sample.Error != "" {
			t.Fatalf("http sample = %+v, want successful latency sample", sample)
		}
	}
}

func TestRunHTTPProbeMarksBadStatusAsFailedSample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	samples := RunHTTPProbe(context.Background(), ProbeTarget{ID: "http-fail", Type: "http_get", Address: server.URL, Count: 1, TimeoutMS: 1000})

	if len(samples) != 1 {
		t.Fatalf("samples len = %d, want 1", len(samples))
	}
	if samples[0].Success || samples[0].LatencyMS != nil || samples[0].Error != "http_status_500" {
		t.Fatalf("http failure sample = %+v, want status failure without latency", samples[0])
	}
}

func TestProbeTargetsRunsHTTPGETTargetsInsteadOfMarkingUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rounds := ProbeTargets(context.Background(), []ProbeTarget{{ID: "http-health", Type: "http_get", Address: server.URL, Count: 1, TimeoutMS: 1000}}, time.Unix(1782990000, 0))

	if len(rounds) != 1 {
		t.Fatalf("rounds len = %d, want 1", len(rounds))
	}
	if rounds[0].TargetID != "http-health" || rounds[0].Type != "http_get" || len(rounds[0].Samples) != 1 {
		t.Fatalf("round = %+v, want one http_get round", rounds[0])
	}
	if rounds[0].Samples[0].Error == "unsupported_http_get" {
		t.Fatalf("http_get target should run real HTTP GET, got unsupported sample: %+v", rounds[0].Samples[0])
	}
	if !rounds[0].Samples[0].Success || rounds[0].Samples[0].LatencyMS == nil {
		t.Fatalf("http sample = %+v, want successful latency sample", rounds[0].Samples[0])
	}
}
