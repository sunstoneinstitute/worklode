package cmd

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/harness"
	"github.com/sunstoneinstitute/worklode/internal/hookrun"
)

// TestParseHookArgs pins the four-way split: event, harness id, the hook's
// own positional args, and the downstream --next argv. --harness is consumed
// wherever it appears before --next (and must not leak into args); after
// --next everything is verbatim and unparsed, including a --harness there.
func TestParseHookArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		event   string
		harness string
		hargs   []string
		next    []string
		wantErr bool
	}{
		{name: "bare event", args: []string{"heartbeat"}, event: "heartbeat"},
		{name: "event with harness", args: []string{"heartbeat", "--harness", "codex"},
			event: "heartbeat", harness: "codex"},
		{name: "next carries a downstream --harness verbatim, unparsed",
			args:  []string{"heartbeat", "--next", "other-hook", "--harness", "x"},
			event: "heartbeat", next: []string{"other-hook", "--harness", "x"}},
		{name: "harness before next: both parsed, next tail verbatim",
			args:  []string{"pre-commit", "--harness", "copilot", "--next", "pre-commit"},
			event: "pre-commit", harness: "copilot", next: []string{"pre-commit"}},
		{name: "positional args preserved, harness pair excluded",
			args:  []string{"commit-msg", ".git/COMMIT_EDITMSG", "--harness", "codex"},
			event: "commit-msg", harness: "codex", hargs: []string{".git/COMMIT_EDITMSG"}},
		{name: "trailing --harness with no id is an error",
			args: []string{"heartbeat", "--harness"}, wantErr: true},
		{name: "empty argv is an error", args: nil, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			event, harnessID, args, next, err := parseHookArgs(c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseHookArgs(%v): want error, got none", c.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHookArgs(%v): %v", c.args, err)
			}
			if event != c.event {
				t.Errorf("event = %q, want %q", event, c.event)
			}
			if harnessID != c.harness {
				t.Errorf("harness = %q, want %q", harnessID, c.harness)
			}
			if !slices.Equal(args, c.hargs) {
				t.Errorf("args = %v, want %v", args, c.hargs)
			}
			if !slices.Equal(next, c.next) {
				t.Errorf("next = %v, want %v", next, c.next)
			}
		})
	}
}

// `lode hook --list` must name every event hookrun accepts — the listing is
// the only place a user learns what `<event>` may be.
func TestHookListNamesEveryEvent(t *testing.T) {
	cmd := newHookCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("hook --list: %v", err)
	}

	got := out.String()
	for _, e := range hookrun.Events() {
		if !strings.Contains(got, e.Name) {
			t.Errorf("event %q missing from --list output:\n%s", e.Name, got)
		}
		if !strings.Contains(got, e.Summary) {
			t.Errorf("summary for %q missing from --list output:\n%s", e.Name, got)
		}
	}
}

// Every Claude Code binding `lode install` writes must show up as the trigger
// of the event it invokes, named alongside the adapter that binds it;
// anything else means the listing and the adapter have drifted.
func TestHookTriggersCoverClaudeBindings(t *testing.T) {
	adapter := harness.ClaudeCode{}
	triggers := hookTriggers()
	for event, natives := range adapter.Events() {
		trigger, ok := triggers[string(event)]
		if !ok {
			t.Errorf("event %s -> %v has no trigger entry", event, natives)
			continue
		}
		if !strings.Contains(trigger, adapter.ID()) {
			t.Errorf("trigger for %s = %q, want it to name %s", event, trigger, adapter.ID())
		}
		for _, native := range natives {
			if !strings.Contains(trigger, native) {
				t.Errorf("trigger for %s = %q, want it to name %s", event, trigger, native)
			}
		}
	}
	if got := triggers["pre-commit"]; !strings.Contains(got, "git") {
		t.Errorf("pre-commit trigger = %q, want the git hook", got)
	}
}
