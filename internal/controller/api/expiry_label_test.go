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

func TestExpiryLabelValueRollsOverAtLocalMidnight(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 7, 11, 0, 30, 0, 0, shanghai)
	label := expiryLabelValue(
		sql.NullString{String: "2026-08-10", Valid: true},
		sql.NullString{String: "月", Valid: true},
		false,
		now,
	)
	if label != "余 30 天" {
		t.Fatalf("expiry label = %q, want recurring billing date to roll over at local midnight", label)
	}

	dueDate, ok := renewalNotificationDueDate(
		"2026-08-10",
		sql.NullString{String: "月", Valid: true},
		now,
	)
	if !ok || dueDate.Format("2006-01-02") != "2026-08-10" {
		t.Fatalf("renewal due date = %s, ok = %v, want 2026-08-10", dueDate.Format("2006-01-02"), ok)
	}
	if got := dateOnlyUTC(now).Format("2006-01-02"); got != "2026-07-11" {
		t.Fatalf("calendar date = %s, want local date 2026-07-11", got)
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
