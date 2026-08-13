package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/hookrun"
)

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
// of the event it invokes; anything else means the two lists have drifted.
func TestHookTriggersCoverClaudeBindings(t *testing.T) {
	triggers := hookTriggers()
	for _, b := range claudeBindings {
		event := strings.TrimPrefix(b.Command, lodeHookPrefix)
		trigger, ok := triggers[event]
		if !ok {
			t.Errorf("binding %s -> %q has no trigger entry", b.Event, b.Command)
			continue
		}
		if !strings.Contains(trigger, b.Event) {
			t.Errorf("trigger for %s = %q, want it to name %s", event, trigger, b.Event)
		}
	}
	if got := triggers["pre-commit"]; !strings.Contains(got, "git") {
		t.Errorf("pre-commit trigger = %q, want the git hook", got)
	}
}
