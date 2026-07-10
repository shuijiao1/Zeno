package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type agentPresenceSession struct {
	nodeID string
	id     uint64
	send   chan []byte
	done   chan struct{}
}

type agentPresenceManager struct {
	mu       sync.Mutex
	nextID   uint64
	sessions map[string]*agentPresenceSession
	seen     map[string]bool
}

func newAgentPresenceManager() *agentPresenceManager {
	return &agentPresenceManager{sessions: map[string]*agentPresenceSession{}, seen: map[string]bool{}}
}

func (m *agentPresenceManager) connect(nodeID string) *agentPresenceSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	session := &agentPresenceSession{nodeID: nodeID, id: m.nextID, send: make(chan []byte, 8), done: make(chan struct{})}
	if previous := m.sessions[nodeID]; previous != nil {
		close(previous.done)
	}
	m.sessions[nodeID] = session
	m.seen[nodeID] = true
	return session
}

func (m *agentPresenceManager) disconnect(session *agentPresenceSession) bool {
	if session == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.sessions[session.nodeID]
	if current == nil || current.id != session.id {
		return false
	}
	delete(m.sessions, session.nodeID)
	return true
}

func (m *agentPresenceManager) isOnline(nodeID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[nodeID] != nil
}

func (m *agentPresenceManager) notifyConfigChanged(nodeID string, version int64) bool {
	if m == nil {
		return false
	}
	payload, err := json.Marshal(agentPresenceServerMessage{Type: "config_changed", Version: version})
	if err != nil {
		return false
	}
	m.mu.Lock()
	session := m.sessions[nodeID]
	m.mu.Unlock()
	if session == nil {
		return false
	}
	select {
	case session.send <- payload:
	default:
		select {
		case <-session.send:
		default:
		}
		select {
		case session.send <- payload:
		default:
		}
	}
	return true
}

func (m *agentPresenceManager) notifyAllConfigChanged(version int64) int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	nodeIDs := make([]string, 0, len(m.sessions))
	for nodeID := range m.sessions {
		nodeIDs = append(nodeIDs, nodeID)
	}
	m.mu.Unlock()
	count := 0
	for _, nodeID := range nodeIDs {
		if m.notifyConfigChanged(nodeID, version) {
			count++
		}
	}
	return count
}

type agentPresenceServerMessage struct {
	Type    string `json:"type"`
	Version int64  `json:"version,omitempty"`
}

type agentPresenceClientMessage struct {
	Type    string `json:"type"`
	Version int64  `json:"version,omitempty"`
}

type staleAgentOfflineStore interface {
	agentStore
	StaleAgentOfflineNodeIDs(ctx context.Context, now time.Time) ([]string, error)
	RecordStaleAgentOfflineTransition(ctx context.Context, nodeID string, now time.Time) (notificationStatusTransition, bool, error)
}

func (h *handler) runStaleAgentOfflineScanner(ctx context.Context, interval time.Duration) {
	h.runStaleAgentOfflineScannerWithGrace(ctx, interval, nodeHeartbeatOfflineAfter)
}

func (h *handler) runStaleAgentOfflineScannerWithGrace(ctx context.Context, interval, startupGrace time.Duration) {
	if h == nil || interval <= 0 || startupGrace <= 0 {
		return
	}
	// A freshly started Controller cannot distinguish an actually offline Agent
	// from one that simply has not reconnected after the Controller restart yet.
	// Give Agents one full liveness window before the first scan so deployments
	// and short Controller outages do not immediately create false incidents.
	startupTimer := time.NewTimer(startupGrace)
	defer startupTimer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-startupTimer.C:
		h.dispatchStaleAgentOfflineChecks(ctx)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.dispatchStaleAgentOfflineChecks(ctx)
		}
	}
}

func (h *handler) dispatchStaleAgentOfflineChecks(ctx context.Context) {
	if h == nil {
		return
	}
	store, ok := h.store.(staleAgentOfflineStore)
	if !ok {
		return
	}
	now := time.Now().UTC()
	nodeIDs, err := store.StaleAgentOfflineNodeIDs(ctx, now)
	if err != nil || len(nodeIDs) == 0 {
		return
	}
	changed := false
	for _, nodeID := range nodeIDs {
		transition, ok, err := store.RecordStaleAgentOfflineTransition(ctx, nodeID, now)
		if err != nil || !ok {
			continue
		}
		h.dispatchAgentStatusNotification(store, transition, now)
		changed = true
	}
	if !changed {
		return
	}
	h.invalidateSummaryCache()
	publishCtx, cancel := context.WithTimeout(h.backgroundContext(), 5*time.Second)
	defer cancel()
	h.publishSummaryNow(publishCtx)
}

func (h *handler) handleAgentPresenceWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, nodeID, ok := h.authorizeAgentRequest(w, r)
	if !ok {
		return
	}
	_ = store
	conn, err := summaryWebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.EnableWriteCompression(true)
	refreshReadDeadline := func() error {
		return conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	}
	_ = refreshReadDeadline()
	conn.SetPongHandler(func(string) error {
		return refreshReadDeadline()
	})

	session := h.presence.connect(nodeID)
	h.invalidateSummaryCache()
	h.publishSummaryNow(r.Context())
	defer func() {
		if h.presence.disconnect(session) {
			h.scheduleAgentPresenceOfflineCheck(store, nodeID)
			h.invalidateSummaryCache()
			ctx, cancel := context.WithTimeout(h.backgroundContext(), 5*time.Second)
			defer cancel()
			h.publishSummaryNow(ctx)
		}
	}()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		conn.SetReadLimit(4 << 10)
		for {
			_, reader, err := conn.NextReader()
			if err != nil {
				return
			}
			_ = refreshReadDeadline()
			var message agentPresenceClientMessage
			if err := json.NewDecoder(reader).Decode(&message); err != nil {
				continue
			}
			// config_applied is intentionally accepted as an acknowledgement only.
			// The authoritative probe configuration still lives behind the HTTP API.
		}
	}()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-session.done:
			_ = conn.WriteControl(websocketCloseMessage, []byte{}, time.Now().Add(2*time.Second))
			return
		case <-readDone:
			return
		case payload := <-session.send:
			if err := conn.WriteMessage(websocketTextMessage, payload); err != nil {
				return
			}
		case <-ping.C:
			if err := conn.WriteControl(websocketPingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

func (h *handler) scheduleAgentPresenceOfflineCheck(store agentStore, nodeID string) {
	if h == nil || h.presence == nil {
		return
	}
	if _, ok := store.(staleAgentOfflineStore); !ok {
		return
	}
	h.startBackground(func(ctx context.Context) {
		timer := time.NewTimer(nodeHeartbeatOfflineAfter)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if h.presence.isOnline(nodeID) {
			return
		}
		h.dispatchStaleAgentOfflineChecks(ctx)
	})
}

const (
	websocketTextMessage  = 1
	websocketCloseMessage = 8
	websocketPingMessage  = 9
)

func (h *handler) bumpProbeConfigAndNotify(ctx context.Context) {
	store, ok := h.store.(probeConfigVersionStore)
	if !ok {
		return
	}
	version, err := store.BumpProbeConfigVersion(ctx)
	if err != nil {
		return
	}
	h.presence.notifyAllConfigChanged(version)
}
