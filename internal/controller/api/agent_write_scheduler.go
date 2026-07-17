package api

import (
	"context"
	"errors"
	"strings"
	"sync"
)

const (
	maxQueuedAgentWrites        = 128
	maxQueuedAgentWritesPerNode = 16
)

var errAgentWriteQueueFull = errors.New("agent write queue full")

type agentWriteWaiter struct {
	ready   chan struct{}
	granted bool
}

// agentWriteScheduler preserves SQLite's one-writer model while preventing a
// noisy node from filling the head of one global mutex queue. Waiting writes
// are selected round-robin by node and the queue is bounded globally and per
// node. The caller's context provides the maximum queue + execution wait.
type agentWriteScheduler struct {
	mu         sync.Mutex
	active     bool
	activeNode string
	queues     map[string][]*agentWriteWaiter
	order      []string
	queued     int
}

func (scheduler *agentWriteScheduler) acquire(ctx context.Context, nodeID string) (func(), error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		nodeID = "_controller"
	}
	waiter := &agentWriteWaiter{ready: make(chan struct{})}

	scheduler.mu.Lock()
	if !scheduler.active {
		scheduler.active = true
		scheduler.activeNode = nodeID
		scheduler.mu.Unlock()
		return scheduler.releaseFunc(), nil
	}
	if scheduler.queued >= maxQueuedAgentWrites || len(scheduler.queues[nodeID]) >= maxQueuedAgentWritesPerNode {
		scheduler.mu.Unlock()
		return nil, errAgentWriteQueueFull
	}
	if scheduler.queues == nil {
		scheduler.queues = make(map[string][]*agentWriteWaiter)
	}
	if len(scheduler.queues[nodeID]) == 0 {
		scheduler.order = append(scheduler.order, nodeID)
	}
	scheduler.queues[nodeID] = append(scheduler.queues[nodeID], waiter)
	scheduler.queued++
	scheduler.mu.Unlock()

	select {
	case <-waiter.ready:
		return scheduler.releaseFunc(), nil
	case <-ctx.Done():
		scheduler.mu.Lock()
		if waiter.granted {
			// Dispatch won the race with cancellation. Hand the permit to the next
			// node instead of stranding the single-writer scheduler.
			scheduler.dispatchNextLocked()
			scheduler.mu.Unlock()
			return nil, ctx.Err()
		}
		scheduler.removeWaiterLocked(nodeID, waiter)
		scheduler.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (scheduler *agentWriteScheduler) releaseFunc() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			scheduler.mu.Lock()
			scheduler.dispatchNextLocked()
			scheduler.mu.Unlock()
		})
	}
}

func (scheduler *agentWriteScheduler) dispatchNextLocked() {
	if scheduler.queued == 0 || len(scheduler.order) == 0 {
		scheduler.active = false
		scheduler.activeNode = ""
		return
	}
	start := 0
	for index, nodeID := range scheduler.order {
		if nodeID == scheduler.activeNode {
			start = (index + 1) % len(scheduler.order)
			break
		}
	}
	selectedIndex := start
	selectedNode := scheduler.order[selectedIndex]
	queue := scheduler.queues[selectedNode]
	waiter := queue[0]
	queue = queue[1:]
	scheduler.queued--
	if len(queue) == 0 {
		delete(scheduler.queues, selectedNode)
		scheduler.order = append(scheduler.order[:selectedIndex], scheduler.order[selectedIndex+1:]...)
	} else {
		scheduler.queues[selectedNode] = queue
	}
	scheduler.active = true
	scheduler.activeNode = selectedNode
	waiter.granted = true
	close(waiter.ready)
}

func (scheduler *agentWriteScheduler) removeWaiterLocked(nodeID string, waiter *agentWriteWaiter) {
	queue := scheduler.queues[nodeID]
	for index, candidate := range queue {
		if candidate != waiter {
			continue
		}
		queue = append(queue[:index], queue[index+1:]...)
		scheduler.queued--
		if len(queue) == 0 {
			delete(scheduler.queues, nodeID)
			for orderIndex, candidateNode := range scheduler.order {
				if candidateNode == nodeID {
					scheduler.order = append(scheduler.order[:orderIndex], scheduler.order[orderIndex+1:]...)
					break
				}
			}
		} else {
			scheduler.queues[nodeID] = queue
		}
		return
	}
}
