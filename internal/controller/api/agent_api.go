package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/shuijiao1/zeno/internal/shared/probe"
)

type agentStore interface {
	AuthorizeAgent(ctx context.Context, nodeID, token string) (bool, error)
	EnabledProbeTargets(ctx context.Context, nodeID string) ([]ProbeTarget, error)
	InsertProbeRound(ctx context.Context, nodeID string, target ProbeTarget, ts time.Time, samples []probe.Sample) error
	RecordAgentHeartbeat(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) error
	UpsertAgentHost(ctx context.Context, nodeID string, host AgentHostRequest) error
	InsertAgentState(ctx context.Context, nodeID string, state AgentStateRequest) error
}

type preparedAgentProbeRound struct {
	target  ProbeTarget
	ts      time.Time
	samples []probe.Sample
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

	prepared := make([]preparedAgentProbeRound, 0, len(request.Rounds))
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
		prepared = append(prepared, preparedAgentProbeRound{target: target, ts: time.Unix(round.TS, 0).UTC(), samples: samples})
	}

	for _, round := range prepared {
		if err := store.InsertProbeRound(r.Context(), nodeID, round.target, round.ts, round.samples); err != nil {
			writeError(w, http.StatusBadRequest, "invalid probe round")
			return
		}
	}
	probeStatus := probeHealthStatus(prepared)
	probeTS := latestPreparedProbeTS(prepared)
	if transitionStore, ok := store.(probeHealthTransitionStore); ok {
		transition, err := transitionStore.RecordAgentProbeHealthTransition(r.Context(), nodeID, probeTS, probeStatus)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.dispatchAgentStatusNotification(store, transition, probeTS)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "accepted": len(prepared)})
}

func probeHealthStatus(rounds []preparedAgentProbeRound) string {
	for _, round := range rounds {
		healthy := false
		for _, sample := range round.samples {
			if sample.Success {
				healthy = true
				break
			}
		}
		if !healthy {
			return "warning"
		}
	}
	return "online"
}

func latestPreparedProbeTS(rounds []preparedAgentProbeRound) time.Time {
	latest := time.Now().UTC()
	for index, round := range rounds {
		if index == 0 || round.ts.After(latest) {
			latest = round.ts
		}
	}
	return latest.UTC()
}

func (h *handler) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	var request AgentHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if request.TS <= 0 {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	status := strings.TrimSpace(request.Status)
	if status == "" {
		status = "online"
	}
	if !validAgentStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	var transition notificationStatusTransition
	heartbeatTS := time.Unix(request.TS, 0).UTC()
	if transitionStore, ok := store.(heartbeatTransitionStore); ok {
		var err error
		transition, err = transitionStore.RecordAgentHeartbeatTransition(r.Context(), nodeID, heartbeatTS, status, strings.TrimSpace(request.AgentVersion))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		if notificationStore, ok := store.(notificationEventStore); ok {
			if snapshot, err := notificationStore.NotificationNode(r.Context(), nodeID); err == nil {
				transition.Previous = snapshot
			}
		}
		if err := store.RecordAgentHeartbeat(r.Context(), nodeID, heartbeatTS, status, strings.TrimSpace(request.AgentVersion)); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if notificationStore, ok := store.(notificationEventStore); ok {
			if snapshot, err := notificationStore.NotificationNode(r.Context(), nodeID); err == nil {
				transition.Current = snapshot
			}
		}
	}
	h.dispatchAgentStatusNotification(store, transition, heartbeatTS)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (h *handler) handleAgentHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	var request AgentHostRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(request.OSName) == "" || strings.TrimSpace(request.Arch) == "" {
		writeError(w, http.StatusBadRequest, "host os and arch required")
		return
	}
	if request.CPUCores < 0 || request.MemoryTotalBytes < 0 || request.DiskTotalBytes < 0 || request.BootTime < 0 {
		writeError(w, http.StatusBadRequest, "invalid host values")
		return
	}
	if err := store.UpsertAgentHost(r.Context(), nodeID, request); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (h *handler) handleAgentState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	var request AgentStateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if request.TS <= 0 {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	if request.CPUPercent < 0 || request.CPUPercent > 100 || request.MemoryUsedBytes < 0 || request.MemoryTotalBytes < 0 || request.DiskUsedBytes < 0 || request.DiskTotalBytes < 0 || request.NetInTotalBytes < 0 || request.NetOutTotalBytes < 0 || request.NetInSpeedBps < 0 || request.NetOutSpeedBps < 0 || request.UptimeSeconds < 0 {
		writeError(w, http.StatusBadRequest, "invalid state values")
		return
	}
	if err := store.InsertAgentState(r.Context(), nodeID, request); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func validAgentStatus(status string) bool {
	switch status {
	case "online", "offline", "warning", "no_data":
		return true
	default:
		return false
	}
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
