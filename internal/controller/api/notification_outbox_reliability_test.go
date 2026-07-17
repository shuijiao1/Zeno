package api

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shuijiao1/zeno/internal/shared/probe"
)

func TestStatusTransitionsQueueOutboxInSameStoreTransaction(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "7579942307", Credential: "super-…oken", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	staleAt := time.Now().UTC().Add(-time.Minute)
	if _, err := store.db.ExecContext(ctx, `UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = 'hytron'`, staleAt.Unix()); err != nil {
		t.Fatalf("make node stale: %v", err)
	}
	transition, changed, err := store.RecordStaleAgentOfflineTransition(ctx, "hytron", time.Now().UTC())
	if err != nil || !changed || transition.Current.Status != "offline" {
		t.Fatalf("record stale transition: changed=%v transition=%+v err=%v", changed, transition, err)
	}
	var offlineDeliveries int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_deliveries WHERE node_id = 'hytron' AND previous_status = 'online' AND status = 'offline'`).Scan(&offlineDeliveries); err != nil {
		t.Fatalf("count atomic offline delivery: %v", err)
	}
	if offlineDeliveries != 1 {
		t.Fatalf("atomic offline deliveries=%d, want 1", offlineDeliveries)
	}

	recovery, err := store.RecordAgentHeartbeatTransition(ctx, "hytron", time.Now().UTC(), "online", "v-test")
	if err != nil || recovery.Previous.Status != "offline" || recovery.Current.Status != "online" {
		t.Fatalf("record heartbeat recovery: transition=%+v err=%v", recovery, err)
	}
	var recoveryDeliveries int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_deliveries WHERE node_id = 'hytron' AND previous_status = 'offline' AND status = 'online'`).Scan(&recoveryDeliveries); err != nil {
		t.Fatalf("count atomic recovery delivery: %v", err)
	}
	if recoveryDeliveries != 1 {
		t.Fatalf("atomic recovery deliveries=%d, want 1", recoveryDeliveries)
	}
}

func TestOutboxDrainDoesNotSynthesizeDeliveriesFromHistoricalMarks(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "7579942307", Credential: "super-…oken", Enabled: &enabled}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO notification_event_marks (event_type, node_id, mark, created_at) VALUES ('node_offline', 'hytron', 'status-recovered:offline', ?)`, time.Now().Add(-24*time.Hour).Unix()); err != nil {
		t.Fatalf("insert historical mark: %v", err)
	}
	telegram := newTelegramTestCapture(t)
	h := &handler{store: store, notificationSender: newHTTPNotificationSender(telegram.server.Client(), telegram.server.URL)}
	h.dispatchPendingNotificationDeliveries(ctx)
	telegram.mu.Lock()
	calls := len(telegram.paths)
	telegram.mu.Unlock()
	if calls != 0 {
		t.Fatalf("historical mark generated %d Telegram calls, want 0", calls)
	}
	var deliveries int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_deliveries`).Scan(&deliveries); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if deliveries != 0 {
		t.Fatalf("historical mark generated %d deliveries, want 0", deliveries)
	}
}

func TestNotificationDeliveryAckFailureKeepsPendingAndMaySendAgain(t *testing.T) {
	store := &ackFailureOutboxStore{delivery: queuedNotificationDelivery{ID: 42, Event: notificationEvent{EventType: "node_offline", NodeID: "hytron", PreviousStatus: "online", Status: "offline"}, Channel: notificationDispatchChannel{ID: "ops", Type: "telegram", Destination: "chat", Credential: "credential"}}}
	sender := &countingNotificationSender{}
	h := &handler{store: store, notificationSender: sender}

	h.dispatchPendingNotificationDeliveries(context.Background())
	if sender.count() != 1 || store.delivered {
		t.Fatalf("after ack failure sends=%d delivered=%v, want one send and still pending", sender.count(), store.delivered)
	}
	h.dispatchPendingNotificationDeliveries(context.Background())
	if sender.count() != 2 || !store.delivered {
		t.Fatalf("after retry sends=%d delivered=%v, want duplicate send and delivered", sender.count(), store.delivered)
	}
}

