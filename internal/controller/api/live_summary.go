package api

import (
	"bytes"
	"context"
	"encoding/json"
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
	last    map[string][]byte
}

func newLiveUpdateHub() *liveUpdateHub {
	return &liveUpdateHub{clients: map[string]map[chan []byte]struct{}{}, last: map[string][]byte{}}
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
	if bytes.Equal(hub.last[topic], payload) {
		return
	}
	hub.last[topic] = append(hub.last[topic][:0], payload...)
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

const (
	summaryPublishCoalesceDelay = 250 * time.Millisecond
	// Agents report state every three seconds. Rebuilding the full public
	// summary more frequently cannot expose newer state, but repeatedly scans
	// the rolling 24-hour latency aggregates on large databases.
	summaryPublishMinInterval = 3 * time.Second
)

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

var summaryWebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:    32 * 1024,
	WriteBufferSize:   32 * 1024,
	EnableCompression: true,
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

const (
	summaryCacheHTTPFreshFor    = 30 * time.Second
	summaryCacheIdleRefreshFor  = 15 * time.Second
	summaryCacheBackgroundDelay = 350 * time.Millisecond
	detailCacheFreshFor         = 3 * time.Second
	websocketWriteTimeout       = 5 * time.Second
	websocketReadTimeout        = 75 * time.Second
)

func (h *handler) handleSummaryWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	release, ok := h.publicWSGate.acquire()
	if !ok {
		writeError(w, http.StatusTooManyRequests, "too many websocket connections")
		return
	}
	defer release()
	initial, cached := h.cachedSummaryJSON(0)
	if !cached {
		var err error
		initial, err = h.summaryJSON(r.Context())
		if err != nil {
			writeStoreError(w, err)
			return
		}
	}
	updates, unsubscribe := h.liveHub.subscribe(summaryLiveTopic)
	if cached {
		h.scheduleSummaryPublishAfter(summaryCacheBackgroundDelay)
	}
	h.handleLiveJSONWebSocket(w, r, initial, updates, unsubscribe)
}

func (h *handler) handleNodeStateWebSocket(w http.ResponseWriter, r *http.Request, nodeID string, window latencyWindow) {
	release, ok := h.publicWSGate.acquire()
	if !ok {
		writeError(w, http.StatusTooManyRequests, "too many websocket connections")
		return
	}
	defer release()
	payload, err := h.nodeStateJSON(r.Context(), nodeID, window)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	updates, unsubscribe := h.liveHub.subscribe(nodeStateLiveTopic(nodeID, window.Name))
	h.handleLiveJSONWebSocket(w, r, payload, updates, unsubscribe)
}

func (h *handler) handleNodeLatencyWebSocket(w http.ResponseWriter, r *http.Request, nodeID string, window latencyWindow) {
	release, ok := h.publicWSGate.acquire()
	if !ok {
		writeError(w, http.StatusTooManyRequests, "too many websocket connections")
		return
	}
	defer release()
	payload, err := h.nodeLatencyJSON(r.Context(), nodeID, window)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	updates, unsubscribe := h.liveHub.subscribe(nodeLatencyLiveTopic(nodeID, window.Name))
	h.handleLiveJSONWebSocket(w, r, payload, updates, unsubscribe)
}

func (h *handler) handleServiceLatencyWebSocket(w http.ResponseWriter, r *http.Request, targetID string, window latencyWindow) {
	release, ok := h.publicWSGate.acquire()
	if !ok {
		writeError(w, http.StatusTooManyRequests, "too many websocket connections")
		return
	}
	defer release()
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
	conn.EnableWriteCompression(true)
	refreshReadDeadline := func() error {
		return conn.SetReadDeadline(time.Now().Add(websocketReadTimeout))
	}
	if err := refreshReadDeadline(); err != nil {
		return
	}
	conn.SetPongHandler(func(string) error { return refreshReadDeadline() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.SetReadLimit(1024)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
			if err := refreshReadDeadline(); err != nil {
				return
			}
		}
	}()

	if err := writeWebSocketMessage(conn, websocket.TextMessage, initial); err != nil {
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
			if err := writeWebSocketMessage(conn, websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ping.C:
			if err := writeWebSocketControl(conn, websocket.PingMessage, []byte{}, websocketWriteTimeout); err != nil {
				return
			}
		}
	}
}

