package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type summaryCountingStore struct {
	mockStore
	calls atomic.Int32
}

type blockingSummaryJSONStore struct {
	mockStore
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

type continuouslyInvalidatingSummaryJSONStore struct {
	mockStore
	handler *handler
	calls   atomic.Int32
}

func (store *continuouslyInvalidatingSummaryJSONStore) Summary(context.Context) (SummaryResponse, error) {
	call := store.calls.Add(1)
	store.handler.invalidateSummaryCache()
	return SummaryResponse{Nodes: []Node{{ID: fmt.Sprintf("snapshot-%d", call)}}, Services: []ServiceTarget{}, LatencyPoints: []LatencyPoint{}}, nil
}

func (store *blockingSummaryJSONStore) Summary(context.Context) (SummaryResponse, error) {
	call := store.calls.Add(1)
	if call == 1 && store.started != nil {
		close(store.started)
		<-store.release
	}
	nodeID := "new"
	if call == 1 {
		nodeID = "old"
	}
	return SummaryResponse{Nodes: []Node{{ID: nodeID}}, Services: []ServiceTarget{}, LatencyPoints: []LatencyPoint{}}, nil
}

func (store *summaryCountingStore) Summary(ctx context.Context) (SummaryResponse, error) {
	store.calls.Add(1)
	return store.mockStore.Summary(ctx)
}

func TestSummaryPublishCadenceDoesNotOutrunAgentState(t *testing.T) {
	const agentStateCadence = 3 * time.Second
	if summaryPublishMinInterval < agentStateCadence {
		t.Fatalf("summary publish interval %s is shorter than agent state cadence %s", summaryPublishMinInterval, agentStateCadence)
	}
	if summaryPublishCoalesceDelay >= summaryPublishMinInterval {
		t.Fatalf("coalesce delay %s must remain shorter than publish interval %s", summaryPublishCoalesceDelay, summaryPublishMinInterval)
	}
}

func TestPublishSummaryWithoutClientsDoesNotReadStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &summaryCountingStore{}
	h := &handler{store: store, liveHub: newLiveUpdateHub(), backgroundCtx: ctx, backgroundCancel: cancel}

	h.publishSummary(ctx)

	if got := store.calls.Load(); got != 0 {
		t.Fatalf("summary store calls without clients = %d, want 0", got)
	}
	h.summaryPublishMu.Lock()
	timer := h.summaryPublishTimer
	h.summaryPublishMu.Unlock()
	if timer != nil {
		t.Fatal("summary publish scheduled without websocket clients")
	}
}

func TestSummaryJSONHTTPMissCoalescesCompleteBuild(t *testing.T) {
	store := &blockingSummaryJSONStore{started: make(chan struct{}), release: make(chan struct{})}
	h := &handler{store: store}

	const callers = 16
	results := make(chan string, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			payload, err := h.summaryJSONForHTTP(context.Background())
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- string(payload)
		}()
	}
	<-store.started
	if got := store.calls.Load(); got != 1 {
		t.Fatalf("Summary calls while leader is blocked = %d, want 1", got)
	}
	close(store.release)
	wait.Wait()
	close(results)

	if got := store.calls.Load(); got != 1 {
		t.Fatalf("coalesced Summary calls = %d, want 1", got)
	}
	for result := range results {
		if !strings.Contains(result, `"id":"old"`) {
			t.Fatalf("coalesced payload = %q", result)
		}
	}
}

func TestSummaryJSONInvalidationPreventsOldFlightCommit(t *testing.T) {
	store := &blockingSummaryJSONStore{started: make(chan struct{}), release: make(chan struct{})}
	h := &handler{store: store}
	done := make(chan struct{})
	var payload []byte
	var loadErr error
	go func() {
		defer close(done)
		payload, loadErr = h.summaryJSONForHTTP(context.Background())
	}()
	<-store.started
	h.invalidateSummaryCache()
	close(store.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("summary JSON reload did not finish")
	}
	if loadErr != nil {
		t.Fatalf("summary JSON reload: %v", loadErr)
	}
	if got := store.calls.Load(); got != 2 {
		t.Fatalf("Summary calls after generation change = %d, want 2", got)
	}
	if strings.Contains(string(payload), `"id":"old"`) || !strings.Contains(string(payload), `"id":"new"`) {
		t.Fatalf("payload after invalidation = %s, want new generation", payload)
	}
	cached, ok := h.cachedSummaryJSON(summaryCacheHTTPFreshFor)
	if !ok || string(cached) != string(payload) {
		t.Fatalf("cached payload = %s ok=%v, want current generation", cached, ok)
	}
}