func TestNotificationOutboxClaimsLeaseAndRecoversExpiredLease(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	enabled := true
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "7579942307", Credential: "credential", Enabled: &enabled})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	event := notificationEvent{EventType: "node_offline", Label: "离线", NodeID: "hytron", NodeName: "Hytron", PreviousStatus: "online", Status: "offline"}
	queued, err := store.QueueNotificationEvent(ctx, event, []notificationDispatchChannel{{ID: channel.ID, Name: channel.Name, Type: "telegram", Destination: channel.Destination, Credential: "credential"}})
	if err != nil || !queued {
		t.Fatalf("queue event queued=%v err=%v", queued, err)
	}
	now := time.Now().UTC()
	first, err := store.PendingNotificationDeliveries(ctx, now, 10)
	if err != nil {
		t.Fatalf("claim first: %v", err)
	}
	if len(first) != 1 || first[0].ClaimToken == "" {
		t.Fatalf("first claim = %+v, want one leased delivery with claim token", first)
	}
	second, err := store.PendingNotificationDeliveries(ctx, now, 10)
	if err != nil {
		t.Fatalf("claim second: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second claim before lease expiry = %+v, want none", second)
	}
	third, err := store.PendingNotificationDeliveries(ctx, now.Add(notificationDeliveryLease+time.Second), 10)
	if err != nil {
		t.Fatalf("claim after lease expiry: %v", err)
	}
	if len(third) != 1 || third[0].ID != first[0].ID || third[0].ClaimToken == first[0].ClaimToken {
		t.Fatalf("expired lease claim = %+v, want same delivery with new token", third)
	}
}

func TestNotificationOutboxPausesDisabledChannelsAndResumesWithoutRetryBurn(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	disabled := false
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "7579942307", Credential: "credential", Enabled: &disabled})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := insertNotificationDeliveriesTxForTest(ctx, store, notificationEvent{EventType: "node_offline", Label: "离线", NodeID: "hytron", NodeName: "Hytron", PreviousStatus: "online", Status: "offline"}, notificationDispatchChannel{ID: channel.ID, Name: channel.Name, Type: "telegram", Destination: channel.Destination, Credential: "credential"}); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}
	claimed, err := store.PendingNotificationDeliveries(ctx, time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("claim disabled: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed disabled delivery = %+v, want none", claimed)
	}
	var state string
	var attempts int
	if err := store.db.QueryRowContext(ctx, `SELECT state, attempts FROM notification_deliveries WHERE channel_id = 'ops'`).Scan(&state, &attempts); err != nil {
		t.Fatalf("query paused delivery: %v", err)
	}
	if state != "paused" || attempts != 0 {
		t.Fatalf("disabled delivery state=%q attempts=%d, want paused/0", state, attempts)
	}
	reenabled := true
	if _, err := store.UpdateAdminNotificationChannel(ctx, "ops", AdminNotificationChannelUpdateRequest{Enabled: &reenabled}); err != nil {
		t.Fatalf("enable channel: %v", err)
	}
	claimed, err = store.PendingNotificationDeliveries(ctx, time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("claim resumed: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("resumed claim = %+v, want one delivery", claimed)
	}
}