func writeWebSocketMessage(conn *websocket.Conn, messageType int, payload []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout)); err != nil {
		return err
	}
	err := conn.WriteMessage(messageType, payload)
	clearErr := conn.SetWriteDeadline(time.Time{})
	if err != nil {
		return err
	}
	return clearErr
}

func writeWebSocketControl(conn *websocket.Conn, messageType int, payload []byte, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = websocketWriteTimeout
	}
	return conn.WriteControl(messageType, payload, time.Now().Add(timeout))
}

func (h *handler) summaryJSON(ctx context.Context) ([]byte, error) {
	summary, err := h.store.Summary(ctx)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		return nil, err
	}
	h.rememberSummaryJSON(payload)
	return payload, nil
}

func (h *handler) nodeStateJSON(ctx context.Context, nodeID string, window latencyWindow) ([]byte, error) {
	key := nodeStateLiveTopic(nodeID, window.Name)
	return h.detailCache.get(ctx, key, detailCacheFreshFor, func() ([]byte, error) {
		state, err := h.store.NodeState(ctx, nodeID, window)
		if err != nil {
			return nil, err
		}
		return json.Marshal(state)
	})
}

func (h *handler) nodeLatencyJSON(ctx context.Context, nodeID string, window latencyWindow) ([]byte, error) {
	key := nodeLatencyLiveTopic(nodeID, window.Name)
	return h.detailCache.get(ctx, key, detailCacheFreshFor, func() ([]byte, error) {
		latency, err := h.store.NodeLatency(ctx, nodeID, window)
		if err != nil {
			return nil, err
		}
		return json.Marshal(latency)
	})
}

func (h *handler) serviceLatencyJSON(ctx context.Context, targetID string, window latencyWindow) ([]byte, error) {
	key := serviceLatencyLiveTopic(targetID, window.Name)
	return h.detailCache.get(ctx, key, detailCacheFreshFor, func() ([]byte, error) {
		latency, err := h.store.ServiceTargetLatency(ctx, targetID, window)
		if err != nil {
			return nil, err
		}
		return json.Marshal(latency)
	})
}

func (h *handler) publishSummary(_ context.Context) {
	if h.liveHub == nil {
		return
	}
	if !h.liveHub.hasClients(summaryLiveTopic) && !h.summaryCacheStale(summaryCacheIdleRefreshFor) {
		return
	}
	h.scheduleSummaryPublish()
}

