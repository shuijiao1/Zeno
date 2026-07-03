package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

var (
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
)

// AdminNodesResponse is the authenticated management view for node inventory.
// It intentionally omits token hashes and other credentials.
type AdminNodesResponse struct {
	Nodes []AdminNode `json:"nodes"`
}

type AdminNodeResponse struct {
	Node AdminNode `json:"node"`
}

type AdminNodeInstallCommandResponse struct {
	NodeID  string `json:"node_id"`
	Command string `json:"command"`
}

type AdminNodeCreateRequest struct {
	ID                string             `json:"id,omitempty"`
	DisplayName       string             `json:"display_name"`
	CountryCode       string             `json:"country_code,omitempty"`
	Region            string             `json:"region,omitempty"`
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
	request.CountryCode = strings.ToUpper(strings.TrimSpace(request.CountryCode))
	if len(request.CountryCode) > 8 {
		return errInvalidAdminNodeCreate
	}
	request.Region = strings.TrimSpace(request.Region)
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
	ID          string             `json:"id,omitempty"`
	Name        string             `json:"name"`
	Type        string             `json:"type"`
	Address     string             `json:"address"`
	Port        adminOptionalInt64 `json:"port,omitempty"`
	Count       int                `json:"count"`
	TimeoutMS   int                `json:"timeout_ms"`
	IntervalSec int                `json:"interval_sec"`
	Enabled     *bool              `json:"enabled,omitempty"`
}

func (request *AdminProbeTargetCreateRequest) normalize() error {
	request.ID = normalizeAdminNodeID(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Type = strings.ToLower(strings.TrimSpace(request.Type))
	request.Address = strings.TrimSpace(request.Address)
	if request.Type == "tcp" {
		request.Type = "tcping"
	}
	if request.Name == "" || request.Address == "" || request.Type != "tcping" || request.Count <= 0 || request.TimeoutMS <= 0 || request.IntervalSec <= 0 {
		return errInvalidAdminTargetWrite
	}
	if !request.Port.Set || !request.Port.Valid || !validPort(request.Port.Value) {
		return errInvalidAdminTargetWrite
	}
	return nil
}

type AdminProbeTargetUpdateRequest struct {
	Name        *string                            `json:"name,omitempty"`
	Type        *string                            `json:"type,omitempty"`
	Address     *string                            `json:"address,omitempty"`
	Port        adminOptionalInt64                 `json:"port,omitempty"`
	Count       *int                               `json:"count,omitempty"`
	TimeoutMS   *int                               `json:"timeout_ms,omitempty"`
	IntervalSec *int                               `json:"interval_sec,omitempty"`
	Enabled     *bool                              `json:"enabled,omitempty"`
	Assignments []AdminProbeTargetAssignmentUpdate `json:"assignments,omitempty"`
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
		trimmed := strings.ToLower(strings.TrimSpace(*request.Type))
		if trimmed == "tcp" {
			trimmed = "tcping"
		}
		if trimmed != "tcping" {
			return errInvalidAdminTargetWrite
		}
		request.Type = &trimmed
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
		if !request.Port.Valid || !validPort(request.Port.Value) {
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

func validPort(port int64) bool {
	return port > 0 && port <= 65535
}

type AdminProbeTarget struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Type        string                       `json:"type"`
	Address     string                       `json:"address"`
	Port        *int                         `json:"port"`
	Count       int                          `json:"count"`
	TimeoutMS   int                          `json:"timeout_ms"`
	IntervalSec int                          `json:"interval_sec"`
	Enabled     bool                         `json:"enabled"`
	Assignments []AdminProbeTargetAssignment `json:"assignments"`
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

type AdminNotificationTypesResponse struct {
	Types []AdminNotificationType `json:"types"`
}

type AdminNotificationTypeResponse struct {
	Type AdminNotificationType `json:"type"`
}

type AdminNotificationDeliveriesResponse struct {
	Deliveries []AdminNotificationDelivery `json:"deliveries"`
}

type AdminNotificationChannelCreateRequest struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Destination string `json:"destination"`
	Credential  string `json:"credential"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

func (request *AdminNotificationChannelCreateRequest) normalize() error {
	request.ID = normalizeAdminNodeID(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Type = strings.ToLower(strings.TrimSpace(request.Type))
	request.Destination = strings.TrimSpace(request.Destination)
	request.Credential = strings.TrimSpace(request.Credential)
	if request.Name == "" || request.Destination == "" || request.Credential == "" || !validAdminNotificationChannelType(request.Type) {
		return errInvalidAdminNotificationChannelWrite
	}
	return nil
}

type AdminNotificationChannelUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Type        *string `json:"type,omitempty"`
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
	if request.Type != nil {
		changed = true
		trimmed := strings.ToLower(strings.TrimSpace(*request.Type))
		if !validAdminNotificationChannelType(trimmed) {
			return errInvalidAdminNotificationChannelWrite
		}
		request.Type = &trimmed
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

func validAdminNotificationChannelType(channelType string) bool {
	switch channelType {
	case "telegram", "webhook":
		return true
	default:
		return false
	}
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

type AdminNotificationChannel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
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
	ChannelType    string `json:"channel_type"`
	Success        bool   `json:"success"`
	Error          string `json:"error,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type AdminNodeUpdateRequest struct {
	DisplayName       *string            `json:"display_name,omitempty"`
	CountryCode       *string            `json:"country_code,omitempty"`
	Region            *string            `json:"region,omitempty"`
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
	Disabled          bool    `json:"disabled"`
	BillingMode       string  `json:"billing_mode"`
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
