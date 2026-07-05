package agent

import "time"

type HeartbeatRequest struct {
	TS           int64  `json:"ts"`
	Status       string `json:"status"`
	AgentVersion string `json:"agent_version"`
}

type HostInfo struct {
	Hostname         string `json:"hostname"`
	OSName           string `json:"os_name"`
	OSVersion        string `json:"os_version"`
	Kernel           string `json:"kernel"`
	Arch             string `json:"arch"`
	Virtualization   string `json:"virtualization"`
	CPUModel         string `json:"cpu_model"`
	CPUCores         int    `json:"cpu_cores"`
	MemoryTotalBytes int64  `json:"memory_total_bytes"`
	DiskTotalBytes   int64  `json:"disk_total_bytes"`
	BootTime         int64  `json:"boot_time"`
	AgentVersion     string `json:"agent_version"`
	PublicIPv4       string `json:"public_ipv4,omitempty"`
	PublicIPv6       string `json:"public_ipv6,omitempty"`
	CountryCode      string `json:"country_code,omitempty"`
}

type StateSample struct {
	TS                 int64   `json:"ts"`
	CPUPercent         float64 `json:"cpu_percent"`
	Load1              float64 `json:"load1"`
	Load5              float64 `json:"load5"`
	Load15             float64 `json:"load15"`
	MemoryUsedBytes    int64   `json:"memory_used_bytes"`
	MemoryTotalBytes   int64   `json:"memory_total_bytes"`
	SwapUsedBytes      int64   `json:"swap_used_bytes"`
	SwapTotalBytes     int64   `json:"swap_total_bytes"`
	DiskUsedBytes      int64   `json:"disk_used_bytes"`
	DiskTotalBytes     int64   `json:"disk_total_bytes"`
	NetInTotalBytes    int64   `json:"net_in_total_bytes"`
	NetOutTotalBytes   int64   `json:"net_out_total_bytes"`
	NetInSpeedBps      float64 `json:"net_in_speed_bps"`
	NetOutSpeedBps     float64 `json:"net_out_speed_bps"`
	ProcessCount       int64   `json:"process_count"`
	TCPConnectionCount int64   `json:"tcp_connection_count"`
	UDPConnectionCount int64   `json:"udp_connection_count"`
	UptimeSeconds      int64   `json:"uptime_seconds"`
}

type ProbeTargetsResponse struct {
	Targets []ProbeTarget `json:"targets"`
}

type ProbeTarget struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Address     string `json:"address"`
	Port        *int   `json:"port,omitempty"`
	Count       int    `json:"count"`
	TimeoutMS   int    `json:"timeout_ms"`
	IntervalSec int    `json:"interval_sec"`
}

type ProbeResultsRequest struct {
	Rounds []probeRoundPayload `json:"rounds"`
}

type ProbeRound struct {
	TargetID string
	TS       time.Time
	Type     string
	Samples  []ProbeSample
}

type probeRoundPayload struct {
	TargetID string        `json:"target_id"`
	TS       int64         `json:"ts"`
	Type     string        `json:"type"`
	Samples  []ProbeSample `json:"samples"`
}

type ProbeSample struct {
	Seq       int      `json:"seq"`
	Success   bool     `json:"success"`
	LatencyMS *float64 `json:"latency_ms"`
	Error     string   `json:"error,omitempty"`
}
