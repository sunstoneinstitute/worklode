package harness

import "testing"

func TestRegistryKnowsClaudeCode(t *testing.T) {
	h, ok := Get("claude-code")
	if !ok || h.ID() != "claude-code" {
		t.Fatalf("Get(claude-code) = %v, %v", h, ok)
	}
	if _, ok := Get("opencode"); ok {
		t.Fatal("opencode has no v1 adapter and must not be registered")
	}
	ids := IDs()
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("IDs() not sorted: %v", ids)
		}
	}
}

func TestEventsCoverHeartbeat(t *testing.T) {
	// Every v1 adapter must map Heartbeat or report it unbound — it is the
	// portability payoff (spec 008 §16.5). claude-code maps it to four events.
	h, _ := Get("claude-code")
	if got := h.Events()[Heartbeat]; len(got) != 4 {
		t.Fatalf("claude-code Heartbeat events = %v; want 4", got)
	}
}
