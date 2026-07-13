package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicSettingsDefaultsAndReflectsAdminPatch(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	defaultRecorder := httptest.NewRecorder()
	handler.ServeHTTP(defaultRecorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/settings", nil))
	if defaultRecorder.Code != http.StatusOK {
		t.Fatalf("default public settings status = %d, want 200; body=%s", defaultRecorder.Code, defaultRecorder.Body.String())
	}
	var defaults SiteSettings
	if err := json.NewDecoder(bytes.NewBufferString(defaultRecorder.Body.String())).Decode(&defaults); err != nil {
		t.Fatalf("decode default settings: %v", err)
	}
	if defaults.SiteTitle != "Zeno" || defaults.LogoURL != "/assets/logo/id.png" || defaults.Theme != "system" || defaults.DesktopBackgroundURL != "" || defaults.MobileBackgroundURL != "" || defaults.AppearancePreset != "default" || defaults.CardOpacity != 0.72 || defaults.CardBlur != 0 || defaults.ThemeColor != "#2563eb" {
		t.Fatalf("default settings = %+v, want Zeno defaults", defaults)
	}
	if strings.Contains(defaultRecorder.Body.String(), `"avatar_url"`) {
		t.Fatalf("default settings should use logo_url only, got retired avatar_url field: %s", defaultRecorder.Body.String())
	}
	assertNoSensitiveSettingsLeak(t, defaultRecorder.Body.String())

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/settings", bytes.NewBufferString(`{
		"site_title": "  水饺监控  ",
		"site_subtitle": "  VPS 状态总览  ",
		"logo_url": "/assets/logo/custom.png",
		"theme": "dark",
		"agent_controller_url": "  https://zeno.example.com/  ",
		"background_url": "https://example.com/legacy-bg.webp",
		"desktop_background_url": "https://example.com/desktop-bg.webp",
		"mobile_background_url": "https://example.com/mobile-bg.webp",
		"appearance_preset": "gaussian_blur",
		"card_opacity": 0.58,
		"card_blur": 18,
		"card_radius": 24,
		"border_strength": 0.34,
		"shadow_strength": 0.34,
		"background_overlay": 0.08,
		"theme_color": "#6366f1",
		"custom_code": "  <style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>  "
	}`))
	patchRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch settings status = %d, want 200; body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}
	assertNoSensitiveSettingsLeak(t, patchRecorder.Body.String())
	var patchResponse struct {
		Settings SiteSettings `json:"settings"`
	}
	if err := json.NewDecoder(bytes.NewBufferString(patchRecorder.Body.String())).Decode(&patchResponse); err != nil {
		t.Fatalf("decode patched settings: %v", err)
	}
	if patchResponse.Settings.SiteTitle != "水饺监控" || patchResponse.Settings.SiteSubtitle != "VPS 状态总览" || patchResponse.Settings.LogoURL != "/assets/logo/custom.png" || patchResponse.Settings.Theme != "dark" || patchResponse.Settings.AgentControllerURL != "https://zeno.example.com" || patchResponse.Settings.BackgroundURL != "https://example.com/desktop-bg.webp" || patchResponse.Settings.DesktopBackgroundURL != "https://example.com/desktop-bg.webp" || patchResponse.Settings.MobileBackgroundURL != "https://example.com/mobile-bg.webp" || patchResponse.Settings.AppearancePreset != "gaussian_blur" || patchResponse.Settings.CardOpacity != 0.58 || patchResponse.Settings.CardBlur != 18 || patchResponse.Settings.CardRadius != 24 || patchResponse.Settings.BorderStrength != 0.34 || patchResponse.Settings.ShadowStrength != 0.34 || patchResponse.Settings.BackgroundOverlay != 0.08 || patchResponse.Settings.ThemeColor != "#6366f1" || patchResponse.Settings.CustomCode != "<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>" {
		t.Fatalf("patched settings = %+v, want trimmed persisted settings", patchResponse.Settings)
	}
	if strings.Contains(patchRecorder.Body.String(), `"avatar_url"`) {
		t.Fatalf("patched settings should not expose retired avatar_url field: %s", patchRecorder.Body.String())
	}

	publicRecorder := httptest.NewRecorder()
	handler.ServeHTTP(publicRecorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/settings", nil))
	if publicRecorder.Code != http.StatusOK {
		t.Fatalf("public settings after patch status = %d, want 200; body=%s", publicRecorder.Code, publicRecorder.Body.String())
	}
	if !strings.Contains(publicRecorder.Body.String(), `"site_title":"水饺监控"`) || !strings.Contains(publicRecorder.Body.String(), `"logo_url":"/assets/logo/custom.png"`) || !strings.Contains(publicRecorder.Body.String(), `"agent_controller_url":"https://zeno.example.com"`) || !strings.Contains(publicRecorder.Body.String(), `"desktop_background_url":"https://example.com/desktop-bg.webp"`) || !strings.Contains(publicRecorder.Body.String(), `"mobile_background_url":"https://example.com/mobile-bg.webp"`) || !strings.Contains(publicRecorder.Body.String(), `"appearance_preset":"gaussian_blur"`) || !strings.Contains(publicRecorder.Body.String(), `"card_blur":18`) || !strings.Contains(publicRecorder.Body.String(), `"theme_color":"#6366f1"`) || !strings.Contains(publicRecorder.Body.String(), `"custom_code":"\u003cstyle\u003e.home-top-card { border-color: #2563eb; }\u003c/style\u003e\u003cscript\u003ewindow.ZenoCustomLoaded = true;\u003c/script\u003e"`) {
		t.Fatalf("public settings after patch did not reflect admin update: %s", publicRecorder.Body.String())
	}
	if strings.Contains(publicRecorder.Body.String(), `"avatar_url"`) {
		t.Fatalf("public settings should not expose retired avatar_url field: %s", publicRecorder.Body.String())
	}
}

func TestAdminSettingsRequiresTokenAndRejectsInvalidValues(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	unauthRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthRecorder, httptest.NewRequest(http.MethodGet, "/api/admin/v1/settings", nil))
	if unauthRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauth settings status = %d, want 401; body=%s", unauthRecorder.Code, unauthRecorder.Body.String())
	}
	assertNoSensitiveSettingsLeak(t, unauthRecorder.Body.String())

	cases := []struct {
		name string
		body string
	}{
		{name: "blank site title", body: `{"site_title":"   "}`},
		{name: "unsupported theme", body: `{"theme":"neon"}`},
		{name: "javascript logo", body: `{"logo_url":"javascript:alert(1)"}`},
		{name: "retired avatar field", body: `{"avatar_url":"/assets/avatar/custom.webp"}`},
		{name: "agent controller URL with credentials", body: `{"agent_controller_url":"https://user:pass@example.com"}`},
		{name: "agent controller URL with query", body: `{"agent_controller_url":"https://example.com/?token=1"}`},
		{name: "remote agent controller URL over HTTP", body: `{"agent_controller_url":"http://example.com"}`},
		{name: "agent controller URL unsupported scheme", body: `{"agent_controller_url":"javascript:alert(1)"}`},
		{name: "javascript background", body: `{"background_url":"data:text/html,<script>alert(1)</script>"}`},
		{name: "javascript desktop background", body: `{"desktop_background_url":"data:text/html,<script>alert(1)</script>"}`},
		{name: "javascript mobile background", body: `{"mobile_background_url":"//evil.example/bg.webp"}`},
		{name: "unsupported appearance preset", body: `{"appearance_preset":"neon"}`},
		{name: "too low opacity", body: `{"card_opacity":0.1}`},
		{name: "too high blur", body: `{"card_blur":41}`},
		{name: "too low radius", body: `{"card_radius":7}`},
		{name: "too high border", body: `{"border_strength":1.1}`},
		{name: "too high shadow", body: `{"shadow_strength":1.1}`},
		{name: "too high overlay", body: `{"background_overlay":0.9}`},
		{name: "invalid theme color", body: `{"theme_color":"blue"}`},
		{name: "oversized custom code", body: `{"custom_code":"` + strings.Repeat("a", maxSettingsCustomCodeRunes+1) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/settings", bytes.NewBufferString(tc.body))
			request.Header.Set("X-Admin-Token", "admin-pass")
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			assertNoSensitiveSettingsLeak(t, recorder.Body.String())
		})
	}

	settings, err := store.PublicSettings(context.Background())
	if err != nil {
		t.Fatalf("public settings after invalid patches: %v", err)
	}
	if settings.SiteTitle != "Zeno" || settings.Theme != "system" {
		t.Fatalf("invalid patches should not mutate defaults, got %+v", settings)
	}
}

func TestValidAgentControllerURLRequiresHTTPSOutsideLoopback(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{url: "https://zeno.example.com", want: true},
		{url: "http://127.0.0.1:18980", want: true},
		{url: "http://[::1]:18980", want: true},
		{url: "http://localhost:18980", want: true},
		{url: "http://zeno.example.com", want: false},
		{url: "https://user:pass@zeno.example.com", want: false},
		{url: "https://zeno.example.com/?token=secret", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			if got := validAgentControllerURL(tc.url); got != tc.want {
				t.Fatalf("validAgentControllerURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func assertNoSensitiveSettingsLeak(t *testing.T, raw string) {
	t.Helper()
	lower := bytes.ToLower([]byte(raw))
	for _, word := range [][]byte{[]byte("token"), []byte("secret"), []byte("credential"), []byte("hash")} {
		if bytes.Contains(lower, word) {
			t.Fatalf("settings response leaked sensitive wording: %s", raw)
		}
	}
}
