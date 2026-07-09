package api

import (
	"database/sql"
	"testing"
	"time"
)

func TestExpiryLabelValueUsesBillingCycleForNextRenewal(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	label := expiryLabelValue(
		sql.NullString{String: "2026-08-10", Valid: true},
		sql.NullString{String: "月", Valid: true},
		false,
		now,
	)
	if label != "余 2 天" {
		t.Fatalf("expiry label = %q, want monthly cycle to use the next renewal as 余 2 天", label)
	}
}

func TestExpiryLabelValueKeepsRawDateWhenBillingCycleUnknown(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	label := expiryLabelValue(
		sql.NullString{String: "2026-08-10", Valid: true},
		sql.NullString{String: "", Valid: true},
		false,
		now,
	)
	if label != "2026-08-10" {
		t.Fatalf("expiry label = %q, want raw date without a recurring billing cycle", label)
	}
}

func TestExpiryLabelValueClampsMonthEndCycle(t *testing.T) {
	now := time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC)
	label := expiryLabelValue(
		sql.NullString{String: "2026-03-31", Valid: true},
		sql.NullString{String: "月付", Valid: true},
		false,
		now,
	)
	if label != "余 1 天" {
		t.Fatalf("expiry label = %q, want February renewal clamped to month end", label)
	}
}
