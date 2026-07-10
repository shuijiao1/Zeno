package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildHandlerUsesSQLiteStoreWhenDBPathProvided(t *testing.T) {
	runtime, err := buildController(handlerConfig{DBPath: filepath.Join(t.TempDir(), "zeno.db")})
	if err != nil {
		t.Fatalf("build controller: %v", err)
	}
	defer runtime.Cleanup(context.Background())

	recorder := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/summary", nil))
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
	runtime, err := buildController(handlerConfig{
		DBPath:      filepath.Join(t.TempDir(), "zeno.db"),
		SeedPreview: true,
		NodeID:      "hytron",
		AgentToken:  "agent-token",
		AdminToken:  "admin-pass",
	})
	if err != nil {
		t.Fatalf("build controller: %v", err)
	}
	defer runtime.Cleanup(context.Background())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/nodes", nil)
	request.Header.Set("X-Admin-Token", "admin-pass")
	runtime.Handler.ServeHTTP(recorder, request)
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
	runtime, err := buildController(handlerConfig{DBPath: filepath.Join(tmp, "zeno.db"), AgentBinaryPath: binaryPath, AgentVersion: "abc1234"})
	if err != nil {
		t.Fatalf("build controller: %v", err)
	}
	defer runtime.Cleanup(context.Background())

	recorder := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/v1/agent/linux-amd64", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "agent-binary" {
		t.Fatalf("agent binary body = %q", recorder.Body.String())
	}
}
