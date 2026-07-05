package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type HandlerOptions struct {
	StaticDir          string
	Store              Store
	AdminTokenHash     string
	AgentBinaryPath    string
	AgentVersion       string
	NotificationClient *http.Client
	TelegramAPIBaseURL string
}

type handler struct {
	store              Store
	adminTokenHash     string
	agentBinaryPath    string
	agentVersion       string
	notificationSender notificationSender
	loginLimiter       *adminLoginLimiter
	liveHub            *liveUpdateHub
}

type adminLoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]adminLoginAttempt
}

type adminLoginAttempt struct {
	Count       int
	FirstSeenAt time.Time
	LockedUntil time.Time
}

const (
	adminLoginWindow       = 15 * time.Minute
	adminLoginLockDuration = 10 * time.Minute
	adminLoginMaxFailures  = 5
)

func newAdminLoginLimiter() *adminLoginLimiter {
	return &adminLoginLimiter{attempts: map[string]adminLoginAttempt{}}
}

func (limiter *adminLoginLimiter) allow(key string) bool {
	now := time.Now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	attempt := limiter.attempts[key]
	if !attempt.LockedUntil.IsZero() && now.Before(attempt.LockedUntil) {
		return false
	}
	if !attempt.FirstSeenAt.IsZero() && now.Sub(attempt.FirstSeenAt) > adminLoginWindow {
		delete(limiter.attempts, key)
	}
	return true
}

func (limiter *adminLoginLimiter) recordFailure(key string) {
	now := time.Now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	attempt := limiter.attempts[key]
	if attempt.FirstSeenAt.IsZero() || now.Sub(attempt.FirstSeenAt) > adminLoginWindow {
		attempt = adminLoginAttempt{FirstSeenAt: now}
	}
	attempt.Count++
	if attempt.Count >= adminLoginMaxFailures {
		attempt.LockedUntil = now.Add(adminLoginLockDuration)
	}
	limiter.attempts[key] = attempt
}

func (limiter *adminLoginLimiter) recordSuccess(key string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	delete(limiter.attempts, key)
}

