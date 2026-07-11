package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/shuijiao1/zeno/internal/shared/probe"
)

const (
	maxAgentProbeRounds          = maxProbeTargetsPerNode
	maxAgentProbeSamplesPerRound = maxProbeTargetCount
	maxAgentTimestampFutureSkew  = 5 * time.Minute
	maxAgentTimestampPastSkew    = 10 * time.Minute
)

type agentStore interface {
	AuthorizeAgent(ctx context.Context, nodeID, token string) (bool, error)
	EnabledProbeTargets(ctx context.Context, nodeID string) ([]ProbeTarget, error)
	InsertProbeRound(ctx context.Context, nodeID string, target ProbeTarget, ts time.Time, samples []probe.Sample) error
	RecordAgentHeartbeat(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) error
	UpsertAgentHost(ctx context.Context, nodeID string, host AgentHostRequest) error
	InsertAgentState(ctx context.Context, nodeID string, state AgentStateRequest) error
}

type agentProbeBatchStore interface {
	InsertAgentProbeResults(ctx context.Context, nodeID string, configVersion int64, rounds []preparedAgentProbeRound) error
}

type preparedAgentProbeRound struct {
	targetID       string
	targetType     string
	target         ProbeTarget
	ts             time.Time
	idempotencyKey string
	agentRoundID   string
	payloadHash    string
	samples        []probe.Sample
}

