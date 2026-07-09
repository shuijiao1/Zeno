package api

import (
	"encoding/json"
	"time"
)

type LatencySeries struct {
	TargetID    string     `json:"target_id"`
	TargetName  string     `json:"target_name"`
	CreatedAt   []int64    `json:"created_at,omitempty"`
	MedianMS    []*float64 `json:"median_ms,omitempty"`
	AvgMS       []*float64 `json:"avg_ms,omitempty"`
	LossPercent []float64  `json:"loss_percent,omitempty"`
}

type ServiceLatencySeries struct {
	NodeID      string     `json:"node_id"`
	NodeName    string     `json:"node_name"`
	CreatedAt   []int64    `json:"created_at,omitempty"`
	MedianMS    []*float64 `json:"median_ms,omitempty"`
	AvgMS       []*float64 `json:"avg_ms,omitempty"`
	LossPercent []float64  `json:"loss_percent,omitempty"`
}

func (response LatencyResponse) MarshalJSON() ([]byte, error) {
	type latencyResponseJSON struct {
		NodeID          string          `json:"node_id"`
		Range           string          `json:"range"`
		SharedCreatedAt []int64         `json:"created_at,omitempty"`
		Series          []LatencySeries `json:"series"`
	}
	createdAt, series := latencySeriesPayloadFromPoints(response.Points)
	return json.Marshal(latencyResponseJSON{
		NodeID:          response.NodeID,
		Range:           response.Range,
		SharedCreatedAt: createdAt,
		Series:          series,
	})
}

func (response *LatencyResponse) UnmarshalJSON(data []byte) error {
	type latencyResponseJSON struct {
		NodeID          string          `json:"node_id"`
		Range           string          `json:"range"`
		SharedCreatedAt []int64         `json:"created_at"`
		Points          []LatencyPoint  `json:"points"`
		Series          []LatencySeries `json:"series"`
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
	response.Points = latencyPointsFromSeries(raw.Series, raw.SharedCreatedAt)
	return nil
}

func (response ServiceTargetLatencyResponse) MarshalJSON() ([]byte, error) {
	type serviceLatencyResponseJSON struct {
		Target          ServiceTarget          `json:"target"`
		Range           string                 `json:"range"`
		SharedCreatedAt []int64                `json:"created_at,omitempty"`
		Series          []ServiceLatencySeries `json:"series"`
	}
	createdAt, series := serviceLatencySeriesPayloadFromPoints(response.Points)
	return json.Marshal(serviceLatencyResponseJSON{
		Target:          response.Target,
		Range:           response.Range,
		SharedCreatedAt: createdAt,
		Series:          series,
	})
}

func (response *ServiceTargetLatencyResponse) UnmarshalJSON(data []byte) error {
	type serviceLatencyResponseJSON struct {
		Target          ServiceTarget          `json:"target"`
		Range           string                 `json:"range"`
		SharedCreatedAt []int64                `json:"created_at"`
		Points          []ServiceLatencyPoint  `json:"points"`
		Series          []ServiceLatencySeries `json:"series"`
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
	response.Points = serviceLatencyPointsFromSeries(raw.Series, raw.SharedCreatedAt)
	return nil
}

func latencySeriesPayloadFromPoints(points []LatencyPoint) ([]int64, []LatencySeries) {
	series := latencySeriesFromPoints(points)
	shared := sharedLatencyCreatedAt(series)
	if len(shared) > 0 {
		for index := range series {
			series[index].CreatedAt = nil
		}
	}
	return shared, series
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

func latencyPointsFromSeries(seriesList []LatencySeries, sharedCreatedAt []int64) []LatencyPoint {
	points := make([]LatencyPoint, 0)
	for _, series := range seriesList {
		createdAtValues := series.CreatedAt
		if len(createdAtValues) == 0 {
			createdAtValues = sharedCreatedAt
		}
		for index, createdAt := range createdAtValues {
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

func serviceLatencySeriesPayloadFromPoints(points []ServiceLatencyPoint) ([]int64, []ServiceLatencySeries) {
	series := serviceLatencySeriesFromPoints(points)
	shared := sharedServiceLatencyCreatedAt(series)
	if len(shared) > 0 {
		for index := range series {
			series[index].CreatedAt = nil
		}
	}
	return shared, series
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

func serviceLatencyPointsFromSeries(seriesList []ServiceLatencySeries, sharedCreatedAt []int64) []ServiceLatencyPoint {
	points := make([]ServiceLatencyPoint, 0)
	for _, series := range seriesList {
		createdAtValues := series.CreatedAt
		if len(createdAtValues) == 0 {
			createdAtValues = sharedCreatedAt
		}
		for index, createdAt := range createdAtValues {
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

func sharedLatencyCreatedAt(seriesList []LatencySeries) []int64 {
	if len(seriesList) == 0 || len(seriesList[0].CreatedAt) == 0 {
		return nil
	}
	shared := seriesList[0].CreatedAt
	for _, series := range seriesList[1:] {
		if !sameInt64Slice(shared, series.CreatedAt) {
			return nil
		}
	}
	return append([]int64(nil), shared...)
}

func sharedServiceLatencyCreatedAt(seriesList []ServiceLatencySeries) []int64 {
	if len(seriesList) == 0 || len(seriesList[0].CreatedAt) == 0 {
		return nil
	}
	shared := seriesList[0].CreatedAt
	for _, series := range seriesList[1:] {
		if !sameInt64Slice(shared, series.CreatedAt) {
			return nil
		}
	}
	return append([]int64(nil), shared...)
}

func sameInt64Slice(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
