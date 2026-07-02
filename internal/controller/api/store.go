package api

import (
	"context"
	"errors"
)

var errNodeNotFound = errors.New("node not found")

type Store interface {
	Summary(ctx context.Context) (SummaryResponse, error)
	NodeLatency(ctx context.Context, nodeID string, window latencyWindow) (LatencyResponse, error)
}

type mockStore struct{}

func (mockStore) Summary(ctx context.Context) (SummaryResponse, error) {
	return SummaryResponse{Nodes: mockNodes(), LatencyPoints: mockLatencyPoints("hytron", "1h")}, nil
}

func (mockStore) NodeLatency(ctx context.Context, nodeID string, window latencyWindow) (LatencyResponse, error) {
	if !mockNodeExists(nodeID) {
		return LatencyResponse{}, errNodeNotFound
	}
	return LatencyResponse{NodeID: nodeID, Range: window.Name, Points: mockLatencyPoints(nodeID, window.Name)}, nil
}

func mockNodeExists(nodeID string) bool {
	for _, node := range mockNodes() {
		if node.ID == nodeID {
			return true
		}
	}
	return false
}
