package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestAdminProbeTargetMutationsCanRepairLegacyOverLimitNodesIndependently(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "zeno.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	for _, seed := range []PreviewSeedOptions{
		{NodeID: "legacy-a", DisplayName: "Legacy A", AgentToken: "legacy-a-token"},
		{NodeID: "legacy-b", DisplayName: "Legacy B", AgentToken: "legacy-b-token"},
	} {
		if err := store.SeedPreviewData(ctx, seed); err != nil {
			t.Fatalf("seed %s: %v", seed.NodeID, err)
		}
	}

	// Recreate a legacy database state that predates the current per-target and
	// per-node execution budgets. Each node has a different oversized target so
	// repairing one must not be blocked by the other node remaining oversized.
	if _, err := store.db.ExecContext(ctx, `UPDATE node_probe_targets SET enabled = 0`); err != nil {
		t.Fatalf("disable preview assignments: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE probe_targets SET enabled = 0`); err != nil {
		t.Fatalf("disable preview targets: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE probe_targets
		SET enabled = 1, count = 32, timeout_ms = 5000, interval_sec = 30
		WHERE id IN ('google-dns', 'cloudflare-dns')
	`); err != nil {
		t.Fatalf("install legacy oversized targets: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE node_probe_targets
		SET enabled = CASE
			WHEN node_id = 'legacy-a' AND target_id = 'google-dns' THEN 1
			WHEN node_id = 'legacy-b' AND target_id = 'cloudflare-dns' THEN 1
			ELSE 0
		END
	`); err != nil {
		t.Fatalf("install legacy oversized assignments: %v", err)
	}

	count := 12
	intervalSec := 60
	if _, err := store.UpdateAdminProbeTarget(ctx, "google-dns", AdminProbeTargetUpdateRequest{Count: &count, IntervalSec: &intervalSec}); err != nil {
		t.Fatalf("repair legacy-a while legacy-b remains oversized: %v", err)
	}

	disabled := false
	if _, err := store.UpdateAdminProbeTarget(ctx, "cloudflare-dns", AdminProbeTargetUpdateRequest{Enabled: &disabled}); err != nil {
		t.Fatalf("disable oversized legacy target: %v", err)
	}

	// Recreate the old enabled target without bumping config metadata, then prove
	// an administrator can still unassign it from one node. Re-enabling the same
	// unsafe assignment through the Admin API must remain rejected.
	if _, err := store.db.ExecContext(ctx, `UPDATE probe_targets SET enabled = 1 WHERE id = 'cloudflare-dns'`); err != nil {
		t.Fatalf("re-enable legacy target directly: %v", err)
	}
	unassign := []AdminProbeTargetAssignmentUpdate{{NodeID: "legacy-b", Enabled: false}}
	if _, err := store.UpdateAdminProbeTarget(ctx, "cloudflare-dns", AdminProbeTargetUpdateRequest{Assignments: unassign}); err != nil {
		t.Fatalf("unassign oversized legacy target: %v", err)
	}

	reassign := []AdminProbeTargetAssignmentUpdate{{NodeID: "legacy-b", Enabled: true}}
	if _, err := store.UpdateAdminProbeTarget(ctx, "cloudflare-dns", AdminProbeTargetUpdateRequest{Assignments: reassign}); !errors.Is(err, errInvalidAdminTargetWrite) {
		t.Fatalf("re-enable oversized legacy assignment error = %v, want %v", err, errInvalidAdminTargetWrite)
	}

	var enabled int
	if err := store.db.QueryRowContext(ctx, `
		SELECT enabled FROM node_probe_targets WHERE node_id = 'legacy-b' AND target_id = 'cloudflare-dns'
	`).Scan(&enabled); err != nil {
		t.Fatalf("read legacy-b assignment: %v", err)
	}
	if enabled != 0 {
		t.Fatalf("legacy-b assignment enabled = %d, want rollback to keep it disabled", enabled)
	}
}
