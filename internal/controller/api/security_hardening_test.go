package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLegacyAgentPlaintextCredentialIsRemovedWithoutInvalidatingRuntimeHash(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "zeno.db")
	store, err := OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "legacy", DisplayName: "Legacy", AgentToken: "legacy-runtime-token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE nodes SET install_token = 'legacy-runtime-token' WHERE id = 'legacy'`); err != nil {
		t.Fatalf("restore legacy plaintext: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close old store: %v", err)
	}

	store, err = OpenSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen upgraded store: %v", err)
	}
	defer store.Close()
	var plaintext *string
	if err := store.db.QueryRow(`SELECT install_token FROM nodes WHERE id = 'legacy'`).Scan(&plaintext); err != nil {
		t.Fatalf("read migrated credential: %v", err)
	}
	if plaintext != nil {
		t.Fatalf("legacy plaintext remained after migration: %q", *plaintext)
	}
	allowed, err := store.AuthorizeAgent(context.Background(), "legacy", "legacy-runtime-token")
	if err != nil || !allowed {
		t.Fatalf("legacy runtime hash was invalidated: allowed=%v err=%v", allowed, err)
	}
}

func TestAuthorizeAgentInvalidTokensStayReadOnlyDuringConcurrentWriter(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	store.db.SetMaxOpenConns(40)
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "node-a", DisplayName: "Node A", AgentToken: "valid-token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	writer, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatalf("reserve writer connection: %v", err)
	}
	defer writer.Close()
	if _, err := writer.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("hold sqlite writer: %v", err)
	}
	defer writer.ExecContext(context.Background(), `ROLLBACK`)

	const invalidCallers = 24
	start := make(chan struct{})
	errCh := make(chan error, invalidCallers+1)
	var wait sync.WaitGroup
	for index := 0; index < invalidCallers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			allowed, err := store.AuthorizeAgent(ctx, "node-a", fmt.Sprintf("invalid-%d", index))
			if err != nil {
				errCh <- fmt.Errorf("invalid token %d: %w", index, err)
			} else if allowed {
				errCh <- fmt.Errorf("invalid token %d was authorized", index)
			}
		}(index)
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-start
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		allowed, err := store.AuthorizeAgent(ctx, "node-a", "valid-token")
		if err != nil {
			errCh <- fmt.Errorf("valid token: %w", err)
		} else if !allowed {
			errCh <- errors.New("valid token was rejected")
		}
	}()
	close(start)
	wait.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestAuthorizeAgentConcurrentPendingPromotionIsAtomic(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "node-a", DisplayName: "Node A", AgentToken: "old-token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	const pendingToken = "new-pending-token"
	if _, err := store.db.Exec(`UPDATE nodes SET pending_token_hash = ?, pending_token_expires_at = ? WHERE id = ?`, hashAgentToken(pendingToken), time.Now().Add(time.Minute).Unix(), "node-a"); err != nil {
		t.Fatalf("stage pending token: %v", err)
	}

	const callers = 24
	start := make(chan struct{})
	results := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			allowed, err := store.AuthorizeAgent(context.Background(), "node-a", pendingToken)
			if err != nil {
				results <- err
			} else if !allowed {
				results <- errors.New("pending token was rejected during concurrent promotion")
			}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	if allowed, err := store.AuthorizeAgent(context.Background(), "node-a", "old-token"); err != nil || allowed {
		t.Fatalf("old token remained valid after promotion: allowed=%v err=%v", allowed, err)
	}
	var pendingHash *string
	if err := store.db.QueryRow(`SELECT pending_token_hash FROM nodes WHERE id = ?`, "node-a").Scan(&pendingHash); err != nil {
		t.Fatalf("read pending hash: %v", err)
	}
	if pendingHash != nil {
		t.Fatalf("pending token hash was not cleared: %q", *pendingHash)
	}
}

