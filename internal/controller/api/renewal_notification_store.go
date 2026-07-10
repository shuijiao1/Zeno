package api

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

func (s *SQLiteStore) PendingRenewalNotifications(ctx context.Context, now time.Time) ([]notificationEvent, error) {
	today := dateOnlyUTC(now)
	markDay := today.Format("2006-01-02")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackUnlessCommitted(tx)

	rows, err := tx.QueryContext(ctx, `
		SELECT id, display_name, expiry_date, expiry_permanent, billing_cycle
		FROM nodes
		WHERE disabled = 0 AND TRIM(COALESCE(expiry_date, '')) <> ''
		ORDER BY display_order ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]notificationEvent, 0)
	for rows.Next() {
		var nodeID, displayName, expiryDate string
		var expiryPermanent int
		var billingCycle sql.NullString
		if err := rows.Scan(&nodeID, &displayName, &expiryDate, &expiryPermanent, &billingCycle); err != nil {
			return nil, err
		}
		if expiryPermanent != 0 {
			continue
		}
		dueDate, ok := renewalNotificationDueDate(expiryDate, billingCycle, now)
		if !ok {
			continue
		}
		dueDateText := dueDate.Format("2006-01-02")
		daysRemaining := int(math.Ceil(dueDate.Sub(today).Hours() / 24))
		rules, err := alertRulesForMetrics(ctx, tx, nodeID, map[string]bool{"expiry_days": true})
		if err != nil {
			return nil, err
		}
		if !renewalRulesMatch(rules, float64(daysRemaining)) {
			continue
		}
		mark := renewalNotificationMark(markDay, dueDateText)
		var exists int
		err = tx.QueryRowContext(ctx, `
			SELECT 1 FROM notification_event_marks
			WHERE event_type = 'renewal_due' AND node_id = ? AND mark = ?
		`, nodeID, mark).Scan(&exists)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
		events = append(events, notificationEvent{
			EventType: "renewal_due",
			NodeID:    nodeID,
			NodeName:  displayName,
			Status:    "renewal_due",
			TS:        now.UTC().Format(time.RFC3339),
			Detail:    formatRenewalNotificationDetail(daysRemaining, dueDateText),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return events, nil
}

func renewalNotificationDueDate(rawDate string, billingCycle sql.NullString, now time.Time) (time.Time, bool) {
	cycleMonths := billingCycleMonths(billingCycle)
	if cycleMonths > 0 {
		return nextBillingCycleDate(rawDate, cycleMonths, now)
	}
	expiresAt, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(rawDate), time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return dateOnlyUTC(expiresAt), true
}

func (s *SQLiteStore) MarkRenewalNotification(ctx context.Context, event notificationEvent, now time.Time) error {
	nodeID := strings.TrimSpace(event.NodeID)
	if nodeID == "" {
		return nil
	}
	markDay := dateOnlyUTC(now).Format("2006-01-02")
	expiryDate := extractRenewalExpiryDate(event.Detail)
	if expiryDate == "" {
		expiryDate = markDay
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO notification_event_marks (event_type, node_id, mark, created_at)
		VALUES ('renewal_due', ?, ?, ?)
	`, nodeID, renewalNotificationMark(markDay, expiryDate), now.UTC().Unix())
	return err
}

func renewalRulesMatch(rules []AdminAlertRule, daysRemaining float64) bool {
	for _, rule := range rules {
		if rule.NotificationEventType != "renewal_due" || rule.Metric != "expiry_days" || !rule.Enabled {
			continue
		}
		if compareAlertRuleValue(daysRemaining, rule.Comparator, rule.Threshold) {
			return true
		}
	}
	return false
}

func renewalNotificationMark(day, expiryDate string) string {
	return strings.TrimSpace(day) + ":" + strings.TrimSpace(expiryDate)
}

func formatRenewalNotificationDetail(daysRemaining int, expiryDate string) string {
	switch {
	case daysRemaining > 0:
		return fmt.Sprintf("还有 %d 天到期，%s", daysRemaining, expiryDate)
	case daysRemaining == 0:
		return fmt.Sprintf("今天到期，%s", expiryDate)
	default:
		return fmt.Sprintf("已过期 %d 天，%s", -daysRemaining, expiryDate)
	}
}

func extractRenewalExpiryDate(detail string) string {
	parts := strings.Split(strings.TrimSpace(detail), "，")
	if len(parts) == 0 {
		return ""
	}
	candidate := strings.TrimSpace(parts[len(parts)-1])
	if _, err := time.ParseInLocation("2006-01-02", candidate, time.UTC); err != nil {
		return ""
	}
	return candidate
}
