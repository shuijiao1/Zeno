package api

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func allowTestTLSServers(t *testing.T) {
	t.Helper()
	original := http.DefaultTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test servers only
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = original
		transport.CloseIdleConnections()
	})
}

func TestHTTPProbeURLPolicy(t *testing.T) {
	for _, address := range []string{
		"https://example.com/health",
		"http://localhost/health",
		"http://127.0.0.1/health",
		"http://[::1]/health",
		"http://192.0.2.10:8080/health",
		"http://[2001:db8::1]:8080/health",
	} {
		if !validHTTPGetTargetAddress(address) {
			t.Fatalf("safe probe URL rejected: %s", address)
		}
	}
	for _, address := range []string{
		"http://example.com/health",
		"http://example.com:8080/health",
		"http://192.0.2.10/health",
		"https://user:pass@example.com/health",
		"ftp://example.com/file",
	} {
		if validHTTPGetTargetAddress(address) {
			t.Fatalf("unsafe probe URL accepted: %s", address)
		}
	}
}

func TestRunHTTPProbeRejectsMultiHopHTTPSDowngrade(t *testing.T) {
	allowTestTLSServers(t)
	var finalHits atomic.Int64
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		finalHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer final.Close()
	middle := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, final.URL, http.StatusFound)
	}))
	defer middle.Close()
	start := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, middle.URL+"/next", http.StatusFound)
	}))
	defer start.Close()

	samples, err := RunHTTPProbe(context.Background(), ProbeTarget{ID: "secure", Type: "http_get", Address: start.URL + "/start", Count: 1, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("run probe: %v", err)
	}
	if len(samples) != 1 || samples[0].Success || samples[0].Error != "redirect_downgrade" {
		t.Fatalf("downgrade sample=%+v", samples)
	}
	if finalHits.Load() != 0 {
		t.Fatalf("downgraded HTTP endpoint received %d requests", finalHits.Load())
	}
}

func TestRunHTTPProbeCapsRedirects(t *testing.T) {
	var hits atomic.Int64
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		http.Redirect(w, request, server.URL+"/loop", http.StatusFound)
	}))
	defer server.Close()
	samples, err := RunHTTPProbe(context.Background(), ProbeTarget{ID: "loop", Type: "http_get", Address: server.URL + "/loop", Count: 1, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("run probe: %v", err)
	}
	if len(samples) != 1 || samples[0].Success || samples[0].Error != "redirect_limit" {
		t.Fatalf("redirect-loop sample=%+v", samples)
	}
	if hits.Load() > 10 {
		t.Fatalf("redirect loop issued %d requests, want at most 10", hits.Load())
	}
}

func TestRunHTTPProbeCrossOriginRedirectDropsReferer(t *testing.T) {
	allowTestTLSServers(t)
	var referer, authorization, cookie, userAgent string
	final := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		referer = request.Header.Get("Referer")
		authorization = request.Header.Get("Authorization")
		cookie = request.Header.Get("Cookie")
		userAgent = request.Header.Get("User-Agent")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer final.Close()
	start := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, final.URL+"/final", http.StatusFound)
	}))
	defer start.Close()

	samples, err := RunHTTPProbe(context.Background(), ProbeTarget{ID: "headers", Type: "http_get", Address: start.URL + "/start?token=secret", Count: 1, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("run probe: %v", err)
	}
	if len(samples) != 1 || !samples[0].Success {
		t.Fatalf("safe HTTPS redirect sample=%+v", samples)
	}
	if referer != "" || authorization != "" || cookie != "" {
		t.Fatalf("cross-origin sensitive headers leaked: referer=%q authorization=%q cookie=%q", referer, authorization, cookie)
	}
	if userAgent != "Zeno-Controller" {
		t.Fatalf("user agent=%q", userAgent)
	}
}

func TestRunHTTPProbeRejectsUserinfoBeforeDial(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	unsafeURL := strings.Replace(server.URL, "http://", "http://user:pass@", 1)
	if _, err := RunHTTPProbe(context.Background(), ProbeTarget{ID: "userinfo", Type: "http_get", Address: unsafeURL, Count: 1, TimeoutMS: 1000}); err == nil {
		t.Fatal("userinfo probe URL was accepted")
	}
	if hits.Load() != 0 {
		t.Fatalf("userinfo endpoint received %d requests", hits.Load())
	}
}
