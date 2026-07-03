package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (h *handler) handleAdminNotificationChannels(w http.ResponseWriter, r *http.Request) {
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		channels, err := store.AdminNotificationChannels(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, AdminNotificationChannelsResponse{Channels: channels})
	case http.MethodPost:
		var create AdminNotificationChannelCreateRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&create); err != nil {
			writeError(w, http.StatusBadRequest, "bad request")
			return
		}
		channel, err := store.CreateAdminNotificationChannel(r.Context(), create)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, AdminNotificationChannelResponse{Channel: channel})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *handler) handleAdminNotificationChannelResource(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/v1/notification-channels/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		if err := store.DeleteAdminNotificationChannel(r.Context(), parts[0]); err != nil {
			writeAdminError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var update AdminNotificationChannelUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	channel, err := store.UpdateAdminNotificationChannel(r.Context(), parts[0], update)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AdminNotificationChannelResponse{Channel: channel})
}

func (h *handler) handleAdminNotificationTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	types, err := store.AdminNotificationTypes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, AdminNotificationTypesResponse{Types: types})
}

func (h *handler) handleAdminNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	limit := 50
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "bad request")
			return
		}
		limit = parsed
	}
	deliveries, err := store.AdminNotificationDeliveries(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, AdminNotificationDeliveriesResponse{Deliveries: deliveries})
}

func (h *handler) handleAdminNotificationTypeResource(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/v1/notification-types/"), "/")
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
	var update AdminNotificationTypeUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	notificationType, err := store.UpdateAdminNotificationType(r.Context(), parts[0], update)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AdminNotificationTypeResponse{Type: notificationType})
}
