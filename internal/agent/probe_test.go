package agent

import (
	"context"
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
