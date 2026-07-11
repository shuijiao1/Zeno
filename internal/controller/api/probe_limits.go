package api

const (
	maxProbeTargetsPerNode      = 32
	maxProbeTargetCount         = 32
	minProbeTargetTimeoutMS     = 100
	maxProbeTargetTimeoutMS     = 5000
	minProbeTargetIntervalSec   = 5
	maxProbeTargetIntervalSec   = 3600
	maxProbeTargetRoundBudgetMS = 60_000
	maxProbeNodeRoundBudgetMS   = 120_000
)

func validProbeTargetResourceConfig(count, timeoutMS, intervalSec int) bool {
	if count < 1 || count > maxProbeTargetCount {
		return false
	}
	if timeoutMS < minProbeTargetTimeoutMS || timeoutMS > maxProbeTargetTimeoutMS {
		return false
	}
	if intervalSec < minProbeTargetIntervalSec || intervalSec > maxProbeTargetIntervalSec {
		return false
	}
	budgetMS := probeTargetRoundBudgetMS(count, timeoutMS)
	if budgetMS > maxProbeTargetRoundBudgetMS {
		return false
	}
	if int64(intervalSec)*1000 < budgetMS {
		return false
	}
	return true
}

func normalizeProbeTargetForExecution(target ProbeTarget) ProbeTarget {
	target.Count = clampInt(target.Count, 1, maxProbeTargetCount)
	target.TimeoutMS = clampInt(target.TimeoutMS, minProbeTargetTimeoutMS, maxProbeTargetTimeoutMS)
	for probeTargetRoundBudgetMS(target.Count, target.TimeoutMS) > maxProbeTargetRoundBudgetMS && target.Count > 1 {
		target.Count--
	}
	target.IntervalSec = clampInt(target.IntervalSec, minProbeTargetIntervalSec, maxProbeTargetIntervalSec)
	minIntervalSec := int((probeTargetRoundBudgetMS(target.Count, target.TimeoutMS) + 999) / 1000)
	if target.IntervalSec < minIntervalSec {
		target.IntervalSec = minIntervalSec
	}
	if target.IntervalSec > maxProbeTargetIntervalSec {
		target.IntervalSec = maxProbeTargetIntervalSec
	}
	return target
}

func probeTargetRoundBudgetMS(count, timeoutMS int) int64 {
	if count <= 0 || timeoutMS <= 0 {
		return 0
	}
	return int64(count) * int64(timeoutMS)
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
