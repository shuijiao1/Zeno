package api

// AdminNodesResponse is the authenticated management view for node inventory.
// It intentionally omits token hashes and other credentials.
type AdminNodesResponse struct {
	Nodes []AdminNode `json:"nodes"`
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
