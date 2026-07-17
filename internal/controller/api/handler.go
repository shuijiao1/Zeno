package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type HandlerOptions struct {
	StaticDir                    string
	Store                        Store
	AdminTokenHash               string
	TrustedProxies               TrustedProxySet
	AgentBinaryPath              string
	AgentVersion                 string
	NotificationClient           *http.Client
	TelegramAPIBaseURL           string
	StaleOfflineScanInterval     time.Duration
	RenewalNotificationInterval  time.Duration
	HistoryRetentionInterval     time.Duration
	NotificationDispatchInterval time.Duration
	DisableNotifications         bool
	BackgroundContext            context.Context
}

type handler struct {
	store                  Store
	adminTokenHash         string
	agentBinaryPath        string
	agentVersion           string
	notificationSender     notificationSender
	loginLimiter           *adminLoginLimiter
	enrollmentLimiter      *adminLoginLimiter
	trustedProxies         TrustedProxySet
	agentQuotas            *agentQuotaManager
	agentAuthAdmission     *agentAuthAdmissionManager
	liveHub                *liveUpdateHub
	presence               *agentPresenceManager
	publicWSGate           *websocketGate
	agentWSGate            *websocketGate
	summaryPublishMu       sync.Mutex
	summaryPublishTimer    *time.Timer
	summaryLastPublished   time.Time
	summaryCacheMu         sync.RWMutex
	summaryCache           []byte
	summaryCacheUpdated    time.Time
	summaryCacheGeneration uint64
	summaryCacheFlight     *jsonCacheFlight
	detailCache            *detailJSONCache
	detailPublishMu        sync.Mutex
	detailPublishPending   map[string]bool
	detailPublishGate      chan struct{}
	backgroundMu           sync.Mutex
	backgroundClosing      bool
	backgroundCtx          context.Context
	backgroundCancel       context.CancelFunc
	backgroundWG           sync.WaitGroup
	notificationDrainMu    sync.Mutex
	notificationWorkerMu   sync.Mutex
	notificationWorker     *notificationOutboxWorker
	router                 http.Handler
}

const (
	adminJSONBodyLimit      int64 = 64 << 10
	agentStateJSONBodyLimit int64 = 64 << 10
	agentProbeJSONBodyLimit int64 = 1 << 20
)

func decodeJSONBody(w http.ResponseWriter, r *http.Request, target any, limit int64, disallowUnknown bool) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if disallowUnknown {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return false
	}
	return true
}

type adminLoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]adminLoginAttempt
}

type adminLoginAttempt struct {
	Count       int
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	LockedUntil time.Time
}

type adminLoginReservation struct {
	limiter *adminLoginLimiter
	key     string
}

const (
	adminLoginWindow       = 15 * time.Minute
	adminLoginLockDuration = 10 * time.Minute
	adminLoginMaxFailures  = 5
	adminLoginMaxEntries   = 4096
)

func newAdminLoginLimiter() *adminLoginLimiter {
	return &adminLoginLimiter{attempts: map[string]adminLoginAttempt{}}
}

func (limiter *adminLoginLimiter) reserve(key string) (adminLoginReservation, bool) {
	now := time.Now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.pruneLocked(now)
	attempt := limiter.attempts[key]
	if !attempt.LockedUntil.IsZero() && now.Before(attempt.LockedUntil) {
		return adminLoginReservation{}, false
	}
	if attempt.FirstSeenAt.IsZero() || now.Sub(attempt.FirstSeenAt) > adminLoginWindow {
		attempt = adminLoginAttempt{FirstSeenAt: now}
	}
	attempt.Count++
	attempt.LastSeenAt = now
	if attempt.Count >= adminLoginMaxFailures {
		attempt.LockedUntil = now.Add(adminLoginLockDuration)
	}
	limiter.attempts[key] = attempt
	return adminLoginReservation{limiter: limiter, key: key}, true
}

