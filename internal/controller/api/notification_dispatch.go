package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type heartbeatTransitionStore interface {
	RecordAgentHeartbeatTransition(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) (notificationStatusTransition, error)
}

type notificationEventStore interface {
	NotificationNode(ctx context.Context, nodeID string) (notificationNodeSnapshot, error)
	EnabledNotificationChannelsForEvent(ctx context.Context, eventType, nodeID string) (string, []notificationDispatchChannel, error)
}

type notificationStatusMarkStore interface {
	ClaimStatusNotification(ctx context.Context, event notificationEvent) (bool, error)
}

type renewalNotificationQueueStore interface {
	QueueDueRenewalNotifications(ctx context.Context, now time.Time) (int, error)
}

type notificationNodeSnapshot struct {
	ID          string
	DisplayName string
	Status      string
	PublicIPv4  string
}

type notificationStatusTransition struct {
	Previous notificationNodeSnapshot
	Current  notificationNodeSnapshot
	Detail   string
}

type notificationDispatchChannel struct {
	ID          string
	Name        string
	Type        string
	Destination string
	Credential  string
}

type notificationEvent struct {
	EventType      string `json:"event_type"`
	Label          string `json:"label"`
	NodeID         string `json:"node_id"`
	NodeName       string `json:"node_name"`
	NodeIP         string `json:"node_ip,omitempty"`
	Status         string `json:"status"`
	PreviousStatus string `json:"previous_status"`
	TS             string `json:"ts"`
	Detail         string `json:"detail,omitempty"`
}

type notificationSender interface {
	Send(ctx context.Context, channel notificationDispatchChannel, event notificationEvent) error
}

type httpNotificationSender struct {
	client             *http.Client
	telegramAPIBaseURL string
}

func newHTTPNotificationSender(client *http.Client, telegramAPIBaseURL string) notificationSender {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	telegramAPIBaseURL = strings.TrimRight(strings.TrimSpace(telegramAPIBaseURL), "/")
	if telegramAPIBaseURL == "" {
		telegramAPIBaseURL = "https://api.telegram.org"
	}
	return httpNotificationSender{client: client, telegramAPIBaseURL: telegramAPIBaseURL}
}

func (sender httpNotificationSender) Send(ctx context.Context, channel notificationDispatchChannel, event notificationEvent) error {
	if channel.Type != "" && strings.ToLower(strings.TrimSpace(channel.Type)) != "telegram" {
		return fmt.Errorf("unsupported notification channel type")
	}
	return sender.sendTelegram(ctx, channel, event)
}

func (sender httpNotificationSender) sendTelegram(ctx context.Context, channel notificationDispatchChannel, event notificationEvent) error {
	botCredential := strings.TrimSpace(channel.Credential)
	chatID := strings.TrimSpace(channel.Destination)
	if botCredential == "" || chatID == "" {
		return fmt.Errorf("missing telegram destination")
	}
	endpoint := sender.telegramAPIBaseURL + "/bot" + url.PathEscape(botCredential) + "/sendMessage"
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", event.messageText())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "Zeno-Controller")
	response, err := sender.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("telegram returned status %d", response.StatusCode)
	}
	return nil
}

func (event notificationEvent) messageText() string {
	nodeName := notificationNodeLabel(event)
	switch event.EventType {
	case "test_notification":
		return "Zeno：通知渠道测试"
	case "node_offline":
		if event.Status == "online" && event.PreviousStatus == "offline" {
			return fmt.Sprintf("🟢[恢复] %s", nodeName)
		}
		return fmt.Sprintf("🔴[离线] %s", nodeName)
	case "probe_unhealthy":
		detail := strings.TrimSpace(event.Detail)
		if event.Status == "online" && event.PreviousStatus == "warning" {
			if detail == "" {
				detail = "状态恢复正常"
			}
			return fmt.Sprintf("🟢[恢复] %s%s", nodeName, detail)
		}
		if detail == "" {
			detail = "状态异常"
		}
		return fmt.Sprintf("⚠️[警告] %s%s", nodeName, detail)
	case "renewal_due":
		detail := strings.TrimSpace(event.Detail)
		if detail != "" {
			return renewalDueMessageText(nodeName, detail)
		}
		return fmt.Sprintf("⚠️[到期] %s 即将到期", nodeName)
	default:
		return fmt.Sprintf("Zeno：%s %s", nodeName, event.Label)
	}
}

