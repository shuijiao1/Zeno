package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildHandlerUsesSQLiteStoreWhenDBPathProvided(t *testing.T) {
	handler, cleanup, err := buildHandler(handlerConfig{DBPath: filepath.Join(t.TempDir(), "zeno.db")})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	defer cleanup()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Nodes []any `json:"nodes"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Nodes) != 0 {
		t.Fatalf("nodes len = %d, want empty sqlite-backed summary instead of mock data", len(body.Nodes))
	}
}

func TestBuildHandlerEnablesAdminAPIWithAdminToken(t *testing.T) {
	handler, cleanup, err := buildHandler(handlerConfig{
		DBPath:      filepath.Join(t.TempDir(), "zeno.db"),
		SeedPreview: true,
		NodeID:      "hytron",
		AgentToken:  "agent-token",
		AdminToken:  "admin-pass",
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	defer cleanup()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBuildHandlerServesConfiguredAgentBinary(t *testing.T) {
	tmp := t.TempDir()
	binaryPath := filepath.Join(tmp, "zeno-agent")
	if err := os.WriteFile(binaryPath, []byte("agent-binary"), 0o755); err != nil {
		t.Fatalf("write agent binary: %v", err)
	}
	handler, cleanup, err := buildHandler(handlerConfig{DBPath: filepath.Join(tmp, "zeno.db"), AgentBinaryPath: binaryPath, AgentVersion: "abc1234"})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	defer cleanup()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/agent/linux-amd64", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "agent-binary" {
		t.Fatalf("agent binary body = %q", recorder.Body.String())
	}
}
