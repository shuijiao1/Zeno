package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type liveUpdateHub struct {
	mu      sync.Mutex
	clients map[string]map[chan []byte]struct{}
}

func newLiveUpdateHub() *liveUpdateHub {
	return &liveUpdateHub{clients: map[string]map[chan []byte]struct{}{}}
}

func (hub *liveUpdateHub) subscribe(topic string) (<-chan []byte, func()) {
	updates := make(chan []byte, 1)
	hub.mu.Lock()
	if hub.clients[topic] == nil {
		hub.clients[topic] = map[chan []byte]struct{}{}
	}
	hub.clients[topic][updates] = struct{}{}
	hub.mu.Unlock()
	return updates, func() {
		hub.mu.Lock()
		if clients, ok := hub.clients[topic]; ok {
			if _, ok := clients[updates]; ok {
				delete(clients, updates)
				close(updates)
			}
			if len(clients) == 0 {
				delete(hub.clients, topic)
			}
		} else {
			close(updates)
		}
		hub.mu.Unlock()
	}
}

func (hub *liveUpdateHub) hasClients(topic string) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.clients[topic]) > 0
}

func (hub *liveUpdateHub) publish(topic string, payload []byte) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	clients := hub.clients[topic]
	for updates := range clients {
		select {
		case updates <- payload:
		default:
			select {
			case <-updates:
			default:
			}
			select {
			case updates <- payload:
			default:
			}
		}
	}
}

const summaryLiveTopic = "summary"

func nodeStateLiveTopic(nodeID, rangeName string) string {
	return "node-state:" + nodeID + ":" + rangeName
}

func nodeLatencyLiveTopic(nodeID, rangeName string) string {
	return "node-latency:" + nodeID + ":" + rangeName
}

func serviceLatencyLiveTopic(targetID, rangeName string) string {
	return "service-latency:" + targetID + ":" + rangeName
}

func liveWindowNames() []string {
	return []string{"1h", "1d", "7d", "30d"}
}

func (h *handler) handleSummaryStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	updates, unsubscribe := h.liveHub.subscribe(summaryLiveTopic)
	defer unsubscribe()

	initial, err := h.summaryJSON(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	writeSummaryEvent(w, initial)
	flusher.Flush()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case payload, ok := <-updates:
			if !ok {
				return
			}
			writeSummaryEvent(w, payload)
			flusher.Flush()
		case <-keepAlive.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

var summaryWebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		parsed, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(parsed.Host, r.Host)
	},
}

func (h *handler) handleSummaryWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	initial, err := h.summaryJSON(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	updates, unsubscribe := h.liveHub.subscribe(summaryLiveTopic)
	h.handleLiveJSONWebSocket(w, r, initial, updates, unsubscribe)
}

func (h *handler) handleNodeStateWebSocket(w http.ResponseWriter, r *http.Request, nodeID string, window latencyWindow) {
	payload, err := h.nodeStateJSON(r.Context(), nodeID, window)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	updates, unsubscribe := h.liveHub.subscribe(nodeStateLiveTopic(nodeID, window.Name))
	h.handleLiveJSONWebSocket(w, r, payload, updates, unsubscribe)
}

func (h *handler) handleNodeLatencyWebSocket(w http.ResponseWriter, r *http.Request, nodeID string, window latencyWindow) {
	payload, err := h.nodeLatencyJSON(r.Context(), nodeID, window)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	updates, unsubscribe := h.liveHub.subscribe(nodeLatencyLiveTopic(nodeID, window.Name))
	h.handleLiveJSONWebSocket(w, r, payload, updates, unsubscribe)
}

func (h *handler) handleServiceLatencyWebSocket(w http.ResponseWriter, r *http.Request, targetID string, window latencyWindow) {
	payload, err := h.serviceLatencyJSON(r.Context(), targetID, window)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	updates, unsubscribe := h.liveHub.subscribe(serviceLatencyLiveTopic(targetID, window.Name))
	h.handleLiveJSONWebSocket(w, r, payload, updates, unsubscribe)
}

func (h *handler) handleLiveJSONWebSocket(w http.ResponseWriter, r *http.Request, initial []byte, updates <-chan []byte, unsubscribe func()) {
	defer unsubscribe()
	conn, err := summaryWebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.SetReadLimit(1024)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	if err := conn.WriteMessage(websocket.TextMessage, initial); err != nil {
		return
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case payload, ok := <-updates:
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

func (h *handler) summaryJSON(ctx context.Context) ([]byte, error) {
	summary, err := h.store.Summary(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(summary)
}

func (h *handler) nodeStateJSON(ctx context.Context, nodeID string, window latencyWindow) ([]byte, error) {
	state, err := h.store.NodeState(ctx, nodeID, window)
	if err != nil {
		return nil, err
	}
	return json.Marshal(state)
}

func (h *handler) nodeLatencyJSON(ctx context.Context, nodeID string, window latencyWindow) ([]byte, error) {
	latency, err := h.store.NodeLatency(ctx, nodeID, window)
	if err != nil {
		return nil, err
	}
	return json.Marshal(latency)
}

func (h *handler) serviceLatencyJSON(ctx context.Context, targetID string, window latencyWindow) ([]byte, error) {
	latency, err := h.store.ServiceTargetLatency(ctx, targetID, window)
	if err != nil {
		return nil, err
	}
	return json.Marshal(latency)
}

func (h *handler) publishSummary(ctx context.Context) {
	if h.liveHub == nil || !h.liveHub.hasClients(summaryLiveTopic) {
		return
	}
	payload, err := h.summaryJSON(ctx)
	if err != nil {
		return
	}
	h.liveHub.publish(summaryLiveTopic, payload)
}

func (h *handler) publishNodeState(ctx context.Context, nodeID string) {
	if h.liveHub == nil {
		return
	}
	for _, rangeName := range liveWindowNames() {
		window, ok := resolveLatencyWindow(rangeName)
		if !ok {
			continue
		}
		topic := nodeStateLiveTopic(nodeID, window.Name)
		if !h.liveHub.hasClients(topic) {
			continue
		}
		payload, err := h.nodeStateJSON(ctx, nodeID, window)
		if err == nil {
			h.liveHub.publish(topic, payload)
		}
	}
}

func (h *handler) publishNodeLatency(ctx context.Context, nodeID string) {
	if h.liveHub == nil {
		return
	}
	for _, rangeName := range liveWindowNames() {
		window, ok := resolveLatencyWindow(rangeName)
		if !ok {
			continue
		}
		topic := nodeLatencyLiveTopic(nodeID, window.Name)
		if !h.liveHub.hasClients(topic) {
			continue
		}
		payload, err := h.nodeLatencyJSON(ctx, nodeID, window)
		if err == nil {
			h.liveHub.publish(topic, payload)
		}
	}
}

func (h *handler) publishServiceLatency(ctx context.Context, targetID string) {
	if h.liveHub == nil {
		return
	}
	for _, rangeName := range liveWindowNames() {
		window, ok := resolveLatencyWindow(rangeName)
		if !ok {
			continue
		}
		topic := serviceLatencyLiveTopic(targetID, window.Name)
		if !h.liveHub.hasClients(topic) {
			continue
		}
		payload, err := h.serviceLatencyJSON(ctx, targetID, window)
		if err == nil {
			h.liveHub.publish(topic, payload)
		}
	}
}

func writeSummaryEvent(w io.Writer, payload []byte) {
	_, _ = fmt.Fprintf(w, "event: summary\ndata: %s\n\n", payload)
}
