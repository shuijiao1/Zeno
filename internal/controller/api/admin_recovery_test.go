package api

import (
	"context"
	"path/filepath"
	"testing"
)

func TestResetAdminAccountResetsUsernamePasswordAndSessions(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fallback := HashAdminToken("bootstrap-password")
	session, err := store.UpdateAdminAccount(context.Background(), "old-name", "bootstrap-password", "old-password", fallback)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResetAdminAccount(context.Background(), "recovered-password"); err != nil {
		t.Fatal(err)
	}
	allowed, err := store.AuthorizeAdminSession(context.Background(), session.Token)
	if err != nil || allowed {
		t.Fatalf("old session allowed=%v err=%v", allowed, err)
	}
	recovered, err := store.AdminLogin(context.Background(), "admin", "recovered-password", fallback)
	if err != nil || recovered.Token == "" {
		t.Fatalf("recovered login=%+v err=%v", recovered, err)
	}
}
