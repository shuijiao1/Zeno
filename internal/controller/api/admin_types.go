package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/url"
	"strings"
	"time"
)

var (
	errInvalidAdminSettingsUpdate           = errors.New("invalid admin settings update")
	errInvalidAdminNodeUpdate               = errors.New("invalid admin node update")
	errInvalidAdminNodeCreate               = errors.New("invalid admin node create")
	errNodeAlreadyExists                    = errors.New("node already exists")
	errInvalidAdminTargetWrite              = errors.New("invalid admin probe target write")
	errProbeTargetNotFound                  = errors.New("probe target not found")
	errProbeTargetAlreadyExists             = errors.New("probe target already exists")
	errInvalidAdminNotificationChannelWrite = errors.New("invalid admin notification channel write")
	errNotificationChannelNotFound          = errors.New("notification channel not found")
	errNotificationChannelAlreadyExists     = errors.New("notification channel already exists")
	errInvalidAdminNotificationTypeWrite    = errors.New("invalid admin notification type write")
	errNotificationTypeNotFound             = errors.New("notification type not found")
	errInvalidAdminAlertRuleUpdate          = errors.New("invalid admin alert rule update")
	errAlertRuleNotFound                    = errors.New("alert rule not found")
)

type AdminSettingsResponse struct {
	Settings SiteSettings `json:"settings"`
}

type AdminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AdminLoginResponse struct {
	Username string `json:"username"`
	Token    string `json:"token"`
}

type AdminAccountResponse struct {
	Account AdminAccount `json:"account"`
}

type AdminAccountUpdateRequest struct {
	Username        string `json:"username"`
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type SiteSettings struct {
	SiteTitle            string `json:"site_title"`
	SiteSubtitle         string `json:"site_subtitle"`
	LogoURL              string `json:"logo_url"`
	Theme                string `json:"theme"`
	AgentControllerURL   string `json:"agent_controller_url"`
	BackgroundURL        string `json:"background_url"`
	DesktopBackgroundURL string `json:"desktop_background_url"`
	MobileBackgroundURL  string `json:"mobile_background_url"`
	UpdatedAt            string `json:"updated_at,omitempty"`
}

type AdminSettingsUpdateRequest struct {
	SiteTitle            *string `json:"site_title,omitempty"`
	SiteSubtitle         *string `json:"site_subtitle,omitempty"`
	LogoURL              *string `json:"logo_url,omitempty"`
	Theme                *string `json:"theme,omitempty"`
	AgentControllerURL   *string `json:"agent_controller_url,omitempty"`
	BackgroundURL        *string `json:"background_url,omitempty"`
	DesktopBackgroundURL *string `json:"desktop_background_url,omitempty"`
	MobileBackgroundURL  *string `json:"mobile_background_url,omitempty"`
}

func defaultSiteSettings() SiteSettings {
	return SiteSettings{
		SiteTitle:            "Zeno",
		SiteSubtitle:         "服务器运行概览",
		LogoURL:              "/assets/logo/id.png",
		Theme:                "system",
		AgentControllerURL:   "",
		BackgroundURL:        "",
		DesktopBackgroundURL: "",
		MobileBackgroundURL:  "",
	}
}

func (request *AdminSettingsUpdateRequest) normalize() error {
	changed := false
	if request.SiteTitle != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.SiteTitle)
		if trimmed == "" || len([]rune(trimmed)) > 64 {
			return errInvalidAdminSettingsUpdate
		}
		request.SiteTitle = &trimmed
	}
	if request.SiteSubtitle != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.SiteSubtitle)
		if len([]rune(trimmed)) > 140 {
			return errInvalidAdminSettingsUpdate
		}
		request.SiteSubtitle = &trimmed
	}
	if request.LogoURL != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.LogoURL)
		if trimmed == "" || !validSettingsAssetURL(trimmed) {
			return errInvalidAdminSettingsUpdate
		}
		request.LogoURL = &trimmed
	}
	if request.Theme != nil {
		changed = true
		trimmed := strings.ToLower(strings.TrimSpace(*request.Theme))
		if !validSettingsTheme(trimmed) {
			return errInvalidAdminSettingsUpdate
		}
		request.Theme = &trimmed
	}
	if request.AgentControllerURL != nil {
		changed = true
		trimmed := strings.TrimRight(strings.TrimSpace(*request.AgentControllerURL), "/")
		if trimmed != "" && !validAgentControllerURL(trimmed) {
			return errInvalidAdminSettingsUpdate
		}
		request.AgentControllerURL = &trimmed
	}
	if request.BackgroundURL != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.BackgroundURL)
		if trimmed != "" && !validSettingsAssetURL(trimmed) {
			return errInvalidAdminSettingsUpdate
		}
		request.BackgroundURL = &trimmed
	}
	if request.DesktopBackgroundURL != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.DesktopBackgroundURL)
		if trimmed != "" && !validSettingsAssetURL(trimmed) {
			return errInvalidAdminSettingsUpdate
		}
		request.DesktopBackgroundURL = &trimmed
	}
	if request.MobileBackgroundURL != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.MobileBackgroundURL)
		if trimmed != "" && !validSettingsAssetURL(trimmed) {
			return errInvalidAdminSettingsUpdate
		}
		request.MobileBackgroundURL = &trimmed
	}
	if !changed {
		return errInvalidAdminSettingsUpdate
	}
	return nil
}

