package api

type SummaryResponse struct {
	Nodes         []Node         `json:"nodes"`
	LatencyPoints []LatencyPoint `json:"latency_points"`
}

type LatencyResponse struct {
	NodeID string         `json:"node_id"`
	Range  string         `json:"range"`
	Points []LatencyPoint `json:"points"`
}

type Node struct {
	ID                   string          `json:"id"`
	DisplayName          string          `json:"display_name"`
	Status               string          `json:"status"`
	OS                   string          `json:"os"`
	CountryCode          string          `json:"country_code,omitempty"`
	Subtitle             string          `json:"subtitle,omitempty"`
	CPUPercent           *float64        `json:"cpu_percent"`
	MemoryUsedBytes      *float64        `json:"memory_used_bytes"`
	MemoryTotalBytes     *float64        `json:"memory_total_bytes"`
	DiskUsedBytes        *float64        `json:"disk_used_bytes"`
	DiskTotalBytes       *float64        `json:"disk_total_bytes"`
	NetInSpeedBps        *float64        `json:"net_in_speed_bps"`
	NetOutSpeedBps       *float64        `json:"net_out_speed_bps"`
	NetInTotalBytes      *float64        `json:"net_in_total_bytes"`
	NetOutTotalBytes     *float64        `json:"net_out_total_bytes"`
	MonthlyBillableBytes *float64        `json:"monthly_billable_bytes"`
	MonthlyQuotaBytes    *float64        `json:"monthly_quota_bytes"`
	LatencySummary       *LatencySummary `json:"latency_summary,omitempty"`
}

type LatencySummary struct {
	TargetID    string   `json:"target_id"`
	TargetName  string   `json:"target_name"`
	MedianMS    *float64 `json:"median_ms"`
	AvgMS       *float64 `json:"avg_ms"`
	LossPercent *float64 `json:"loss_percent"`
	UpdatedAt   string   `json:"updated_at"`
}

type LatencyPoint struct {
	TS          string   `json:"ts"`
	TargetID    string   `json:"target_id"`
	TargetName  string   `json:"target_name"`
	MedianMS    *float64 `json:"median_ms"`
	LossPercent float64  `json:"loss_percent"`
}
