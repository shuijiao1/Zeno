package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestBuildHandlerUsesSQLiteStoreWhenDBPathProvided(t *testing.T) {
	handler, cleanup, err := buildHandler(handlerConfig{DBPath: filepath.Join(t.TempDir(), "jiaoprobe.db")})
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
		DBPath:      filepath.Join(t.TempDir(), "jiaoprobe.db"),
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