func TestAgentEnrollmentHTTPIsOneTimeExpiringAndPromotesOnFirstUse(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "node-a", DisplayName: "Node A", AgentToken: "old-runtime-token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	commands, err := store.AdminNodeInstallCommand(ctx, "node-a", "https://zeno.example.com", "latest")
	if err != nil {
		t.Fatalf("issue enrollment: %v", err)
	}
	enrollmentToken := extractQuotedInstallCredential(t, commands.Linux)
	runtimeToken := strings.Repeat("r", 64)
	handler := NewHandler(HandlerOptions{Store: store})
	post := func(enrollment, runtime string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(AgentEnrollmentRequest{NodeID: "node-a", EnrollmentToken: enrollment, RuntimeToken: runtime})
		if err != nil {
			t.Fatalf("marshal enrollment: %v", err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", strings.NewReader(string(payload)))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "198.51.100.10:12345"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	if recorder := post(enrollmentToken, runtimeToken); recorder.Code != http.StatusNoContent {
		t.Fatalf("redeem status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := post(enrollmentToken, strings.Repeat("s", 64)); recorder.Code != http.StatusGone {
		t.Fatalf("replay status=%d want 410; body=%s", recorder.Code, recorder.Body.String())
	}
	if allowed, err := store.AuthorizeAgent(ctx, "node-a", "old-runtime-token"); err != nil || !allowed {
		t.Fatalf("old token should remain valid before pending activation: allowed=%v err=%v", allowed, err)
	}
	if allowed, err := store.AuthorizeAgent(ctx, "node-a", runtimeToken); err != nil || !allowed {
		t.Fatalf("new runtime token should activate: allowed=%v err=%v", allowed, err)
	}
	if allowed, err := store.AuthorizeAgent(ctx, "node-a", "old-runtime-token"); err != nil || allowed {
		t.Fatalf("old token should retire after activation: allowed=%v err=%v", allowed, err)
	}

	expiredToken, _, err := store.issueAgentEnrollment(ctx, "node-a")
	if err != nil {
		t.Fatalf("issue expiring enrollment: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE agent_enrollment_tokens SET expires_at = ? WHERE token_hash = ?`, time.Now().Add(-time.Minute).Unix(), hashAgentToken(expiredToken)); err != nil {
		t.Fatalf("expire enrollment: %v", err)
	}
	if recorder := post(expiredToken, strings.Repeat("t", 64)); recorder.Code != http.StatusGone {
		t.Fatalf("expired status=%d want 410; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentEnrollmentRejectsSimpleCrossSiteRequestsAndRateLimitsFailures(t *testing.T) {
	handler := NewHandler()
	plain := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", strings.NewReader(`{}`))
	plain.Header.Set("Content-Type", "text/plain")
	plainRecorder := httptest.NewRecorder()
	handler.ServeHTTP(plainRecorder, plain)
	if plainRecorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain status=%d want 415", plainRecorder.Code)
	}

	crossSite := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", strings.NewReader(`{}`))
	crossSite.Header.Set("Content-Type", "application/json")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(crossSiteRecorder, crossSite)
	if crossSiteRecorder.Code != http.StatusForbidden {
		t.Fatalf("cross-site status=%d want 403", crossSiteRecorder.Code)
	}

	for attempt := 0; attempt < adminLoginMaxFailures; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "198.51.100.20:12345"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("failed enrollment %d status=%d want 404", attempt+1, recorder.Code)
		}
	}
	blocked := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", strings.NewReader(`{}`))
	blocked.Header.Set("Content-Type", "application/json")
	blocked.RemoteAddr = "198.51.100.20:12345"
	blockedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(blockedRecorder, blocked)
	if blockedRecorder.Code != http.StatusTooManyRequests || blockedRecorder.Header().Get("Retry-After") != "600" {
		t.Fatalf("blocked status=%d retry=%q", blockedRecorder.Code, blockedRecorder.Header().Get("Retry-After"))
	}
	other := httptest.NewRequest(http.MethodPost, "/api/agent/v1/enroll", strings.NewReader(`{}`))
	other.Header.Set("Content-Type", "application/json")
	other.RemoteAddr = "198.51.100.21:12345"
	otherRecorder := httptest.NewRecorder()
	handler.ServeHTTP(otherRecorder, other)
	if otherRecorder.Code != http.StatusNotFound {
		t.Fatalf("other client was globally limited: status=%d", otherRecorder.Code)
	}
}

func TestAdminLoginRequiresJSONAndRejectsCrossSite(t *testing.T) {
	handler := NewHandler(HandlerOptions{AdminTokenHash: HashAdminToken("admin-pass")})
	plain := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
	plain.Header.Set("Content-Type", "text/plain")
	plainRecorder := httptest.NewRecorder()
	handler.ServeHTTP(plainRecorder, plain)
	if plainRecorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain login status=%d want 415", plainRecorder.Code)
	}
	crossSite := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
	crossSite.Header.Set("Content-Type", "application/json")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(crossSiteRecorder, crossSite)
	if crossSiteRecorder.Code != http.StatusForbidden {
		t.Fatalf("cross-site login status=%d want 403", crossSiteRecorder.Code)
	}
}

func TestTrustedProxyChainControlsClientIdentityAndHTTPS(t *testing.T) {
	trusted, err := ParseTrustedProxies("172.30.250.1/32,10.0.0.0/8")
	if err != nil {
		t.Fatalf("parse proxies: %v", err)
	}
	h := &handler{trustedProxies: trusted}
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.RemoteAddr = "172.30.250.1:40000"
	request.Header.Set("X-Forwarded-For", "198.51.100.42, 10.0.0.2")
	if got := h.clientIPForRateLimit(request); got != "198.51.100.42" {
		t.Fatalf("trusted chain client=%q", got)
	}
	request.Header.Set("X-Forwarded-For", "spoofed, 198.51.100.42")
	if got := h.clientIPForRateLimit(request); got != "172.30.250.1" {
		t.Fatalf("malformed chain fell through to spoofed identity: %q", got)
	}
	request.RemoteAddr = "203.0.113.10:40000"
	request.Header.Set("X-Forwarded-For", "198.51.100.42")
	if got := h.clientIPForRateLimit(request); got != "203.0.113.10" {
		t.Fatalf("untrusted peer controlled identity: %q", got)
	}

	secured := NewHandler(HandlerOptions{TrustedProxies: trusted})
	trustedHTTPS := httptest.NewRequest(http.MethodGet, "/health", nil)
	trustedHTTPS.RemoteAddr = "172.30.250.1:40000"
	trustedHTTPS.Header.Set("X-Forwarded-Proto", "https")
	trustedRecorder := httptest.NewRecorder()
	secured.ServeHTTP(trustedRecorder, trustedHTTPS)
	if trustedRecorder.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("trusted HTTPS proxy response omitted HSTS")
	}
	untrustedHTTPS := httptest.NewRequest(http.MethodGet, "/health", nil)
	untrustedHTTPS.RemoteAddr = "203.0.113.10:40000"
	untrustedHTTPS.Header.Set("X-Forwarded-Proto", "https")
	untrustedRecorder := httptest.NewRecorder()
	secured.ServeHTTP(untrustedRecorder, untrustedHTTPS)
	if untrustedRecorder.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("untrusted peer enabled HSTS with forwarding header")
	}
	if _, err := ParseTrustedProxies("0.0.0.0/0"); err == nil {
		t.Fatal("all-address trusted proxy range was accepted")
	}
}

