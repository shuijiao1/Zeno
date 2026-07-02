package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/api/public/v1/summary", handleSummary)
	mux.HandleFunc("/api/public/v1/nodes/", handleNodeLatency)
	return mux
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, SummaryResponse{Nodes: mockNodes(), LatencyPoints: mockLatencyPoints("hytron")})
}

func handleNodeLatency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/public/v1/nodes/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[1] != "latency" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	nodeID := parts[0]
	if !mockNodeExists(nodeID) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	rangeName := r.URL.Query().Get("range")
	if rangeName == "" {
		rangeName = "1h"
	}
	writeJSON(w, http.StatusOK, LatencyResponse{NodeID: nodeID, Range: rangeName, Points: mockLatencyPoints(nodeID)})
}

func mockNodeExists(nodeID string) bool {
	for _, node := range mockNodes() {
		if node.ID == nodeID {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
