package agent

import (
	"testing"
	"time"
)

func TestProbeSchedulerSelectsAllTargetsOnFirstRun(t *testing.T) {
	scheduler := NewProbeScheduler()
	now := time.Unix(1782990000, 0)
	targets := []ProbeTarget{
		{ID: "fast", IntervalSec: 30},
		{ID: "slow", IntervalSec: 120},
	}

	due := scheduler.Due(targets, now)

	if len(due) != 2 || due[0].ID != "fast" || due[1].ID != "slow" {
		t.Fatalf("due = %+v, want both targets on first run", due)
	}
}

func TestProbeSchedulerRespectsPerTargetIntervalsAfterMarking(t *testing.T) {
	scheduler := NewProbeScheduler()
	first := time.Unix(1782990000, 0)
	targets := []ProbeTarget{
		{ID: "fast", IntervalSec: 30},
		{ID: "slow", IntervalSec: 120},
	}
	scheduler.MarkCompleted(targets, first)

	due := scheduler.Due(targets, first.Add(59*time.Second))
	if len(due) != 1 || due[0].ID != "fast" {
		t.Fatalf("due at 59s = %+v, want only fast target", due)
	}

	due = scheduler.Due(targets, first.Add(120*time.Second))
	if len(due) != 2 || due[0].ID != "fast" || due[1].ID != "slow" {
		t.Fatalf("due at 120s = %+v, want both targets", due)
	}
}

func TestProbeSchedulerTreatsNewTargetsAsDue(t *testing.T) {
	scheduler := NewProbeScheduler()
	first := time.Unix(1782990000, 0)
	scheduler.MarkCompleted([]ProbeTarget{{ID: "known", IntervalSec: 300}}, first)

	due := scheduler.Due([]ProbeTarget{{ID: "known", IntervalSec: 300}, {ID: "new", IntervalSec: 300}}, first.Add(time.Minute))

	if len(due) != 1 || due[0].ID != "new" {
		t.Fatalf("due = %+v, want only new target", due)
	}
}
