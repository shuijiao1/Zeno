package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

var onePixelPNG = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89}

func TestAdminAssetUploadServeAndDelete(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	unauthorized := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/assets", bytes.NewBufferString(`{}`))
	handler.ServeHTTP(unauthorized, unauthorizedRequest)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", unauthorized.Code)
	}

	payload := AdminAssetUploadRequest{Filename: "../logo.png", ContentType: "image/png", DataBase64: base64.StdEncoding.EncodeToString(onePixelPNG)}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	upload := httptest.NewRecorder()
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/assets", bytes.NewReader(body))
	uploadRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(upload, uploadRequest)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201; body=%s", upload.Code, upload.Body.String())
	}
	assertNoSensitiveAdminProbeTargetLeak(t, upload.Body.String())
	var response AdminAssetResponse
	if err := json.NewDecoder(bytes.NewBufferString(upload.Body.String())).Decode(&response); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if response.Asset.ID == "" || response.Asset.Filename != "logo.png" || response.Asset.ContentType != "image/png" || response.Asset.SizeBytes != int64(len(onePixelPNG)) || !strings.HasPrefix(response.Asset.URL, "/api/public/v1/assets/") {
		t.Fatalf("asset response = %+v", response.Asset)
	}

	settingsPatch := httptest.NewRecorder()
	settingsRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/settings", bytes.NewBufferString(`{"logo_url":"`+response.Asset.URL+`"}`))
	settingsRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(settingsPatch, settingsRequest)
	if settingsPatch.Code != http.StatusOK {
		t.Fatalf("settings patch status = %d, want 200; body=%s", settingsPatch.Code, settingsPatch.Body.String())
	}

	served := httptest.NewRecorder()
	servedRequest := httptest.NewRequest(http.MethodGet, response.Asset.URL, nil)
	handler.ServeHTTP(served, servedRequest)
	if served.Code != http.StatusOK {
		t.Fatalf("asset get status = %d, want 200; body=%s", served.Code, served.Body.String())
	}
	if served.Header().Get("Content-Type") != "image/png" || !bytes.Equal(served.Body.Bytes(), onePixelPNG) {
		t.Fatalf("served asset content-type=%q bytes=%x", served.Header().Get("Content-Type"), served.Body.Bytes())
	}

	deleteRecorder := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/admin/v1/assets/"+response.Asset.ID, nil)
	deleteRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, servedRequest)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("asset after delete status = %d, want 404", missing.Code)
	}
}

func TestAdminAssetUploadRejectsNonImagesAndOversize(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	handler := NewHandler(HandlerOptions{Store: store, AdminTokenHash: HashAdminToken("admin-pass")})

	cases := []struct {
		name string
		body string
	}{
		{name: "text", body: `{"filename":"note.txt","content_type":"text/plain","data_base64":"` + base64.StdEncoding.EncodeToString([]byte("hello")) + `"}`},
		{name: "oversize", body: `{"filename":"big.png","content_type":"image/png","data_base64":"` + base64.StdEncoding.EncodeToString(append(onePixelPNG, bytes.Repeat([]byte{0}, maxAdminAssetBytes)...)) + `"}`},
		{name: "mismatched content type", body: `{"filename":"logo.png","content_type":"image/jpeg","data_base64":"` + base64.StdEncoding.EncodeToString(onePixelPNG) + `"}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/assets", bytes.NewBufferString(tt.body))
			request.Header.Set("X-Admin-Token", "admin-pass")
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
