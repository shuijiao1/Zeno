package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthorizeAdminSessionBucketsLastSeenWrites(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	token := "bucketed-session"
	now := time.Now().UTC().Unix()
	freshLastSeen := now - int64((adminSessionLastSeenBucket / 2).Seconds())
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO admin_sessions (token_hash, created_at, last_seen_at)
		VALUES (?, ?, ?)
	`, HashAdminToken(token), now-3600, freshLastSeen); err != nil {
		t.Fatalf("insert fresh session: %v", err)
	}
	authorized, err := store.AuthorizeAdminSession(ctx, token)
	if err != nil {
		t.Fatalf("authorize fresh session: %v", err)
	}
	if !authorized {
		t.Fatal("fresh session was rejected")
	}
	var storedLastSeen int64
	if err := store.db.QueryRowContext(ctx, `SELECT last_seen_at FROM admin_sessions WHERE token_hash = ?`, HashAdminToken(token)).Scan(&storedLastSeen); err != nil {
		t.Fatalf("read fresh last_seen: %v", err)
	}
	if storedLastSeen != freshLastSeen {
		t.Fatalf("fresh last_seen updated within bucket: got %d want unchanged %d", storedLastSeen, freshLastSeen)
	}

	staleLastSeen := now - int64(adminSessionLastSeenBucket.Seconds()) - 10
	if _, err := store.db.ExecContext(ctx, `UPDATE admin_sessions SET last_seen_at = ? WHERE token_hash = ?`, staleLastSeen, HashAdminToken(token)); err != nil {
		t.Fatalf("age session: %v", err)
	}
	authorized, err = store.AuthorizeAdminSession(ctx, token)
	if err != nil {
		t.Fatalf("authorize stale-bucket session: %v", err)
	}
	if !authorized {
		t.Fatal("stale-bucket session was rejected")
	}
	if err := store.db.QueryRowContext(ctx, `SELECT last_seen_at FROM admin_sessions WHERE token_hash = ?`, HashAdminToken(token)).Scan(&storedLastSeen); err != nil {
		t.Fatalf("read updated last_seen: %v", err)
	}
	if storedLastSeen <= staleLastSeen {
		t.Fatalf("stale-bucket last_seen was not refreshed: got %d old %d", storedLastSeen, staleLastSeen)
	}
}

func TestAuthorizeAdminSessionOccasionalPruneKeepsIdleBoundary(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC().Unix()

	activeToken := "active-session"
	idleExpiredToken := "idle-expired-session"
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO admin_sessions (token_hash, created_at, last_seen_at)
		VALUES (?, ?, ?), (?, ?, ?)
	`, HashAdminToken(activeToken), now-3600, now-10, HashAdminToken(idleExpiredToken), now-3600, now-int64(adminSessionIdleTimeout.Seconds())); err != nil {
		t.Fatalf("insert sessions: %v", err)
	}
	store.adminSessionLastPruned = time.Now().UTC()
	authorized, err := store.AuthorizeAdminSession(ctx, idleExpiredToken)
	if err != nil {
		t.Fatalf("authorize idle boundary session: %v", err)
	}
	if authorized {
		t.Fatal("session at the idle timeout boundary was accepted")
	}
	authorized, err = store.AuthorizeAdminSession(ctx, activeToken)
	if err != nil {
		t.Fatalf("authorize active session: %v", err)
	}
	if !authorized {
		t.Fatal("active session was rejected")
	}
}