func (reservation adminLoginReservation) release(success bool) {
	if reservation.limiter == nil || reservation.key == "" || !success {
		return
	}
	reservation.limiter.recordSuccess(reservation.key)
}

func (limiter *adminLoginLimiter) pruneLocked(now time.Time) {
	oldestKey := ""
	oldestSeen := now
	for key, attempt := range limiter.attempts {
		lastSeen := attempt.LastSeenAt
		if lastSeen.IsZero() {
			lastSeen = attempt.FirstSeenAt
		}
		if (!attempt.LockedUntil.IsZero() && now.After(attempt.LockedUntil.Add(adminLoginWindow))) || (attempt.LockedUntil.IsZero() && !attempt.FirstSeenAt.IsZero() && now.Sub(attempt.FirstSeenAt) > adminLoginWindow) {
			delete(limiter.attempts, key)
			continue
		}
		if oldestKey == "" || lastSeen.Before(oldestSeen) {
			oldestKey = key
			oldestSeen = lastSeen
		}
	}
	for len(limiter.attempts) > adminLoginMaxEntries && oldestKey != "" {
		delete(limiter.attempts, oldestKey)
		oldestKey = ""
		oldestSeen = now
		for key, attempt := range limiter.attempts {
			lastSeen := attempt.LastSeenAt
			if lastSeen.IsZero() {
				lastSeen = attempt.FirstSeenAt
			}
			if oldestKey == "" || lastSeen.Before(oldestSeen) {
				oldestKey = key
				oldestSeen = lastSeen
			}
		}
	}
}

func (limiter *adminLoginLimiter) recordSuccess(key string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	delete(limiter.attempts, key)
}

type websocketGate struct {
	mu        sync.Mutex
	current   int
	max       int
	maxPerKey int
	byKey     map[string]int
}

func newWebSocketGate(max int) *websocketGate {
	return newWebSocketGateWithPerKey(max, 0)
}

func newWebSocketGateWithPerKey(max, maxPerKey int) *websocketGate {
	return &websocketGate{max: max, maxPerKey: maxPerKey, byKey: make(map[string]int)}
}

func (gate *websocketGate) acquire() (func(), bool) {
	return gate.acquireFor("")
}