func validSettingsTheme(theme string) bool {
	switch theme {
	case "system", "dark", "light":
		return true
	default:
		return false
	}
}

func validSettingsAssetURL(value string) bool {
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return true
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "https"
}

func validAgentControllerURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func normalizeAdminNodeDate(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := time.Parse("2006-01-02", trimmed)
	if err != nil || parsed.Format("2006-01-02") != trimmed {
		return "", errInvalidAdminNodeUpdate
	}
	return trimmed, nil
}

func normalizeAdminNodeShortText(value string, maxRunes int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len([]rune(trimmed)) > maxRunes {
		return "", errInvalidAdminNodeUpdate
	}
	return trimmed, nil
}

func normalizeAdminNodeIP(value string, family int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	parsed := net.ParseIP(trimmed)
	if parsed == nil {
		return "", errInvalidAdminNodeUpdate
	}
	if family == 4 {
		ipv4 := parsed.To4()
		if ipv4 == nil {
			return "", errInvalidAdminNodeUpdate
		}
		return ipv4.String(), nil
	}
	if family == 6 {
		if parsed.To4() != nil || parsed.To16() == nil {
			return "", errInvalidAdminNodeUpdate
		}
		return parsed.String(), nil
	}
	return "", errInvalidAdminNodeUpdate
}

func normalizeAdminNodeBillingMode(value string) (string, bool) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		return "both", true
	}
	switch mode {
	case "in", "download", "inbound":
		return "in", true
	case "out", "upload", "outbound":
		return "out", true
	case "both", "sum", "total":
		return "both", true
	case "max", "higher":
		return "max", true
	default:
		return "", false
	}
}

func normalizeAdminNodeMonthlyResetDay(value int) (int, bool) {
	if value == 0 {
		return 1, true
	}
	if value < 1 || value > 31 {
		return 0, false
	}
	return value, true
}

// AdminNodesResponse is the authenticated management view for node inventory.
// It intentionally omits token hashes and other credentials.
type AdminNodesResponse struct {
	Nodes []AdminNode `json:"nodes"`
}

type AdminNodeResponse struct {
	Node AdminNode `json:"node"`
}

type AdminNodeInstallCommandResponse struct {
	NodeID   string            `json:"node_id"`
	Command  string            `json:"command"`
	Commands map[string]string `json:"commands,omitempty"`
}

type AdminNodeCreateRequest struct {
	ID                string             `json:"id,omitempty"`
	DisplayName       string             `json:"display_name"`
	InstallToken      string             `json:"install_token,omitempty"`
	CountryCode       string             `json:"country_code,omitempty"`
	Region            string             `json:"region,omitempty"`
	ExpiryDate        string             `json:"expiry_date,omitempty"`
	BillingCycle      string             `json:"billing_cycle,omitempty"`
	BillingMode       string             `json:"billing_mode,omitempty"`
	MonthlyResetDay   *int               `json:"monthly_reset_day,omitempty"`
	DisplayOrder      int                `json:"display_order,omitempty"`
	PublicIPv4        string             `json:"public_ipv4,omitempty"`
	PublicIPv6        string             `json:"public_ipv6,omitempty"`
	MonthlyQuotaBytes adminOptionalInt64 `json:"monthly_quota_bytes,omitempty"`
	Disabled          bool               `json:"disabled,omitempty"`
}

