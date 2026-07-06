package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type adminStore interface {
	AdminSettings(ctx context.Context) (SiteSettings, error)
	UpdateAdminSettings(ctx context.Context, update AdminSettingsUpdateRequest) (SiteSettings, error)
	AdminNodes(ctx context.Context) ([]AdminNode, error)
	AdminProbeTargets(ctx context.Context) ([]AdminProbeTarget, error)
	AdminNotificationChannels(ctx context.Context) ([]AdminNotificationChannel, error)
	AdminAlertRules(ctx context.Context) ([]AdminAlertRule, error)
	AdminNotificationDispatchChannel(ctx context.Context, channelID string) (notificationDispatchChannel, error)
	CreateAdminNode(ctx context.Context, create AdminNodeCreateRequest) (AdminNode, error)
	UpdateAdminNode(ctx context.Context, nodeID string, update AdminNodeUpdateRequest) (AdminNode, error)
	DeleteAdminNode(ctx context.Context, nodeID string) error
	AdminNodeInstallCommand(ctx context.Context, nodeID, controllerURL, agentVersion string) (string, error)
	CreateAdminProbeTarget(ctx context.Context, create AdminProbeTargetCreateRequest) (AdminProbeTarget, error)
	UpdateAdminProbeTarget(ctx context.Context, targetID string, update AdminProbeTargetUpdateRequest) (AdminProbeTarget, error)
	DeleteAdminProbeTarget(ctx context.Context, targetID string) error
	CreateAdminNotificationChannel(ctx context.Context, create AdminNotificationChannelCreateRequest) (AdminNotificationChannel, error)
	UpdateAdminNotificationChannel(ctx context.Context, channelID string, update AdminNotificationChannelUpdateRequest) (AdminNotificationChannel, error)
	DeleteAdminNotificationChannel(ctx context.Context, channelID string) error
	UpdateAdminNotificationType(ctx context.Context, eventType string, update AdminNotificationTypeUpdateRequest) (AdminNotificationType, error)
	UpdateAdminAlertRule(ctx context.Context, ruleID string, update AdminAlertRuleUpdateRequest) (AdminAlertRule, error)
}

type adminAuthStore interface {
	AdminLogin(ctx context.Context, username, password, fallbackHash string) (AdminSession, error)
	AuthorizeAdminSession(ctx context.Context, token string) (bool, error)
	RevokeAdminSession(ctx context.Context, token string) error
	AdminAccount(ctx context.Context) (AdminAccount, error)
	UpdateAdminAccount(ctx context.Context, username, currentPassword, newPassword, fallbackHash string) (AdminSession, error)
	AdminAccountConfigured(ctx context.Context) (bool, error)
}

