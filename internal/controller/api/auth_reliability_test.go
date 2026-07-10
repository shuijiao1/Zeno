package api

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestAdminSessionsAreBoundedAndExactExpiryIsRejected(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	for index := 0; index < adminSessionMaxActive+4; index++ {
		if err := store.createAdminSession(ctx, fmt.Sprintf("session-%02d", index)); err != nil {
			t.Fatalf("create session %d: %v", index, err)
		}
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_sessions`).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != adminSessionMaxActive {
		t.Fatalf("active sessions = %d, want %d", count, adminSessionMaxActive)
	}

	token := "exactly-expired-session"
	now := time.Now().UTC().Unix()
	if _, err := store.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO admin_sessions (token_hash, created_at, last_seen_at)
		VALUES (?, ?, ?)
	`, HashAdminToken(token), now-int64(adminSessionAbsoluteTimeout.Seconds()), now); err != nil {
		t.Fatalf("insert exact-boundary session: %v", err)
	}
	authorized, err := store.AuthorizeAdminSession(ctx, token)
	if err != nil {
		t.Fatalf("authorize exact-boundary session: %v", err)
	}
	if authorized {
		t.Fatal("session at the absolute timeout boundary was accepted")
	}
}
