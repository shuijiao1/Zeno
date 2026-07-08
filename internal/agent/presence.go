package agent

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

type PresenceServerMessage struct {
	Type    string `json:"type"`
	Version int64  `json:"version,omitempty"`
}

type PresenceClientMessage struct {
	Type    string `json:"type"`
	Version int64  `json:"version,omitempty"`
}

type PresenceConfigHandler func(ctx context.Context, requestedVersion int64) (appliedVersion int64, err error)

var (
	presenceReadTimeout  = 75 * time.Second
	presenceWriteTimeout = 5 * time.Second
)

func (c *Client) RunPresence(ctx context.Context, handleConfig PresenceConfigHandler) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := c.runPresenceOnce(ctx, handleConfig)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("presence websocket disconnected: %v", err)
		}
		jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
		wait := backoff + jitter
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (c *Client) runPresenceOnce(ctx context.Context, handleConfig PresenceConfigHandler) error {
	presenceURL, err := c.PresenceWebSocketURL()
	if err != nil {
		return err
	}
	header := http.Header{}
	c.setAuthHeaders(header)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, presenceURL, header)
	if err != nil {
		return err
	}
	defer conn.Close()
	log.Printf("presence websocket connected")
	refreshReadDeadline := func() error {
		return conn.SetReadDeadline(time.Now().Add(presenceReadTimeout))
	}
	if err := refreshReadDeadline(); err != nil {
		return err
	}
	conn.SetPingHandler(func(appData string) error {
		if err := refreshReadDeadline(); err != nil {
			return err
		}
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(presenceWriteTimeout))
	})
	conn.SetPongHandler(func(appData string) error {
		return refreshReadDeadline()
	})
	writeJSON := func(value any) error {
		if err := conn.SetWriteDeadline(time.Now().Add(presenceWriteTimeout)); err != nil {
			return err
		}
		return conn.WriteJSON(value)
	}
	writeMessage := func(messageType int, payload []byte) error {
		if err := conn.SetWriteDeadline(time.Now().Add(presenceWriteTimeout)); err != nil {
			return err
		}
		return conn.WriteMessage(messageType, payload)
	}
	if handleConfig != nil {
		refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		appliedVersion, refreshErr := handleConfig(refreshCtx, 0)
		cancel()
		if refreshErr != nil {
			log.Printf("presence startup config refresh failed: %v", refreshErr)
		} else if appliedVersion > 0 {
			_ = writeJSON(PresenceClientMessage{Type: "config_applied", Version: appliedVersion})
		}
	}
	for {
		var message PresenceServerMessage
		if err := conn.ReadJSON(&message); err != nil {
			return err
		}
		switch message.Type {
		case "config_changed":
			if handleConfig == nil {
				continue
			}
			refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			appliedVersion, refreshErr := handleConfig(refreshCtx, message.Version)
			cancel()
			if refreshErr != nil {
				log.Printf("presence config refresh failed: %v", refreshErr)
				continue
			}
			if appliedVersion == 0 {
				appliedVersion = message.Version
			}
			payload, _ := json.Marshal(PresenceClientMessage{Type: "config_applied", Version: appliedVersion})
			if err := writeMessage(websocket.TextMessage, payload); err != nil {
				return err
			}
		}
	}
}
