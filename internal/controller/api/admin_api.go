package api

import (
	"context"
	"net/http"
	"strings"
)

type adminStore interface {
	AdminNodes(ctx context.Context) ([]AdminNode, error)
}

func (h *handler) handleAdminNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	nodes, err := store.AdminNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, AdminNodesResponse{Nodes: nodes})
}

func (h *handler) authorizeAdminRequest(w http.ResponseWriter, r *http.Request) (adminStore, bool) {
	if h.adminTokenHash == "" {
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	store, ok := h.store.(adminStore)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	provided := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if !adminTokenMatches(h.adminTokenHash, provided) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	return store, true
}
