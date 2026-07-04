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
		DurationSec:           300,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
		Description:           "CPU 使用率持续超过阈值时进入异常通知类型。",
	},
	{
		ID:                    "memory_high",
		Name:                  "内存使用率",
		Category:              "resource",
		Metric:                "memory_percent",
		Comparator:            ">=",
		Threshold:             90,
		ThresholdUnit:         "%",
		DurationSec:           300,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
		Description:           "内存使用率持续超过阈值时进入异常通知类型。",
	},
	{
		ID:                    "disk_high",
		Name:                  "磁盘使用率",
		Category:              "resource",
		Metric:                "disk_percent",
		Comparator:            ">=",
		Threshold:             90,
		ThresholdUnit:         "%",
		DurationSec:           600,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
		Description:           "磁盘使用率持续超过阈值时进入异常通知类型。",
	},
	{
		ID:                    "probe_latency_high",
		Name:                  "探测延迟",
		Category:              "probe",
		Metric:                "probe_median_ms",
		Comparator:            ">=",
		Threshold:             800,
		ThresholdUnit:         "ms",
		DurationSec:           180,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
		Description:           "探测中位延迟持续超过阈值时进入异常通知类型。",
	},
	{
		ID:                    "probe_loss_high",
		Name:                  "探测丢包",
		Category:              "probe",
		Metric:                "probe_loss_percent",
		Comparator:            ">=",
		Threshold:             50,
		ThresholdUnit:         "%",
		DurationSec:           180,
		Enabled:               true,
		NotificationEventType: "probe_unhealthy",
		Description:           "探测丢包持续超过阈值时进入异常通知类型。",
	},
	{
		ID:                    "node_offline",
		Name:                  "离线判定",
		Category:              "liveness",
		Metric:                "heartbeat_age_sec",
		Comparator:            ">=",
		Threshold:             180,
		ThresholdUnit:         "s",
		DurationSec:           180,
		Enabled:               true,
		NotificationEventType: "node_offline",
		Description:           "Agent 心跳超过离线窗口后映射为离线通知类型。",
	},
	{
		ID:                    "node_recovered",
		Name:                  "恢复判定",
		Category:              "liveness",
		Metric:                "public_status",
		Comparator:            "transition_to_online",
		Threshold:             0,
		ThresholdUnit:         "status",
		DurationSec:           0,
		Enabled:               true,
		NotificationEventType: "node_online",
		Description:           "离线或异常恢复到在线时映射为上线通知类型。",
	},
}

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
	}
	return nil
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

func (s *SQLiteStore) AdminAlertRuleStates(ctx context.Context) ([]AdminAlertRuleState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ars.node_id, n.display_name, n.status, n.disabled, n.last_seen_at,
		       ar.id, ar.name, ar.category, ar.metric, ar.comparator, ar.threshold,
		       ar.threshold_unit, ar.duration_sec, ar.enabled, ars.last_value, ars.active,
		       ar.notification_event_type, ars.first_seen_at, ars.last_seen_at, ars.updated_at,
		       CASE
		         WHEN NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		           OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ars.node_id)
		         THEN 1 ELSE 0
		       END AS scope_applies
		FROM alert_rule_states ars
		JOIN alert_rules ar ON ar.id = ars.rule_id
		JOIN nodes n ON n.id = ars.node_id
		ORDER BY ars.active DESC, ars.updated_at DESC, ar.sort_order ASC, ar.id ASC, n.display_name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	states := make([]AdminAlertRuleState, 0)
	now := time.Now().UTC()
	for rows.Next() {
		var state AdminAlertRuleState
		var storedNodeStatus string
		var nodeLastSeenAt, firstSeenAt, lastSeenAt, updatedAt sql.NullInt64
		var enabled, active, disabled, scopeApplies int
		var lastValue sql.NullFloat64
		if err := rows.Scan(
			&state.NodeID,
			&state.NodeName,
			&storedNodeStatus,
			&disabled,
			&nodeLastSeenAt,
			&state.RuleID,
			&state.RuleName,
			&state.Category,
			&state.Metric,
			&state.Comparator,
			&state.Threshold,
			&state.ThresholdUnit,
			&state.DurationSec,
			&enabled,
			&lastValue,
			&active,
			&state.NotificationEventType,
			&firstSeenAt,
			&lastSeenAt,
			&updatedAt,
			&scopeApplies,
		); err != nil {
			return nil, err
		}
		state.Enabled = enabled != 0
		nodeDisabled := disabled != 0
		valueMatchesCurrentRule := active != 0
		if lastValue.Valid {
			valueMatchesCurrentRule = compareAlertRuleValue(lastValue.Float64, state.Comparator, state.Threshold)
		}
		state.Active = state.Enabled && !nodeDisabled && scopeApplies != 0 && valueMatchesCurrentRule
		state.NodeStatus = publicNodeStatus(storedNodeStatus, nodeLastSeenAt, now)
		if nodeDisabled {
			state.NodeStatus = "disabled"
		}
		state.LastValue = floatPtr(lastValue)
		if label, ok := adminNotificationTypeLabel(state.NotificationEventType); ok {
			state.NotificationLabel = label
		}
		state.FirstSeenAt = unixStringOr(firstSeenAt, now)
		state.LastSeenAt = unixStringOr(lastSeenAt, now)
		state.UpdatedAt = unixStringOr(updatedAt, now)
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return states, nil
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

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM alert_rules WHERE id = ?`, ruleID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return AdminAlertRule{}, errAlertRuleNotFound
		}
		return AdminAlertRule{}, err
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
