package api

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func decodedTelegramText(form string) string {
	values, err := url.ParseQuery(form)
	if err != nil {
		return form
	}
	return values.Get("text")
}
