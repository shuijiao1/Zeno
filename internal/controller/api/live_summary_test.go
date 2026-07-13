package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type summaryCountingStore struct {
	mockStore
	calls atomic.Int32
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
