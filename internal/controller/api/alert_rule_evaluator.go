package api

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type probeAlertRuleTransitionStore interface {
	RecordAgentProbeAlertRuleTransition(ctx context.Context, nodeID string, ts time.Time, rounds []preparedAgentProbeRound) (notificationStatusTransition, error)
}

type stateAlertRuleTransitionStore interface {
	RecordAgentStateAlertRuleTransition(ctx context.Context, nodeID string, ts time.Time, state AgentStateRequest) (notificationStatusTransition, error)
}

func (s *SQLiteStore) RecordAgentProbeAlertRuleTransition(ctx context.Context, nodeID string, ts time.Time, rounds []preparedAgentProbeRound) (notificationStatusTransition, error) {
	return notificationStatusTransition{}, nil
}

func (s *SQLiteStore) RecordAgentStateAlertRuleTransition(ctx context.Context, nodeID string, ts time.Time, state AgentStateRequest) (notificationStatusTransition, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	defer rollbackUnlessCommitted(tx)

	rules, err := alertRulesForMetrics(ctx, tx, nodeID, map[string]bool{"cpu_percent": true, "memory_percent": true, "disk_percent": true})
	if err != nil {
		return notificationStatusTransition{}, err
	}
	values := stateAlertMetricValues(state)
	_, hadRelevantActive, err := setAlertRuleStates(ctx, tx, nodeID, ts, rules, values)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	status, err := aggregateAlertRuleStatus(ctx, tx, nodeID)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	transition, err := updateNodeStatusForAlertRules(ctx, tx, nodeID, ts, status, !hadRelevantActive)
	if err != nil {
		return notificationStatusTransition{}, err
	}
	if err := tx.Commit(); err != nil {
		return notificationStatusTransition{}, err
	}
	tx = nil
	return transition, nil
}

func alertRulesForMetrics(ctx context.Context, tx *sql.Tx, nodeID string, metrics map[string]bool) ([]AdminAlertRule, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, name, category, metric, comparator, threshold, threshold_unit, duration_sec,
		       enabled, notification_event_type, description, created_at, updated_at
		FROM alert_rules ar
		WHERE NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		   OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ?)
		ORDER BY sort_order ASC, id ASC
	`, nodeID)
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
		if metrics[rule.Metric] {
			rules = append(rules, rule)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

func setAlertRuleStates(ctx context.Context, tx *sql.Tx, nodeID string, ts time.Time, rules []AdminAlertRule, values map[string]*float64) (bool, bool, error) {
	anyActive := false
	hadRelevantActive := false
	seenAt := ts.UTC().Unix()
	now := time.Now().UTC().Unix()
	for _, rule := range rules {
		var previousActive int
		err := tx.QueryRowContext(ctx, `SELECT active FROM alert_rule_states WHERE node_id = ? AND rule_id = ?`, nodeID, rule.ID).Scan(&previousActive)
		if err != nil && err != sql.ErrNoRows {
			return false, false, err
		}
		if previousActive != 0 {
			hadRelevantActive = true
		}
		value := values[rule.Metric]
		active := rule.Enabled && value != nil && compareAlertRuleValue(*value, rule.Comparator, rule.Threshold)
		if active {
			anyActive = true
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO alert_rule_states (node_id, rule_id, active, first_seen_at, last_seen_at, last_value, updated_at)
				VALUES (?, ?, 1, ?, ?, ?, ?)
				ON CONFLICT(node_id, rule_id) DO UPDATE SET
					active = 1,
					first_seen_at = CASE
						WHEN alert_rule_states.active = 1 AND alert_rule_states.first_seen_at IS NOT NULL THEN alert_rule_states.first_seen_at
						ELSE excluded.first_seen_at
					END,
					last_seen_at = excluded.last_seen_at,
					last_value = excluded.last_value,
					updated_at = excluded.updated_at
			`, nodeID, rule.ID, seenAt, seenAt, *value, now); err != nil {
				return false, false, err
			}
			continue
		}
		var lastValue any
		if value != nil {
			lastValue = *value
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert_rule_states
			SET active = 0, last_seen_at = ?, last_value = ?, updated_at = ?
			WHERE node_id = ? AND rule_id = ?
		`, seenAt, lastValue, now, nodeID, rule.ID); err != nil {
			return false, false, err
		}
	}
	return anyActive, hadRelevantActive, nil
}

func aggregateAlertRuleStatus(ctx context.Context, tx *sql.Tx, nodeID string) (string, error) {
	var activeRules int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM alert_rule_states ars
		JOIN alert_rules ar ON ar.id = ars.rule_id
		WHERE ars.node_id = ?
		  AND ars.active = 1
		  AND ar.enabled = 1
		  AND ar.notification_event_type = 'probe_unhealthy'
		  AND (
		    NOT EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_all WHERE scope_all.rule_id = ar.id)
		    OR EXISTS (SELECT 1 FROM alert_rule_node_scopes scope_node WHERE scope_node.rule_id = ar.id AND scope_node.node_id = ?)
		  )
	`, nodeID, nodeID).Scan(&activeRules); err != nil {
		return "", err
	}
	if activeRules > 0 {
		return "warning", nil
	}
	return "online", nil
}

