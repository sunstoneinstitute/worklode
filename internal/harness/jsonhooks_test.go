package harness

import (
	"reflect"
	"testing"
)

// TestHookCommandsMirrorsApply pins the reader to the writer: whatever
// applyGroupedHooks writes for an event, HookCommands must read back, in
// order. A schema change that touches only one side fails here.
func TestHookCommandsMirrorsApply(t *testing.T) {
	settings := map[string]any{}
	applyGroupedHooks(settings, []hookBinding{
		{Event: "SessionStart", Command: "lode hook session-start"},
		{Event: "PostToolUse", Matcher: "Edit", Command: "lode hook post-tool"},
		{Event: "PostToolUse", Matcher: "Write", Command: "lode hook post-tool"},
	})

	if got := HookCommands(settings, "SessionStart"); !reflect.DeepEqual(got, []string{"lode hook session-start"}) {
		t.Errorf("SessionStart = %#v", got)
	}
	want := []string{"lode hook post-tool", "lode hook post-tool"}
	if got := HookCommands(settings, "PostToolUse"); !reflect.DeepEqual(got, want) {
		t.Errorf("PostToolUse = %#v, want %#v", got, want)
	}
	if got := HookCommands(settings, "Stop"); got != nil {
		t.Errorf("unbound event = %#v, want nil", got)
	}
	if got := HookCommands(settings, "SessionStart"); len(got) != 1 {
		t.Errorf("second read differs: %#v", got)
	}
}

// TestHookCommandsToleratesForeignShapes covers hand-edited settings: the
// reader reports no commands rather than panicking or erroring, the same way
// stripLodeHooks leaves shapes it did not write alone.
func TestHookCommandsToleratesForeignShapes(t *testing.T) {
	cases := map[string]map[string]any{
		"no hooks key":        {},
		"hooks not an object": {"hooks": "off"},
		"event not a list":    {"hooks": map[string]any{"Stop": "my-tool"}},
		"group not an object": {"hooks": map[string]any{"Stop": []any{"my-tool"}}},
		"group has no handlers": {"hooks": map[string]any{
			"Stop": []any{map[string]any{"matcher": "*"}},
		}},
		"handler not an object": {"hooks": map[string]any{
			"Stop": []any{map[string]any{"hooks": []any{"my-tool"}}},
		}},
		"command not a string": {"hooks": map[string]any{
			"Stop": []any{map[string]any{"hooks": []any{map[string]any{"command": 7}}}},
		}},
	}
	for name, settings := range cases {
		t.Run(name, func(t *testing.T) {
			if got := HookCommands(settings, "Stop"); got != nil {
				t.Errorf("HookCommands = %#v, want nil", got)
			}
		})
	}
}

// TestHookCommandsKeepsForeignCommands makes sure the reader is not filtered
// to Worklode's own entries: callers assert that a third-party hook sharing an
// event survives an install, which needs it visible.
func TestHookCommandsKeepsForeignCommands(t *testing.T) {
	settings := map[string]any{"hooks": map[string]any{
		"Stop": []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": "their-hook"}},
		}},
	}}
	applyGroupedHooks(settings, []hookBinding{{Event: "Stop", Command: "lode hook heartbeat"}})

	want := []string{"their-hook", "lode hook heartbeat"}
	if got := HookCommands(settings, "Stop"); !reflect.DeepEqual(got, want) {
		t.Errorf("HookCommands = %#v, want %#v", got, want)
	}
}
