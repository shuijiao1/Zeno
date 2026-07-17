package probe

import (
	"math"
	"testing"
)

func f64(v float64) *float64 { return &v }

func almostEqual(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("got nil, want %.6f", want)
	}
	if math.Abs(*got-want) > 0.000001 {
		t.Fatalf("got %.6f, want %.6f", *got, want)
	}
}

func TestComputeStatsAllSuccessfulSamples(t *testing.T) {
	stats, err := ComputeStats([]Sample{
		{Seq: 1, Success: true, LatencyMS: f64(10)},
		{Seq: 2, Success: true, LatencyMS: f64(20)},
		{Seq: 3, Success: true, LatencyMS: f64(30)},
	})
	if err != nil {
		t.Fatalf("ComputeStats returned error: %v", err)
	}
	if stats.Sent != 3 || stats.Received != 3 {
		t.Fatalf("sent/received = %d/%d, want 3/3", stats.Sent, stats.Received)
	}
	if stats.LossPercent != 0 {
		t.Fatalf("loss = %.2f, want 0", stats.LossPercent)
	}
	almostEqual(t, stats.MinMS, 10)
	almostEqual(t, stats.AvgMS, 20)
	almostEqual(t, stats.MedianMS, 20)
	almostEqual(t, stats.MaxMS, 30)
	almostEqual(t, stats.StddevMS, math.Sqrt(200.0/3.0))
}

func TestComputeStatsPartialTimeoutCountsLossAndExcludesFailedLatency(t *testing.T) {
	stats, err := ComputeStats([]Sample{
		{Seq: 1, Success: true, LatencyMS: f64(10)},
		{Seq: 2, Success: false, LatencyMS: f64(1200), Error: "timeout"},
		{Seq: 3, Success: true, LatencyMS: f64(50)},
		{Seq: 4, Success: false, LatencyMS: nil, Error: "connect_error"},
	})
	if err != nil {
		t.Fatalf("ComputeStats returned error: %v", err)
	}
	if stats.Sent != 4 || stats.Received != 2 {
		t.Fatalf("sent/received = %d/%d, want 4/2", stats.Sent, stats.Received)
	}
	if stats.LossPercent != 50 {
		t.Fatalf("loss = %.2f, want 50", stats.LossPercent)
	}
	almostEqual(t, stats.MinMS, 10)
	almostEqual(t, stats.AvgMS, 30)
	almostEqual(t, stats.MedianMS, 30)
	almostEqual(t, stats.MaxMS, 50)
}

func TestComputeStatsAllUnmeasuredTimeoutsHaveNullLatencyStats(t *testing.T) {
	stats, err := ComputeStats([]Sample{
		{Seq: 1, Success: false, Error: "timeout"},
		{Seq: 2, Success: false, Error: "timeout"},
	})
	if err != nil {
		t.Fatalf("ComputeStats returned error: %v", err)
	}
	if stats.Sent != 2 || stats.Received != 0 || stats.LossPercent != 100 {
		t.Fatalf("sent/received/loss = %d/%d/%.2f, want 2/0/100", stats.Sent, stats.Received, stats.LossPercent)
	}
	if stats.MinMS != nil || stats.AvgMS != nil || stats.MedianMS != nil || stats.MaxMS != nil || stats.StddevMS != nil {
		t.Fatalf("latency stats should be nil when all samples fail: %+v", stats)
	}
}

func TestComputeStatsAllMeasuredTimeoutsHaveNullLatencyStats(t *testing.T) {
	stats, err := ComputeStats([]Sample{
		{Seq: 1, Success: false, LatencyMS: f64(1500), Error: "timeout"},
		{Seq: 2, Success: false, LatencyMS: f64(3500), Error: "timeout"},
	})
	if err != nil {
		t.Fatalf("ComputeStats returned error: %v", err)
	}
	if stats.Sent != 2 || stats.Received != 0 || stats.LossPercent != 100 {
		t.Fatalf("sent/received/loss = %d/%d/%.2f, want 2/0/100", stats.Sent, stats.Received, stats.LossPercent)
	}
	if stats.MinMS != nil || stats.AvgMS != nil || stats.MedianMS != nil || stats.MaxMS != nil || stats.StddevMS != nil {
		t.Fatalf("failed samples must not produce latency stats: %+v", stats)
	}
}

func TestComputeStatsEvenMedian(t *testing.T) {
	stats, err := ComputeStats([]Sample{
		{Seq: 1, Success: true, LatencyMS: f64(1)},
		{Seq: 2, Success: true, LatencyMS: f64(100)},
		{Seq: 3, Success: true, LatencyMS: f64(2)},
		{Seq: 4, Success: true, LatencyMS: f64(3)},
	})
	if err != nil {
		t.Fatalf("ComputeStats returned error: %v", err)
	}
	almostEqual(t, stats.MedianMS, 2.5)
}

func TestComputeStatsRejectsEmptyRound(t *testing.T) {
	_, err := ComputeStats(nil)
	if err == nil {
		t.Fatal("expected error for empty sample round")
	}
}

func TestComputeStatsRejectsSuccessfulSampleWithoutLatency(t *testing.T) {
	_, err := ComputeStats([]Sample{{Seq: 1, Success: true, LatencyMS: nil}})
	if err == nil {
		t.Fatal("expected error for successful sample without latency")
	}
}
