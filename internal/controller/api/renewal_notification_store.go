package api

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

type renewalNotificationCandidate struct {
	event notificationEvent
	mark  string
}

func (s *SQLiteStore) PendingRenewalNotifications(ctx context.Context, now time.Time) ([]notificationEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackUnlessCommitted(tx)
	candidates, err := renewalNotificationCandidatesTx(ctx, tx, now, true)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	events := make([]notificationEvent, 0, len(candidates))
	for _, candidate := range candidates {
		events = append(events, candidate.event)
	}
	return events, nil
}

// QueueDueRenewalNotifications claims due renewal reminders and their outbox
// deliveries in one SQLite transaction. The notification_event_marks primary
// key is the stable idempotency key, so concurrent scanners or a crash between
// claim and delivery creation cannot produce duplicate reminders.
func (s *SQLiteStore) QueueDueRenewalNotifications(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackUnlessCommitted(tx)

	channels, err := enabledNotificationChannelsTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	if len(channels) == 0 {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		tx = nil
		return 0, nil
	}
	candidates, err := renewalNotificationCandidatesTx(ctx, tx, now, true)
	if err != nil {
		return 0, err
	}
	nowUnix := now.UTC().Unix()
	queued := 0
	for _, candidate := range candidates {
		result, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO notification_event_marks (event_type, node_id, mark, created_at)
			VALUES ('renewal_due', ?, ?, ?)
		`, candidate.event.NodeID, candidate.mark, nowUnix)
		if err != nil {
			return 0, err
		}
		claimed, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if claimed == 0 {
			continue
		}
		if err := insertNotificationDeliveriesTx(ctx, tx, candidate.event, channels, nowUnix); err != nil {
			return 0, err
		}
		queued += len(channels)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	tx = nil
	return queued, nil
}

func renewalNotificationCandidatesTx(ctx context.Context, tx *sql.Tx, now time.Time, skipClaimed bool) ([]renewalNotificationCandidate, error) {
	today := dateOnlyUTC(now)
	markDay := today.Format("2006-01-02")
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

	type renewalNode struct {
		id              string
		displayName     string
		expiryDate      string
		expiryPermanent int
		billingCycle    sql.NullString
	}
	nodes := make([]renewalNode, 0)
	for rows.Next() {
		var node renewalNode
		if err := rows.Scan(&node.id, &node.displayName, &node.expiryDate, &node.expiryPermanent, &node.billingCycle); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	label, _ := adminNotificationTypeLabel("renewal_due")
	candidates := make([]renewalNotificationCandidate, 0)
	for _, node := range nodes {
		if node.expiryPermanent != 0 {
			continue
		}
		dueDate, ok := renewalNotificationDueDate(node.expiryDate, node.billingCycle, now)
		if !ok {
			continue
		}
		dueDateText := dueDate.Format("2006-01-02")
		daysRemaining := int(math.Ceil(dueDate.Sub(today).Hours() / 24))
		rules, err := alertRulesForMetrics(ctx, tx, node.id, map[string]bool{"expiry_days": true})
		if err != nil {
			return nil, err
		}
		if !renewalRulesMatch(rules, float64(daysRemaining)) {
			continue
		}
		mark := renewalNotificationMark(markDay, dueDateText)
		if skipClaimed {
			var exists int
			err = tx.QueryRowContext(ctx, `
				SELECT 1 FROM notification_event_marks
				WHERE event_type = 'renewal_due' AND node_id = ? AND mark = ?
			`, node.id, mark).Scan(&exists)
			if err == nil {
				continue
			}
			if err != sql.ErrNoRows {
				return nil, err
			}
		}
		candidates = append(candidates, renewalNotificationCandidate{event: notificationEvent{
			EventType: "renewal_due",
			Label:     label,
			NodeID:    node.id,
			NodeName:  node.displayName,
			Status:    "renewal_due",
			TS:        now.UTC().Format(time.RFC3339),
			Detail:    formatRenewalNotificationDetail(daysRemaining, dueDateText),
		}, mark: mark})
	}
	return candidates, nil
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
	// Stable historical idempotency key: one reminder per node, scan day, and
	// effective due date. Keep the shape unchanged so upgrades preserve existing
	// notification_event_marks de-duplication rows.
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