func TestAgentQuotaSupportsNormalCadenceAndIsolatesAbusiveNodes(t *testing.T) {
	manager := newAgentQuotaManager()
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	for request := 0; request < int(agentRequestBucketSpecs[agentQuotaState].burst); request++ {
		if _, ok := manager.admitRequest("node-a", agentQuotaState); !ok {
			t.Fatalf("normal state burst rejected at %d", request)
		}
	}
	if _, ok := manager.admitRequest("node-a", agentQuotaState); ok {
		t.Fatal("abusive state burst was accepted")
	}
	if _, ok := manager.admitRequest("node-b", agentQuotaState); !ok {
		t.Fatal("one node's quota affected another node")
	}
	now = now.Add(time.Second)
	if _, ok := manager.admitRequest("node-a", agentQuotaState); !ok {
		t.Fatal("one-second normal state cadence did not refill")
	}

	maximumProbe := AgentProbeResultsRequest{Rounds: make([]AgentProbeRound, 32)}
	for index := range maximumProbe.Rounds {
		maximumProbe.Rounds[index].Samples = make([]AgentProbeSample, 32)
	}
	units := agentProbeWriteUnits(maximumProbe)
	release, _, ok := manager.admitWrite("node-probe", units)
	if !ok {
		t.Fatalf("maximum valid probe batch cost %.0f was rejected", units)
	}
	release()
	now = now.Add(5 * time.Second)
	release, _, ok = manager.admitWrite("node-probe", units)
	if !ok {
		t.Fatal("five-second maximum probe cadence was rejected")
	}
	release()

	releases := make([]func(), 0, agentWriteMaxConcurrent)
	for index := 0; index < agentWriteMaxConcurrent; index++ {
		release, _, ok := manager.admitWrite("node-concurrent", 1)
		if !ok {
			t.Fatalf("write concurrency %d rejected", index+1)
		}
		releases = append(releases, release)
	}
	if release, _, ok := manager.admitWrite("node-concurrent", 1); ok {
		release()
		t.Fatal("per-node write concurrency cap was bypassed")
	}
	if release, _, ok := manager.admitWrite("node-other", 1); !ok {
		t.Fatal("write concurrency leaked across nodes")
	} else {
		release()
	}
	for _, release := range releases {
		release()
	}

	globalReleases := make([]func(), 0, agentWriteMaxGlobalConcurrent)
	for index := 0; index < agentWriteMaxGlobalConcurrent; index++ {
		release, _, ok := manager.admitWrite(fmt.Sprintf("global-node-%d", index), 1)
		if !ok {
			t.Fatalf("global write admission rejected node %d before the bound", index)
		}
		globalReleases = append(globalReleases, release)
	}
	if release, retryAfter, ok := manager.admitWrite("global-overflow", 1); ok {
		release()
		t.Fatal("global multi-node write admission bound was bypassed")
	} else if retryAfter != time.Second {
		t.Fatalf("global admission retry-after = %s, want fast one-second retry", retryAfter)
	}
	globalReleases[0]()
	if release, _, ok := manager.admitWrite("global-after-release", 1); !ok {
		t.Fatal("global write slot was not released")
	} else {
		release()
	}
	for _, release := range globalReleases[1:] {
		release()
	}
}