func renewalDueMessageText(nodeName, detail string) string {
	parts := strings.Split(strings.TrimSpace(detail), "，")
	statusText := strings.TrimSpace(parts[0])
	dateText := ""
	if len(parts) > 1 {
		dateText = formatRenewalMessageDate(strings.TrimSpace(parts[len(parts)-1]))
	}
	if dateText == "" {
		return fmt.Sprintf("⚠️[到期] %s 即将到期", nodeName)
	}
	if statusText == "今天到期" {
		return fmt.Sprintf("⚠️[到期] %s 今天（%s）到期", nodeName, dateText)
	}
	if strings.HasPrefix(statusText, "还有 ") && strings.HasSuffix(statusText, " 天到期") {
		days := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(statusText, "还有 "), " 天到期"))
		if days != "" {
			return fmt.Sprintf("⚠️[到期] %s 将于 %s 天后（%s）到期", nodeName, days, dateText)
		}
	}
	if strings.HasPrefix(statusText, "已过期 ") && strings.HasSuffix(statusText, " 天") {
		days := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(statusText, "已过期 "), " 天"))
		if days != "" {
			return fmt.Sprintf("⚠️[到期] %s 已于 %s 天前（%s）到期", nodeName, days, dateText)
		}
	}
	return fmt.Sprintf("⚠️[到期] %s 将于 %s 到期", nodeName, dateText)
}

func formatRenewalMessageDate(value string) string {
	date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), time.UTC)
	if err != nil {
		return strings.TrimSpace(value)
	}
	year, month, day := date.Date()
	return fmt.Sprintf("%d-%d-%d", year, int(month), day)
}

func notificationNodeLabel(event notificationEvent) string {
	nodeName := strings.TrimSpace(event.NodeName)
	if nodeName == "" {
		nodeName = event.NodeID
	}
	if maskedIP := maskIPv4(event.NodeIP); maskedIP != "" {
		return fmt.Sprintf("%s(%s)", nodeName, maskedIP)
	}
	return nodeName
}

func maskIPv4(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 4 {
		return ""
	}
	for _, part := range parts {
		if part == "" {
			return ""
		}
	}
	return parts[0] + "." + parts[1] + ".***.***"
}

func (h *handler) dispatchNotificationEvent(store agentStore, event notificationEvent) bool {
	if strings.TrimSpace(event.EventType) == "" || h.notificationSender == nil {
		return false
	}
	notificationStore, ok := store.(notificationEventStore)
	if !ok {
		return false
	}
	label, channels, err := notificationStore.EnabledNotificationChannelsForEvent(context.Background(), event.EventType, event.NodeID)
	if err != nil || len(channels) == 0 {
		return false
	}
	if event.Label == "" {
		event.Label = label
	}
	if outboxStore, ok := store.(notificationOutboxStore); ok {
		queued, err := outboxStore.QueueNotificationEvent(context.Background(), event, channels)
		if err != nil || !queued {
			return false
		}
		// Kick the durable worker immediately; the periodic worker remains the
		// restart-safe fallback and handles backoff retries.
		h.startBackground(func(ctx context.Context) { h.dispatchPendingNotificationDeliveries(ctx) })
		return true
	}
	if shouldClaimStatusNotification(event) {
		if markStore, ok := store.(notificationStatusMarkStore); ok {
			claimed, err := markStore.ClaimStatusNotification(context.Background(), event)
			if err != nil || !claimed {
				return false
			}
		}
	}
	for _, channel := range channels {
		channel := channel
		event := event
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = h.notificationSender.Send(ctx, channel, event)
		}()
	}
	return true
}

func (h *handler) dispatchAgentStatusNotification(store agentStore, transition notificationStatusTransition, ts time.Time) {
	eventType, ok := notificationEventTypeForStatusChange(transition.Previous.Status, transition.Current.Status)
	if !ok || h.notificationSender == nil {
		return
	}
	node := transition.Current
	if node.ID == "" {
		node = transition.Previous
	}
	event := notificationEvent{
		EventType:      eventType,
		NodeID:         node.ID,
		NodeName:       node.DisplayName,
		NodeIP:         node.PublicIPv4,
		Status:         transition.Current.Status,
		PreviousStatus: transition.Previous.Status,
		TS:             ts.UTC().Format(time.RFC3339),
		Detail:         transition.Detail,
	}
	h.dispatchNotificationEvent(store, event)
}

func (h *handler) runRenewalNotificationScanner(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	h.queueDueRenewalNotifications(ctx, time.Now().UTC())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.queueDueRenewalNotifications(ctx, now.UTC())
		}
	}
}

func (h *handler) queueDueRenewalNotifications(ctx context.Context, now time.Time) int {
	store, ok := h.store.(renewalNotificationQueueStore)
	if !ok {
		return 0
	}
	queued, err := store.QueueDueRenewalNotifications(ctx, now)
	if err != nil {
		log.Printf("renewal notification scan failed: %v", err)
		return 0
	}
	if queued > 0 {
		h.dispatchPendingNotificationDeliveries(ctx)
	}
	return queued
}

func shouldClaimStatusNotification(event notificationEvent) bool {
	return strings.TrimSpace(event.NodeID) != "" && strings.TrimSpace(event.Status) != "" && strings.TrimSpace(event.PreviousStatus) != ""
}

