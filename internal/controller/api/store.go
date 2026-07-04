package api

import (
	"context"
	"errors"
)

var errNodeNotFound = errors.New("node not found")
var errAssetNotFound = errors.New("asset not found")

type Store interface {
	Summary(ctx context.Context) (SummaryResponse, error)
	PublicSettings(ctx context.Context) (SiteSettings, error)
	PublicAsset(ctx context.Context, assetID string) (PublicAsset, error)
	NodeLatency(ctx context.Context, nodeID string, window latencyWindow) (LatencyResponse, error)
	NodeState(ctx context.Context, nodeID string, window latencyWindow) (StateResponse, error)
}

type mockStore struct{}

func (mockStore) Summary(ctx context.Context) (SummaryResponse, error) {
	return SummaryResponse{Nodes: mockNodes(), LatencyPoints: mockLatencyPoints("hytron", "1h")}, nil
}

func (mockStore) PublicSettings(ctx context.Context) (SiteSettings, error) {
	return defaultSiteSettings(), nil
}

func (mockStore) PublicAsset(ctx context.Context, assetID string) (PublicAsset, error) {
	return PublicAsset{}, errAssetNotFound
}

func (mockStore) NodeLatency(ctx context.Context, nodeID string, window latencyWindow) (LatencyResponse, error) {
	if !mockNodeExists(nodeID) {
		return LatencyResponse{}, errNodeNotFound
	}
	return LatencyResponse{NodeID: nodeID, Range: window.Name, Points: mockLatencyPoints(nodeID, window.Name)}, nil
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
