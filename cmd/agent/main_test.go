package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shuijiao1/jiaoprobe/internal/agent"
)

func TestReportOnceSkipsProbeResultsWhenNoTargetsAreDue(t *testing.T) {
	var probePosts [][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/v1/heartbeat", "/api/agent/v1/state":
			w.WriteHeader(http.StatusAccepted)
		case "/api/agent/v1/probe-targets":
			_ = json.NewEncoder(w).Encode(agent.ProbeTargetsResponse{Targets: []agent.ProbeTarget{
				{ID: "fast", Type: "unsupported", Count: 1, IntervalSec: 3600},
				{ID: "slow", Type: "unsupported", Count: 1, IntervalSec: 3600},
			}})
		case "/api/agent/v1/probe-results":
			var payload struct {
				Rounds []struct {
					TargetID string `json:"target_id"`
				} `json:"rounds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode probe results: %v", err)
			}
			ids := make([]string, 0, len(payload.Rounds))
			for _, round := range payload.Rounds {
				ids = append(ids, round.TargetID)
			}
			probePosts = append(probePosts, ids)
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := agent.NewClient(server.URL, "hytron", "token")
	collector := agent.NewMetricsCollector()
	scheduler := agent.NewProbeScheduler()
	if err := reportOnce(context.Background(), client, collector, "scheduler-test", false, scheduler); err != nil {
		t.Fatalf("first reportOnce: %v", err)
	}
	if err := reportOnce(context.Background(), client, collector, "scheduler-test", false, scheduler); err != nil {
		t.Fatalf("second reportOnce: %v", err)
	}

	if len(probePosts) != 1 {
		t.Fatalf("probe result posts = %+v, want exactly one post because second run has no due targets", probePosts)
	}
	if len(probePosts[0]) != 2 || probePosts[0][0] != "fast" || probePosts[0][1] != "slow" {
		t.Fatalf("first probe post target ids = %+v, want fast and slow", probePosts[0])
	}
}