func NewHandler(options ...HandlerOptions) http.Handler {
	opts := HandlerOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	store := opts.Store
	if store == nil {
		store = mockStore{}
	}
	h := &handler{
		store:              store,
		adminTokenHash:     opts.AdminTokenHash,
		agentBinaryPath:    opts.AgentBinaryPath,
		agentVersion:       opts.AgentVersion,
		notificationSender: newHTTPNotificationSender(opts.NotificationClient, opts.TelegramAPIBaseURL),
		loginLimiter:       newAdminLoginLimiter(),
		liveHub:            newLiveUpdateHub(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/api/public/v1/agent/linux-amd64", h.handleAgentBinary)
	mux.HandleFunc("/api/public/v1/settings", h.handlePublicSettings)
	mux.HandleFunc("/api/public/v1/summary", h.handleSummary)
	mux.HandleFunc("/api/public/v1/summary/ws", h.handleSummaryWebSocket)
	mux.HandleFunc("/api/public/v1/services/", h.handlePublicServiceResource)
	mux.HandleFunc("/api/public/v1/nodes/", h.handlePublicNodeResource)
	mux.HandleFunc("/api/admin/v1/login", h.handleAdminLogin)
	mux.HandleFunc("/api/admin/v1/logout", h.handleAdminLogout)
	mux.HandleFunc("/api/admin/v1/account", h.handleAdminAccount)
	mux.HandleFunc("/api/admin/v1/password", h.handleAdminPassword)
	mux.HandleFunc("/api/admin/v1/settings", h.handleAdminSettings)
	mux.HandleFunc("/api/admin/v1/notification-channels", h.handleAdminNotificationChannels)
	mux.HandleFunc("/api/admin/v1/notification-channels/", h.handleAdminNotificationChannelResource)
	mux.HandleFunc("/api/admin/v1/notification-deliveries", h.handleAdminNotificationDeliveries)
	mux.HandleFunc("/api/admin/v1/alert-rules", h.handleAdminAlertRules)
	mux.HandleFunc("/api/admin/v1/alert-rule-states", h.handleAdminAlertRuleStates)
	mux.HandleFunc("/api/admin/v1/alert-rules/", h.handleAdminAlertRuleResource)
	mux.HandleFunc("/api/admin/v1/notification-types", h.handleAdminNotificationTypes)
	mux.HandleFunc("/api/admin/v1/notification-types/", h.handleAdminNotificationTypeResource)
	mux.HandleFunc("/api/admin/v1/probe-targets", h.handleAdminProbeTargets)
	mux.HandleFunc("/api/admin/v1/probe-targets/", h.handleAdminProbeTargetResource)
	mux.HandleFunc("/api/admin/v1/nodes", h.handleAdminNodes)
	mux.HandleFunc("/api/admin/v1/nodes/", h.handleAdminNodeResource)
	mux.HandleFunc("/api/agent/v1/probe-targets", h.handleAgentProbeTargets)
	mux.HandleFunc("/api/agent/v1/probe-results", h.handleAgentProbeResults)
	mux.HandleFunc("/api/agent/v1/heartbeat", h.handleAgentHeartbeat)
	mux.HandleFunc("/api/agent/v1/host", h.handleAgentHost)
	mux.HandleFunc("/api/agent/v1/state", h.handleAgentState)
	if opts.StaticDir != "" {
		mux.HandleFunc("/", handleStatic(opts.StaticDir))
	}
	return mux
}

func (h *handler) handlePublicServiceResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/public/v1/services/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 && len(parts) != 3 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if parts[1] != "latency" || (len(parts) == 3 && parts[2] != "ws") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	window, ok := resolveLatencyWindow(r.URL.Query().Get("range"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported range")
		return
	}
	if len(parts) == 3 {
		h.handleServiceLatencyWebSocket(w, r, parts[0], window)
		return
	}
	response, err := h.store.ServiceTargetLatency(r.Context(), parts[0], window)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *handler) handleAgentBinary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.agentBinaryPath == "" {
		writeError(w, http.StatusNotFound, "agent binary not configured")
		return
	}
	if info, err := os.Stat(h.agentBinaryPath); err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "agent binary not found")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="zeno-agent-linux-amd64"`)
	http.ServeFile(w, r, h.agentBinaryPath)
}

func requestBaseURL(r *http.Request) string {
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if host == "" {
		host = "127.0.0.1:18980"
	}
	return strings.TrimRight(proto+"://"+host, "/")
}

func (h *handler) handlePublicSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	settings, err := h.store.PublicSettings(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *handler) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	summary, err := h.store.Summary(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *handler) handlePublicNodeResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/public/v1/nodes/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 && len(parts) != 3 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if len(parts) == 3 && parts[2] != "ws" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	nodeID := parts[0]
	rangeName := r.URL.Query().Get("range")
	switch parts[1] {
	case "latency":
		window, ok := resolveLatencyWindow(rangeName)
		if !ok {
			writeError(w, http.StatusBadRequest, "unsupported range")
			return
		}
		if len(parts) == 3 {
			h.handleNodeLatencyWebSocket(w, r, nodeID, window)
			return
		}
		response, err := h.store.NodeLatency(r.Context(), nodeID, window)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	case "state":
		window, ok := resolveStateWindow(rangeName)
		if !ok {
			writeError(w, http.StatusBadRequest, "unsupported range")
			return
		}
		if len(parts) == 3 {
			h.handleNodeStateWebSocket(w, r, nodeID, window)
			return
		}
		response, err := h.store.NodeState(r.Context(), nodeID, window)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func handleStatic(staticDir string) http.HandlerFunc {
	fileServer := http.FileServer(http.Dir(staticDir))
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		cleanPath := filepath.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		if strings.HasPrefix(cleanPath, "/api/") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		filePath := filepath.Join(staticDir, strings.TrimPrefix(cleanPath, "/"))
		if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}

		indexPath := filepath.Join(staticDir, "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			writeError(w, http.StatusNotFound, "dashboard not built")
			return
		}
		http.ServeFile(w, r, indexPath)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, errNodeNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	if errors.Is(err, errProbeTargetNotFound) {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal error")
}
