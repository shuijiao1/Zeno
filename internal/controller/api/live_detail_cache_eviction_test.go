package api

import (
	"context"
	"testing"
	"time"
)

func TestDetailJSONCachePrunesExpiredEntriesAndEvictsKeys(t *testing.T) {
	cache := newDetailJSONCache()
	ctx := context.Background()
	if _, err := cache.get(ctx, "node-state:old:1h", time.Minute, func() ([]byte, error) { return []byte(`{"old":true}`), nil }); err != nil {
		t.Fatalf("prime old entry: %v", err)
	}
	cache.mu.Lock()
	cache.entries["node-state:old:1h"] = detailJSONCacheEntry{payload: []byte(`{"old":true}`), updatedAt: time.Now().Add(-2 * time.Minute)}
	cache.generations["node-state:old:1h"] = 7
	cache.mu.Unlock()
	if _, err := cache.get(ctx, "node-state:new:1h", time.Minute, func() ([]byte, error) { return []byte(`{"new":true}`), nil }); err != nil {
		t.Fatalf("load new entry: %v", err)
	}
	cache.mu.Lock()
	_, oldEntry := cache.entries["node-state:old:1h"]
	_, oldGeneration := cache.generations["node-state:old:1h"]
	cache.mu.Unlock()
	if oldEntry || oldGeneration {
		t.Fatal("expired detail cache key was not pruned")
	}

	cache.evict("node-state:new:1h")
	cache.mu.Lock()
	_, newEntry := cache.entries["node-state:new:1h"]
	_, newGeneration := cache.generations["node-state:new:1h"]
	cache.mu.Unlock()
	if newEntry || newGeneration {
		t.Fatal("explicit detail cache eviction left key state behind")
	}
}

func TestLiveUpdateHubDropsLastPayloadWhenTopicBecomesIdle(t *testing.T) {
	hub := newLiveUpdateHub()
	updates, unsubscribe := hub.subscribe("node-latency:hytron:1h")
	hub.publish("node-latency:hytron:1h", []byte(`{"ok":true}`))
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published payload")
	}
	unsubscribe()
	hub.mu.Lock()
	_, hasClients := hub.clients["node-latency:hytron:1h"]
	_, hasLast := hub.last["node-latency:hytron:1h"]
	hub.mu.Unlock()
	if hasClients || hasLast {
		t.Fatalf("idle topic retained clients=%v last=%v", hasClients, hasLast)
	}
}

func TestAgentPresenceManagerDoesNotRetainDisconnectedNodeKeys(t *testing.T) {
	manager := newAgentPresenceManager()
	session := manager.connect("hytron")
	if !manager.disconnect(session) {
		t.Fatal("disconnect returned false")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.sessions) != 0 {
		t.Fatalf("presence sessions retained after disconnect: %d", len(manager.sessions))
	}
}
