package api

import (
	"testing"
	"time"
)

func TestSummaryPublishCadenceDoesNotOutrunAgentState(t *testing.T) {
	const agentStateCadence = 3 * time.Second
	if summaryPublishMinInterval < agentStateCadence {
		t.Fatalf("summary publish interval %s is shorter than agent state cadence %s", summaryPublishMinInterval, agentStateCadence)
	}
	if summaryPublishCoalesceDelay >= summaryPublishMinInterval {
		t.Fatalf("coalesce delay %s must remain shorter than publish interval %s", summaryPublishCoalesceDelay, summaryPublishMinInterval)
	}
}
