package herdrclient

import (
	"slices"
	"testing"
	"time"
)

func TestAgentStartCallTimeoutExceedsReadinessBound(t *testing.T) {
	if AgentStartTimeout != 90*time.Second {
		t.Fatalf("AgentStartTimeout = %s, want 90s", AgentStartTimeout)
	}
	if AgentStartCallTimeout <= AgentStartTimeout {
		t.Fatalf("AgentStartCallTimeout = %s, want above %s", AgentStartCallTimeout, AgentStartTimeout)
	}
	args := AgentStartArgs("t-test", "grok", "w1:p1")
	want := []string{"agent", "start", "t-test", "--kind", "grok", "--pane", "w1:p1", "--timeout", "90000"}
	if !slices.Equal(args, want) {
		t.Fatalf("AgentStartArgs = %v, want %v", args, want)
	}
}
