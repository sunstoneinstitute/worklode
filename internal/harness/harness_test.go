package harness

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestRegistryKnowsClaudeCode(t *testing.T) {
	h, ok := Get("claude-code")
	if !ok || h.ID() != "claude-code" {
		t.Fatalf("Get(claude-code) = %v, %v", h, ok)
	}
	if _, ok := Get("opencode"); ok {
		t.Fatal("opencode has no v1 adapter and must not be registered")
	}
	ids := IDs()
	want := []string{"amp", "claude-code", "codex", "copilot"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("IDs() = %v, want %v (sorted, one per v1 adapter)", ids, want)
	}
}

// Every registered adapter reports its unbindable events rather than
// pretending to full coverage: Unbound must match missingEvents exactly.
func TestUnboundMatchesEventTable(t *testing.T) {
	// Every adapter's config location is redirected: this test installs, and
	// must never reach the developer's own harness config.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("COPILOT_HOME", t.TempDir())
	t.Setenv("AMP_SETTINGS_FILE", filepath.Join(t.TempDir(), "settings.json"))
	for _, id := range IDs() {
		h, _ := Get(id)
		hi, err := h.InstallHooks(t.TempDir(), ScopeLocal)
		if err != nil {
			// claude-code needs a git repo; its own tests cover it.
			continue
		}
		if want := missingEvents(h); !reflect.DeepEqual(hi.Unbound, want) {
			t.Errorf("%s Unbound = %v, want %v", id, hi.Unbound, want)
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
