package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminSessionsAreBoundedAndExactExpiryIsRejected(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	for index := 0; index < adminSessionMaxActive+4; index++ {
		if err := store.createAdminSession(ctx, fmt.Sprintf("session-%02d", index)); err != nil {
			t.Fatalf("create session %d: %v", index, err)
		}
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_sessions`).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != adminSessionMaxActive {
		t.Fatalf("active sessions = %d, want %d", count, adminSessionMaxActive)
	}

	token := "exactly-expired-session"
	now := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO admin_sessions (token_hash, created_at, last_seen_at)
		VALUES (?, ?, ?)
	`, HashAdminToken(token), now-int64(adminSessionAbsoluteTimeout.Seconds()), now); err != nil {
		t.Fatalf("insert exact-boundary session: %v", err)
	}
	authorized, err := store.AuthorizeAdminSession(ctx, token)
	if err != nil {
		t.Fatalf("authorize exact-boundary session: %v", err)
	}
	if authorized {
		t.Fatal("session at the absolute timeout boundary was accepted")
	}
}

func TestAdminLoginRateLimitKeyHandlesIPv6AndTrustedProxy(t *testing.T) {
	direct := &http.Request{RemoteAddr: "[2001:db8::1]:443"}
	if got := adminLoginRateLimitKey(direct, "Admin"); !strings.HasPrefix(got, "2001:db8::1:") {
		t.Fatalf("direct IPv6 rate key = %q, want parsed IPv6 host", got)
	}

	proxied := &http.Request{RemoteAddr: "127.0.0.1:12345", Header: http.Header{"X-Forwarded-For": []string{"203.0.113.10, 10.0.0.2"}}}
	if got := adminLoginRateLimitKey(proxied, "Admin"); !strings.HasPrefix(got, "10.0.0.2:") {
		t.Fatalf("trusted proxy rate key = %q, want rightmost trusted-edge peer", got)
	}

	spoofedLeftmost := &http.Request{RemoteAddr: "127.0.0.1:12345", Header: http.Header{"X-Forwarded-For": []string{"198.51.100.99, 203.0.113.10"}}}
	if got := adminLoginRateLimitKey(spoofedLeftmost, "Admin"); !strings.HasPrefix(got, "203.0.113.10:") {
		t.Fatalf("spoofed forwarded rate key = %q, want trusted-edge client", got)
	}

	untrusted := &http.Request{RemoteAddr: "198.51.100.2:12345", Header: http.Header{"X-Forwarded-For": []string{"203.0.113.10"}}}
	if got := adminLoginRateLimitKey(untrusted, "Admin"); !strings.HasPrefix(got, "198.51.100.2:") {
		t.Fatalf("untrusted proxy rate key = %q, want remote addr", got)
	}

	privateDirect := &http.Request{RemoteAddr: "192.168.1.20:12345", Header: http.Header{"X-Forwarded-For": []string{"203.0.113.10"}}}
	if got := adminLoginRateLimitKey(privateDirect, "Admin"); !strings.HasPrefix(got, "192.168.1.20:") {
		t.Fatalf("private direct rate key = %q, want unspoofed remote addr", got)
	}
}

func TestAdminLoginIPRateLimitCannotBeBypassedWithDifferentUsernames(t *testing.T) {
	limiter := newAdminLoginLimiter()
	request := &http.Request{RemoteAddr: "198.51.100.9:12345"}
	for index := 0; index < adminLoginMaxFailures; index++ {
		ipReservation, ok := limiter.reserve(adminLoginIPRateLimitKey(request))
		if !ok {
			t.Fatalf("IP attempt %d rejected before limit", index+1)
		}
		accountReservation, ok := limiter.reserve(adminLoginRateLimitKey(request, fmt.Sprintf("user-%d", index)))
		if !ok {
			t.Fatalf("account attempt %d rejected unexpectedly", index+1)
		}
		ipReservation.release(false)
		accountReservation.release(false)
	}
	if _, ok := limiter.reserve(adminLoginIPRateLimitKey(request)); ok {
		t.Fatal("rotating usernames bypassed the per-IP login limit")
	}
}

func TestAdminPasswordHashRejectsOversizedArgonParameters(t *testing.T) {
	oversized := "argon2id:v=19:m=1048576:t=3:p=2:emVuby1kdW1teS1zYWx0:MfaHhKQHaOt+QsALfIOerW4EtUmf5zKMiHhxvflHstY"
	if adminPasswordMatches(oversized, "", "wrong-pass") {
		t.Fatal("oversized stored argon2 hash matched unexpectedly")
	}
}

func TestAdminLoginRejectsWhenArgon2QueueIsFull(t *testing.T) {
	queueDrained := false
	for index := 0; index < cap(adminArgon2Admissions); index++ {
		adminArgon2Admissions <- struct{}{}
	}
	defer func() {
		if !queueDrained {
			for index := 0; index < cap(adminArgon2Admissions); index++ {
				<-adminArgon2Admissions
			}
		}
	}()

	handler := NewHandler(HandlerOptions{AdminTokenHash: HashAdminToken("admin-pass")})
	for attempt := 0; attempt < adminLoginMaxFailures; attempt++ {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("admission rejection %d status = %d, want 429; body=%s", attempt+1, recorder.Code, recorder.Body.String())
		}
	}
	for index := 0; index < cap(adminArgon2Admissions); index++ {
		<-adminArgon2Admissions
	}
	queueDrained = true
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid login after admission recovery status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
}