func updateNodeStatusForAlertRules(ctx context.Context, tx *sql.Tx, nodeID string, ts time.Time, status string, preserveExistingWarning bool) (notificationStatusTransition, error) {
	now := time.Now().UTC()
	nowUnix := now.Unix()
	seenAt := ts.UTC().Unix()
	var previous notificationNodeSnapshot
	var storedStatus string
	var lastSeenAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT id, display_name, status, last_seen_at
		FROM nodes
		WHERE id = ? AND disabled = 0
	`, nodeID).Scan(&previous.ID, &previous.DisplayName, &storedStatus, &lastSeenAt); err != nil {
		if err == sql.ErrNoRows {
			return notificationStatusTransition{}, errNodeNotFound
		}
		return notificationStatusTransition{}, err
	}
	previous.Status = publicNodeStatus(storedStatus, lastSeenAt, now)
	nextStatus := status
	if preserveExistingWarning && status == "online" && storedStatus == "warning" {
		nextStatus = "warning"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes
		SET status = ?, last_seen_at = ?, updated_at = ?
		WHERE id = ? AND disabled = 0
	`, nextStatus, seenAt, nowUnix, nodeID); err != nil {
		return notificationStatusTransition{}, err
	}
	current := notificationNodeSnapshot{ID: previous.ID, DisplayName: previous.DisplayName, Status: publicNodeStatus(nextStatus, sql.NullInt64{Int64: seenAt, Valid: true}, now)}
	return notificationStatusTransition{Previous: previous, Current: current}, nil
}

func stateAlertMetricValues(state AgentStateRequest) map[string]*float64 {
	return map[string]*float64{
		"cpu_percent":    floatValuePtr(state.CPUPercent),
		"memory_percent": percentValuePtr(state.MemoryUsedBytes, state.MemoryTotalBytes),
		"disk_percent":   percentValuePtr(state.DiskUsedBytes, state.DiskTotalBytes),
	}
}

func compareAlertRuleValue(value float64, comparator string, threshold float64) bool {
	switch strings.TrimSpace(comparator) {
	case ">=":
		return value >= threshold
	case ">":
		return value > threshold
	case "<=":
		return value <= threshold
	case "<":
		return value < threshold
	case "=", "==":
		return value == threshold
	default:
		return false
	}
}

func floatValuePtr(value float64) *float64 {
	converted := value
	return &converted
}

func percentValuePtr(used, total int64) *float64 {
	if total <= 0 || used < 0 {
		return nil
	}
	converted := float64(used) / float64(total) * 100
	return &converted
}