var (
	errAgentProbeConfigStale    = errors.New("stale probe config")
	errInvalidAgentProbeResults = errors.New("invalid agent probe results")
)

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
	var version int64
	if versionStore, ok := h.store.(probeConfigVersionStore); ok {
		version, _ = versionStore.ProbeConfigVersion(r.Context())
	}
	response := AgentProbeTargetsResponse{Targets: make([]AgentProbeTarget, 0, len(targets)), Version: version}
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
	if !decodeJSONBody(w, r, &request, agentProbeJSONBodyLimit, false) {
		return
	}
	if len(request.Rounds) == 0 {
		writeError(w, http.StatusBadRequest, "rounds required")
		return
	}
	if len(request.Rounds) > maxAgentProbeRounds {
		writeError(w, http.StatusBadRequest, "too many rounds")
		return
	}
	configVersion, validConfigVersion := request.effectiveConfigVersion()
	if !validConfigVersion {
		writeError(w, http.StatusBadRequest, "invalid config version")
		return
	}

	prepared := make([]preparedAgentProbeRound, 0, len(request.Rounds))
	for _, round := range request.Rounds {
		roundID := strings.TrimSpace(round.RoundID)
		if roundID != "" && !validAgentProbeRoundID(roundID) {
			writeError(w, http.StatusBadRequest, "invalid probe round id")
			return
		}
		targetID := strings.TrimSpace(round.TargetID)
		if targetID == "" {
			writeError(w, http.StatusBadRequest, "unknown target")
			return
		}
		targetType := strings.TrimSpace(round.Type)
		if round.TS <= 0 {
			writeError(w, http.StatusBadRequest, "invalid timestamp")
			return
		}
		roundTS := time.Unix(round.TS, 0).UTC()
		if !agentTimestampWithinSkew(roundTS, time.Now().UTC()) {
			writeError(w, http.StatusBadRequest, "timestamp skew too large")
			return
		}
		if len(round.Samples) > maxAgentProbeSamplesPerRound {
			writeError(w, http.StatusBadRequest, "too many samples")
			return
		}
		samples := make([]probe.Sample, 0, len(round.Samples))
		seenSequences := make(map[int]struct{}, len(round.Samples))
		for index, sample := range round.Samples {
			seq := sample.Seq
			if seq == 0 {
				seq = index + 1
			}
			if seq < 1 {
				writeError(w, http.StatusBadRequest, "invalid sample sequence")
				return
			}
			if _, duplicate := seenSequences[seq]; duplicate {
				writeError(w, http.StatusBadRequest, "duplicate sample sequence")
				return
			}
			seenSequences[seq] = struct{}{}
			latency := sample.LatencyMS
			if latency != nil {
				if math.IsNaN(*latency) || math.IsInf(*latency, 0) || *latency < 0 {
					writeError(w, http.StatusBadRequest, "invalid sample latency")
					return
				}
				normalized := *latency
				if normalized > float64(localDrawableLatencyCap/time.Millisecond) {
					normalized = float64(localDrawableLatencyCap / time.Millisecond)
				}
				latency = &normalized
			}
			samples = append(samples, probe.Sample{Seq: seq, Success: sample.Success, LatencyMS: latency, Error: strings.TrimSpace(sample.Error)})
		}
		if len(samples) == 0 {
			writeError(w, http.StatusBadRequest, "samples required")
			return
		}
		prepared = append(prepared, preparedAgentProbeRound{targetID: targetID, targetType: targetType, ts: roundTS, agentRoundID: roundID, samples: samples})
	}

	if batchStore, ok := store.(agentProbeBatchStore); ok {
		if err := batchStore.InsertAgentProbeResults(r.Context(), nodeID, configVersion, prepared); err != nil {
			if errors.Is(err, errAgentProbeConfigStale) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "stale_probe_config"})
				return
			}
			if errors.Is(err, errInvalidAgentProbeResults) {
				writeError(w, http.StatusBadRequest, "invalid probe round")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.publishSummary(r.Context())
	h.publishNodeLatency(r.Context(), nodeID)
	seenTargetIDs := map[string]struct{}{}
	for _, round := range prepared {
		targetID := round.target.ID
		if targetID == "" {
			targetID = round.targetID
		}
		if targetID == "" {
			continue
		}
		if _, ok := seenTargetIDs[targetID]; ok {
			continue
		}
		seenTargetIDs[targetID] = struct{}{}
		h.publishServiceLatency(r.Context(), targetID)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "accepted": len(prepared)})
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
	if !decodeJSONBody(w, r, &request, agentStateJSONBodyLimit, false) {
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
	h.invalidateSummaryCache()
	h.publishSummary(r.Context())
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
	if !decodeJSONBody(w, r, &request, agentStateJSONBodyLimit, false) {
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
	h.invalidateSummaryCache()
	h.publishSummary(r.Context())
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
	if !decodeJSONBody(w, r, &request, agentStateJSONBodyLimit, false) {
		return
	}
	if request.TS <= 0 {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	stateTS := time.Unix(request.TS, 0).UTC()
	if !agentTimestampWithinSkew(stateTS, time.Now().UTC()) {
		writeError(w, http.StatusBadRequest, "timestamp skew too large")
		return
	}
	if request.CPUPercent < 0 || request.CPUPercent > 100 || optionalFloatNegative(request.Load1) || optionalFloatNegative(request.Load5) || optionalFloatNegative(request.Load15) || request.MemoryUsedBytes < 0 || request.MemoryTotalBytes < 0 || optionalIntNegative(request.SwapUsedBytes) || optionalIntNegative(request.SwapTotalBytes) || request.DiskUsedBytes < 0 || request.DiskTotalBytes < 0 || request.NetInTotalBytes < 0 || request.NetOutTotalBytes < 0 || request.NetInSpeedBps < 0 || request.NetOutSpeedBps < 0 || optionalIntNegative(request.ProcessCount) || optionalIntNegative(request.TCPConnectionCount) || request.UptimeSeconds < 0 {
		writeError(w, http.StatusBadRequest, "invalid state values")
		return
	}
	if err := store.InsertAgentState(r.Context(), nodeID, request); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if transitionStore, ok := store.(stateAlertRuleTransitionStore); ok {
		transition, err := transitionStore.RecordAgentStateAlertRuleTransition(r.Context(), nodeID, stateTS, request)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.dispatchAgentStatusNotification(store, transition, stateTS)
	}
	h.publishSummary(r.Context())
	h.publishNodeState(r.Context(), nodeID)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func agentTimestampWithinSkew(ts, now time.Time) bool {
	if ts.After(now.Add(maxAgentTimestampFutureSkew)) {
		return false
	}
	if ts.Before(now.Add(-maxAgentTimestampPastSkew)) {
		return false
	}
	return true
}

func validAgentProbeRoundID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validAgentStatus(status string) bool {
	switch status {
	case "online", "offline", "warning", "no_data":
		return true
	default:
		return false
	}
}

func optionalFloatNegative(value *float64) bool {
	return value != nil && *value < 0
}

func optionalIntNegative(value *int64) bool {
	return value != nil && *value < 0
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
