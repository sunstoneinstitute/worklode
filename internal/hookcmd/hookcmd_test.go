package hookcmd

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/harness"
	"github.com/sunstoneinstitute/worklode/internal/hookrun"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		event    string
		harness  string
		hookArgs []string
		next     []string
		wantErr  bool
	}{
		{name: "bare event", args: []string{"heartbeat"}, event: "heartbeat"},
		{name: "harness and next", args: []string{"pre-commit", "--harness", "copilot", "--next", "other", "--harness", "x"}, event: "pre-commit", harness: "copilot", next: []string{"other", "--harness", "x"}},
		{name: "positional args", args: []string{"commit-msg", ".git/COMMIT_EDITMSG", "--harness", "codex"}, event: "commit-msg", harness: "codex", hookArgs: []string{".git/COMMIT_EDITMSG"}},
		{name: "missing event", wantErr: true},
		{name: "missing harness", args: []string{"heartbeat", "--harness"}, wantErr: true},
		{name: "missing next", args: []string{"heartbeat", "--next"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, harnessID, hookArgs, next, err := parseArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if event != tc.event || harnessID != tc.harness || !slices.Equal(hookArgs, tc.hookArgs) || !slices.Equal(next, tc.next) {
				t.Fatalf("got (%q, %q, %v, %v)", event, harnessID, hookArgs, next)
			}
		})
	}
}

func TestRunListNamesEveryEvent(t *testing.T) {
	var out bytes.Buffer
	if code := Run(t.Context(), []string{"--list"}, nil, &out, nil); code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, event := range hookrun.Events() {
		if !strings.Contains(out.String(), event.Name) || !strings.Contains(out.String(), event.Summary) {
			t.Errorf("event %q missing from %q", event.Name, out.String())
		}
	}
}

func TestHookTriggersCoverEveryAdapterBinding(t *testing.T) {
	triggers := hookTriggers()
	for _, id := range harness.IDs() {
		adapter, ok := harness.Get(id)
		if !ok {
			t.Fatalf("registry lists %s but Get does not resolve it", id)
		}
		for event, natives := range adapter.Events() {
			trigger := triggers[string(event)]
			if !strings.Contains(trigger, id) {
				t.Errorf("%s missing from %q", id, trigger)
			}
			for _, native := range natives {
				if !strings.Contains(trigger, native) {
					t.Errorf("%s missing from %q", native, trigger)
				}
			}
		}
	}
}
