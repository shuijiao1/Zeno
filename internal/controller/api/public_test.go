package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
	if len(summary.LatencyPoints) != 0 {
		t.Fatalf("summary latency points len = %d, want 0 because details use dedicated websocket feeds", len(summary.LatencyPoints))
	}
	if len(summary.Services) == 0 || summary.Services[0].Name == "" || summary.Services[0].AssignedNodeCount == 0 {
		t.Fatalf("summary services = %+v, want public monitor service status", summary.Services)
	}

	if strings.Contains(strings.ToLower(raw), "token") || strings.Contains(strings.ToLower(raw), "secret") {
		t.Fatalf("public summary leaked token/secret wording: %s", raw)
	}
}

func TestSummaryWebSocketPublishesAgentStateUpdates(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	server := httptest.NewServer(NewHandler(HandlerOptions{Store: store}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/public/v1/summary/ws"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("open summary websocket: %v", err)
	}
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	defer conn.Close()

	_, initial, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read initial websocket summary: %v", err)
	}
	if !strings.Contains(string(initial), `"nodes"`) {
		t.Fatalf("initial websocket message = %q, want summary payload", string(initial))
	}

	payload := []byte(`{"ts":` + strconv.FormatInt(time.Now().UTC().Unix(), 10) + `,"cpu_percent":12.5,"memory_used_bytes":100,"memory_total_bytes":200,"disk_used_bytes":300,"disk_total_bytes":400,"net_in_total_bytes":1000,"net_out_total_bytes":2000,"net_in_speed_bps":4321,"net_out_speed_bps":8765,"uptime_seconds":60}`)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/agent/v1/state", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new state request: %v", err)
	}
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	stateResponse, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("post agent state: %v", err)
	}
	defer stateResponse.Body.Close()
	if stateResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("state status = %d, want 202; body=%s", stateResponse.StatusCode, readAllString(t, stateResponse.Body))
	}

	_, update, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read updated websocket summary: %v", err)
	}
	if !strings.Contains(string(update), `"net_in_speed_bps":4321`) || !strings.Contains(string(update), `"net_out_speed_bps":8765`) {
		t.Fatalf("websocket update = %q, want latest agent speeds", string(update))
	}
}

func TestNodeStateWebSocketPublishesAgentStateUpdates(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	server := httptest.NewServer(NewHandler(HandlerOptions{Store: store}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/public/v1/nodes/hytron/state/ws?range=1h"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("open node state websocket: %v", err)
	}
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	defer conn.Close()

	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read initial node state websocket message: %v", err)
	}

	payload := []byte(`{"ts":` + strconv.FormatInt(time.Now().UTC().Unix(), 10) + `,"cpu_percent":22.5,"memory_used_bytes":100,"memory_total_bytes":200,"disk_used_bytes":300,"disk_total_bytes":400,"net_in_total_bytes":1000,"net_out_total_bytes":2000,"net_in_speed_bps":2468,"net_out_speed_bps":8642,"uptime_seconds":60}`)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/agent/v1/state", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new state request: %v", err)
	}
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	stateResponse, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("post agent state: %v", err)
	}
	defer stateResponse.Body.Close()
	if stateResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("state status = %d, want 202; body=%s", stateResponse.StatusCode, readAllString(t, stateResponse.Body))
	}

	_, update, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read updated node state websocket message: %v", err)
	}
	if !strings.Contains(string(update), `"node_id":"hytron"`) || !strings.Contains(string(update), `"net_out_speed_bps":8642`) {
		t.Fatalf("node state websocket update = %q, want latest state point", string(update))
	}
}