func (request *AdminNodeCreateRequest) normalize() error {
	trimmedName := strings.TrimSpace(request.DisplayName)
	if trimmedName == "" {
		return errInvalidAdminNodeCreate
	}
	request.DisplayName = trimmedName
	request.ID = strings.TrimSpace(request.ID)
	request.InstallToken = strings.TrimSpace(request.InstallToken)
	if len(request.InstallToken) > 256 {
		return errInvalidAdminNodeCreate
	}
	request.CountryCode = strings.ToUpper(strings.TrimSpace(request.CountryCode))
	if len(request.CountryCode) > 8 {
		return errInvalidAdminNodeCreate
	}
	request.Region = strings.TrimSpace(request.Region)
	expiryDate, err := normalizeAdminNodeDate(request.ExpiryDate)
	if err != nil {
		return errInvalidAdminNodeCreate
	}
	request.ExpiryDate = expiryDate
	billingCycle, err := normalizeAdminNodeShortText(request.BillingCycle, 64)
	if err != nil {
		return errInvalidAdminNodeCreate
	}
	request.BillingCycle = billingCycle
	billingMode, ok := normalizeAdminNodeBillingMode(request.BillingMode)
	if !ok {
		return errInvalidAdminNodeCreate
	}
	request.BillingMode = billingMode
	if request.MonthlyResetDay == nil {
		defaultResetDay := 1
		request.MonthlyResetDay = &defaultResetDay
	} else {
		if *request.MonthlyResetDay == 0 {
			return errInvalidAdminNodeCreate
		}
		resetDay, ok := normalizeAdminNodeMonthlyResetDay(*request.MonthlyResetDay)
		if !ok {
			return errInvalidAdminNodeCreate
		}
		request.MonthlyResetDay = &resetDay
	}
	if request.DisplayOrder < 0 {
		return errInvalidAdminNodeCreate
	}
	publicIPv4, err := normalizeAdminNodeIP(request.PublicIPv4, 4)
	if err != nil {
		return errInvalidAdminNodeCreate
	}
	request.PublicIPv4 = publicIPv4
	publicIPv6, err := normalizeAdminNodeIP(request.PublicIPv6, 6)
	if err != nil {
		return errInvalidAdminNodeCreate
	}
	request.PublicIPv6 = publicIPv6
	if request.MonthlyQuotaBytes.Set && request.MonthlyQuotaBytes.Valid && request.MonthlyQuotaBytes.Value < 0 {
		return errInvalidAdminNodeCreate
	}
	return nil
}

type AdminProbeTargetsResponse struct {
	Targets []AdminProbeTarget `json:"targets"`
}

type AdminProbeTargetResponse struct {
	Target AdminProbeTarget `json:"target"`
}

type AdminProbeTargetCreateRequest struct {
	ID           string                             `json:"id,omitempty"`
	Name         string                             `json:"name"`
	Type         string                             `json:"type"`
	Address      string                             `json:"address"`
	Port         adminOptionalInt64                 `json:"port,omitempty"`
	Count        int                                `json:"count"`
	TimeoutMS    int                                `json:"timeout_ms"`
	IntervalSec  int                                `json:"interval_sec"`
	DisplayOrder int                                `json:"display_order,omitempty"`
	Enabled      *bool                              `json:"enabled,omitempty"`
	Assignments  []AdminProbeTargetAssignmentUpdate `json:"assignments,omitempty"`
}