func (gate *websocketGate) acquireFor(key string) (func(), bool) {
	if gate == nil || gate.max <= 0 {
		return func() {}, true
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.current >= gate.max || (key != "" && gate.maxPerKey > 0 && gate.byKey[key] >= gate.maxPerKey) {
		return nil, false
	}
	gate.current++
	if key != "" && gate.maxPerKey > 0 {
		gate.byKey[key]++
	}
	released := false
	return func() {
		gate.mu.Lock()
		defer gate.mu.Unlock()
		if released {
			return
		}
		released = true
		if gate.current > 0 {
			gate.current--
		}
		if key != "" && gate.maxPerKey > 0 {
			if gate.byKey[key] <= 1 {
				delete(gate.byKey, key)
			} else {
				gate.byKey[key]--
			}
		}
	}, true
}

const (
	publicWebSocketMaxConnections      = 128
	publicWebSocketMaxConnectionsPerIP = 16
	agentWebSocketMaxConnections       = 256
	detailPublishMaxConcurrent         = 2
)

func NewHandler(options ...HandlerOptions) http.Handler {
	opts := HandlerOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	store := opts.Store
	if store == nil {
		store = mockStore{}
	}
	backgroundParent := opts.BackgroundContext
	if backgroundParent == nil {
		backgroundParent = context.Background()
	}
	backgroundCtx, backgroundCancel := context.WithCancel(backgroundParent)
	h := &handler{
		store:                store,
		adminTokenHash:       opts.AdminTokenHash,
		agentBinaryPath:      opts.AgentBinaryPath,
		agentVersion:         opts.AgentVersion,
		notificationSender:   newHTTPNotificationSender(opts.NotificationClient, opts.TelegramAPIBaseURL),
		loginLimiter:         newAdminLoginLimiter(),
		enrollmentLimiter:    newAdminLoginLimiter(),
		trustedProxies:       opts.TrustedProxies,
		agentQuotas:          newAgentQuotaManager(),
		agentAuthAdmission:   newAgentAuthAdmissionManager(),
		liveHub:              newLiveUpdateHub(),
		presence:             newAgentPresenceManager(),
		publicWSGate:         newWebSocketGateWithPerKey(publicWebSocketMaxConnections, publicWebSocketMaxConnectionsPerIP),
		agentWSGate:          newWebSocketGate(agentWebSocketMaxConnections),
		detailCache:          newDetailJSONCache(),
		detailPublishPending: make(map[string]bool),
		detailPublishGate:    make(chan struct{}, detailPublishMaxConcurrent),
		notificationWorker:   &notificationOutboxWorker{wake: make(chan struct{}, 1)},
		backgroundCtx:        backgroundCtx,
		backgroundCancel:     backgroundCancel,
	}
	if opts.DisableNotifications {
		h.notificationSender = nil
	}
	if opts.StaleOfflineScanInterval > 0 {
		h.startBackground(func(ctx context.Context) { h.runStaleAgentOfflineScanner(ctx, opts.StaleOfflineScanInterval) })
	}
	if opts.RenewalNotificationInterval > 0 {
		h.startBackground(func(ctx context.Context) { h.runRenewalNotificationScanner(ctx, opts.RenewalNotificationInterval) })
	}
	if opts.HistoryRetentionInterval > 0 {
		h.startBackground(func(ctx context.Context) { h.runHistoryRetention(ctx, opts.HistoryRetentionInterval) })
	}
	if opts.NotificationDispatchInterval > 0 {
		h.ensureNotificationOutboxWorker(opts.NotificationDispatchInterval)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/ready", h.handleReady)
	mux.HandleFunc("/api/public/v1/agent/linux-amd64", h.handleAgentBinary)
	mux.HandleFunc("/api/public/v1/settings", h.handlePublicSettings)
	mux.HandleFunc("/api/public/v1/summary", h.handleSummary)
	mux.HandleFunc("/api/public/v1/summary/ws", h.handleSummaryWebSocket)
	mux.HandleFunc("/api/public/v1/services/", h.handlePublicServiceResource)
	mux.HandleFunc("/api/public/v1/nodes/", h.handlePublicNodeResource)
	mux.HandleFunc("/api/admin/v1/login", h.handleAdminLogin)
	mux.HandleFunc("/api/admin/v1/logout", h.handleAdminLogout)
	mux.HandleFunc("/api/admin/v1/account", h.handleAdminAccount)
	mux.HandleFunc("/api/admin/v1/settings", h.handleAdminSettings)
	mux.HandleFunc("/api/admin/v1/notification-channels", h.handleAdminNotificationChannels)
	mux.HandleFunc("/api/admin/v1/notification-channels/", h.handleAdminNotificationChannelResource)
	mux.HandleFunc("/api/admin/v1/notification-deliveries/", h.handleAdminNotificationDeliveryResource)
	mux.HandleFunc("/api/admin/v1/alert-rules", h.handleAdminAlertRules)
	mux.HandleFunc("/api/admin/v1/alert-rules/", h.handleAdminAlertRuleResource)
	mux.HandleFunc("/api/admin/v1/notification-types/", h.handleAdminNotificationTypeResource)
	mux.HandleFunc("/api/admin/v1/probe-targets", h.handleAdminProbeTargets)
	mux.HandleFunc("/api/admin/v1/probe-targets/", h.handleAdminProbeTargetResource)
	mux.HandleFunc("/api/admin/v1/nodes", h.handleAdminNodes)
	mux.HandleFunc("/api/admin/v1/nodes/", h.handleAdminNodeResource)
	mux.HandleFunc("/api/agent/v1/enroll", h.handleAgentEnrollment)
	mux.HandleFunc("/api/agent/v1/probe-targets", h.handleAgentProbeTargets)
	mux.HandleFunc("/api/agent/v1/presence/ws", h.handleAgentPresenceWebSocket)
	mux.HandleFunc("/api/agent/v1/probe-results", h.handleAgentProbeResults)
	mux.HandleFunc("/api/agent/v1/heartbeat", h.handleAgentHeartbeat)
	mux.HandleFunc("/api/agent/v1/host", h.handleAgentHost)
	mux.HandleFunc("/api/agent/v1/state", h.handleAgentState)
	if opts.StaticDir != "" {
		mux.HandleFunc("/", handleStatic(opts.StaticDir))
	}
	h.router = h.withSecurityHeaders(mux)
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.router.ServeHTTP(w, r)
}

func (h *handler) startBackground(fn func(context.Context)) {
	if h == nil || fn == nil {
		return
	}
	ctx, ok := h.beginBackground()
	if !ok {
		return
	}
	go func() {
		defer h.backgroundWG.Done()
		fn(ctx)
	}()
}

func (h *handler) backgroundContext() context.Context {
	if h == nil {
		return context.Background()
	}
	h.backgroundMu.Lock()
	defer h.backgroundMu.Unlock()
	if h.backgroundCtx == nil {
		return context.Background()
	}
	return h.backgroundCtx
}

func (h *handler) beginBackground() (context.Context, bool) {
	if h == nil {
		return nil, false
	}
	h.backgroundMu.Lock()
	defer h.backgroundMu.Unlock()
	if h.backgroundClosing {
		return nil, false
	}
	if h.backgroundCtx == nil {
		h.backgroundCtx, h.backgroundCancel = context.WithCancel(context.Background())
	}
	h.backgroundWG.Add(1)
	return h.backgroundCtx, true
}

func (h *handler) Cleanup(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.backgroundMu.Lock()
	h.backgroundClosing = true
	if h.backgroundCancel != nil {
		h.backgroundCancel()
	}
	h.backgroundMu.Unlock()
	if h.presence != nil {
		h.presence.cancelOfflineChecks()
	}
	h.summaryPublishMu.Lock()
	if h.summaryPublishTimer != nil {
		h.summaryPublishTimer.Stop()
	}
	h.summaryPublishMu.Unlock()
	done := make(chan struct{})
	go func() {
		h.backgroundWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}

}

func (h *handler) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, adminCookieErr := r.Cookie(adminSessionCookieName)
		hasAdminCookie := adminCookieErr == nil
		hasAdminHeader := strings.TrimSpace(r.Header.Get("X-Admin-Token")) != ""
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; font-src 'self' https: data:; connect-src 'self'")
		if h.requestUsesHTTPS(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/") || r.URL.Path == "/api/agent/v1/enroll" || hasAdminHeader || hasAdminCookie {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.URL.Path == "/api/agent/v1/enroll" {
			w.Header().Set("Pragma", "no-cache")
		}
		if hasAdminHeader {
			w.Header().Add("Vary", "X-Admin-Token")
		}
		if hasAdminCookie {
			w.Header().Add("Vary", "Cookie")
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) requestUsesHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	return h.requestProto(r) == "https"
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
	if extendedHistoryWindow(window) && !h.authorizeExtendedHistoryRequest(w, r) {
		return
	}
	if len(parts) == 3 {
		h.handleServiceLatencyWebSocket(w, r, parts[0], window)
		return
	}
	payload, err := h.serviceLatencyJSON(r.Context(), parts[0], window)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeRawJSON(w, http.StatusOK, payload)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *handler) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	checker, ok := h.store.(interface {
		Ready(ctx context.Context) error
	})
	if !ok {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := checker.Ready(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not ready")
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

func (h *handler) requestBaseURL(r *http.Request) string {
	proto := h.requestProto(r)
	if proto == "" {
		proto = "http"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "127.0.0.1:18980"
	}
	return strings.TrimRight(proto+"://"+host, "/")
}

func (h *handler) requestProto(r *http.Request) string {
	if r == nil {
		return ""
	}
	return h.trustedProxies.requestProto(&httpRequestView{
		remoteAddr:     r.RemoteAddr,
		forwardedProto: r.Header.Get("X-Forwarded-Proto"),
		tls:            r.TLS != nil,
	})
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
	if payload, ok := h.cachedSummaryJSON(summaryCacheHTTPFreshFor); ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
		if h.summaryCacheStale(summaryCacheIdleRefreshFor) && h.liveHub != nil && h.liveHub.hasClients(summaryLiveTopic) {
			h.scheduleSummaryPublishAfter(summaryCacheBackgroundDelay)
		}
		return
	}
	payload, err := h.summaryJSONForHTTP(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
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
		if extendedHistoryWindow(window) && !h.authorizeExtendedHistoryRequest(w, r) {
			return
		}
		if len(parts) == 3 {
			h.handleNodeLatencyWebSocket(w, r, nodeID, window)
			return
		}
		payload, err := h.nodeLatencyJSON(r.Context(), nodeID, window)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeRawJSON(w, http.StatusOK, payload)
	case "state":
		window, ok := resolveStateWindow(rangeName)
		if !ok {
			writeError(w, http.StatusBadRequest, "unsupported range")
			return
		}
		if extendedHistoryWindow(window) && !h.authorizeExtendedHistoryRequest(w, r) {
			return
		}
		if len(parts) == 3 {
			h.handleNodeStateWebSocket(w, r, nodeID, window)
			return
		}
		payload, err := h.nodeStateJSON(r.Context(), nodeID, window)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeRawJSON(w, http.StatusOK, payload)
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
			setStaticCacheHeader(w, cleanPath)
			fileServer.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(cleanPath, "/assets/") {
			if assetPath, ok := fallbackReleaseAssetPath(staticDir, strings.TrimPrefix(cleanPath, "/assets/")); ok {
				setStaticCacheHeader(w, cleanPath)
				http.ServeFile(w, r, assetPath)
				return
			}
		}

		indexPath := filepath.Join(staticDir, "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			writeError(w, http.StatusNotFound, "dashboard not built")
			return
		}
		setStaticCacheHeader(w, "/index.html")
		http.ServeFile(w, r, indexPath)
	}
}

var fingerprintedAssetName = regexp.MustCompile(`-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$`)

func setStaticCacheHeader(w http.ResponseWriter, cleanPath string) {
	if cleanPath == "/index.html" || cleanPath == "/" {
		w.Header().Set("Cache-Control", "no-store")
		return
	}
	if strings.HasPrefix(cleanPath, "/assets/") {
		if fingerprintedAssetName.MatchString(filepath.Base(cleanPath)) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// Public assets such as the favicon and OS logos keep stable names and
			// must be revalidated across releases.
			w.Header().Set("Cache-Control", "public, max-age=300, must-revalidate")
		}
	}
}

func fallbackReleaseAssetPath(staticDir string, assetName string) (string, bool) {
	cleanAssetName := filepath.Clean(assetName)
	if cleanAssetName == "." || strings.HasPrefix(cleanAssetName, "..") || filepath.IsAbs(cleanAssetName) {
		return "", false
	}

	installDir := filepath.Dir(filepath.Dir(staticDir))
	candidates, err := filepath.Glob(filepath.Join(installDir, "releases", "*", "web", "assets", cleanAssetName))
	if err != nil || len(candidates) == 0 {
		return "", false
	}
	sort.Sort(sort.Reverse(sort.StringSlice(candidates)))
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeRawJSON(w http.ResponseWriter, status int, payload []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
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