func TestNotificationOutboxFailedDeliveryKeepsLowFrequencyAutomaticRetry(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "hytron", DisplayName: "Hytron", CountryCode: "HK", AgentToken: "test-agent-token"}); err != nil {
		t.Fatalf("seed preview data: %v", err)
	}
	enabled := true
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "7579942307", Credential: "credential", Enabled: &enabled})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := insertNotificationDeliveriesTxForTest(ctx, store, notificationEvent{EventType: "node_offline", Label: "离线", NodeID: "hytron", NodeName: "Hytron", PreviousStatus: "online", Status: "offline"}, notificationDispatchChannel{ID: channel.ID, Name: channel.Name, Type: "telegram", Destination: channel.Destination, Credential: "credential"}); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claimed, err := store.PendingNotificationDeliveries(ctx, now, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim initial delivery: %+v err=%v", claimed, err)
	}
	claimed[0].Attempts = notificationDeliveryMaxAttempts - 1
	if err := store.RecordNotificationDeliveryAttempt(ctx, claimed[0], errors.New("Telegram unavailable"), now); err != nil {
		t.Fatalf("record exhausted attempt: %v", err)
	}
	var state string
	var attempts int
	var nextAttemptAt int64
	if err := store.db.QueryRowContext(ctx, `SELECT state, attempts, next_attempt_at FROM notification_deliveries`).Scan(&state, &attempts, &nextAttemptAt); err != nil {
		t.Fatalf("read exhausted delivery: %v", err)
	}
	if state != "failed" || attempts != notificationDeliveryMaxAttempts || nextAttemptAt != now.Add(notificationDeliveryLongRetryDelay).Unix() {
		t.Fatalf("exhausted delivery state=%q attempts=%d next=%d, want failed/%d/%d", state, attempts, nextAttemptAt, notificationDeliveryMaxAttempts, now.Add(notificationDeliveryLongRetryDelay).Unix())
	}
	tooEarly, err := store.PendingNotificationDeliveries(ctx, now.Add(notificationDeliveryLongRetryDelay-time.Second), 1)
	if err != nil || len(tooEarly) != 0 {
		t.Fatalf("claim before long retry: %+v err=%v", tooEarly, err)
	}
	claimed, err = store.PendingNotificationDeliveries(ctx, now.Add(notificationDeliveryLongRetryDelay), 1)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != notificationDeliveryMaxAttempts {
		t.Fatalf("automatic long retry claim = %+v err=%v", claimed, err)
	}
	if err := store.RecordNotificationDeliveryAttempt(ctx, claimed[0], nil, now.Add(notificationDeliveryLongRetryDelay)); err != nil {
		t.Fatalf("record long retry success: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT state, attempts FROM notification_deliveries`).Scan(&state, &attempts); err != nil {
		t.Fatalf("read delivered retry: %v", err)
	}
	if state != "delivered" || attempts != notificationDeliveryMaxAttempts+1 {
		t.Fatalf("long retry result state=%q attempts=%d, want delivered/%d", state, attempts, notificationDeliveryMaxAttempts+1)
	}
}

func insertNotificationDeliveriesTxForTest(ctx context.Context, store *SQLiteStore, event notificationEvent, channel notificationDispatchChannel) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if err := insertNotificationDeliveriesTx(ctx, tx, event, []notificationDispatchChannel{channel}, time.Now().UTC().Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func TestNotificationOutboxWakeUsesSingleBoundedWorker(t *testing.T) {
	store := &concurrentOutboxStore{}
	sender := &countingNotificationSender{}
	h := &handler{store: store, notificationSender: sender}
	if _, ok := any(store).(notificationEventStore); !ok {
		t.Fatalf("fake store does not implement notificationEventStore")
	}
	if _, ok := any(store).(notificationOutboxStore); !ok {
		t.Fatalf("fake store does not implement notificationOutboxStore")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Cleanup(ctx)
	}()

	const events = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < events; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h.dispatchNotificationEvent(store, notificationEvent{EventType: "node_offline", NodeID: "hytron", NodeName: "Hytron", PreviousStatus: "online", Status: "offline"})
		}()
	}
	close(start)
	wg.Wait()
	waitUntil(t, time.Second, func() bool { return sender.count() >= events })
	if sender.count() != events {
		store.mu.Lock()
		pending := len(store.pending)
		nextID := store.nextID
		store.mu.Unlock()
		t.Fatalf("sent %d notifications, want %d (queued=%d pending=%d)", sender.count(), events, nextID, pending)
	}
	if max := store.maxPendingConcurrent(); max > 1 {
		t.Fatalf("pending drains overlapped: max concurrency %d, want 1", max)
	}
}

