package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newNotificationConsistencyStore(t *testing.T) (*SQLiteStore, AdminNotificationChannel) {
	t.Helper()
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	enableTestNotificationCredentialEncryption(t, store)
	ctx := context.Background()
	if err := store.SeedPreviewData(ctx, PreviewSeedOptions{NodeID: "node-a", DisplayName: "Node A", AgentToken: "agent-token"}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	enabled := true
	channel, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{
		ID: "ops", Name: "Ops", Destination: "chat-old", Credential: "credential-old", Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return store, channel
}

func dispatchChannelFromAdmin(channel AdminNotificationChannel) notificationDispatchChannel {
	return notificationDispatchChannel{ID: channel.ID, Name: channel.Name, Type: "telegram", Destination: channel.Destination}
}

func TestNotificationRecoverySupersedesOnlyNeverAttemptedPredecessorAndPreservesEventTime(t *testing.T) {
	store, channel := newNotificationConsistencyStore(t)
	ctx := context.Background()
	offlineAt := "2026-07-13T12:00:01.123456789Z"
	recoveryAt := "2026-07-13T12:00:02.987654321Z"
	offline := notificationEvent{EventType: "node_offline", Label: "离线", NodeID: "node-a", NodeName: "Node A", PreviousStatus: "online", Status: "offline", TS: offlineAt}
	if queued, err := store.QueueNotificationEvent(ctx, offline, []notificationDispatchChannel{dispatchChannelFromAdmin(channel)}); err != nil || !queued {
		t.Fatalf("queue offline: queued=%v err=%v", queued, err)
	}
	recovery := notificationEvent{EventType: "node_offline", Label: "离线", NodeID: "node-a", NodeName: "Node A", PreviousStatus: "offline", Status: "online", TS: recoveryAt}
	if queued, err := store.QueueNotificationEvent(ctx, recovery, []notificationDispatchChannel{dispatchChannelFromAdmin(channel)}); err != nil || !queued {
		t.Fatalf("queue recovery: queued=%v err=%v", queued, err)
	}

	var offlineState, supersededBy, storedOfflineAt string
	if err := store.db.QueryRowContext(ctx, `
		SELECT state, superseded_by_event_id, event_ts
		FROM notification_deliveries WHERE status = 'offline'
	`).Scan(&offlineState, &supersededBy, &storedOfflineAt); err != nil {
		t.Fatalf("read offline delivery: %v", err)
	}
	if offlineState != "canceled" || supersededBy == "" || storedOfflineAt != offlineAt {
		t.Fatalf("offline state=%q superseded_by=%q ts=%q", offlineState, supersededBy, storedOfflineAt)
	}
	claimed, err := store.PendingNotificationDeliveries(ctx, time.Now().UTC(), 32)
	if err != nil {
		t.Fatalf("claim recovery: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Event.Status != "online" || claimed[0].Event.TS != recoveryAt || claimed[0].Event.EventID == "" {
		t.Fatalf("claimed recovery = %+v", claimed)
	}
	if claimed[0].Event.EventID != supersededBy {
		t.Fatalf("recovery event id=%q, superseded_by=%q", claimed[0].Event.EventID, supersededBy)
	}
}

func TestNotificationRecoveryCannotOvertakeAttemptedOrFailedPredecessor(t *testing.T) {
	store, channel := newNotificationConsistencyStore(t)
	ctx := context.Background()
	offline := notificationEvent{EventType: "node_offline", NodeID: "node-a", PreviousStatus: "online", Status: "offline", TS: "2026-07-13T12:00:01Z"}
	if queued, err := store.QueueNotificationEvent(ctx, offline, []notificationDispatchChannel{dispatchChannelFromAdmin(channel)}); err != nil || !queued {
		t.Fatalf("queue offline: queued=%v err=%v", queued, err)
	}
	attemptAt := time.Now().UTC()
	claimed, err := store.PendingNotificationDeliveries(ctx, attemptAt, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim offline: %+v err=%v", claimed, err)
	}
	if err := store.RecordNotificationDeliveryAttempt(ctx, claimed[0], errors.New("temporary failure"), attemptAt); err != nil {
		t.Fatalf("record failed attempt: %v", err)
	}
	recovery := notificationEvent{EventType: "node_offline", NodeID: "node-a", PreviousStatus: "offline", Status: "online", TS: "2026-07-13T12:00:02Z"}
	if queued, err := store.QueueNotificationEvent(ctx, recovery, []notificationDispatchChannel{dispatchChannelFromAdmin(channel)}); err != nil || !queued {
		t.Fatalf("queue recovery: queued=%v err=%v", queued, err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE notification_deliveries SET next_attempt_at = ? WHERE status = 'offline'`, attemptAt.Add(time.Hour).Unix()); err != nil {
		t.Fatalf("delay predecessor: %v", err)
	}
	blocked, err := store.PendingNotificationDeliveries(ctx, attemptAt.Add(time.Second), 1)
	if err != nil {
		t.Fatalf("claim while predecessor delayed: %v", err)
	}
	if len(blocked) != 0 {
		t.Fatalf("recovery overtook predecessor: %+v", blocked)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE notification_deliveries SET next_attempt_at = 0 WHERE status = 'offline'`); err != nil {
		t.Fatalf("make predecessor due: %v", err)
	}
	claimed, err = store.PendingNotificationDeliveries(ctx, attemptAt.Add(2*time.Second), 1)
	if err != nil || len(claimed) != 1 || claimed[0].Event.Status != "offline" {
		t.Fatalf("reclaim predecessor: %+v err=%v", claimed, err)
	}
	claimed[0].Attempts = notificationDeliveryMaxAttempts - 1
	if err := store.RecordNotificationDeliveryAttempt(ctx, claimed[0], errors.New("permanent failure"), attemptAt.Add(2*time.Second)); err != nil {
		t.Fatalf("record terminal predecessor failure: %v", err)
	}
	var recoveryState, recoveryError string
	if err := store.db.QueryRowContext(ctx, `SELECT state, last_error FROM notification_deliveries WHERE status = 'online'`).Scan(&recoveryState, &recoveryError); err != nil {
		t.Fatalf("read recovery: %v", err)
	}
	if recoveryState != "canceled" || recoveryError != "predecessor delivery failed" {
		t.Fatalf("recovery state=%q error=%q", recoveryState, recoveryError)
	}
}

func TestNotificationChannelRouteChangeAndDeleteCancelOldBacklog(t *testing.T) {
	store, channel := newNotificationConsistencyStore(t)
	ctx := context.Background()
	queue := func(ts string) {
		t.Helper()
		event := notificationEvent{EventType: "test_notification", TS: ts}
		if queued, err := store.QueueNotificationEvent(ctx, event, []notificationDispatchChannel{dispatchChannelFromAdmin(channel)}); err != nil || !queued {
			t.Fatalf("queue event: queued=%v err=%v", queued, err)
		}
	}
	queue("2026-07-13T12:00:01Z")
	newDestination := "chat-new"
	if _, err := store.UpdateAdminNotificationChannel(ctx, channel.ID, AdminNotificationChannelUpdateRequest{Destination: &newDestination}); err != nil {
		t.Fatalf("change destination: %v", err)
	}
	var state, fingerprint string
	var boundVersion, currentVersion int64
	if err := store.db.QueryRowContext(ctx, `SELECT state, channel_version, destination_fingerprint FROM notification_deliveries LIMIT 1`).Scan(&state, &boundVersion, &fingerprint); err != nil {
		t.Fatalf("read canceled backlog: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT delivery_version FROM notification_channels WHERE id = ?`, channel.ID).Scan(&currentVersion); err != nil {
		t.Fatalf("read channel version: %v", err)
	}
	if state != "canceled" || boundVersion >= currentVersion || strings.Contains(fingerprint, "chat-old") {
		t.Fatalf("state=%q bound_version=%d current_version=%d fingerprint=%q", state, boundVersion, currentVersion, fingerprint)
	}
	claimed, err := store.PendingNotificationDeliveries(ctx, time.Now().UTC(), 1)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("old route was claimable: %+v err=%v", claimed, err)
	}

	current, err := store.AdminNotificationDispatchChannel(ctx, channel.ID)
	if err != nil {
		t.Fatalf("load current route: %v", err)
	}
	if queued, err := store.QueueNotificationEvent(ctx, notificationEvent{EventType: "test_notification", TS: "2026-07-13T12:00:02Z"}, []notificationDispatchChannel{current}); err != nil || !queued {
		t.Fatalf("queue current route: queued=%v err=%v", queued, err)
	}
	if err := store.DeleteAdminNotificationChannel(ctx, channel.ID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	var active int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_deliveries WHERE state IN ('pending','leased','paused','failed')`).Scan(&active); err != nil {
		t.Fatalf("count active backlog: %v", err)
	}
	if active != 0 {
		t.Fatalf("active backlog after delete=%d", active)
	}
	columns, err := store.tableColumns(ctx, "notification_deliveries")
	if err != nil {
		t.Fatalf("delivery columns: %v", err)
	}
	if columns["credential"] || columns["destination"] {
		t.Fatalf("delivery table persisted plaintext routing secrets/targets: %+v", columns)
	}
}

func TestNotificationClaimQuarantinesBadCiphertextAndReturnsHealthyChannel(t *testing.T) {
	store, first := newNotificationConsistencyStore(t)
	ctx := context.Background()
	enabled := true
	second, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "healthy", Name: "Healthy", Destination: "chat-good", Credential: "credential-good", Enabled: &enabled})
	if err != nil {
		t.Fatalf("create healthy channel: %v", err)
	}
	event := notificationEvent{EventType: "test_notification", TS: "2026-07-13T12:00:01Z"}
	if queued, err := store.QueueNotificationEvent(ctx, event, []notificationDispatchChannel{dispatchChannelFromAdmin(first), dispatchChannelFromAdmin(second)}); err != nil || !queued {
		t.Fatalf("queue channels: queued=%v err=%v", queued, err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE notification_channels SET credential = ? WHERE id = ?`, notificationCredentialCiphertextPrefix+"broken", first.ID); err != nil {
		t.Fatalf("damage first credential: %v", err)
	}
	claimed, err := store.PendingNotificationDeliveries(ctx, time.Now().UTC(), 32)
	if err != nil {
		t.Fatalf("claim after damaged channel: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Channel.ID != second.ID || claimed[0].Channel.Credential != "credential-good" {
		t.Fatalf("healthy claim = %+v", claimed)
	}
	var state, lastError string
	if err := store.db.QueryRowContext(ctx, `SELECT state, last_error FROM notification_deliveries WHERE channel_id = ?`, first.ID).Scan(&state, &lastError); err != nil {
		t.Fatalf("read quarantined delivery: %v", err)
	}
	if state != "failed" || lastError != "notification credential unavailable" {
		t.Fatalf("quarantine state=%q error=%q", state, lastError)
	}
}

func TestRecordNotificationAttemptDetectsLostLease(t *testing.T) {
	store, channel := newNotificationConsistencyStore(t)
	ctx := context.Background()
	if queued, err := store.QueueNotificationEvent(ctx, notificationEvent{EventType: "test_notification", TS: "2026-07-13T12:00:01Z"}, []notificationDispatchChannel{dispatchChannelFromAdmin(channel)}); err != nil || !queued {
		t.Fatalf("queue: queued=%v err=%v", queued, err)
	}
	claimed, err := store.PendingNotificationDeliveries(ctx, time.Now().UTC(), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %+v err=%v", claimed, err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE notification_deliveries SET claim_token = 'replacement' WHERE id = ?`, claimed[0].ID); err != nil {
		t.Fatalf("replace lease token: %v", err)
	}
	if err := store.RecordNotificationDeliveryAttempt(ctx, claimed[0], nil, time.Now().UTC()); !errors.Is(err, errNotificationDeliveryLeaseLost) {
		t.Fatalf("record with lost lease error=%v, want %v", err, errNotificationDeliveryLeaseLost)
	}
}

func TestLegacyNotificationWithoutImmutableRouteIsCanceledFailClosed(t *testing.T) {
	store, channel := newNotificationConsistencyStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO notification_deliveries (
			event_type, label, node_id, node_name, previous_status, status, detail,
			channel_id, channel_name, state, attempts, next_attempt_at, last_error,
			lease_until, claim_token, created_at, updated_at,
			channel_version, destination_fingerprint
		) VALUES ('node_offline', '节点离线', 'node-a', 'Node A', 'online', 'offline', '',
			?, ?, 'pending', 0, ?, '', 0, '', ?, ?, 1, '')
	`, channel.ID, channel.Name, now, now, now); err != nil {
		t.Fatalf("insert legacy delivery: %v", err)
	}
	if err := store.migrateNotificationRoutingBindings(ctx); err != nil {
		t.Fatalf("migrate routing: %v", err)
	}
	var state, lastError, fingerprint string
	if err := store.db.QueryRowContext(ctx, `
		SELECT state, last_error, destination_fingerprint
		FROM notification_deliveries ORDER BY id DESC LIMIT 1
	`).Scan(&state, &lastError, &fingerprint); err != nil {
		t.Fatalf("read migrated delivery: %v", err)
	}
	if state != "canceled" || lastError != "legacy notification route unverifiable" || fingerprint == "" {
		t.Fatalf("legacy route state=%q error=%q fingerprint=%q", state, lastError, fingerprint)
	}
}

func TestNotificationCredentialAndAuthorityKeyringsRotateWithoutEcho(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	oldKey := []byte("0123456789abcdef0123456789abcdef")
	newKey := []byte("abcdef0123456789abcdef0123456789")
	if err := store.ConfigureNotificationCredentialKeyring(ctx, "old", map[string][]byte{"old": oldKey}); err != nil {
		t.Fatalf("configure old key: %v", err)
	}
	enabled := true
	if _, err := store.CreateAdminNotificationChannel(ctx, AdminNotificationChannelCreateRequest{ID: "ops", Name: "Ops", Destination: "chat", Credential: "old-secret", Enabled: &enabled}); err != nil {
		t.Fatalf("create old ciphertext: %v", err)
	}
	var oldCiphertext string
	if err := store.db.QueryRowContext(ctx, `SELECT credential FROM notification_channels WHERE id = 'ops'`).Scan(&oldCiphertext); err != nil {
		t.Fatalf("read old ciphertext: %v", err)
	}
	if !strings.HasPrefix(oldCiphertext, notificationCredentialCiphertextPrefix+"old:") {
		t.Fatalf("old ciphertext key id missing: %q", oldCiphertext)
	}
	if err := store.ConfigureNotificationCredentialKeyring(ctx, "new", map[string][]byte{"old": oldKey, "new": newKey}); err != nil {
		t.Fatalf("install rolling keyring: %v", err)
	}
	channel, err := store.AdminNotificationDispatchChannel(ctx, "ops")
	if err != nil || channel.Credential != "old-secret" {
		t.Fatalf("read old ciphertext with ring: channel=%+v err=%v", channel, err)
	}
	var newCiphertext string
	if err := store.db.QueryRowContext(ctx, `SELECT credential FROM notification_channels WHERE id = 'ops'`).Scan(&newCiphertext); err != nil {
		t.Fatalf("read new ciphertext: %v", err)
	}
	if !strings.HasPrefix(newCiphertext, notificationCredentialCiphertextPrefix+"new:") || strings.Contains(newCiphertext, "old-secret") {
		t.Fatalf("new ciphertext=%q", newCiphertext)
	}
	if err := store.ConfigureNotificationCredentialKeyring(ctx, "new", map[string][]byte{"new": newKey}); err != nil {
		t.Fatalf("drop old credential key after automatic rewrite: %v", err)
	}
	channel, err = store.AdminNotificationDispatchChannel(ctx, "ops")
	if err != nil || channel.Credential != "old-secret" {
		t.Fatalf("read automatically re-encrypted credential: channel=%+v err=%v", channel, err)
	}

	if authorized, err := store.AuthorizeNotificationAuthorityKeyring(ctx, "old", map[string]string{"old": "authority-old"}); err != nil || !authorized {
		t.Fatalf("bind old authority: authorized=%v err=%v", authorized, err)
	}
	if authorized, err := store.AuthorizeNotificationAuthorityKeyring(ctx, "new", map[string]string{"old": "authority-old", "new": "authority-new"}); err != nil || !authorized {
		t.Fatalf("rotate authority: authorized=%v err=%v", authorized, err)
	}
	if authorized, err := store.AuthorizeNotificationAuthorityKeyring(ctx, "new", map[string]string{"new": "authority-new"}); err != nil || !authorized {
		t.Fatalf("authorize with new-only ring: authorized=%v err=%v", authorized, err)
	}
	if authorized, err := store.AuthorizeNotificationAuthorityKeyring(ctx, "old", map[string]string{"old": "authority-old"}); err != nil || authorized {
		t.Fatalf("old authority remained valid: authorized=%v err=%v", authorized, err)
	}
}

func TestNotificationTypeCompatibilityWriteRollsBackBothTables(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO notification_types (event_type, enabled, updated_at)
		VALUES ('node_offline', 1, ?)
	`, time.Now().UTC().Unix()); err != nil {
		t.Fatalf("seed legacy type: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		CREATE TRIGGER reject_alert_rule_update
		BEFORE UPDATE ON alert_rules WHEN OLD.id = 'node_offline'
		BEGIN SELECT RAISE(ABORT, 'reject'); END
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	disabled := false
	if _, err := store.UpdateAdminNotificationType(ctx, "node_offline", AdminNotificationTypeUpdateRequest{Enabled: &disabled}); err == nil {
		t.Fatal("compatibility write unexpectedly succeeded")
	}
	var legacyEnabled, ruleEnabled int
	if err := store.db.QueryRowContext(ctx, `SELECT enabled FROM notification_types WHERE event_type = 'node_offline'`).Scan(&legacyEnabled); err != nil {
		t.Fatalf("read legacy type: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT enabled FROM alert_rules WHERE id = 'node_offline'`).Scan(&ruleEnabled); err != nil {
		t.Fatalf("read rule: %v", err)
	}
	if legacyEnabled != 1 || ruleEnabled != 1 {
		t.Fatalf("partial write legacy=%d rule=%d", legacyEnabled, ruleEnabled)
	}
}

func TestConcurrentSparseSettingsPatchesDoNotLoseDisjointFields(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	for iteration := 0; iteration < 20; iteration++ {
		title := fmt.Sprintf("title-%d", iteration)
		subtitle := fmt.Sprintf("subtitle-%d", iteration)
		start := make(chan struct{})
		errorsByWorker := make(chan error, 2)
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			_, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{SiteTitle: &title})
			errorsByWorker <- err
		}()
		go func() {
			defer workers.Done()
			<-start
			_, err := store.UpdateAdminSettings(ctx, AdminSettingsUpdateRequest{SiteSubtitle: &subtitle})
			errorsByWorker <- err
		}()
		close(start)
		workers.Wait()
		close(errorsByWorker)
		for err := range errorsByWorker {
			if err != nil {
				t.Fatalf("concurrent patch: %v", err)
			}
		}
		settings, err := store.AdminSettings(ctx)
		if err != nil {
			t.Fatalf("read settings: %v", err)
		}
		if settings.SiteTitle != title || settings.SiteSubtitle != subtitle {
			t.Fatalf("iteration %d lost update: title=%q subtitle=%q", iteration, settings.SiteTitle, settings.SiteSubtitle)
		}
	}
}
