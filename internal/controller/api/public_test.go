package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHealthEndpoint(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var body map[string]bool
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if !body["ok"] {
		t.Fatalf("health ok = false, want true")
	}
}

func TestSummaryEndpointReturnsMockHomeCardsWithoutSecrets(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	raw := recorder.Body.String()
	var summary SummaryResponse
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if len(summary.Nodes) != 11 {
		t.Fatalf("nodes len = %d, want 11 Kulin-style mock cards", len(summary.Nodes))
	}
	if summary.Nodes[0].DisplayName != "Mechrevo" {
		t.Fatalf("first node = %q, want Mechrevo", summary.Nodes[0].DisplayName)
	}
	if summary.Nodes[0].OS != "windows" {
		t.Fatalf("first node os = %q, want windows", summary.Nodes[0].OS)
	}
	if summary.Nodes[0].CPUCores == nil || *summary.Nodes[0].CPUCores != 16 {
		t.Fatalf("first node cpu cores = %v, want 16", summary.Nodes[0].CPUCores)
	}
	if summary.Nodes[0].CPUModel == "" || summary.Nodes[0].Virtualization == "" {
		t.Fatalf("first node details = cpu_model %q virtualization %q, want public host details", summary.Nodes[0].CPUModel, summary.Nodes[0].Virtualization)
	}
	if summary.Nodes[0].LatencySummary != nil {
		t.Fatalf("first node should omit latency summary like the reference homepage")
	}
	if summary.Nodes[0].ExpiryLabel == "" {
		t.Fatalf("first node should include a Kulin-style expiry label")
	}
	if len(summary.LatencyPoints) == 0 {
		t.Fatal("summary should include latency points for the mock chart")
	}

	if strings.Contains(strings.ToLower(raw), "token") || strings.Contains(strings.ToLower(raw), "secret") {
		t.Fatalf("public summary leaked token/secret wording: %s", raw)
	}
}

func TestNodeLatencyEndpointReturnsKulinStyleMonitorTargets(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/sharon/latency?range=1d", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response LatencyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode latency response: %v", err)
	}

	seen := map[string]bool{}
	for _, point := range response.Points {
		seen[point.TargetName] = true
	}
	wantNames := []string{"重庆联通", "重庆移动", "重庆电信", "DC5", "Google", "DC2", "DC1", "Akari TW", "Akari JP", "Akari HK", "Hytron", "HostDZire", "BAGE"}
	for _, name := range wantNames {
		if !seen[name] {
			t.Fatalf("missing Kulin-style monitor target %q; saw=%v", name, seen)
		}
	}
	if len(seen) != len(wantNames) {
		t.Fatalf("target count = %d, want %d; saw=%v", len(seen), len(wantNames), seen)
	}
}

func TestNodeLatencyEndpointUsesRangeSpecificWindows(t *testing.T) {
	oneDay := requestLatency(t, "/api/public/v1/nodes/sharon/latency?range=1d")
	sevenDays := requestLatency(t, "/api/public/v1/nodes/sharon/latency?range=7d")
	thirtyDays := requestLatency(t, "/api/public/v1/nodes/sharon/latency?range=30d")

	if got := len(uniquePointTimes(oneDay.Points)); got != 48 {
		t.Fatalf("1d timestamps = %d, want 48 half-hour samples", got)
	}
	if got := len(uniquePointTimes(sevenDays.Points)); got != 56 {
		t.Fatalf("7d timestamps = %d, want 56 three-hour samples", got)
	}
	if got := len(uniquePointTimes(thirtyDays.Points)); got != 60 {
		t.Fatalf("30d timestamps = %d, want 60 twelve-hour samples", got)
	}

	if pointSpan(t, oneDay.Points) < 23*time.Hour {
		t.Fatalf("1d window span = %s, want roughly one day", pointSpan(t, oneDay.Points))
	}
	if pointSpan(t, sevenDays.Points) < 6*24*time.Hour {
		t.Fatalf("7d window span = %s, want roughly seven days", pointSpan(t, sevenDays.Points))
	}
	if pointSpan(t, thirtyDays.Points) < 29*24*time.Hour {
		t.Fatalf("30d window span = %s, want roughly thirty days", pointSpan(t, thirtyDays.Points))
	}
}

func TestNodeLatencyEndpointPreservesLossOnlyPointsAsNullLatency(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/nodes/hytron/latency?range=1h", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response LatencyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode latency response: %v", err)
	}
	if response.NodeID != "hytron" {
		t.Fatalf("node_id = %q, want hytron", response.NodeID)
	}
	if response.Range != "1h" {
		t.Fatalf("range = %q, want 1h", response.Range)
	}

	var sawLossOnly bool
	for _, point := range response.Points {
		if point.TargetID == "telegram-dc1" && point.LossPercent == 100 {
			sawLossOnly = true
			if point.MedianMS != nil {
				t.Fatalf("100%% loss point median = %v, want nil", *point.MedianMS)
			}
		}
	}
	if !sawLossOnly {
		t.Fatal("expected at least one telegram-dc1 100% loss point")
	}
}

func requestLatency(t *testing.T, path string) LatencyResponse {
	t.Helper()
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response LatencyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode latency response: %v", err)
	}
	return response
}

func uniquePointTimes(points []LatencyPoint) map[string]bool {
	times := map[string]bool{}
	for _, point := range points {
		times[point.TS] = true
	}
	return times
}

func pointSpan(t *testing.T, points []LatencyPoint) time.Duration {
	t.Helper()
	if len(points) == 0 {
		return 0
	}
	var first, last time.Time
	for index, point := range points {
		ts, err := time.Parse(time.RFC3339, point.TS)
		if err != nil {
			t.Fatalf("parse point timestamp %q: %v", point.TS, err)
		}
		if index == 0 || ts.Before(first) {
			first = ts
		}
		if index == 0 || ts.After(last) {
			last = ts
		}
	}
	return last.Sub(first)
}

func TestStaticWebFallbackServesIndexForDashboardRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<div id=\"root\">JiaoProbe UI</div>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log('asset')"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	handler := NewHandler(HandlerOptions{StaticDir: dir})

	assetRecorder := httptest.NewRecorder()
	handler.ServeHTTP(assetRecorder, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if assetRecorder.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want %d", assetRecorder.Code, http.StatusOK)
	}
	if !strings.Contains(assetRecorder.Body.String(), "asset") {
		t.Fatalf("asset body = %q, want asset content", assetRecorder.Body.String())
	}

	spaRecorder := httptest.NewRecorder()
	handler.ServeHTTP(spaRecorder, httptest.NewRequest(http.MethodGet, "/nodes/hytron", nil))
	if spaRecorder.Code != http.StatusOK {
		t.Fatalf("spa status = %d, want %d", spaRecorder.Code, http.StatusOK)
	}
	if !strings.Contains(spaRecorder.Body.String(), "JiaoProbe UI") {
		t.Fatalf("spa body = %q, want index.html", spaRecorder.Body.String())
	}
}
