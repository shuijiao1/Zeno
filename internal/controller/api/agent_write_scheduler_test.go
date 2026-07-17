package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

type acquiredAgentWrite struct {
	node    string
	release func()
	err     error
}

func TestAgentWriteSchedulerRoundRobinsWaitingNodes(t *testing.T) {
	var scheduler agentWriteScheduler
	firstRelease, err := scheduler.acquire(context.Background(), "node-a")
	if err != nil {
		t.Fatalf("acquire first node-a write: %v", err)
	}
	results := make(chan acquiredAgentWrite, 2)
	go func() {
		release, err := scheduler.acquire(context.Background(), "node-a")
		results <- acquiredAgentWrite{node: "node-a", release: release, err: err}
	}()
	waitForAgentWriteQueue(t, &scheduler, 1)
	go func() {
		release, err := scheduler.acquire(context.Background(), "node-b")
		results <- acquiredAgentWrite{node: "node-b", release: release, err: err}
	}()
	waitForAgentWriteQueue(t, &scheduler, 2)

	firstRelease()
	first := receiveAgentWrite(t, results)
	if first.err != nil || first.node != "node-b" {
		t.Fatalf("first queued grant = %+v, want node-b before second node-a write", first)
	}
	first.release()
	second := receiveAgentWrite(t, results)
	if second.err != nil || second.node != "node-a" {
		t.Fatalf("second queued grant = %+v, want node-a", second)
	}
	second.release()
}

func TestAgentWriteSchedulerCancellationDoesNotStrandPermit(t *testing.T) {
	var scheduler agentWriteScheduler
	release, err := scheduler.acquire(context.Background(), "node-a")
	if err != nil {
		t.Fatalf("acquire active write: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := scheduler.acquire(ctx, "node-b")
		result <- err
	}()
	waitForAgentWriteQueue(t, &scheduler, 1)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled acquire error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled acquire did not return")
	}
	release()
	finalRelease, err := scheduler.acquire(context.Background(), "node-c")
	if err != nil {
		t.Fatalf("acquire after cancellation: %v", err)
	}
	finalRelease()
}

func waitForAgentWriteQueue(t *testing.T, scheduler *agentWriteScheduler, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		scheduler.mu.Lock()
		queued := scheduler.queued
		scheduler.mu.Unlock()
		if queued == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("agent write queue did not reach %d", want)
}

func receiveAgentWrite(t *testing.T, results <-chan acquiredAgentWrite) acquiredAgentWrite {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduled writer")
		return acquiredAgentWrite{}
	}
}