func (request *AdminProbeTargetCreateRequest) normalize() error {
	request.ID = normalizeAdminNodeID(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	normalizedType, ok := normalizeAdminProbeTargetType(request.Type)
	request.Type = normalizedType
	request.Address = strings.TrimSpace(request.Address)
	if request.Name == "" || request.Address == "" || !ok || request.Count <= 0 || request.TimeoutMS <= 0 || request.IntervalSec <= 0 {
		return errInvalidAdminTargetWrite
	}
	if request.DisplayOrder < 0 {
		return errInvalidAdminTargetWrite
	}
	if request.Assignments != nil {
		seen := map[string]struct{}{}
		for index := range request.Assignments {
			trimmed := strings.TrimSpace(request.Assignments[index].NodeID)
			if trimmed == "" {
				return errInvalidAdminTargetWrite
			}
			if _, exists := seen[trimmed]; exists {
				return errInvalidAdminTargetWrite
			}
			seen[trimmed] = struct{}{}
			request.Assignments[index].NodeID = trimmed
		}
	}
	if request.Type == "tcping" {
		if !request.Port.Set || !request.Port.Valid || !validPort(request.Port.Value) {
			return errInvalidAdminTargetWrite
		}
		return nil
	}
	if request.Type == "http_get" && !validHTTPGetTargetAddress(request.Address) {
		return errInvalidAdminTargetWrite
	}
	if request.Port.Set && request.Port.Valid {
		return errInvalidAdminTargetWrite
	}
	request.Port.Set = true
	request.Port.Valid = false
	request.Port.Value = 0
	return nil
}

type AdminProbeTargetUpdateRequest struct {
	Name         *string                            `json:"name,omitempty"`
	Type         *string                            `json:"type,omitempty"`
	Address      *string                            `json:"address,omitempty"`
	Port         adminOptionalInt64                 `json:"port,omitempty"`
	Count        *int                               `json:"count,omitempty"`
	TimeoutMS    *int                               `json:"timeout_ms,omitempty"`
	IntervalSec  *int                               `json:"interval_sec,omitempty"`
	DisplayOrder *int                               `json:"display_order,omitempty"`
	Enabled      *bool                              `json:"enabled,omitempty"`
	Assignments  []AdminProbeTargetAssignmentUpdate `json:"assignments,omitempty"`
}

type AdminProbeTargetAssignmentUpdate struct {
	NodeID  string `json:"node_id"`
	Enabled bool   `json:"enabled"`
}

func (request *AdminProbeTargetUpdateRequest) normalize() error {
	changed := false
	if request.Name != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.Name)
		if trimmed == "" {
			return errInvalidAdminTargetWrite
		}
		request.Name = &trimmed
	}
	if request.Type != nil {
		changed = true
		normalizedType, ok := normalizeAdminProbeTargetType(*request.Type)
		if !ok {
			return errInvalidAdminTargetWrite
		}
		request.Type = &normalizedType
		if normalizedType == "ping" || normalizedType == "http_get" {
			if request.Port.Set && request.Port.Valid {
				return errInvalidAdminTargetWrite
			}
			request.Port.Set = true
			request.Port.Valid = false
			request.Port.Value = 0
		}
	}
	if request.Address != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.Address)
		if trimmed == "" {
			return errInvalidAdminTargetWrite
		}
		request.Address = &trimmed
	}
	if request.Port.Set {
		changed = true
		if request.Port.Valid && !validPort(request.Port.Value) {
			return errInvalidAdminTargetWrite
		}
	}
	if request.Count != nil {
		changed = true
		if *request.Count <= 0 {
			return errInvalidAdminTargetWrite
		}
	}
	if request.TimeoutMS != nil {
		changed = true
		if *request.TimeoutMS <= 0 {
			return errInvalidAdminTargetWrite
		}
	}
	if request.IntervalSec != nil {
		changed = true
		if *request.IntervalSec <= 0 {
			return errInvalidAdminTargetWrite
		}
	}
	if request.DisplayOrder != nil {
		changed = true
		if *request.DisplayOrder < 0 {
			return errInvalidAdminTargetWrite
		}
	}
	if request.Enabled != nil {
		changed = true
	}
	if request.Assignments != nil {
		changed = true
		if len(request.Assignments) == 0 {
			return errInvalidAdminTargetWrite
		}
		seen := map[string]struct{}{}
		for index := range request.Assignments {
			trimmed := strings.TrimSpace(request.Assignments[index].NodeID)
			if trimmed == "" {
				return errInvalidAdminTargetWrite
			}
			if _, exists := seen[trimmed]; exists {
				return errInvalidAdminTargetWrite
			}
			seen[trimmed] = struct{}{}
			request.Assignments[index].NodeID = trimmed
		}
	}
	if !changed {
		return errInvalidAdminTargetWrite
	}
	return nil
}

