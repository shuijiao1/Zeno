package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func secureAdminRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, "https://zeno.example.com"+path, strings.NewReader(body))
	request.Host = "zeno.example.com"
	request.TLS = &tls.ConnectionState{}
	return request
}

func TestBrowserAdminSessionUsesSecureHttpOnlyCookieAndCSRF(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if err := store.SeedPreviewData(context.Background(), PreviewSeedOptions{NodeID: "node-1", DisplayName: "Node 1", AgentToken: "agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	insecureLogin := httptest.NewRequest(http.MethodPost, "http://zeno.example.com/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
	insecureLogin.Header.Set("Content-Type", "application/json")
	insecureLogin.Header.Set("Origin", "http://zeno.example.com")
	insecureLogin.Header.Set(adminCSRFHeaderName, adminCSRFHeaderValue)
	insecureRecorder := httptest.NewRecorder()
	handler.ServeHTTP(insecureRecorder, insecureLogin)
	if insecureRecorder.Code != http.StatusForbidden || insecureRecorder.Header().Get("Set-Cookie") != "" {
		t.Fatalf("insecure browser login status=%d cookie=%q", insecureRecorder.Code, insecureRecorder.Header().Get("Set-Cookie"))
	}

	login := secureAdminRequest(http.MethodPost, "/api/admin/v1/login", `{"username":"admin","password":"admin-pass"}`)
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "https://zeno.example.com")
	login.Header.Set(adminCSRFHeaderName, adminCSRFHeaderValue)
	loginRecorder := httptest.NewRecorder()
	handler.ServeHTTP(loginRecorder, login)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("browser login status=%d body=%s", loginRecorder.Code, loginRecorder.Body.String())
	}
	var response AdminLoginResponse
	if err := json.NewDecoder(loginRecorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if response.Username != "admin" || response.Token != "" || strings.Contains(loginRecorder.Body.String(), "token") {
		t.Fatalf("browser login leaked replayable session: %s", loginRecorder.Body.String())
	}
	cookies := loginRecorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies=%v, want one admin session cookie", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != adminSessionCookieName || cookie.Value == "" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("admin cookie attributes=%+v", cookie)
	}

	list := secureAdminRequest(http.MethodGet, "/api/admin/v1/nodes", "")
	list.AddCookie(cookie)
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("cookie-authenticated GET status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	if listRecorder.Header().Get("Cache-Control") != "no-store" || !strings.Contains(strings.Join(listRecorder.Header().Values("Vary"), ","), "Cookie") {
		t.Fatalf("cookie-authenticated response cache headers=%v", listRecorder.Header())
	}

	for _, tc := range []struct {
		name   string
		origin string
		csrf   string
	}{
		{name: "missing csrf", origin: "https://zeno.example.com"},
		{name: "cross origin", origin: "https://evil.example", csrf: adminCSRFHeaderValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logout := secureAdminRequest(http.MethodPost, "/api/admin/v1/logout", "")
			logout.AddCookie(cookie)
			logout.Header.Set("Origin", tc.origin)
			if tc.csrf != "" {
				logout.Header.Set(adminCSRFHeaderName, tc.csrf)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, logout)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status=%d want 403 body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	logout := secureAdminRequest(http.MethodPost, "/api/admin/v1/logout", "")
	logout.AddCookie(cookie)
	logout.Header.Set("Origin", "https://zeno.example.com")
	logout.Header.Set(adminCSRFHeaderName, adminCSRFHeaderValue)
	logoutRecorder := httptest.NewRecorder()
	handler.ServeHTTP(logoutRecorder, logout)
	if logoutRecorder.Code != http.StatusNoContent {
		t.Fatalf("same-origin logout status=%d body=%s", logoutRecorder.Code, logoutRecorder.Body.String())
	}
	cleared := logoutRecorder.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != adminSessionCookieName || cleared[0].MaxAge >= 0 {
		t.Fatalf("logout cookie=%+v, want explicit secure deletion", cleared)
	}
}

func TestAdminHeaderTokenRemainsCLICompatibleWithoutBrowserCSRF(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	login := httptest.NewRequest(http.MethodPost, "/api/admin/v1/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
	login.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	handler.ServeHTTP(loginRecorder, login)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("CLI login status=%d body=%s", loginRecorder.Code, loginRecorder.Body.String())
	}
	var session AdminLoginResponse
	if err := json.NewDecoder(loginRecorder.Body).Decode(&session); err != nil || session.Token == "" {
		t.Fatalf("CLI login response=%+v err=%v", session, err)
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/admin/v1/logout", nil)
	logout.Header.Set("X-Admin-Token", session.Token)
	logoutRecorder := httptest.NewRecorder()
	handler.ServeHTTP(logoutRecorder, logout)
	if logoutRecorder.Code != http.StatusNoContent {
		t.Fatalf("header-token logout status=%d body=%s", logoutRecorder.Code, logoutRecorder.Body.String())
	}
}