type countingNotificationSender struct {
	mu    sync.Mutex
	sends int
}

func (sender *countingNotificationSender) Send(context.Context, notificationDispatchChannel, notificationEvent) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.sends++
	return nil
}

func (sender *countingNotificationSender) count() int {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return sender.sends
}

type ackFailureOutboxStore struct {
	mockStore
	delivery  queuedNotificationDelivery
	calls     int
	delivered bool
}

func (store *ackFailureOutboxStore) QueueNotificationEvent(context.Context, notificationEvent, []notificationDispatchChannel) (bool, error) {
	return false, nil
}

func (store *ackFailureOutboxStore) PendingNotificationDeliveries(context.Context, time.Time, int) ([]queuedNotificationDelivery, error) {
	if store.delivered {
		return nil, nil
	}
	return []queuedNotificationDelivery{store.delivery}, nil
}

func (store *ackFailureOutboxStore) RecordNotificationDeliveryAttempt(context.Context, queuedNotificationDelivery, error, time.Time) error {
	store.calls++
	if store.calls == 1 {
		return errors.New("ack write failed")
	}
	store.delivered = true
	return nil
}

type concurrentOutboxStore struct {
	mockStore
	mu            sync.Mutex
	pending       []queuedNotificationDelivery
	nextID        int64
	activePending int
	maxActive     int
}

func (store *concurrentOutboxStore) AuthorizeAgent(context.Context, string, string) (bool, error) {
	return true, nil
}
func (store *concurrentOutboxStore) EnabledProbeTargets(context.Context, string) ([]ProbeTarget, error) {
	return nil, nil
}
func (store *concurrentOutboxStore) InsertProbeRound(context.Context, string, ProbeTarget, time.Time, []probe.Sample) error {
	return nil
}
func (store *concurrentOutboxStore) RecordAgentHeartbeat(context.Context, string, time.Time, string, string) error {
	return nil
}
func (store *concurrentOutboxStore) UpsertAgentHost(context.Context, string, AgentHostRequest) error {
	return nil
}
func (store *concurrentOutboxStore) InsertAgentState(context.Context, string, AgentStateRequest) error {
	return nil
}

func (store *concurrentOutboxStore) EnabledNotificationChannelsForEvent(context.Context, string, string) (string, []notificationDispatchChannel, error) {
	return "离线", []notificationDispatchChannel{{ID: "ops", Type: "telegram", Destination: "chat", Credential: "credential"}}, nil
}

func (store *concurrentOutboxStore) NotificationNode(context.Context, string) (notificationNodeSnapshot, error) {
	return notificationNodeSnapshot{ID: "hytron", DisplayName: "Hytron", Status: "online"}, nil
}

func (store *concurrentOutboxStore) QueueNotificationEvent(_ context.Context, event notificationEvent, channels []notificationDispatchChannel) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, channel := range channels {
		store.nextID++
		store.pending = append(store.pending, queuedNotificationDelivery{ID: store.nextID, Event: event, Channel: channel})
	}
	return len(channels) > 0, nil
}

func (store *concurrentOutboxStore) PendingNotificationDeliveries(context.Context, time.Time, int) ([]queuedNotificationDelivery, error) {
	store.mu.Lock()
	store.activePending++
	if store.activePending > store.maxActive {
		store.maxActive = store.activePending
	}
	deliveries := append([]queuedNotificationDelivery(nil), store.pending...)
	store.pending = nil
	store.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	store.mu.Lock()
	store.activePending--
	store.mu.Unlock()
	return deliveries, nil
}

func (store *concurrentOutboxStore) RecordNotificationDeliveryAttempt(context.Context, queuedNotificationDelivery, error, time.Time) error {
	return nil
}

func (store *concurrentOutboxStore) maxPendingConcurrent() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.maxActive
}