func normalizeAdminProbeTargetType(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tcp", "tcping":
		return "tcping", true
	case "icmp", "ping":
		return "ping", true
	case "http", "https", "http_get", "http-get":
		return "http_get", true
	default:
		return "", false
	}
}

func validHTTPGetTargetAddress(address string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(address))
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func validPort(port int64) bool {
	return port > 0 && port <= 65535
}

type AdminProbeTarget struct {
	ID           string                       `json:"id"`
	Name         string                       `json:"name"`
	Type         string                       `json:"type"`
	Address      string                       `json:"address"`
	Port         *int                         `json:"port"`
	Count        int                          `json:"count"`
	TimeoutMS    int                          `json:"timeout_ms"`
	IntervalSec  int                          `json:"interval_sec"`
	DisplayOrder int                          `json:"display_order"`
	Enabled      bool                         `json:"enabled"`
	Assignments  []AdminProbeTargetAssignment `json:"assignments"`
}

type AdminProbeTargetAssignment struct {
	NodeID          string `json:"node_id"`
	NodeDisplayName string `json:"node_display_name"`
	Enabled         bool   `json:"enabled"`
}

type AdminNotificationChannelsResponse struct {
	Channels []AdminNotificationChannel `json:"channels"`
}

type AdminNotificationChannelResponse struct {
	Channel AdminNotificationChannel `json:"channel"`
}

type AdminNotificationTypeResponse struct {
	Type AdminNotificationType `json:"type"`
}

type AdminAlertRulesResponse struct {
	Rules []AdminAlertRule `json:"rules"`
}

type AdminAlertRuleResponse struct {
	Rule AdminAlertRule `json:"rule"`
}

type AdminNotificationTestResponse struct {
	Delivery AdminNotificationDelivery `json:"delivery"`
}

type AdminNotificationChannelCreateRequest struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Destination string `json:"destination"`
	Credential  string `json:"credential"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

func (request *AdminNotificationChannelCreateRequest) normalize() error {
	request.ID = normalizeAdminNodeID(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Destination = strings.TrimSpace(request.Destination)
	request.Credential = strings.TrimSpace(request.Credential)
	if request.Name == "" || request.Destination == "" || request.Credential == "" {
		return errInvalidAdminNotificationChannelWrite
	}
	return nil
}

type AdminNotificationChannelUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Destination *string `json:"destination,omitempty"`
	Credential  *string `json:"credential,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

func (request *AdminNotificationChannelUpdateRequest) normalize() error {
	changed := false
	if request.Name != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.Name)
		if trimmed == "" {
			return errInvalidAdminNotificationChannelWrite
		}
		request.Name = &trimmed
	}
	if request.Destination != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.Destination)
		if trimmed == "" {
			return errInvalidAdminNotificationChannelWrite
		}
		request.Destination = &trimmed
	}
	if request.Credential != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.Credential)
		if trimmed == "" {
			return errInvalidAdminNotificationChannelWrite
		}
		request.Credential = &trimmed
	}
	if request.Enabled != nil {
		changed = true
	}
	if !changed {
		return errInvalidAdminNotificationChannelWrite
	}
	return nil
}

type AdminNotificationTypeUpdateRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func (request AdminNotificationTypeUpdateRequest) normalize() error {
	if request.Enabled == nil {
		return errInvalidAdminNotificationTypeWrite
	}
	return nil
}

type AdminAlertRuleUpdateRequest struct {
	Enabled      *bool     `json:"enabled,omitempty"`
	Threshold    *float64  `json:"threshold,omitempty"`
	DurationSec  *int      `json:"duration_sec,omitempty"`
	ScopeNodeIDs *[]string `json:"scope_node_ids,omitempty"`
}