func TestSummaryJSONContinuousInvalidationReturnsBoundedSnapshot(t *testing.T) {
	store := &continuouslyInvalidatingSummaryJSONStore{}
	h := &handler{store: store}
	store.handler = h

	started := time.Now()
	payload, err := h.summaryJSONForHTTP(context.Background())
	if err != nil {
		t.Fatalf("summary JSON under continuous invalidation: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded summary build took %s", elapsed)
	}
	if got := store.calls.Load(); got != summaryGenerationMaxRetries+1 {
		t.Fatalf("Summary calls = %d, want bounded %d", got, summaryGenerationMaxRetries+1)
	}
	if !strings.Contains(string(payload), `"id":"snapshot-2"`) {
		t.Fatalf("payload = %s, want latest completed snapshot", payload)
	}
	if _, ok := h.cachedSummaryJSON(summaryCacheHTTPFreshFor); ok {
		t.Fatal("continuously invalidated snapshot was committed to cache")
	}
}

func TestHTTPOnlyStaleSummaryHitDoesNotScheduleNoopRefresh(t *testing.T) {
	store := &summaryCountingStore{}
	h := &handler{store: store, liveHub: newLiveUpdateHub()}
	h.rememberSummaryJSON([]byte(`{"nodes":[],"services":[],"latency_points":[]}`))
	h.summaryCacheMu.Lock()
	h.summaryCacheUpdated = time.Now().Add(-summaryCacheIdleRefreshFor - time.Second)
	h.summaryCacheMu.Unlock()

	request := httptest.NewRequest("GET", "/api/public/v1/summary", nil)
	response := httptest.NewRecorder()
	h.handleSummary(response, request)
	if response.Code != 200 {
		t.Fatalf("summary status = %d, want 200", response.Code)
	}
	if got := store.calls.Load(); got != 0 {
		t.Fatalf("HTTP stale cache hit read store %d times, want 0", got)
	}
	h.summaryPublishMu.Lock()
	timer := h.summaryPublishTimer
	h.summaryPublishMu.Unlock()
	if timer != nil {
		t.Fatal("HTTP-only stale cache hit created a no-op publish timer")
	}
}

func TestPublicWebSocketGateLimitsEachTrustedProxyClientFairly(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatalf("parse trusted proxies: %v", err)
	}
	h := &handler{
		trustedProxies: trusted,
		publicWSGate:   newWebSocketGateWithPerKey(4, 1),
	}
	requestFor := func(client string) *http.Request {
		request := httptest.NewRequest("GET", "/api/public/v1/summary/ws", nil)
		request.RemoteAddr = "10.0.0.10:443"
		request.Header.Set("X-Forwarded-For", client)
		return request
	}

	releaseA, ok := h.acquirePublicWebSocket(requestFor("203.0.113.10"))
	if !ok {
		t.Fatal("first client was rejected")
	}
	defer releaseA()
	if _, ok := h.acquirePublicWebSocket(requestFor("203.0.113.10")); ok {
		t.Fatal("same client exceeded per-IP websocket limit")
	}
	releaseB, ok := h.acquirePublicWebSocket(requestFor("203.0.113.11"))
	if !ok {
		t.Fatal("one saturated client blocked a different client")
	}
	releaseB()
}

func TestScheduleDetailPublishCoalescesSameTopicWithTrailingRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &handler{
		backgroundCtx:        ctx,
		backgroundCancel:     cancel,
		detailPublishPending: make(map[string]bool),
		detailPublishGate:    make(chan struct{}, detailPublishMaxConcurrent),
	}
	started := make(chan struct{})
	trailing := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	publish := func(context.Context) {
		call := calls.Add(1)
		if call == 1 {
			close(started)
		}
		<-release
		if call == 2 {
			close(trailing)
		}
	}

	h.scheduleDetailPublish("node-state:hytron", publish)
	<-started
	h.scheduleDetailPublish("node-state:hytron", publish)
	close(release)
	select {
	case <-trailing:
	case <-time.After(time.Second):
		t.Fatal("trailing detail refresh did not run")
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
	defer cleanupCancel()
	if err := h.Cleanup(cleanupCtx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("coalesced detail publish calls = %d, want one active and one trailing refresh", got)
	}
}
