package api

import "testing"

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
