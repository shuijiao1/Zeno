package api

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

var defaultAdminAlertRules = []AdminAlertRule{
	{
		ID:                    "cpu_high",
		Name:                  "CPU 使用率",
		Category:              "resource",
		Metric:                "cpu_percent",
		Comparator:            ">=",
		Threshold:             90,
		ThresholdUnit:         "%",
		DurationSec:           60,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
	},
	{
		ID:                    "memory_high",
		Name:                  "内存使用率",
		Category:              "resource",
		Metric:                "memory_percent",
		Comparator:            ">=",
		Threshold:             90,
		ThresholdUnit:         "%",
		DurationSec:           60,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
	},
	{
		ID:                    "disk_high",
		Name:                  "磁盘使用率",
		Category:              "resource",
		Metric:                "disk_percent",
		Comparator:            ">=",
		Threshold:             90,
		ThresholdUnit:         "%",
		DurationSec:           60,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
	},
	{
		ID:                    "node_offline",
		Name:                  "离线通知",
		Category:              "liveness",
		Metric:                "heartbeat_age_sec",
		Comparator:            ">=",
		Threshold:             180,
		ThresholdUnit:         "s",
		DurationSec:           30,
		Enabled:               true,
		NotificationEventType: "node_offline",
	},
	{
		ID:                    "renewal_due",
		Name:                  "续费提醒",
		Category:              "billing",
		Metric:                "expiry_days",
		Comparator:            "<=",
		Threshold:             3,
		ThresholdUnit:         "d",
		DurationSec:           0,
		Enabled:               false,
		NotificationEventType: "renewal_due",
	},
}

var retiredAdminAlertRuleIDs = []string{"probe_latency_high", "probe_loss_high", "node_recovered"}

var retiredAdminNotificationEventTypes = []string{"node_online"}

var allowedRenewalNoticeDays = map[int]bool{0: true, 1: true, 3: true, 7: true, 15: true, 30: true}

