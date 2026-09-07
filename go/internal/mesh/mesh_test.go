package mesh

import "testing"

func TestToolDefinitions_preserveTheTenToolContract(t *testing.T) {
	definitions := ToolDefinitions()
	if len(definitions) != 10 {
		t.Fatalf("tool count = %d, want 10", len(definitions))
	}
	for _, name := range []string{"herdr_agent_list", "herdr_agent_get", "herdr_agent_read", "herdr_relay", "herdr_handoff", "herdr_agent_wait", "herdr_agent_start", "herdr_agent_focus", "herdr_integration_status", "herdr_pane_read"} {
		if _, ok := definitions[name]; !ok {
			t.Fatalf("missing tool %q", name)
		}
	}
}