func sanitizeNotificationDeliveryError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "delivery failed"
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") {
		return "delivery timed out"
	}
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") || strings.Contains(lower, "network is unreachable") {
		return "delivery connection failed"
	}
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "bearer ") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") || strings.Contains(lower, "/bot") {
		return "delivery failed"
	}
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}

func notificationEventTypeForStatusChange(previousStatus, currentStatus string) (string, bool) {
	previousStatus = strings.TrimSpace(previousStatus)
	currentStatus = strings.TrimSpace(currentStatus)
	if previousStatus == currentStatus {
		return "", false
	}
	switch currentStatus {
	case "online":
		if previousStatus == "offline" {
			return "node_offline", true
		}
		if previousStatus == "warning" {
			return "probe_unhealthy", true
		}
		return "", false
	case "offline":
		return "node_offline", true
	case "warning":
		return "probe_unhealthy", true
	default:
		return "", false
	}
}

func (s *SQLiteStore) NotificationNode(ctx context.Context, nodeID string) (notificationNodeSnapshot, error) {
	var snapshot notificationNodeSnapshot
	var storedStatus string
	var lastSeenAt sql.NullInt64
	var offlineDurationSec sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT n.id, n.display_name, n.status, n.last_seen_at, COALESCE(n.public_ipv4, ''),
		       COALESCE((
		         SELECT MAX(ar.duration_sec)
		         FROM alert_rules ar
		         WHERE ar.notification_event_type = 'node_offline'
		           AND (
		             NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		             OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = n.id)
		           )
		       ), ?) AS offline_duration_sec
		FROM nodes n
		WHERE n.id = ? AND n.disabled = 0
	`, int64(nodeHeartbeatOfflineAfter/time.Second), nodeID).Scan(&snapshot.ID, &snapshot.DisplayName, &storedStatus, &lastSeenAt, &snapshot.PublicIPv4, &offlineDurationSec); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return notificationNodeSnapshot{}, errNodeNotFound
		}
		return notificationNodeSnapshot{}, err
	}
	snapshot.Status = publicNodeStatusAfter(storedStatus, lastSeenAt, time.Now().UTC(), nodeOfflineAfterFromSeconds(offlineDurationSec))
	return snapshot, nil
}

func (s *SQLiteStore) EnabledNotificationChannelsForEvent(ctx context.Context, eventType, nodeID string) (string, []notificationDispatchChannel, error) {
	label, ok := adminNotificationTypeLabel(eventType)
	if !ok {
		return "", nil, errNotificationTypeNotFound
	}
	var enabledRuleCount int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM alert_rules ar
		WHERE ar.notification_event_type = ?
		  AND ar.enabled = 1
		  AND (
		    ? = ''
		    OR NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		    OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ?)
		  )
	`, eventType, strings.TrimSpace(nodeID), strings.TrimSpace(nodeID)).Scan(&enabledRuleCount); err != nil {
		return "", nil, err
	}
	if enabledRuleCount == 0 {
		return label, nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, destination, credential
		FROM notification_channels
		WHERE enabled = 1 AND TRIM(credential) <> ''
		ORDER BY id ASC
	`)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	channels := make([]notificationDispatchChannel, 0)
	for rows.Next() {
		var channel notificationDispatchChannel
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.Destination, &channel.Credential); err != nil {
			return "", nil, err
		}
		channel.Type = "telegram"
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	return label, channels, nil
}

func (s *SQLiteStore) NotificationEventDelay(ctx context.Context, eventType, nodeID string) (time.Duration, bool, error) {
	eventType = strings.TrimSpace(eventType)
	nodeID = strings.TrimSpace(nodeID)
	var durationSec sql.NullInt64
	var matched int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(duration_sec), 0), COUNT(*)
		FROM alert_rules ar
		WHERE ar.notification_event_type = ?
		  AND (
		    ? = ''
		    OR NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		    OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ?)
		  )
	`, eventType, nodeID, nodeID).Scan(&durationSec, &matched); err != nil {
		return 0, false, err
	}
	if matched == 0 {
		return 0, false, nil
	}
	seconds := int64(0)
	if durationSec.Valid && durationSec.Int64 > 0 {
		seconds = durationSec.Int64
	}
	return time.Duration(seconds) * time.Second, true, nil
}

func (s *SQLiteStore) ClaimStatusNotification(ctx context.Context, event notificationEvent) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer rollbackUnlessCommitted(tx)
	claimed, err := claimStatusNotificationTx(ctx, tx, event)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	tx = nil
	return claimed, nil
}

func activeStatusNotificationMark(status string) string {
	status = strings.TrimSpace(status)
	if status == "" || status == "online" {
		return ""
	}
	return "status-active:" + status
}

func recoveredStatusNotificationMark(status string) string {
	status = strings.TrimSpace(status)
	if status == "" || status == "online" {
		return ""
	}
	return "status-recovered:" + status
}