func (s *SQLiteStore) ensureDefaultAlertRules(ctx context.Context) error {
	now := time.Now().UTC().Unix()
	for sortOrder, rule := range defaultAdminAlertRules {
		enabled := 0
		if rule.Enabled {
			enabled = 1
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO alert_rules (
				id, name, category, metric, comparator, threshold, threshold_unit,
				duration_sec, enabled, notification_event_type, description, sort_order,
				created_at, updated_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, rule.ID, rule.Name, rule.Category, rule.Metric, rule.Comparator, rule.Threshold, rule.ThresholdUnit, rule.DurationSec, enabled, rule.NotificationEventType, rule.Description, sortOrder, now, now); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE alert_rules
			SET name = ?, description = '', sort_order = ?
			WHERE id = ?
		`, rule.Name, sortOrder, rule.ID); err != nil {
			return err
		}
	}
	if err := s.migrateDefaultAlertRuleDurations(ctx); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) migrateDefaultAlertRuleDurations(ctx context.Context) error {
	now := time.Now().UTC().Unix()
	const migrationKey = "alert_default_durations_v2_migrated"
	var marker string
	migrated := true
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, migrationKey).Scan(&marker); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		migrated = false
	}
	if migrated {
		return nil
	}
	resourceOldSecs := []int{30, 300}
	diskOldSecs := []int{30, 300, 600}
	updates := []struct {
		id      string
		oldSecs []int
	}{
		{id: "cpu_high", oldSecs: resourceOldSecs},
		{id: "memory_high", oldSecs: resourceOldSecs},
		{id: "disk_high", oldSecs: diskOldSecs},
		{id: "node_offline", oldSecs: []int{180}},
	}
	for _, update := range updates {
		newSec := 30
		if update.id == "cpu_high" || update.id == "memory_high" || update.id == "disk_high" {
			newSec = 60
		}
		for _, oldSec := range update.oldSecs {
			if _, err := s.db.ExecContext(ctx, `
				UPDATE alert_rules
				SET duration_sec = ?, updated_at = ?
				WHERE id = ? AND duration_sec = ?
			`, newSec, now, update.id, oldSec); err != nil {
				return err
			}
		}
	}
	if _, err := s.db.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at)
			VALUES (?, '1', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
		`, migrationKey, now); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) pruneRetiredNotificationConfig(ctx context.Context) error {
	for _, ruleID := range retiredAdminAlertRuleIDs {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM alert_rule_node_scopes WHERE rule_id = ?`, ruleID); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM alert_rule_states WHERE rule_id = ?`, ruleID); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = ?`, ruleID); err != nil {
			return err
		}
	}
	for _, eventType := range retiredAdminNotificationEventTypes {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM notification_types WHERE event_type = ?`, eventType); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE alert_rules SET description = '' WHERE description <> ''`)
	return err
}

func (s *SQLiteStore) AdminAlertRules(ctx context.Context) ([]AdminAlertRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, category, metric, comparator, threshold, threshold_unit, duration_sec,
		       enabled, notification_event_type, description, created_at, updated_at
		FROM alert_rules
		ORDER BY sort_order ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := make([]AdminAlertRule, 0)
	for rows.Next() {
		rule, err := scanAdminAlertRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachAlertRuleScopes(ctx, rules)
}

func (s *SQLiteStore) UpdateAdminAlertRule(ctx context.Context, ruleID string, update AdminAlertRuleUpdateRequest) (AdminAlertRule, error) {
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return AdminAlertRule{}, errAlertRuleNotFound
	}
	if err := update.normalize(); err != nil {
		return AdminAlertRule{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminAlertRule{}, err
	}
	defer rollbackUnlessCommitted(tx)

	var metric string
	if err := tx.QueryRowContext(ctx, `SELECT metric FROM alert_rules WHERE id = ?`, ruleID).Scan(&metric); err != nil {
		if err == sql.ErrNoRows {
			return AdminAlertRule{}, errAlertRuleNotFound
		}
		return AdminAlertRule{}, err
	}
	if update.Threshold != nil && metric == "expiry_days" {
		threshold := *update.Threshold
		thresholdDays := int(threshold)
		if threshold != float64(thresholdDays) || !allowedRenewalNoticeDays[thresholdDays] {
			return AdminAlertRule{}, errInvalidAdminAlertRuleUpdate
		}
	}
	sets := make([]string, 0, 4)
	args := make([]any, 0, 5)
	if update.Enabled != nil {
		sets = append(sets, "enabled = ?")
		if *update.Enabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if update.Threshold != nil {
		sets = append(sets, "threshold = ?")
		args = append(args, *update.Threshold)
	}
	if update.DurationSec != nil {
		sets = append(sets, "duration_sec = ?")
		args = append(args, *update.DurationSec)
	}
	now := time.Now().UTC().Unix()
	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, now, ruleID)
		if _, err := tx.ExecContext(ctx, "UPDATE alert_rules SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
			return AdminAlertRule{}, err
		}
	} else if update.ScopeNodeIDs != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE alert_rules SET updated_at = ? WHERE id = ?`, now, ruleID); err != nil {
			return AdminAlertRule{}, err
		}
	}
	if update.ScopeNodeIDs != nil {
		if err := replaceAlertRuleNodeScopes(ctx, tx, ruleID, *update.ScopeNodeIDs, now); err != nil {
			return AdminAlertRule{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AdminAlertRule{}, err
	}
	tx = nil
	return s.adminAlertRuleByID(ctx, ruleID)
}

func (s *SQLiteStore) adminAlertRuleByID(ctx context.Context, ruleID string) (AdminAlertRule, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, category, metric, comparator, threshold, threshold_unit, duration_sec,
		       enabled, notification_event_type, description, created_at, updated_at
		FROM alert_rules
		WHERE id = ?
	`, ruleID)
	rule, err := scanAdminAlertRule(row)
	if err != nil {
		return AdminAlertRule{}, err
	}
	rules, err := s.attachAlertRuleScopes(ctx, []AdminAlertRule{rule})
	if err != nil {
		return AdminAlertRule{}, err
	}
	if len(rules) == 0 {
		return AdminAlertRule{}, errAlertRuleNotFound
	}
	return rules[0], nil
}

func (s *SQLiteStore) attachAlertRuleScopes(ctx context.Context, rules []AdminAlertRule) ([]AdminAlertRule, error) {
	if len(rules) == 0 {
		return rules, nil
	}
	for index := range rules {
		scopeNodeIDs, err := s.alertRuleScopeNodeIDs(ctx, rules[index].ID)
		if err != nil {
			return nil, err
		}
		rules[index].ScopeNodeIDs = scopeNodeIDs
	}
	return rules, nil
}

func (s *SQLiteStore) alertRuleScopeNodeIDs(ctx context.Context, ruleID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT scope.node_id
		FROM alert_rule_node_scopes scope
		LEFT JOIN nodes n ON n.id = scope.node_id
		WHERE scope.rule_id = ?
		ORDER BY COALESCE(n.display_order, 0) ASC, scope.node_id ASC
	`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodeIDs := make([]string, 0)
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, err
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodeIDs, nil
}

func replaceAlertRuleNodeScopes(ctx context.Context, tx *sql.Tx, ruleID string, nodeIDs []string, now int64) error {
	for _, nodeID := range nodeIDs {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ?`, nodeID).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return errInvalidAdminAlertRuleUpdate
			}
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM alert_rule_node_scopes WHERE rule_id = ?`, ruleID); err != nil {
		return err
	}
	for _, nodeID := range nodeIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO alert_rule_node_scopes (rule_id, node_id, created_at)
			VALUES (?, ?, ?)
		`, ruleID, nodeID, now); err != nil {
			return err
		}
	}
	return nil
}

type adminAlertRuleScanner interface {
	Scan(dest ...any) error
}

func scanAdminAlertRule(scanner adminAlertRuleScanner) (AdminAlertRule, error) {
	var rule AdminAlertRule
	var enabled int
	var createdAt, updatedAt sql.NullInt64
	if err := scanner.Scan(
		&rule.ID,
		&rule.Name,
		&rule.Category,
		&rule.Metric,
		&rule.Comparator,
		&rule.Threshold,
		&rule.ThresholdUnit,
		&rule.DurationSec,
		&enabled,
		&rule.NotificationEventType,
		&rule.Description,
		&createdAt,
		&updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return AdminAlertRule{}, errAlertRuleNotFound
		}
		return AdminAlertRule{}, err
	}
	rule.Enabled = enabled != 0
	if label, ok := adminNotificationTypeLabel(rule.NotificationEventType); ok {
		rule.NotificationLabel = label
	}
	rule.CreatedAt = unixStringOr(createdAt, time.Now().UTC())
	rule.UpdatedAt = unixStringOr(updatedAt, time.Now().UTC())
	return rule, nil
}