func TestLatencyWebSocketsPublishAgentProbeResults(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}

	server := httptest.NewServer(NewHandler(HandlerOptions{Store: store}))
	defer server.Close()

	nodeConn, nodeResponse, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/public/v1/nodes/hytron/latency/ws?range=1h", nil)
	if err != nil {
		t.Fatalf("open node latency websocket: %v", err)
	}
	if nodeResponse != nil && nodeResponse.Body != nil {
		defer nodeResponse.Body.Close()
	}
	defer nodeConn.Close()
	serviceConn, serviceResponse, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/public/v1/services/google-dns/latency/ws?range=1h", nil)
	if err != nil {
		t.Fatalf("open service latency websocket: %v", err)
	}
	if serviceResponse != nil && serviceResponse.Body != nil {
		defer serviceResponse.Body.Close()
	}
	defer serviceConn.Close()
	if _, _, err := nodeConn.ReadMessage(); err != nil {
		t.Fatalf("read initial node latency websocket message: %v", err)
	}
	if _, _, err := serviceConn.ReadMessage(); err != nil {
		t.Fatalf("read initial service latency websocket message: %v", err)
	}

	body := map[string]any{
		"rounds": []map[string]any{{
			"target_id": "google-dns",
			"ts":        time.Now().UTC().Unix(),
			"type":      "tcping",
			"samples": []map[string]any{
				{"seq": 1, "success": true, "latency_ms": 10.0},
				{"seq": 2, "success": true, "latency_ms": 30.0},
			},
		}},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal probe results: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/agent/v1/probe-results", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new probe request: %v", err)
	}
	request.Header.Set("X-Node-ID", "hytron")
	request.Header.Set("Authorization", "Bearer test-agent-token")
	request.Header.Set("Content-Type", "application/json")
	probeResponse, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("post probe results: %v", err)
	}
	defer probeResponse.Body.Close()
	if probeResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("probe status = %d, want 202; body=%s", probeResponse.StatusCode, readAllString(t, probeResponse.Body))
	}

	_, nodeUpdate, err := nodeConn.ReadMessage()
	if err != nil {
		t.Fatalf("read node latency update: %v", err)
	}
	if !strings.Contains(string(nodeUpdate), `"target_id":"google-dns"`) || !strings.Contains(string(nodeUpdate), `"median_ms":20`) {
		t.Fatalf("node latency websocket update = %q, want posted probe median", string(nodeUpdate))
	}
	_, serviceUpdate, err := serviceConn.ReadMessage()
	if err != nil {
		t.Fatalf("read service latency update: %v", err)
	}
	if !strings.Contains(string(serviceUpdate), `"node_id":"hytron"`) || !strings.Contains(string(serviceUpdate), `"median_ms":20`) {
		t.Fatalf("service latency websocket update = %q, want posted probe median", string(serviceUpdate))
	}
}

func TestServiceLatencyEndpointReturnsNodeSeries(t *testing.T) {
	handler := NewHandler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/services/google/latency?range=1d", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response ServiceTargetLatencyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode service latency: %v", err)
	}
	if response.Target.ID != "google" || response.Target.Name != "Google" {
		t.Fatalf("target = %+v, want google target", response.Target)
	}
	if response.Range != "1d" || len(response.Points) == 0 {
		t.Fatalf("service latency range/points = %q/%d, want 1d points", response.Range, len(response.Points))
	}
	if response.Points[0].NodeID == "" || response.Points[0].NodeName == "" {
		t.Fatalf("service latency point missing node identity: %+v", response.Points[0])
	}
	if strings.Contains(strings.ToLower(recorder.Body.String()), "token") || strings.Contains(strings.ToLower(recorder.Body.String()), "secret") {
		t.Fatalf("service latency leaked sensitive wording: %s", recorder.Body.String())
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

	if got := len(uniquePointTimes(oneDay.Points)); got != 1440 {
		t.Fatalf("1d timestamps = %d, want 1440 one-minute samples", got)
	}
	if got := len(uniquePointTimes(sevenDays.Points)); got != 336 {
		t.Fatalf("7d timestamps = %d, want 336 thirty-minute samples", got)
	}
	if got := len(uniquePointTimes(thirtyDays.Points)); got != 360 {
		t.Fatalf("30d timestamps = %d, want 360 two-hour samples", got)
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
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<div id=\"root\">Zeno UI</div>"), 0o644); err != nil {
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
	if !strings.Contains(spaRecorder.Body.String(), "Zeno UI") {
		t.Fatalf("spa body = %q, want index.html", spaRecorder.Body.String())
	}
}
