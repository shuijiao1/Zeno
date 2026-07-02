package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/shuijiao1/jiaoprobe/internal/shared/probe"
)

type agentStore interface {
	AuthorizeAgent(ctx context.Context, nodeID, token string) (bool, error)
	EnabledProbeTargets(ctx context.Context, nodeID string) ([]ProbeTarget, error)
	InsertProbeRound(ctx context.Context, nodeID string, target ProbeTarget, ts time.Time, samples []probe.Sample) error
}

func (h *handler) handleAgentProbeTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	targets, err := store.EnabledProbeTargets(r.Context(), nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	response := AgentProbeTargetsResponse{Targets: make([]AgentProbeTarget, 0, len(targets))}
	for _, target := range targets {
		response.Targets = append(response.Targets, AgentProbeTarget{
			ID:          target.ID,
			Name:        target.Name,
			Type:        target.Type,
			Address:     target.Address,
			Port:        target.Port,
			Count:       target.Count,
			TimeoutMS:   target.TimeoutMS,
			IntervalSec: target.IntervalSec,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) handleAgentProbeResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}

	var request AgentProbeResultsRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(request.Rounds) == 0 {
		writeError(w, http.StatusBadRequest, "rounds required")
		return
	}

	enabledTargets, err := store.EnabledProbeTargets(r.Context(), nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	targetsByID := make(map[string]ProbeTarget, len(enabledTargets))
	for _, target := range enabledTargets {
		targetsByID[target.ID] = target
	}

	prepared := make([]struct {
		target  ProbeTarget
		ts      time.Time
		samples []probe.Sample
	}, 0, len(request.Rounds))
	for _, round := range request.Rounds {
		target, exists := targetsByID[round.TargetID]
		if !exists {
			writeError(w, http.StatusBadRequest, "unknown target")
			return
		}
		if round.Type != "" && round.Type != target.Type {
			writeError(w, http.StatusBadRequest, "target type mismatch")
			return
		}
		if round.TS <= 0 {
			writeError(w, http.StatusBadRequest, "invalid timestamp")
			return
		}
		samples := make([]probe.Sample, 0, len(round.Samples))
		for index, sample := range round.Samples {
			seq := sample.Seq
			if seq == 0 {
				seq = index + 1
			}
			samples = append(samples, probe.Sample{Seq: seq, Success: sample.Success, LatencyMS: sample.LatencyMS, Error: sample.Error})
		}
		if len(samples) == 0 {
			writeError(w, http.StatusBadRequest, "samples required")
			return
		}
		prepared = append(prepared, struct {
			target  ProbeTarget
			ts      time.Time
			samples []probe.Sample
		}{target: target, ts: time.Unix(round.TS, 0).UTC(), samples: samples})
	}

	for _, round := range prepared {
		if err := store.InsertProbeRound(r.Context(), nodeID, round.target, round.ts, round.samples); err != nil {
			writeError(w, http.StatusBadRequest, "invalid probe round")
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "accepted": len(prepared)})
}

func (h *handler) authorizeAgentRequest(w http.ResponseWriter, r *http.Request) (agentStore, string, bool) {
	store, ok := h.store.(agentStore)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return nil, "", false
	}
	nodeID := strings.TrimSpace(r.Header.Get("X-Node-ID"))
	if nodeID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, "", false
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	allowed, err := store.AuthorizeAgent(r.Context(), nodeID, token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, "", false
	}
	if !allowed {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, "", false
	}
	return store, nodeID, true
}