func (request *AdminAlertRuleUpdateRequest) normalize() error {
	changed := false
	if request.Enabled != nil {
		changed = true
	}
	if request.Threshold != nil {
		changed = true
		if math.IsNaN(*request.Threshold) || math.IsInf(*request.Threshold, 0) || *request.Threshold < 0 {
			return errInvalidAdminAlertRuleUpdate
		}
	}
	if request.DurationSec != nil {
		changed = true
		if *request.DurationSec < 0 {
			return errInvalidAdminAlertRuleUpdate
		}
	}
	if request.ScopeNodeIDs != nil {
		changed = true
		normalized := make([]string, 0, len(*request.ScopeNodeIDs))
		seen := map[string]bool{}
		for _, rawNodeID := range *request.ScopeNodeIDs {
			nodeID := normalizeAdminNodeID(rawNodeID)
			if nodeID == "" || seen[nodeID] {
				return errInvalidAdminAlertRuleUpdate
			}
			seen[nodeID] = true
			normalized = append(normalized, nodeID)
		}
		request.ScopeNodeIDs = &normalized
	}
	if !changed {
		return errInvalidAdminAlertRuleUpdate
	}
	return nil
}

type AdminAlertRule struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Category              string   `json:"category"`
	Metric                string   `json:"metric"`
	Comparator            string   `json:"comparator"`
	Threshold             float64  `json:"threshold"`
	ThresholdUnit         string   `json:"threshold_unit"`
	DurationSec           int      `json:"duration_sec"`
	Enabled               bool     `json:"enabled"`
	NotificationEventType string   `json:"notification_event_type"`
	NotificationLabel     string   `json:"notification_label"`
	Description           string   `json:"description"`
	ScopeNodeIDs          []string `json:"scope_node_ids"`
	CreatedAt             string   `json:"created_at"`
	UpdatedAt             string   `json:"updated_at"`
}

