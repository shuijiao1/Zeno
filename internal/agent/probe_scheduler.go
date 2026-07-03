package agent

import "time"

// ProbeScheduler tracks the last successful probe report per target so Agent
// heartbeats can stay frequent without probing every target on every tick.
type ProbeScheduler struct {
	lastCompleted map[string]time.Time
}

func NewProbeScheduler() *ProbeScheduler {
	return &ProbeScheduler{lastCompleted: map[string]time.Time{}}
}

func (s *ProbeScheduler) Due(targets []ProbeTarget, now time.Time) []ProbeTarget {
	if s == nil {
		return targets
	}
	due := make([]ProbeTarget, 0, len(targets))
	for _, target := range targets {
		last, seen := s.lastCompleted[target.ID]
		if !seen || targetIntervalElapsed(target, last, now) {
			due = append(due, target)
		}
	}
	return due
}

func (s *ProbeScheduler) MarkCompleted(targets []ProbeTarget, now time.Time) {
	if s == nil {
		return
	}
	for _, target := range targets {
		if target.ID == "" {
			continue
		}
		s.lastCompleted[target.ID] = now
	}
}

func targetIntervalElapsed(target ProbeTarget, last time.Time, now time.Time) bool {
	interval := time.Duration(target.IntervalSec) * time.Second
	if interval <= 0 {
		return true
	}
	return !now.Before(last.Add(interval))
}
