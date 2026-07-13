package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
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
	AdminNodeInstallCommand(ctx context.Context, nodeID, controllerURL, agentVersion string) (AgentInstallCommands, error)
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
	releaseArgon2, admitted := reserveAdminArgon2Request()
	if !admitted {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	defer releaseArgon2()
	accountKey := adminLoginRateLimitKey(r, request.Username)
	ipKey := adminLoginIPRateLimitKey(r)
	accountReservation := adminLoginReservation{}
	ipReservation := adminLoginReservation{}
	if h.loginLimiter != nil {
		var reserved bool
		ipReservation, reserved = h.loginLimiter.reserve(ipKey)
		if !reserved {
			writeError(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
		accountReservation, reserved = h.loginLimiter.reserve(accountKey)
		if !reserved {
			writeError(w, http.StatusTooManyRequests, "too many attempts")
			return
		}
	}
	loginSucceeded := false
	defer func() {
		accountReservation.release(loginSucceeded)
		ipReservation.release(loginSucceeded)
	}()
	if authStore, ok := h.store.(adminAuthStore); ok {
		session, err := authStore.AdminLogin(r.Context(), request.Username, request.Password, h.adminTokenHash)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		loginSucceeded = true
		writeJSON(w, http.StatusOK, AdminLoginResponse(session))
		return
	}
	passwordOK := adminPasswordMatches("", h.adminTokenHash, request.Password)
	if strings.TrimSpace(request.Username) != "admin" || !passwordOK {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	loginSucceeded = true
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
		writeJSON(w, http.StatusOK, AdminLoginResponse(session))
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
		h.notifyProbeConfigChanged(r.Context())
		h.publishSummaryNow(r.Context())
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
		h.notifyProbeConfigChanged(r.Context())
		h.publishSummaryNow(r.Context())
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
	h.notifyProbeConfigChanged(r.Context())
	h.publishSummaryNow(r.Context())
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
		h.publishSummaryNow(r.Context())
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
		h.publishSummaryNow(r.Context())
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
	h.publishSummaryNow(r.Context())
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
	nodes, err := store.AdminNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	found := false
	for _, node := range nodes {
		if node.ID == nodeID {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	settings, err := store.AdminSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	controllerURL := strings.TrimSpace(settings.AgentControllerURL)
	if controllerURL != "" && !validAgentControllerURL(controllerURL) {
		writeError(w, http.StatusConflict, "configure a secure agent controller url before generating install commands")
		return
	}
	if controllerURL == "" {
		fallbackURL := requestBaseURL(r)
		parsedFallback, parseErr := url.Parse(fallbackURL)
		if parseErr != nil || !loopbackURLHost(parsedFallback.Hostname()) {
			writeError(w, http.StatusConflict, "configure agent controller url before generating install commands")
			return
		}
		controllerURL = fallbackURL
	}
	commands, err := store.AdminNodeInstallCommand(r.Context(), nodeID, controllerURL, h.agentVersion)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AdminNodeInstallCommandResponse{NodeID: nodeID, Command: commands.Linux, Commands: commands.Map()})
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

// authorizeExtendedHistoryRequest protects the heavier 7-day and 30-day
// history endpoints while allowing the public dashboard to pass its existing
// opaque admin session in X-Admin-Token.
func (h *handler) authorizeExtendedHistoryRequest(w http.ResponseWriter, r *http.Request) bool {
	provided := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if authStore, ok := h.store.(adminAuthStore); ok {
		authorized, err := authStore.AuthorizeAdminSession(r.Context(), provided)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return false
		}
		if authorized {
			return true
		}
		configured, err := authStore.AdminAccountConfigured(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return false
		}
		if configured {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return false
		}
	}
	if h.adminTokenHash != "" && adminTokenMatches(h.adminTokenHash, provided) {
		return true
	}
	writeError(w, http.StatusUnauthorized, "unauthorized")
	return false
}

func adminLoginIPRateLimitKey(r *http.Request) string {
	return "ip:" + clientIPForRateLimit(r)
}

func adminLoginRateLimitKey(r *http.Request, username string) string {
	remote := clientIPForRateLimit(r)
	// Keep attacker-controlled usernames out of the limiter map. Login bodies can
	// be tens of KiB, while the digest is fixed-size and preserves exact keys.
	usernameDigest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return remote + ":" + hex.EncodeToString(usernameDigest[:])
}

func clientIPForRateLimit(r *http.Request) string {
	remoteIP := parseRemoteIP(r.RemoteAddr)
	if remoteIP != nil && trustedForwardingProxy(remoteIP) {
		if forwarded := forwardedClientIP(r.Header.Get("X-Forwarded-For")); forwarded != nil {
			return forwarded.String()
		}
		if realIP := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); realIP != nil {
			return realIP.String()
		}
	}
	if remoteIP != nil {
		return remoteIP.String()
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func parseRemoteIP(remoteAddr string) net.IP {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return net.ParseIP(strings.Trim(host, "[]"))
	}
	return net.ParseIP(strings.Trim(remoteAddr, "[]"))
}

func forwardedClientIP(value string) net.IP {
	parts := strings.Split(value, ",")
	// A reverse proxy appends the address of its direct peer. Use the rightmost
	// valid value so an untrusted client cannot choose the limiter key by
	// prepending its own X-Forwarded-For entry. Multi-proxy deployments without
	// an explicit trusted-proxy list may aggregate at the nearest proxy, which is
	// safer than accepting a spoofable client address.
	for index := len(parts) - 1; index >= 0; index-- {
		if ip := net.ParseIP(strings.TrimSpace(parts[index])); ip != nil {
			return ip
		}
	}
	return nil
}

func trustedForwardingProxy(ip net.IP) bool {
	// The official deployment binds Controller to 127.0.0.1 and proxies from the
	// same host. Treating every RFC1918 peer as a proxy would let any LAN client
	// spoof X-Forwarded-For and bypass the per-IP login limiter.
	return ip.IsLoopback()
}

func writeAdminError(w http.ResponseWriter, err error) {
	if errors.Is(err, errNotificationTypeGone) {
		writeError(w, http.StatusGone, "notification type is managed by alert rules")
		return
	}
	if errors.Is(err, errNodeNotFound) || errors.Is(err, errProbeTargetNotFound) || errors.Is(err, errNotificationChannelNotFound) || errors.Is(err, errNotificationTypeNotFound) || errors.Is(err, errAlertRuleNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if errors.Is(err, errInvalidAdminSettingsUpdate) || errors.Is(err, errInvalidAdminNodeUpdate) || errors.Is(err, errInvalidAdminNodeCreate) || errors.Is(err, errInvalidAdminTargetWrite) || errors.Is(err, errInvalidAdminNotificationChannelWrite) || errors.Is(err, errInvalidAdminNotificationTypeWrite) || errors.Is(err, errInvalidAdminAlertRuleUpdate) || errors.Is(err, errInvalidAdminPasswordUpdate) {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if errors.Is(err, errNotificationCredentialKeyRequired) {
		writeError(w, http.StatusConflict, "notification key unavailable")
		return
	}
	if errors.Is(err, errNodeAlreadyExists) || errors.Is(err, errProbeTargetAlreadyExists) || errors.Is(err, errNotificationChannelAlreadyExists) {
		writeError(w, http.StatusConflict, "already exists")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal error")
}
