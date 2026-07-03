package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type adminStore interface {
	AdminNodes(ctx context.Context) ([]AdminNode, error)
	AdminProbeTargets(ctx context.Context) ([]AdminProbeTarget, error)
	AdminNotificationChannels(ctx context.Context) ([]AdminNotificationChannel, error)
	AdminNotificationTypes(ctx context.Context) ([]AdminNotificationType, error)
	AdminNotificationDeliveries(ctx context.Context, limit int) ([]AdminNotificationDelivery, error)
	CreateAdminNode(ctx context.Context, create AdminNodeCreateRequest) (AdminNode, error)
	UpdateAdminNode(ctx context.Context, nodeID string, update AdminNodeUpdateRequest) (AdminNode, error)
	AdminNodeInstallCommand(ctx context.Context, nodeID, controllerURL, agentVersion string) (string, error)
	CreateAdminProbeTarget(ctx context.Context, create AdminProbeTargetCreateRequest) (AdminProbeTarget, error)
	UpdateAdminProbeTarget(ctx context.Context, targetID string, update AdminProbeTargetUpdateRequest) (AdminProbeTarget, error)
	CreateAdminNotificationChannel(ctx context.Context, create AdminNotificationChannelCreateRequest) (AdminNotificationChannel, error)
	UpdateAdminNotificationChannel(ctx context.Context, channelID string, update AdminNotificationChannelUpdateRequest) (AdminNotificationChannel, error)
	DeleteAdminNotificationChannel(ctx context.Context, channelID string) error
	UpdateAdminNotificationType(ctx context.Context, eventType string, update AdminNotificationTypeUpdateRequest) (AdminNotificationType, error)
}

func (h *handler) handleAdminProbeTargets(w http.ResponseWriter, r *http.Request) {
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		targets, err := store.AdminProbeTargets(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, AdminProbeTargetsResponse{Targets: targets})
	case http.MethodPost:
		var create AdminProbeTargetCreateRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&create); err != nil {
			writeError(w, http.StatusBadRequest, "bad request")
			return
		}
		target, err := store.CreateAdminProbeTarget(r.Context(), create)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, AdminProbeTargetResponse{Target: target})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *handler) handleAdminProbeTargetResource(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/v1/probe-targets/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	var update AdminProbeTargetUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	target, err := store.UpdateAdminProbeTarget(r.Context(), parts[0], update)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AdminProbeTargetResponse{Target: target})
}

func (h *handler) handleAdminNodes(w http.ResponseWriter, r *http.Request) {
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		nodes, err := store.AdminNodes(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, AdminNodesResponse{Nodes: nodes})
	case http.MethodPost:
		var create AdminNodeCreateRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&create); err != nil {
			writeError(w, http.StatusBadRequest, "bad request")
			return
		}
		node, err := store.CreateAdminNode(r.Context(), create)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, AdminNodeResponse{Node: node})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *handler) handleAdminNodeResource(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/v1/nodes/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "install-command" {
		h.handleAdminNodeInstallCommand(w, r, parts[0])
		return
	}
	if len(parts) != 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	nodeID := parts[0]
	var update AdminNodeUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	node, err := store.UpdateAdminNode(r.Context(), nodeID, update)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AdminNodeResponse{Node: node})
}

func (h *handler) handleAdminNodeInstallCommand(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	command, err := store.AdminNodeInstallCommand(r.Context(), nodeID, requestBaseURL(r), h.agentVersion)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AdminNodeInstallCommandResponse{NodeID: nodeID, Command: command})
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

func writeAdminError(w http.ResponseWriter, err error) {
	if errors.Is(err, errNodeNotFound) || errors.Is(err, errProbeTargetNotFound) || errors.Is(err, errNotificationChannelNotFound) || errors.Is(err, errNotificationTypeNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if errors.Is(err, errInvalidAdminNodeUpdate) || errors.Is(err, errInvalidAdminNodeCreate) || errors.Is(err, errInvalidAdminTargetWrite) || errors.Is(err, errInvalidAdminNotificationChannelWrite) || errors.Is(err, errInvalidAdminNotificationTypeWrite) {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if errors.Is(err, errNodeAlreadyExists) || errors.Is(err, errProbeTargetAlreadyExists) || errors.Is(err, errNotificationChannelAlreadyExists) {
		writeError(w, http.StatusConflict, "already exists")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal error")
}
