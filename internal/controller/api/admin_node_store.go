package api

import (
	"context"
	"crypto/rand"
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

	hideForGuest := 0
	if create.HideForGuest {
		hideForGuest = 1
	}
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO nodes (id, display_name, token_hash, status, country_code, region, expiry_date, billing_cycle, display_order, public_ipv4, public_ipv6, billing_mode, monthly_quota_bytes, monthly_reset_day, hide_for_guest, disabled, created_at, updated_at, last_seen_at)
		VALUES (?, ?, ?, 'no_data', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, nodeID, create.DisplayName, hashAgentToken(credential), nullIfEmpty(create.CountryCode), nullIfEmpty(create.Region), nullIfEmpty(create.ExpiryDate), nullIfEmpty(create.BillingCycle), create.DisplayOrder, nullIfEmpty(create.PublicIPv4), nullIfEmpty(create.PublicIPv6), create.BillingMode, quota, monthlyResetDay, hideForGuest, disabled, now, now)
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
		SELECT ?, id, 1 FROM probe_targets WHERE enabled = 1
	`, nodeID); err != nil {
		return AdminNode{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminNode{}, err
	}
	tx = nil
	return s.adminNodeByID(ctx, nodeID)
}

func (s *SQLiteStore) AdminNodeInstallCommand(ctx context.Context, nodeID, controllerURL, agentVersion string) (string, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || strings.Contains(nodeID, "/") {
		return "", errNodeNotFound
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ?`, nodeID).Scan(&exists); err != nil {
		return "", errNodeNotFound
	}
	credential, err := randomAdminCredential()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET token_hash = ?, updated_at = ? WHERE id = ?`, hashAgentToken(credential), now, nodeID)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected == 0 {
		return "", errNodeNotFound
	}
	return buildAgentInstallCommand(controllerURL, nodeID, credential, agentVersion), nil
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

func buildAgentInstallCommand(controllerURL, nodeID, credential, agentVersion string) string {
	controllerURL = strings.TrimRight(strings.TrimSpace(controllerURL), "/")
	if controllerURL == "" {
		controllerURL = "http://127.0.0.1:18980"
	}
	versionArg := ""
	if strings.TrimSpace(agentVersion) != "" {
		versionArg = " -version " + shellSingleQuote(strings.TrimSpace(agentVersion))
	}
	binaryURL := controllerURL + "/api/public/v1/agent/linux-amd64"
	return fmt.Sprintf(`curl -fsSL %s -o /tmp/zeno-agent && \
sudo install -m 755 /tmp/zeno-agent /usr/local/bin/zeno-agent && \
sudo install -d -m 700 /etc/zeno && \
printf '%%s\n' %s | sudo tee /etc/zeno/agent-token >/dev/null && \
sudo tee /etc/systemd/system/zeno-agent.service >/dev/null <<'ZENO_SERVICE'
[Unit]
Description=Zeno Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/zeno-agent -controller-url %s -node-id %s -token-file /etc/zeno/agent-token -interval 60s%s
Restart=always
RestartSec=5s

[Install]
WantedBy=multi-user.target
ZENO_SERVICE
sudo systemctl daemon-reload && sudo systemctl enable --now zeno-agent`, shellSingleQuote(binaryURL), shellSingleQuote(credential), shellSingleQuote(controllerURL), shellSingleQuote(nodeID), versionArg)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
