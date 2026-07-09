package probe

import (
	"errors"
	"math"
	"sort"
)

// Sample is one ping/tcping attempt inside a probe round.
// Failed samples count toward sent/loss. When the agent can still measure an
// elapsed duration for a failed/timeout sample, that latency is retained for
// charting so slow probes below the visual cap are drawn instead of flattened
// to a gap/zero-loss-only sample.
type Sample struct {
	Seq       int
	Success   bool
	LatencyMS *float64
	Error     string
}

// Stats is the aggregate summary for one probe round.
// Latency fields are nil when no sample carried a measurable latency.
type Stats struct {
	Sent        int
	Received    int
	LossPercent float64
	MinMS       *float64
	AvgMS       *float64
	MedianMS    *float64
	MaxMS       *float64
	StddevMS    *float64
}

// ComputeStats calculates Zeno's locked latency statistics.
func ComputeStats(samples []Sample) (Stats, error) {
	if len(samples) == 0 {
		return Stats{}, errors.New("probe round must contain at least one sample")
	}

	latencies := make([]float64, 0, len(samples))
	successes := 0
	for _, sample := range samples {
		if sample.Success {
			successes++
			if sample.LatencyMS == nil {
				return Stats{}, errors.New("successful sample must include latency")
			}
		}
		if sample.LatencyMS == nil {
			continue
		}
		if *sample.LatencyMS < 0 || math.IsNaN(*sample.LatencyMS) || math.IsInf(*sample.LatencyMS, 0) {
			return Stats{}, errors.New("sample latency must be a finite non-negative number")
		}
		latencies = append(latencies, *sample.LatencyMS)
	}

	stats := Stats{
		Sent:        len(samples),
		Received:    successes,
		LossPercent: float64(len(samples)-successes) / float64(len(samples)) * 100,
	}
	if len(latencies) == 0 {
		return stats, nil
	}

	sort.Float64s(latencies)
	minV := latencies[0]
	maxV := latencies[len(latencies)-1]
	avgV := average(latencies)
	medianV := median(latencies)
	stddevV := stddev(latencies, avgV)

	stats.MinMS = &minV
	stats.AvgMS = &avgV
	stats.MedianMS = &medianV
	stats.MaxMS = &maxV
	stats.StddevMS = &stddevV
	return stats, nil
}

func average(values []float64) float64 {
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func median(values []float64) float64 {
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return (values[middle-1] + values[middle]) / 2
}

func stddev(values []float64, avg float64) float64 {
	if len(values) <= 1 {
		return 0
	}
	var sumSquares float64
	for _, value := range values {
		delta := value - avg
		sumSquares += delta * delta
	}
	return math.Sqrt(sumSquares / float64(len(values)))
}
