package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

var (
	errInvalidAdminNodeUpdate = errors.New("invalid admin node update")
	errInvalidAdminNodeCreate = errors.New("invalid admin node create")
	errNodeAlreadyExists      = errors.New("node already exists")
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
