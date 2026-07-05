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

type liveSummaryHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newLiveSummaryHub() *liveSummaryHub {
	return &liveSummaryHub{clients: map[chan []byte]struct{}{}}
}

func (hub *liveSummaryHub) subscribe() (<-chan []byte, func()) {
	updates := make(chan []byte, 1)
	hub.mu.Lock()
	hub.clients[updates] = struct{}{}
	hub.mu.Unlock()
	return updates, func() {
		hub.mu.Lock()
		if _, ok := hub.clients[updates]; ok {
			delete(hub.clients, updates)
			close(updates)
		}
		hub.mu.Unlock()
	}
}

func (hub *liveSummaryHub) hasClients() bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.clients) > 0
}

func (hub *liveSummaryHub) publish(payload []byte) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for updates := range hub.clients {
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
	updates, unsubscribe := h.summaryHub.subscribe()
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
	conn, err := summaryWebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	updates, unsubscribe := h.summaryHub.subscribe()
	defer unsubscribe()

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

func (h *handler) publishSummary(ctx context.Context) {
	if h.summaryHub == nil || !h.summaryHub.hasClients() {
		return
	}
	payload, err := h.summaryJSON(ctx)
	if err != nil {
		return
	}
	h.summaryHub.publish(payload)
}

func writeSummaryEvent(w io.Writer, payload []byte) {
	_, _ = fmt.Fprintf(w, "event: summary\ndata: %s\n\n", payload)
}