func TestAgentAuthAdmissionBoundsPreAuthenticationDatabaseWork(t *testing.T) {
	now := time.Now().UTC()
	manager := newAgentAuthAdmissionManager()
	manager.now = func() time.Time { return now }
	for index := 0; index < int(agentAuthPerIPBucketSpec.burst); index++ {
		release, _, ok := manager.admit("198.51.100.1")
		if !ok {
			t.Fatalf("per-IP admission rejected request %d before burst", index)
		}
		release()
	}
	if _, _, ok := manager.admit("198.51.100.1"); ok {
		t.Fatal("per-IP authentication burst limit was bypassed")
	}
	if release, _, ok := manager.admit("198.51.100.2"); !ok {
		t.Fatal("one abusive IP blocked an independent IP")
	} else {
		release()
	}

	concurrency := newAgentAuthAdmissionManager()
	concurrency.now = func() time.Time { return now }
	releases := make([]func(), 0, agentAuthMaxConcurrent)
	for index := 0; index < agentAuthMaxConcurrent; index++ {
		release, _, ok := concurrency.admit(fmt.Sprintf("203.0.113.%d", index))
		if !ok {
			t.Fatalf("global concurrency rejected request %d before bound", index)
		}
		releases = append(releases, release)
	}
	if _, _, ok := concurrency.admit("192.0.2.1"); ok {
		t.Fatal("global pre-authentication concurrency bound was bypassed")
	}
	for _, release := range releases {
		release()
	}
}

func TestAgentAuthPerIPRejectionDoesNotDrainGlobalBucket(t *testing.T) {
	now := time.Now().UTC()
	manager := newAgentAuthAdmissionManager()
	manager.now = func() time.Time { return now }
	abusiveIP := "198.51.100.99"
	for index := 0; index < int(agentAuthPerIPBucketSpec.burst); index++ {
		release, _, ok := manager.admit(abusiveIP)
		if !ok {
			t.Fatalf("abusive IP rejected before its own burst at request %d", index)
		}
		release()
	}
	globalBefore := manager.global.tokens
	for index := 0; index < 500; index++ {
		if release, _, ok := manager.admit(abusiveIP); ok {
			release()
			t.Fatalf("per-IP exhausted source admitted request %d", index)
		}
	}
	if manager.global.tokens != globalBefore {
		t.Fatalf("per-IP rejections drained global tokens: before=%v after=%v", globalBefore, manager.global.tokens)
	}
	if release, _, ok := manager.admit("203.0.113.10"); !ok {
		t.Fatal("abusive IP exhausted global authentication capacity")
	} else {
		release()
	}
}

func TestAgentAuthGlobalRejectionDoesNotCreatePerIPEntries(t *testing.T) {
	now := time.Now().UTC()
	manager := newAgentAuthAdmissionManager()
	manager.now = func() time.Time { return now }
	manager.global = agentTokenBucket{tokens: 0, updatedAt: now}
	for index := 0; index < agentAuthMaxKeys*2; index++ {
		if release, _, ok := manager.admit(fmt.Sprintf("192.0.2.%d", index)); ok {
			release()
			t.Fatalf("request %d admitted with empty global bucket", index)
		}
	}
	if len(manager.entries) != 0 {
		t.Fatalf("globally rejected requests created %d per-IP entries", len(manager.entries))
	}
}

func TestNetworkCounterSourceValidationIsBoundedAndCanonical(t *testing.T) {
	if !validNetworkCounterSource("") || !validNetworkCounterSource(strings.Repeat("a", 64)) {
		t.Fatal("valid empty or SHA-256 counter source was rejected")
	}
	for _, value := range []string{strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("z", 64)} {
		if validNetworkCounterSource(value) {
			t.Fatalf("invalid counter source accepted: %q", value)
		}
	}
}

func TestAgentEnrollmentStoreUsesUniformUnavailableError(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	err = store.RedeemAgentEnrollment(context.Background(), "unknown", "unknown", strings.Repeat("x", 64))
	if !errors.Is(err, errAgentEnrollmentUnavailable) {
		t.Fatalf("unknown enrollment error=%v", err)
	}
}
