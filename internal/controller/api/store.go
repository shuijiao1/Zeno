package api

import (
	"context"
	"errors"
)

var errNodeNotFound = errors.New("node not found")

type Store interface {
	Summary(ctx context.Context) (SummaryResponse, error)
	PublicSettings(ctx context.Context) (SiteSettings, error)
	NodeLatency(ctx context.Context, nodeID string, window latencyWindow) (LatencyResponse, error)
	ServiceTargetLatency(ctx context.Context, targetID string, window latencyWindow) (ServiceTargetLatencyResponse, error)
	NodeState(ctx context.Context, nodeID string, window latencyWindow) (StateResponse, error)
}

type mockStore struct{}

func (mockStore) Summary(ctx context.Context) (SummaryResponse, error) {
	return SummaryResponse{Nodes: mockNodes(), Services: mockServiceTargets(), LatencyPoints: []LatencyPoint{}}, nil
}

func (mockStore) PublicSettings(ctx context.Context) (SiteSettings, error) {
	return defaultSiteSettings(), nil
}

func (mockStore) NodeLatency(ctx context.Context, nodeID string, window latencyWindow) (LatencyResponse, error) {
	if !mockNodeExists(nodeID) {
		return LatencyResponse{}, errNodeNotFound
	}
	return LatencyResponse{NodeID: nodeID, Range: window.Name, Points: mockLatencyPoints(nodeID, window.Name)}, nil
}

func (mockStore) ServiceTargetLatency(ctx context.Context, targetID string, window latencyWindow) (ServiceTargetLatencyResponse, error) {
	for _, target := range mockServiceTargets() {
		if target.ID == targetID {
			return ServiceTargetLatencyResponse{Target: target, Range: window.Name, Points: mockServiceLatencyPoints(targetID, window.Name)}, nil
		}
	}
	return ServiceTargetLatencyResponse{}, errProbeTargetNotFound
}

func (mockStore) NodeState(ctx context.Context, nodeID string, window latencyWindow) (StateResponse, error) {
	if !mockNodeExists(nodeID) {
		return StateResponse{}, errNodeNotFound
	}
	return StateResponse{NodeID: nodeID, Range: window.Name, Points: mockStatePoints(window)}, nil
}

func mockNodeExists(nodeID string) bool {
	for _, node := range mockNodes() {
		if node.ID == nodeID {
			return true
		}
	}
	return false
}
