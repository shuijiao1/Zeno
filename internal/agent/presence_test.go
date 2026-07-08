package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRunPresenceOnceTimesOutIdleConnection(t *testing.T) {
	oldReadTimeout := presenceReadTimeout
	presenceReadTimeout = 50 * time.Millisecond
	t.Cleanup(func() { presenceReadTimeout = oldReadTimeout })

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewClient(server.URL, "hytron", "token")
	started := time.Now()
	err := client.runPresenceOnce(context.Background(), nil)
	if err == nil {
		t.Fatal("runPresenceOnce returned nil, want idle read timeout")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("runPresenceOnce error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runPresenceOnce elapsed %s, want quick timeout", elapsed)
	}
}

func TestRunPresenceOnceExtendsDeadlineOnServerPing(t *testing.T) {
	oldReadTimeout := presenceReadTimeout
	oldWriteTimeout := presenceWriteTimeout
	presenceReadTimeout = 50 * time.Millisecond
	presenceWriteTimeout = 50 * time.Millisecond
	t.Cleanup(func() {
		presenceReadTimeout = oldReadTimeout
		presenceWriteTimeout = oldWriteTimeout
	})

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for i := 0; i < 3; i++ {
			deadline := time.Now().Add(50 * time.Millisecond)
			if err := conn.WriteControl(websocket.PingMessage, []byte("keepalive"), deadline); err != nil {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		_ = conn.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(50*time.Millisecond))
	}))
	defer server.Close()

	client := NewClient(server.URL, "hytron", "token")
	started := time.Now()
	err := client.runPresenceOnce(context.Background(), nil)
	if err == nil {
		t.Fatal("runPresenceOnce returned nil, want close error")
	}
	if elapsed := time.Since(started); elapsed < 70*time.Millisecond {
		t.Fatalf("runPresenceOnce elapsed %s, deadline was not extended by pings", elapsed)
	}
}