func (h *handler) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.adminTokenHash == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var request AdminLoginRequest
	if !decodeJSONBody(w, r, &request, adminJSONBodyLimit, true) {
		return
	}
	key := adminLoginRateLimitKey(r, request.Username)
	if h.loginLimiter != nil && !h.loginLimiter.allow(key) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	if authStore, ok := h.store.(adminAuthStore); ok {
		session, err := authStore.AdminLogin(r.Context(), request.Username, request.Password, h.adminTokenHash)
		if err != nil {
			if h.loginLimiter != nil {
				h.loginLimiter.recordFailure(key)
			}
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if h.loginLimiter != nil {
			h.loginLimiter.recordSuccess(key)
		}
		writeJSON(w, http.StatusOK, AdminLoginResponse{Username: session.Username, Token: session.Token})
		return
	}
	if strings.TrimSpace(request.Username) != "admin" || !adminTokenMatches(h.adminTokenHash, request.Password) {
		if h.loginLimiter != nil {
			h.loginLimiter.recordFailure(key)
		}
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.loginLimiter != nil {
		h.loginLimiter.recordSuccess(key)
	}
	writeJSON(w, http.StatusOK, AdminLoginResponse{Username: "admin", Token: strings.TrimSpace(request.Password)})
}

func (h *handler) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	token := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if authStore, ok := h.store.(adminAuthStore); ok {
		if err := authStore.RevokeAdminSession(r.Context(), token); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleAdminAccount(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorizeAdminRequest(w, r); !ok {
		return
	}
	authStore, ok := h.store.(adminAuthStore)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		account, err := authStore.AdminAccount(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, AdminAccountResponse{Account: account})
	case http.MethodPost:
		var request AdminAccountUpdateRequest
		if !decodeJSONBody(w, r, &request, adminJSONBodyLimit, true) {
			return
		}
		session, err := authStore.UpdateAdminAccount(r.Context(), request.Username, request.CurrentPassword, request.NewPassword, h.adminTokenHash)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, AdminLoginResponse{Username: session.Username, Token: session.Token})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *handler) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		settings, err := store.AdminSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, AdminSettingsResponse{Settings: settings})
	case http.MethodPatch:
		var update AdminSettingsUpdateRequest
		if !decodeJSONBody(w, r, &update, adminJSONBodyLimit, true) {
			return
		}
		settings, err := store.UpdateAdminSettings(r.Context(), update)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, AdminSettingsResponse{Settings: settings})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
		if !decodeJSONBody(w, r, &create, adminJSONBodyLimit, true) {
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
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		if err := store.DeleteAdminProbeTarget(r.Context(), parts[0]); err != nil {
			writeAdminError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var update AdminProbeTargetUpdateRequest
	if !decodeJSONBody(w, r, &update, adminJSONBodyLimit, true) {
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
		if !decodeJSONBody(w, r, &create, adminJSONBodyLimit, true) {
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
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	store, ok := h.authorizeAdminRequest(w, r)
	if !ok {
		return
	}
	nodeID := parts[0]
	if r.Method == http.MethodDelete {
		if err := store.DeleteAdminNode(r.Context(), nodeID); err != nil {
			writeAdminError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var update AdminNodeUpdateRequest
	if !decodeJSONBody(w, r, &update, adminJSONBodyLimit, true) {
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
	controllerURL := requestBaseURL(r)
	settings, err := store.AdminSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if strings.TrimSpace(settings.AgentControllerURL) != "" {
		controllerURL = settings.AgentControllerURL
	}
	command, err := store.AdminNodeInstallCommand(r.Context(), nodeID, controllerURL, h.agentVersion)
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
	if authStore, ok := h.store.(adminAuthStore); ok {
		authorized, err := authStore.AuthorizeAdminSession(r.Context(), provided)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return nil, false
		}
		if authorized {
			return store, true
		}
		configured, err := authStore.AdminAccountConfigured(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return nil, false
		}
		if configured {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return nil, false
		}
	}
	if !adminTokenMatches(h.adminTokenHash, provided) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	return store, true
}

func adminLoginRateLimitKey(r *http.Request, username string) string {
	remote := strings.TrimSpace(r.RemoteAddr)
	if host, _, ok := strings.Cut(remote, ":"); ok && host != "" {
		remote = host
	}
	return strings.ToLower(remote + ":" + strings.TrimSpace(username))
}

func writeAdminError(w http.ResponseWriter, err error) {
	if errors.Is(err, errNodeNotFound) || errors.Is(err, errProbeTargetNotFound) || errors.Is(err, errNotificationChannelNotFound) || errors.Is(err, errNotificationTypeNotFound) || errors.Is(err, errAlertRuleNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if errors.Is(err, errInvalidAdminSettingsUpdate) || errors.Is(err, errInvalidAdminNodeUpdate) || errors.Is(err, errInvalidAdminNodeCreate) || errors.Is(err, errInvalidAdminTargetWrite) || errors.Is(err, errInvalidAdminNotificationChannelWrite) || errors.Is(err, errInvalidAdminNotificationTypeWrite) || errors.Is(err, errInvalidAdminAlertRuleUpdate) || errors.Is(err, errInvalidAdminPasswordUpdate) {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if errors.Is(err, errNodeAlreadyExists) || errors.Is(err, errProbeTargetAlreadyExists) || errors.Is(err, errNotificationChannelAlreadyExists) {
		writeError(w, http.StatusConflict, "already exists")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal error")
}