type AdminNotificationChannel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Destination   string `json:"destination"`
	CredentialSet bool   `json:"credential_set"`
	Enabled       bool   `json:"enabled"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type AdminNotificationType struct {
	EventType string `json:"event_type"`
	Label     string `json:"label"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type AdminNotificationDelivery struct {
	ID             int64  `json:"id"`
	EventType      string `json:"event_type"`
	Label          string `json:"label"`
	NodeID         string `json:"node_id"`
	NodeName       string `json:"node_name"`
	PreviousStatus string `json:"previous_status"`
	Status         string `json:"status"`
	ChannelID      string `json:"channel_id"`
	ChannelName    string `json:"channel_name"`
	Success        bool   `json:"success"`
	Error          string `json:"error,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type AdminNodeUpdateRequest struct {
	DisplayName       *string            `json:"display_name,omitempty"`
	CountryCode       *string            `json:"country_code,omitempty"`
	Region            *string            `json:"region,omitempty"`
	HomeProbeTargetID *string            `json:"home_probe_target_id,omitempty"`
	ExpiryDate        *string            `json:"expiry_date,omitempty"`
	BillingCycle      *string            `json:"billing_cycle,omitempty"`
	BillingMode       *string            `json:"billing_mode,omitempty"`
	MonthlyResetDay   *int               `json:"monthly_reset_day,omitempty"`
	DisplayOrder      *int               `json:"display_order,omitempty"`
	PublicIPv4        *string            `json:"public_ipv4,omitempty"`
	PublicIPv6        *string            `json:"public_ipv6,omitempty"`
	MonthlyQuotaBytes adminOptionalInt64 `json:"monthly_quota_bytes,omitempty"`
	Disabled          *bool              `json:"disabled,omitempty"`
}

func (request *AdminNodeUpdateRequest) normalize() error {
	changed := false
	if request.DisplayName != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.DisplayName)
		if trimmed == "" {
			return errInvalidAdminNodeUpdate
		}
		request.DisplayName = &trimmed
	}
	if request.CountryCode != nil {
		changed = true
		trimmed := strings.ToUpper(strings.TrimSpace(*request.CountryCode))
		if len(trimmed) > 8 {
			return errInvalidAdminNodeUpdate
		}
		request.CountryCode = &trimmed
	}
	if request.Region != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.Region)
		request.Region = &trimmed
	}
	if request.HomeProbeTargetID != nil {
		changed = true
		trimmed := strings.TrimSpace(*request.HomeProbeTargetID)
		request.HomeProbeTargetID = &trimmed
	}
	if request.ExpiryDate != nil {
		changed = true
		trimmed, err := normalizeAdminNodeDate(*request.ExpiryDate)
		if err != nil {
			return errInvalidAdminNodeUpdate
		}
		request.ExpiryDate = &trimmed
	}
	if request.BillingCycle != nil {
		changed = true
		trimmed, err := normalizeAdminNodeShortText(*request.BillingCycle, 64)
		if err != nil {
			return errInvalidAdminNodeUpdate
		}
		request.BillingCycle = &trimmed
	}
	if request.BillingMode != nil {
		changed = true
		mode, ok := normalizeAdminNodeBillingMode(*request.BillingMode)
		if !ok {
			return errInvalidAdminNodeUpdate
		}
		request.BillingMode = &mode
	}
	if request.MonthlyResetDay != nil {
		changed = true
		if *request.MonthlyResetDay == 0 {
			return errInvalidAdminNodeUpdate
		}
		resetDay, ok := normalizeAdminNodeMonthlyResetDay(*request.MonthlyResetDay)
		if !ok {
			return errInvalidAdminNodeUpdate
		}
		request.MonthlyResetDay = &resetDay
	}
	if request.DisplayOrder != nil {
		changed = true
		if *request.DisplayOrder < 0 {
			return errInvalidAdminNodeUpdate
		}
	}
	if request.PublicIPv4 != nil {
		changed = true
		trimmed, err := normalizeAdminNodeIP(*request.PublicIPv4, 4)
		if err != nil {
			return errInvalidAdminNodeUpdate
		}
		request.PublicIPv4 = &trimmed
	}
	if request.PublicIPv6 != nil {
		changed = true
		trimmed, err := normalizeAdminNodeIP(*request.PublicIPv6, 6)
		if err != nil {
			return errInvalidAdminNodeUpdate
		}
		request.PublicIPv6 = &trimmed
	}
	if request.MonthlyQuotaBytes.Set {
		changed = true
		if request.MonthlyQuotaBytes.Valid && request.MonthlyQuotaBytes.Value < 0 {
			return errInvalidAdminNodeUpdate
		}
	}
	if request.Disabled != nil {
		changed = true
	}
	if !changed {
		return errInvalidAdminNodeUpdate
	}
	return nil
}

type adminOptionalInt64 struct {
	Set   bool
	Valid bool
	Value int64
}

func (value *adminOptionalInt64) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.Valid = false
		value.Value = 0
		return nil
	}
	var parsed int64
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	value.Valid = true
	value.Value = parsed
	return nil
}

type AdminNode struct {
	ID                string  `json:"id"`
	DisplayName       string  `json:"display_name"`
	Status            string  `json:"status"`
	CountryCode       string  `json:"country_code,omitempty"`
	Region            string  `json:"region,omitempty"`
	HomeProbeTargetID string  `json:"home_probe_target_id,omitempty"`
	Disabled          bool    `json:"disabled"`
	BillingMode       string  `json:"billing_mode"`
	MonthlyResetDay   int     `json:"monthly_reset_day"`
	ExpiryDate        string  `json:"expiry_date,omitempty"`
	BillingCycle      string  `json:"billing_cycle,omitempty"`
	DisplayOrder      int     `json:"display_order"`
	PublicIPv4        string  `json:"public_ipv4,omitempty"`
	PublicIPv6        string  `json:"public_ipv6,omitempty"`
	MonthlyQuotaBytes *int64  `json:"monthly_quota_bytes,omitempty"`
	LastSeenAt        *string `json:"last_seen_at,omitempty"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
	Hostname          string  `json:"hostname,omitempty"`
	OSName            string  `json:"os_name,omitempty"`
	OSVersion         string  `json:"os_version,omitempty"`
	Kernel            string  `json:"kernel,omitempty"`
	Arch              string  `json:"arch,omitempty"`
	Virtualization    string  `json:"virtualization,omitempty"`
	CPUModel          string  `json:"cpu_model,omitempty"`
	CPUCores          *int    `json:"cpu_cores,omitempty"`
	MemoryTotalBytes  *int64  `json:"memory_total_bytes,omitempty"`
	DiskTotalBytes    *int64  `json:"disk_total_bytes,omitempty"`
	BootTime          *string `json:"boot_time,omitempty"`
	AgentVersion      string  `json:"agent_version,omitempty"`
}
