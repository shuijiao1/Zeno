package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"
)

func (s *SQLiteStore) CreateAdminNode(ctx context.Context, create AdminNodeCreateRequest) (AdminNode, error) {
	if err := create.normalize(); err != nil {
		return AdminNode{}, err
	}
	nodeID := normalizeAdminNodeID(create.ID)
	if nodeID == "" {
		generated, err := generatedAdminNodeID(create.DisplayName)
		if err != nil {
			return AdminNode{}, err
		}
		nodeID = generated
	}
	credential, err := randomAdminCredential()
	if err != nil {
		return AdminNode{}, err
	}
	now := time.Now().UTC().Unix()
	disabled := 0
	if create.Disabled {
		disabled = 1
	}
	var quota any
	if create.MonthlyQuotaBytes.Set && create.MonthlyQuotaBytes.Valid {
		quota = create.MonthlyQuotaBytes.Value
	}
	monthlyResetDay := 1
	if create.MonthlyResetDay != nil {
		monthlyResetDay = *create.MonthlyResetDay
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminNode{}, err
	}
	defer rollbackUnlessCommitted(tx)

	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO nodes (id, display_name, token_hash, install_token, status, country_code, region, expiry_date, billing_cycle, display_order, public_ipv4, public_ipv6, billing_mode, monthly_quota_bytes, monthly_reset_day, disabled, created_at, updated_at, last_seen_at)
		VALUES (?, ?, ?, ?, 'no_data', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, nodeID, create.DisplayName, hashAgentToken(credential), credential, nullIfEmpty(create.CountryCode), nullIfEmpty(create.Region), nullIfEmpty(create.ExpiryDate), nullIfEmpty(create.BillingCycle), create.DisplayOrder, nullIfEmpty(create.PublicIPv4), nullIfEmpty(create.PublicIPv6), create.BillingMode, quota, monthlyResetDay, disabled, now, now)
	if err != nil {
		return AdminNode{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AdminNode{}, err
	}
	if affected == 0 {
		return AdminNode{}, errNodeAlreadyExists
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO node_probe_targets (node_id, target_id, enabled)
		SELECT ?, id, 0 FROM probe_targets WHERE enabled = 1
	`, nodeID); err != nil {
		return AdminNode{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminNode{}, err
	}
	tx = nil
	return s.adminNodeByID(ctx, nodeID)
}

type AgentInstallCommands struct {
	Linux   string
	MacOS   string
	Windows string
}

func (commands AgentInstallCommands) Map() map[string]string {
	return map[string]string{
		"linux":   commands.Linux,
		"macos":   commands.MacOS,
		"windows": commands.Windows,
	}
}

func (s *SQLiteStore) AdminNodeInstallCommand(ctx context.Context, nodeID, controllerURL, agentVersion string) (AgentInstallCommands, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || strings.Contains(nodeID, "/") {
		return AgentInstallCommands{}, errNodeNotFound
	}
	var installToken sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT install_token FROM nodes WHERE id = ?`, nodeID).Scan(&installToken); err != nil {
		return AgentInstallCommands{}, errNodeNotFound
	}
	credential := strings.TrimSpace(installToken.String)
	if credential == "" {
		generated, err := randomAdminCredential()
		if err != nil {
			return AgentInstallCommands{}, err
		}
		credential = generated
		now := time.Now().UTC().Unix()
		result, err := s.db.ExecContext(ctx, `UPDATE nodes SET token_hash = ?, install_token = ?, updated_at = ? WHERE id = ?`, hashAgentToken(credential), credential, now, nodeID)
		if err != nil {
			return AgentInstallCommands{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return AgentInstallCommands{}, err
		}
		if affected == 0 {
			return AgentInstallCommands{}, errNodeNotFound
		}
	}
	return buildAgentInstallCommands(controllerURL, nodeID, credential, agentVersion), nil
}

func (s *SQLiteStore) DeleteAdminNode(ctx context.Context, nodeID string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || strings.Contains(nodeID, "/") {
		return errNodeNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)

	if _, err := tx.ExecContext(ctx, `DELETE FROM probe_samples WHERE round_id IN (SELECT id FROM probe_rounds WHERE node_id = ?)`, nodeID); err != nil {
		return err
	}
	for _, statement := range []string{
		`DELETE FROM probe_rounds WHERE node_id = ?`,
		`DELETE FROM state_samples WHERE node_id = ?`,
		`DELETE FROM traffic_monthly WHERE node_id = ?`,
		`DELETE FROM node_probe_targets WHERE node_id = ?`,
		`DELETE FROM alert_rule_states WHERE node_id = ?`,
		`DELETE FROM host_info WHERE node_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, nodeID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE alert_rules
		SET enabled = 0, updated_at = ?
		WHERE id IN (
			SELECT scope.rule_id
			FROM alert_rule_node_scopes scope
			WHERE scope.node_id = ?
			  AND (SELECT COUNT(*) FROM alert_rule_node_scopes all_scope WHERE all_scope.rule_id = scope.rule_id) = 1
		)
	`, time.Now().UTC().Unix(), nodeID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, nodeID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errNodeNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) adminNodeByID(ctx context.Context, nodeID string) (AdminNode, error) {
	nodes, err := s.AdminNodes(ctx)
	if err != nil {
		return AdminNode{}, err
	}
	for _, node := range nodes {
		if node.ID == nodeID {
			return node, nil
		}
	}
	return AdminNode{}, errNodeNotFound
}

func generatedAdminNodeID(displayName string) (string, error) {
	base := normalizeAdminNodeID(displayName)
	if base == "" {
		base = "node"
	}
	suffix, err := randomHex(4)
	if err != nil {
		return "", err
	}
	return base + "-" + suffix, nil
}

func normalizeAdminNodeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if r == '-' || r == '_' || unicode.IsSpace(r) {
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= 48 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func randomAdminCredential() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func buildAgentInstallCommands(controllerURL, nodeID, credential, agentVersion string) AgentInstallCommands {
	controllerURL = strings.TrimRight(strings.TrimSpace(controllerURL), "/")
	if controllerURL == "" {
		controllerURL = "http://127.0.0.1:18980"
	}
	versionEnv := ""
	windowsVersionEnv := ""
	if strings.TrimSpace(agentVersion) != "" {
		version := strings.TrimSpace(agentVersion)
		versionEnv = " ZENO_AGENT_VERSION=" + shellSingleQuote(version)
		windowsVersionEnv = "$env:ZENO_AGENT_VERSION=" + powershellSingleQuote(version) + "; "
	}
	installURL := "https://raw.githubusercontent.com/shuijiao1/Zeno-Agent/main/install.sh"
	windowsInstallURL := "https://raw.githubusercontent.com/shuijiao1/Zeno-Agent/main/install.ps1"
	return AgentInstallCommands{
		Linux:   fmt.Sprintf(`curl -fsSL %s | sudo env ZENO_CONTROLLER_URL=%s ZENO_NODE_ID=%s ZENO_AGENT_TOKEN=%s%s bash`, shellSingleQuote(installURL), shellSingleQuote(controllerURL), shellSingleQuote(nodeID), shellSingleQuote(credential), versionEnv),
		MacOS:   fmt.Sprintf(`curl -fsSL %s | sudo env ZENO_CONTROLLER_URL=%s ZENO_NODE_ID=%s ZENO_AGENT_TOKEN=%s%s bash`, shellSingleQuote(installURL), shellSingleQuote(controllerURL), shellSingleQuote(nodeID), shellSingleQuote(credential), versionEnv),
		Windows: fmt.Sprintf(`powershell -NoProfile -ExecutionPolicy Bypass -Command "%s$env:ZENO_CONTROLLER_URL=%s; $env:ZENO_NODE_ID=%s; $env:ZENO_AGENT_TOKEN=%s; irm %s | iex"`, windowsVersionEnv, powershellSingleQuote(controllerURL), powershellSingleQuote(nodeID), powershellSingleQuote(credential), powershellSingleQuote(windowsInstallURL)),
	}
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func powershellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