func (h *handler) scheduleSummaryPublish() {
	if h == nil || h.backgroundContext().Err() != nil {
		return
	}
	now := time.Now()
	h.summaryPublishMu.Lock()
	if h.summaryPublishTimer != nil {
		h.summaryPublishMu.Unlock()
		return
	}
	wait := summaryPublishCoalesceDelay
	if !h.summaryLastPublished.IsZero() {
		minWait := h.summaryLastPublished.Add(summaryPublishMinInterval).Sub(now)
		if minWait > wait {
			wait = minWait
		}
	}
	timer := time.NewTimer(wait)
	h.summaryPublishTimer = timer
	h.backgroundWG.Add(1)
	go func() {
		defer h.backgroundWG.Done()
		select {
		case <-h.backgroundContext().Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
		h.summaryPublishMu.Lock()
		if h.summaryPublishTimer != timer {
			h.summaryPublishMu.Unlock()
			return
		}
		h.summaryPublishTimer = nil
		h.summaryLastPublished = time.Now()
		h.summaryPublishMu.Unlock()

		ctx, cancel := context.WithTimeout(h.backgroundContext(), 5*time.Second)
		defer cancel()
		h.publishSummaryNow(ctx)
	}()
	h.summaryPublishMu.Unlock()
}

func (h *handler) publishSummaryNow(ctx context.Context) {
	if h.liveHub == nil {
		return
	}
	payload, err := h.summaryJSON(ctx)
	if err != nil {
		return
	}
	if !h.liveHub.hasClients(summaryLiveTopic) {
		return
	}
	h.liveHub.publish(summaryLiveTopic, payload)
}

func (h *handler) cachedSummaryJSON(maxAge time.Duration) ([]byte, bool) {
	h.summaryCacheMu.RLock()
	defer h.summaryCacheMu.RUnlock()
	if len(h.summaryCache) == 0 {
		return nil, false
	}
	if maxAge > 0 && time.Since(h.summaryCacheUpdated) > maxAge {
		return nil, false
	}
	return append([]byte(nil), h.summaryCache...), true
}

func (h *handler) rememberSummaryJSON(payload []byte) {
	h.summaryCacheMu.Lock()
	h.summaryCache = append(h.summaryCache[:0], payload...)
	h.summaryCacheUpdated = time.Now()
	h.summaryCacheMu.Unlock()
}

func (h *handler) invalidateSummaryCache() {
	h.summaryCacheMu.Lock()
	h.summaryCache = nil
	h.summaryCacheUpdated = time.Time{}
	h.summaryCacheMu.Unlock()
}

func (h *handler) summaryCacheStale(maxAge time.Duration) bool {
	h.summaryCacheMu.RLock()
	defer h.summaryCacheMu.RUnlock()
	return len(h.summaryCache) == 0 || time.Since(h.summaryCacheUpdated) > maxAge
}

func (h *handler) scheduleSummaryPublishAfter(delay time.Duration) {
	if h == nil || h.backgroundContext().Err() != nil {
		return
	}
	h.summaryPublishMu.Lock()
	if h.summaryPublishTimer != nil {
		h.summaryPublishMu.Unlock()
		return
	}
	timer := time.NewTimer(delay)
	h.summaryPublishTimer = timer
	h.backgroundWG.Add(1)
	go func() {
		defer h.backgroundWG.Done()
		select {
		case <-h.backgroundContext().Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
		h.summaryPublishMu.Lock()
		if h.summaryPublishTimer != timer {
			h.summaryPublishMu.Unlock()
			return
		}
		h.summaryPublishTimer = nil
		h.summaryLastPublished = time.Now()
		h.summaryPublishMu.Unlock()

		ctx, cancel := context.WithTimeout(h.backgroundContext(), 5*time.Second)
		defer cancel()
		h.publishSummaryNow(ctx)
	}()
	h.summaryPublishMu.Unlock()
}

func (h *handler) publishNodeState(ctx context.Context, nodeID string) {
	if h.liveHub == nil {
		return
	}
	for _, rangeName := range liveWindowNames() {
		window, ok := resolveStateWindow(rangeName)
		if !ok {
			continue
		}
		topic := nodeStateLiveTopic(nodeID, window.Name)
		if !h.liveHub.hasClients(topic) {
			continue
		}
		payload, err := h.detailCache.refresh(topic, func() ([]byte, error) {
			state, err := h.store.NodeState(ctx, nodeID, window)
			if err != nil {
				return nil, err
			}
			return json.Marshal(state)
		})
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
		payload, err := h.detailCache.refresh(topic, func() ([]byte, error) {
			latency, err := h.store.NodeLatency(ctx, nodeID, window)
			if err != nil {
				return nil, err
			}
			return json.Marshal(latency)
		})
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
		payload, err := h.detailCache.refresh(topic, func() ([]byte, error) {
			latency, err := h.store.ServiceTargetLatency(ctx, targetID, window)
			if err != nil {
				return nil, err
			}
			return json.Marshal(latency)
		})
		if err == nil {
			h.liveHub.publish(topic, payload)
		}
	}
}
