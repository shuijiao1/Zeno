package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestAdminCanRetryOneFailedNotificationDelivery(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	enabled := true
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID: "ops", Name: "Ops", Destination: "chat", Credential: "credential", Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	dispatch := notificationDispatchChannel{ID: channel.ID, Name: channel.Name, Type: "telegram", Destination: channel.Destination}
	for _, ts := range []string{"2026-07-17T01:00:00Z", "2026-07-17T01:00:01Z"} {
		if err := insertNotificationDeliveriesTxForTest(ctx, store, notificationEvent{EventType: "test_notification", TS: ts}, dispatch); err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET state = 'failed', attempts = ?, next_attempt_at = ?, last_error = 'Telegram unavailable'
	`, notificationDeliveryMaxAttempts, time.Now().UTC().Add(notificationDeliveryLongRetryDelay).Unix()); err != nil {
		t.Fatalf("mark deliveries failed: %v", err)
	}
	var firstID, secondID int64
	rows, err := store.db.QueryContext(ctx, `SELECT id FROM notification_deliveries ORDER BY id`)
	if err != nil {
		t.Fatalf("list delivery ids: %v", err)
	}
	if !rows.Next() || rows.Scan(&firstID) != nil || !rows.Next() || rows.Scan(&secondID) != nil {
		rows.Close()
		t.Fatal("read delivery ids")
	}
	rows.Close()

	handler := NewHandler(HandlerOptions{
		Store: store, AdminTokenHash: HashAdminToken("admin-pass"), DisableNotifications: true,
	})

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-deliveries/"+formatInt64(firstID)+"/retry", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d, want 401; body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	retry := httptest.NewRecorder()
	retryRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-deliveries/"+formatInt64(firstID)+"/retry", nil)
	retryRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(retry, retryRequest)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status=%d, want 200; body=%s", retry.Code, retry.Body.String())
	}
	var response AdminNotificationRetryResponse
	if err := json.NewDecoder(retry.Body).Decode(&response); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if response.DeliveryID != firstID || response.State != "pending" {
		t.Fatalf("retry response=%+v", response)
	}

	var firstState, firstError, secondState string
	var firstAttempts int
	if err := store.db.QueryRowContext(ctx, `SELECT state, attempts, last_error FROM notification_deliveries WHERE id = ?`, firstID).Scan(&firstState, &firstAttempts, &firstError); err != nil {
		t.Fatalf("read retried delivery: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT state FROM notification_deliveries WHERE id = ?`, secondID).Scan(&secondState); err != nil {
		t.Fatalf("read untouched delivery: %v", err)
	}
	if firstState != "pending" || firstAttempts != notificationDeliveryMaxAttempts || firstError != "" || secondState != "failed" {
		t.Fatalf("states after single retry: first=%q attempts=%d error=%q second=%q", firstState, firstAttempts, firstError, secondState)
	}

	retryAgain := httptest.NewRecorder()
	retryAgainRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-deliveries/"+formatInt64(firstID)+"/retry", nil)
	retryAgainRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(retryAgain, retryAgainRequest)
	if retryAgain.Code != http.StatusConflict {
		t.Fatalf("retry non-failed status=%d, want 409; body=%s", retryAgain.Code, retryAgain.Body.String())
	}

	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodPost, "/api/admin/v1/notification-deliveries/999999/retry", nil)
	missingRequest.Header.Set("X-Admin-Token", "admin-pass")
	handler.ServeHTTP(missing, missingRequest)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("retry missing status=%d, want 404; body=%s", missing.Code, missing.Body.String())
	}
}

func formatInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
