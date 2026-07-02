package api

type AgentProbeTargetsResponse struct {
	Targets []AgentProbeTarget `json:"targets"`
}

type AgentProbeTarget struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Address     string `json:"address"`
	Port        *int   `json:"port,omitempty"`
	Count       int    `json:"count"`
	TimeoutMS   int    `json:"timeout_ms"`
	IntervalSec int    `json:"interval_sec"`
}

type AgentProbeResultsRequest struct {
	Rounds []AgentProbeRound `json:"rounds"`
}

type AgentProbeRound struct {
	TargetID string             `json:"target_id"`
	TS       int64              `json:"ts"`
	Type     string             `json:"type"`
	Samples  []AgentProbeSample `json:"samples"`
}

type AgentProbeSample struct {
	Seq       int      `json:"seq"`
	Success   bool     `json:"success"`
	LatencyMS *float64 `json:"latency_ms"`
	Error     string   `json:"error,omitempty"`
}
