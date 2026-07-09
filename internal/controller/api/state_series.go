package api

import (
	"encoding/json"
	"time"
)

type StateSeries struct {
	CPUPercent         []*float64 `json:"cpu_percent,omitempty"`
	Load1              []*float64 `json:"load1,omitempty"`
	Load5              []*float64 `json:"load5,omitempty"`
	Load15             []*float64 `json:"load15,omitempty"`
	MemoryUsedBytes    []*float64 `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes   []*float64 `json:"memory_total_bytes,omitempty"`
	SwapUsedBytes      []*float64 `json:"swap_used_bytes,omitempty"`
	SwapTotalBytes     []*float64 `json:"swap_total_bytes,omitempty"`
	DiskUsedBytes      []*float64 `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes     []*float64 `json:"disk_total_bytes,omitempty"`
	NetInTotalBytes    []*float64 `json:"net_in_total_bytes,omitempty"`
	NetOutTotalBytes   []*float64 `json:"net_out_total_bytes,omitempty"`
	NetInSpeedBps      []*float64 `json:"net_in_speed_bps,omitempty"`
	NetOutSpeedBps     []*float64 `json:"net_out_speed_bps,omitempty"`
	ProcessCount       []*float64 `json:"process_count,omitempty"`
	TCPConnectionCount []*float64 `json:"tcp_connection_count,omitempty"`
	UDPConnectionCount []*float64 `json:"udp_connection_count,omitempty"`
	UptimeSeconds      []*float64 `json:"uptime_seconds,omitempty"`
}

func (response StateResponse) MarshalJSON() ([]byte, error) {
	type stateResponseJSON struct {
		NodeID    string       `json:"node_id"`
		Range     string       `json:"range"`
		CreatedAt []int64      `json:"created_at,omitempty"`
		Series    *StateSeries `json:"series,omitempty"`
	}
	createdAt, series := stateSeriesPayloadFromPoints(response.Points)
	return json.Marshal(stateResponseJSON{
		NodeID:    response.NodeID,
		Range:     response.Range,
		CreatedAt: createdAt,
		Series:    series,
	})
}

func (response *StateResponse) UnmarshalJSON(data []byte) error {
	type stateResponseJSON struct {
		NodeID    string       `json:"node_id"`
		Range     string       `json:"range"`
		Points    []StatePoint `json:"points"`
		CreatedAt []int64      `json:"created_at"`
		Series    *StateSeries `json:"series"`
	}
	var raw stateResponseJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	response.NodeID = raw.NodeID
	response.Range = raw.Range
	if raw.Points != nil {
		response.Points = raw.Points
		return nil
	}
	response.Points = statePointsFromSeries(raw.CreatedAt, raw.Series)
	return nil
}

func stateSeriesPayloadFromPoints(points []StatePoint) ([]int64, *StateSeries) {
	if len(points) == 0 {
		return nil, nil
	}
	createdAt := make([]int64, 0, len(points))
	series := &StateSeries{}
	for _, point := range points {
		createdAt = append(createdAt, latencyTimestampMillis(point.TS))
		series.CPUPercent = append(series.CPUPercent, compactLatencyValue(point.CPUPercent))
		series.Load1 = append(series.Load1, compactLatencyValue(point.Load1))
		series.Load5 = append(series.Load5, compactLatencyValue(point.Load5))
		series.Load15 = append(series.Load15, compactLatencyValue(point.Load15))
		series.MemoryUsedBytes = append(series.MemoryUsedBytes, compactLatencyValue(point.MemoryUsedBytes))
		series.MemoryTotalBytes = append(series.MemoryTotalBytes, compactLatencyValue(point.MemoryTotalBytes))
		series.SwapUsedBytes = append(series.SwapUsedBytes, compactLatencyValue(point.SwapUsedBytes))
		series.SwapTotalBytes = append(series.SwapTotalBytes, compactLatencyValue(point.SwapTotalBytes))
		series.DiskUsedBytes = append(series.DiskUsedBytes, compactLatencyValue(point.DiskUsedBytes))
		series.DiskTotalBytes = append(series.DiskTotalBytes, compactLatencyValue(point.DiskTotalBytes))
		series.NetInTotalBytes = append(series.NetInTotalBytes, compactLatencyValue(point.NetInTotalBytes))
		series.NetOutTotalBytes = append(series.NetOutTotalBytes, compactLatencyValue(point.NetOutTotalBytes))
		series.NetInSpeedBps = append(series.NetInSpeedBps, compactLatencyValue(point.NetInSpeedBps))
		series.NetOutSpeedBps = append(series.NetOutSpeedBps, compactLatencyValue(point.NetOutSpeedBps))
		series.ProcessCount = append(series.ProcessCount, compactLatencyValue(point.ProcessCount))
		series.TCPConnectionCount = append(series.TCPConnectionCount, compactLatencyValue(point.TCPConnectionCount))
		series.UDPConnectionCount = append(series.UDPConnectionCount, compactLatencyValue(point.UDPConnectionCount))
		series.UptimeSeconds = append(series.UptimeSeconds, compactLatencyValue(point.UptimeSeconds))
	}
	return createdAt, series
}

func statePointsFromSeries(createdAt []int64, series *StateSeries) []StatePoint {
	if series == nil || len(createdAt) == 0 {
		return nil
	}
	points := make([]StatePoint, 0, len(createdAt))
	for index, timestamp := range createdAt {
		points = append(points, StatePoint{
			TS:                 time.UnixMilli(timestamp).UTC().Format(time.RFC3339),
			CPUPercent:         stateSeriesValue(series.CPUPercent, index),
			Load1:              stateSeriesValue(series.Load1, index),
			Load5:              stateSeriesValue(series.Load5, index),
			Load15:             stateSeriesValue(series.Load15, index),
			MemoryUsedBytes:    stateSeriesValue(series.MemoryUsedBytes, index),
			MemoryTotalBytes:   stateSeriesValue(series.MemoryTotalBytes, index),
			SwapUsedBytes:      stateSeriesValue(series.SwapUsedBytes, index),
			SwapTotalBytes:     stateSeriesValue(series.SwapTotalBytes, index),
			DiskUsedBytes:      stateSeriesValue(series.DiskUsedBytes, index),
			DiskTotalBytes:     stateSeriesValue(series.DiskTotalBytes, index),
			NetInTotalBytes:    stateSeriesValue(series.NetInTotalBytes, index),
			NetOutTotalBytes:   stateSeriesValue(series.NetOutTotalBytes, index),
			NetInSpeedBps:      stateSeriesValue(series.NetInSpeedBps, index),
			NetOutSpeedBps:     stateSeriesValue(series.NetOutSpeedBps, index),
			ProcessCount:       stateSeriesValue(series.ProcessCount, index),
			TCPConnectionCount: stateSeriesValue(series.TCPConnectionCount, index),
			UDPConnectionCount: stateSeriesValue(series.UDPConnectionCount, index),
			UptimeSeconds:      stateSeriesValue(series.UptimeSeconds, index),
		})
	}
	return points
}

func stateSeriesValue(values []*float64, index int) *float64 {
	if index < 0 || index >= len(values) {
		return nil
	}
	return values[index]
}
