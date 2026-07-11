package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDetailJSONCacheCoalescesConcurrentLoads(t *testing.T) {
	cache := newDetailJSONCache()
	started := make(chan struct{})
	release := make(chan struct{})
	var loads atomic.Int32
	load := func() ([]byte, error) {
		if loads.Add(1) == 1 {
			close(started)
		}
		<-release
		return []byte(`{"ok":true}`), nil
	}

	const callers = 8
	results := make(chan string, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			payload, err := cache.get(context.Background(), "node-latency:hytron:1d", time.Second, load)
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- string(payload)
		}()
	}
	<-started
	close(release)
	wait.Wait()
	close(results)

	if got := loads.Load(); got != 1 {
		t.Fatalf("load count = %d, want 1", got)
	}
	for result := range results {
		if result != `{"ok":true}` {
			t.Fatalf("payload = %q", result)
		}
	}
}

func TestDetailJSONCacheRefreshWinsOverOlderInflightLoad(t *testing.T) {
	cache := newDetailJSONCache()
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		_, _ = cache.get(context.Background(), "node-state:hytron:1h", time.Second, func() ([]byte, error) {
			close(oldStarted)
			<-releaseOld
			return []byte(`{"value":"old"}`), nil
		})
	}()
	<-oldStarted

	payload, err := cache.refresh("node-state:hytron:1h", func() ([]byte, error) {
		return []byte(`{"value":"fresh"}`), nil
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if string(payload) != `{"value":"fresh"}` {
		t.Fatalf("refresh payload = %s", payload)
	}
	close(releaseOld)
	<-oldDone

	payload, err = cache.get(context.Background(), "node-state:hytron:1h", time.Second, func() ([]byte, error) {
		return []byte(`{"value":"unexpected"}`), nil
	})
	if err != nil {
		t.Fatalf("cached get: %v", err)
	}
	if string(payload) != `{"value":"fresh"}` {
		t.Fatalf("cached payload = %s, want fresh", payload)
	}
}
