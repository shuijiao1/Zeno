package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type heartbeatTransitionStore interface {
	RecordAgentHeartbeatTransition(ctx context.Context, nodeID string, ts time.Time, status, agentVersion string) (notificationStatusTransition, error)
}

type probeHealthTransitionStore interface {
	RecordAgentProbeHealthTransition(ctx context.Context, nodeID string, ts time.Time, status string) (notificationStatusTransition, error)
}

type notificationEventStore interface {
	NotificationNode(ctx context.Context, nodeID string) (notificationNodeSnapshot, error)
	EnabledNotificationChannelsForEvent(ctx context.Context, eventType string) (string, []notificationDispatchChannel, error)
}

type notificationDeliveryStore interface {
	RecordNotificationDelivery(ctx context.Context, event notificationEvent, channel notificationDispatchChannel, success bool, deliveryError string) (AdminNotificationDelivery, error)
}

type notificationNodeSnapshot struct {
	ID          string
	DisplayName string
	Status      string
}

type notificationStatusTransition struct {
	Previous notificationNodeSnapshot
	Current  notificationNodeSnapshot
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
	Status         string `json:"status"`
	PreviousStatus string `json:"previous_status"`
	TS             string `json:"ts"`
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
	switch strings.ToLower(strings.TrimSpace(channel.Type)) {
	case "webhook":
		return sender.sendWebhook(ctx, channel, event)
	case "telegram":
		return sender.sendTelegram(ctx, channel, event)
	default:
		return fmt.Errorf("unsupported notification channel type %q", channel.Type)
	}
}

func (sender httpNotificationSender) sendWebhook(ctx context.Context, channel notificationDispatchChannel, event notificationEvent) error {
	endpoint := strings.TrimSpace(channel.Destination)
	credential := strings.TrimSpace(channel.Credential)
	authorization := ""
	if isHTTPURL(credential) && !isHTTPURL(endpoint) {
		endpoint = credential
	} else if credential != "" {
		authorization = "Bearer " + credential
	}
	if !isHTTPURL(endpoint) {
		return fmt.Errorf("invalid webhook destination")
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Zeno-Controller")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := sender.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", response.StatusCode)
	}
	return nil
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

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func (event notificationEvent) messageText() string {
	nodeName := strings.TrimSpace(event.NodeName)
	if nodeName == "" {
		nodeName = event.NodeID
	}
	switch event.EventType {
	case "test_notification":
		return "Zeno：通知渠道测试"
	case "node_online":
		return fmt.Sprintf("Zeno：%s 已上线", nodeName)
	case "node_offline":
		return fmt.Sprintf("Zeno：%s 已离线", nodeName)
	case "probe_unhealthy":
		return fmt.Sprintf("Zeno：%s 状态异常", nodeName)
	default:
		return fmt.Sprintf("Zeno：%s %s", nodeName, event.Label)
	}
}

func (h *handler) dispatchAgentStatusNotification(store agentStore, transition notificationStatusTransition, ts time.Time) {
	eventType, ok := notificationEventTypeForStatusChange(transition.Previous.Status, transition.Current.Status)
	if !ok || h.notificationSender == nil {
		return
	}
	notificationStore, ok := store.(notificationEventStore)
	if !ok {
		return
	}
	label, channels, err := notificationStore.EnabledNotificationChannelsForEvent(context.Background(), eventType)
	if err != nil || len(channels) == 0 {
		return
	}
	node := transition.Current
	if node.ID == "" {
		node = transition.Previous
	}
	event := notificationEvent{
		EventType:      eventType,
		Label:          label,
		NodeID:         node.ID,
		NodeName:       node.DisplayName,
		Status:         transition.Current.Status,
		PreviousStatus: transition.Previous.Status,
		TS:             ts.UTC().Format(time.RFC3339),
	}
	for _, channel := range channels {
		channel := channel
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := h.notificationSender.Send(ctx, channel, event)
			if deliveryStore, ok := store.(notificationDeliveryStore); ok {
				_, _ = deliveryStore.RecordNotificationDelivery(context.Background(), event, channel, err == nil, sanitizeNotificationDeliveryError(err))
			}
		}()
	}
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
		return "node_online", true
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
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, display_name, status, last_seen_at
		FROM nodes
		WHERE id = ? AND disabled = 0
	`, nodeID).Scan(&snapshot.ID, &snapshot.DisplayName, &storedStatus, &lastSeenAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return notificationNodeSnapshot{}, errNodeNotFound
		}
		return notificationNodeSnapshot{}, err
	}
	snapshot.Status = publicNodeStatus(storedStatus, lastSeenAt, time.Now().UTC())
	return snapshot, nil
}

func (s *SQLiteStore) EnabledNotificationChannelsForEvent(ctx context.Context, eventType string) (string, []notificationDispatchChannel, error) {
	label, ok := adminNotificationTypeLabel(eventType)
	if !ok {
		return "", nil, errNotificationTypeNotFound
	}
	var enabledRuleCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_rules WHERE notification_event_type = ? AND enabled = 1`, eventType).Scan(&enabledRuleCount); err != nil {
		return "", nil, err
	}
	if enabledRuleCount == 0 {
		return label, nil, nil
	}
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT enabled FROM notification_types WHERE event_type = ?`, eventType).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return label, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if enabled == 0 {
		return label, nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, type, destination, credential
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
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.Type, &channel.Destination, &channel.Credential); err != nil {
			return "", nil, err
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	return label, channels, nil
}
