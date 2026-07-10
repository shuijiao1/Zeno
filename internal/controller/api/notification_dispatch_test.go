package api

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type failingNotificationSender struct{}

func (failingNotificationSender) Send(context.Context, notificationDispatchChannel, notificationEvent) error {
	return errors.New("temporary network failure")
}

func TestNotificationMessageTextUsesMaskedIPv4AndCompactStatusFormat(t *testing.T) {
	cases := []struct {
		name  string
		event notificationEvent
		want  string
	}{
		{
			name:  "offline",
			event: notificationEvent{EventType: "node_offline", NodeName: "Zouter", NodeIP: "203.0.113.9", PreviousStatus: "online", Status: "offline"},
			want:  "🔴[离线] Zouter(203.0.***.***)",
		},
		{
			name:  "offline recovery",
			event: notificationEvent{EventType: "node_offline", NodeName: "Zouter", NodeIP: "203.0.113.9", PreviousStatus: "offline", Status: "online"},
			want:  "🟢[恢复] Zouter(203.0.***.***)",
		},
		{
			name:  "cpu warning",
			event: notificationEvent{EventType: "probe_unhealthy", NodeName: "Zouter", NodeIP: "203.0.113.9", PreviousStatus: "online", Status: "warning", Detail: "CPU持续占用过高"},
			want:  "⚠️[警告] Zouter(203.0.***.***)CPU持续占用过高",
		},
		{
			name:  "cpu recovery",
			event: notificationEvent{EventType: "probe_unhealthy", NodeName: "Zouter", NodeIP: "203.0.113.9", PreviousStatus: "warning", Status: "online", Detail: "CPU恢复正常"},
			want:  "🟢[恢复] Zouter(203.0.***.***)CPU恢复正常",
		},
		{
			name:  "renewal due future",
			event: notificationEvent{EventType: "renewal_due", NodeName: "Sharon", Detail: "还有 1 天到期，2026-07-10"},
			want:  "⚠️[到期] Sharon 将于 1 天后（2026-7-10）到期",
		},
		{
			name:  "renewal due today",
			event: notificationEvent{EventType: "renewal_due", NodeName: "Sharon", Detail: "今天到期，2026-07-10"},
			want:  "⚠️[到期] Sharon 今天（2026-7-10）到期",
		},
		{
			name:  "renewal due expired",
			event: notificationEvent{EventType: "renewal_due", NodeName: "Sharon", Detail: "已过期 2 天，2026-07-10"},
			want:  "⚠️[到期] Sharon 已于 2 天前（2026-7-10）到期",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.messageText(); got != tt.want {
				t.Fatalf("messageText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDispatchAgentStatusNotificationDedupesActiveWarningsUntilRecovery(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops-telegram", Name: "Ops Telegram", Destination: "7579942307", Credential: "telegram-bot-credential-value", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.UpdateAdminNotificationType(ctx, "probe_unhealthy", AdminNotificationTypeUpdateRequest{Enabled: &enabled}); err != nil {
		t.Fatalf("enable notification type: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	h := &handler{store: store, notificationSender: newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL), liveHub: newLiveUpdateHub(), presence: newAgentPresenceManager()}
	warning := notificationStatusTransition{
		Previous: notificationNodeSnapshot{ID: "hytron", DisplayName: "Hytron", Status: "online", PublicIPv4: "203.0.113.9"},
		Current:  notificationNodeSnapshot{ID: "hytron", DisplayName: "Hytron", Status: "warning", PublicIPv4: "203.0.113.9"},
		Detail:   "CPU持续占用过高",
	}
	h.dispatchAgentStatusNotification(store, warning, time.Unix(1783491510, 0))
	h.dispatchAgentStatusNotification(store, warning, time.Unix(1783491513, 0))
	_, forms, errors := telegram.waitForCalls(t, 1)
	if len(errors) != 0 {
		t.Fatalf("telegram errors = %+v", errors)
	}
	if len(forms) != 1 || !strings.Contains(decodedTelegramText(forms[0]), "⚠️[警告]") {
		t.Fatalf("forms after duplicate warning = %+v, want one warning", forms)
	}
	time.Sleep(100 * time.Millisecond)
	_, forms, errors = telegram.waitForCalls(t, 1)
	if len(errors) != 0 || len(forms) != 1 {
		t.Fatalf("forms after duplicate settle = %+v errors=%+v, want still one warning", forms, errors)
	}

	recovery := notificationStatusTransition{
		Previous: notificationNodeSnapshot{ID: "hytron", DisplayName: "Hytron", Status: "warning", PublicIPv4: "203.0.113.9"},
		Current:  notificationNodeSnapshot{ID: "hytron", DisplayName: "Hytron", Status: "online", PublicIPv4: "203.0.113.9"},
		Detail:   "CPU恢复正常",
	}
	h.dispatchAgentStatusNotification(store, recovery, time.Unix(1783491600, 0))
	_, forms, errors = telegram.waitForCalls(t, 2)
	if len(errors) != 0 || len(forms) != 2 || !strings.Contains(decodedTelegramText(forms[1]), "🟢[恢复]") {
		t.Fatalf("forms after recovery = %+v errors=%+v, want one recovery", forms, errors)
	}

	h.dispatchAgentStatusNotification(store, warning, time.Unix(1783491660, 0))
	_, forms, errors = telegram.waitForCalls(t, 3)
	if len(errors) != 0 || len(forms) != 3 || !strings.Contains(decodedTelegramText(forms[2]), "⚠️[警告]") {
		t.Fatalf("forms after new warning cycle = %+v errors=%+v, want warning allowed after recovery", forms, errors)
	}
}

func TestNotificationOutboxPersistsFailureAndRetriesAfterRestart(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	enabled := true
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "7579942307", Credential: "super-secret-bot-token", Enabled: &enabled})
	if err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	event := notificationEvent{EventType: "node_offline", Label: "离线", NodeID: "hytron", NodeName: "Hytron", PreviousStatus: "online", Status: "offline", TS: time.Now().UTC().Format(time.RFC3339)}
	queued, err := store.QueueNotificationEvent(ctx, event, []notificationDispatchChannel{{ID: channel.ID, Name: channel.Name, Destination: channel.Destination, Credential: "super-secret-bot-token", Type: "telegram"}})
	if err != nil || !queued {
		t.Fatalf("queue event = %v, %v", queued, err)
	}

	failing := &handler{store: store, notificationSender: failingNotificationSender{}}
	failing.dispatchPendingNotificationDeliveries(ctx)
	var state, lastError string
	var attempts int
	if err := store.db.QueryRowContext(ctx, `SELECT state, attempts, last_error FROM notification_deliveries ORDER BY id DESC LIMIT 1`).Scan(&state, &attempts, &lastError); err != nil {
		t.Fatalf("read failed delivery: %v", err)
	}
	if state != "pending" || attempts != 1 || lastError == "" || strings.Contains(lastError, "super-secret") {
		t.Fatalf("failed delivery = state %q attempts %d error %q", state, attempts, lastError)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE notification_deliveries SET next_attempt_at = 0`); err != nil {
		t.Fatalf("make delivery retryable: %v", err)
	}

	telegram := newTelegramTestCapture(t)
	restarted := &handler{store: store, notificationSender: newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL)}
	restarted.dispatchPendingNotificationDeliveries(ctx)
	_, forms, captureErrors := telegram.waitForCalls(t, 1)
	if len(captureErrors) != 0 || len(forms) != 1 {
		t.Fatalf("retry calls=%d errors=%v", len(forms), captureErrors)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT state, attempts, last_error FROM notification_deliveries ORDER BY id DESC LIMIT 1`).Scan(&state, &attempts, &lastError); err != nil {
		t.Fatalf("read delivered row: %v", err)
	}
	if state != "delivered" || attempts != 2 || lastError != "" {
		t.Fatalf("delivered row = state %q attempts %d error %q", state, attempts, lastError)
	}
}

func decodedTelegramText(form string) string {
	values, err := url.ParseQuery(form)
	if err != nil {
		return form
	}
	return values.Get("text")
}
