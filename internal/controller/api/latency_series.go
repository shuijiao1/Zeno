package api

import (
	"encoding/json"
	"time"
)

type LatencySeries struct {
	TargetID    string     `json:"target_id"`
	TargetName  string     `json:"target_name"`
	CreatedAt   []int64    `json:"created_at"`
	MedianMS    []*float64 `json:"median_ms"`
	AvgMS       []*float64 `json:"avg_ms"`
	LossPercent []float64  `json:"loss_percent"`
}

type ServiceLatencySeries struct {
	NodeID      string     `json:"node_id"`
	NodeName    string     `json:"node_name"`
	CreatedAt   []int64    `json:"created_at"`
	MedianMS    []*float64 `json:"median_ms"`
	AvgMS       []*float64 `json:"avg_ms"`
	LossPercent []float64  `json:"loss_percent"`
}

func (response LatencyResponse) MarshalJSON() ([]byte, error) {
	type latencyResponseJSON struct {
		NodeID string          `json:"node_id"`
		Range  string          `json:"range"`
		Series []LatencySeries `json:"series"`
	}
	return json.Marshal(latencyResponseJSON{
		NodeID: response.NodeID,
		Range:  response.Range,
		Series: latencySeriesFromPoints(response.Points),
	})
}

func (response *LatencyResponse) UnmarshalJSON(data []byte) error {
	type latencyResponseJSON struct {
		NodeID string          `json:"node_id"`
		Range  string          `json:"range"`
		Points []LatencyPoint  `json:"points"`
		Series []LatencySeries `json:"series"`
	}
	var raw latencyResponseJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	response.NodeID = raw.NodeID
	response.Range = raw.Range
	if len(raw.Points) > 0 || raw.Series == nil {
		response.Points = raw.Points
		return nil
	}
	response.Points = latencyPointsFromSeries(raw.Series)
	return nil
}

func (response ServiceTargetLatencyResponse) MarshalJSON() ([]byte, error) {
	type serviceLatencyResponseJSON struct {
		Target ServiceTarget          `json:"target"`
		Range  string                 `json:"range"`
		Series []ServiceLatencySeries `json:"series"`
	}
	return json.Marshal(serviceLatencyResponseJSON{
		Target: response.Target,
		Range:  response.Range,
		Series: serviceLatencySeriesFromPoints(response.Points),
	})
}

func (response *ServiceTargetLatencyResponse) UnmarshalJSON(data []byte) error {
	type serviceLatencyResponseJSON struct {
		Target ServiceTarget          `json:"target"`
		Range  string                 `json:"range"`
		Points []ServiceLatencyPoint  `json:"points"`
		Series []ServiceLatencySeries `json:"series"`
	}
	var raw serviceLatencyResponseJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	response.Target = raw.Target
	response.Range = raw.Range
	if len(raw.Points) > 0 || raw.Series == nil {
		response.Points = raw.Points
		return nil
	}
	response.Points = serviceLatencyPointsFromSeries(raw.Series)
	return nil
}

func latencySeriesFromPoints(points []LatencyPoint) []LatencySeries {
	order := make([]string, 0)
	byTarget := make(map[string]*LatencySeries)
	for _, point := range points {
		series := byTarget[point.TargetID]
		if series == nil {
			series = &LatencySeries{TargetID: point.TargetID, TargetName: point.TargetName}
			byTarget[point.TargetID] = series
			order = append(order, point.TargetID)
		}
		series.CreatedAt = append(series.CreatedAt, latencyTimestampMillis(point.TS))
		series.MedianMS = append(series.MedianMS, point.MedianMS)
		series.AvgMS = append(series.AvgMS, point.AvgMS)
		series.LossPercent = append(series.LossPercent, point.LossPercent)
	}
	seriesList := make([]LatencySeries, 0, len(order))
	for _, targetID := range order {
		seriesList = append(seriesList, *byTarget[targetID])
	}
	return seriesList
}

func latencyPointsFromSeries(seriesList []LatencySeries) []LatencyPoint {
	points := make([]LatencyPoint, 0)
	for _, series := range seriesList {
		for index, createdAt := range series.CreatedAt {
			points = append(points, LatencyPoint{
				TS:          latencyTimestampString(createdAt),
				TargetID:    series.TargetID,
				TargetName:  series.TargetName,
				MedianMS:    floatSliceValue(series.MedianMS, index),
				AvgMS:       floatSliceValue(series.AvgMS, index),
				LossPercent: floatValue(series.LossPercent, index),
			})
		}
	}
	return points
}

func serviceLatencySeriesFromPoints(points []ServiceLatencyPoint) []ServiceLatencySeries {
	order := make([]string, 0)
	byNode := make(map[string]*ServiceLatencySeries)
	for _, point := range points {
		series := byNode[point.NodeID]
		if series == nil {
			series = &ServiceLatencySeries{NodeID: point.NodeID, NodeName: point.NodeName}
			byNode[point.NodeID] = series
			order = append(order, point.NodeID)
		}
		series.CreatedAt = append(series.CreatedAt, latencyTimestampMillis(point.TS))
		series.MedianMS = append(series.MedianMS, point.MedianMS)
		series.AvgMS = append(series.AvgMS, point.AvgMS)
		series.LossPercent = append(series.LossPercent, point.LossPercent)
	}
	seriesList := make([]ServiceLatencySeries, 0, len(order))
	for _, nodeID := range order {
		seriesList = append(seriesList, *byNode[nodeID])
	}
	return seriesList
}

func serviceLatencyPointsFromSeries(seriesList []ServiceLatencySeries) []ServiceLatencyPoint {
	points := make([]ServiceLatencyPoint, 0)
	for _, series := range seriesList {
		for index, createdAt := range series.CreatedAt {
			points = append(points, ServiceLatencyPoint{
				TS:          latencyTimestampString(createdAt),
				NodeID:      series.NodeID,
				NodeName:    series.NodeName,
				MedianMS:    floatSliceValue(series.MedianMS, index),
				AvgMS:       floatSliceValue(series.AvgMS, index),
				LossPercent: floatValue(series.LossPercent, index),
			})
		}
	}
	return points
}

func latencyTimestampMillis(value string) int64 {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0
	}
	return parsed.UTC().UnixMilli()
}

func latencyTimestampString(value int64) string {
	if value <= 0 {
		return time.Unix(0, 0).UTC().Format(time.RFC3339)
	}
	return time.UnixMilli(value).UTC().Format(time.RFC3339)
}

func floatSliceValue(values []*float64, index int) *float64 {
	if index < 0 || index >= len(values) {
		return nil
	}
	return values[index]
}

func floatValue(values []float64, index int) float64 {
	if index < 0 || index >= len(values) {
		return 0
	}
	return values[index]
}
