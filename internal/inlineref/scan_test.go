package inlineref

import (
	"fmt"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/ns"
	"github.com/sunstoneinstitute/worklode/internal/refkind"
)

var wl = map[string]bool{"WL": true}

func TestScan(t *testing.T) {
	tests := []struct {
		name string
		text string
		keys map[string]bool
		want []Hit // Line 1 and Raw checked via fields below
	}{
		{"task id known key", "see WL-12 now", wl, []Hit{{Form: FormTask, Key: "WL", Number: 12, Raw: "WL-12"}}},
		{"task id unknown key", "UTF-8 and SHA-256", wl, nil},
		{"task id in branch name", "WL-12-fix-it", wl, nil},
		{"anchor on doc", "WL-SPEC-7#sec-3.1", wl, []Hit{{Form: FormNode, Key: "WL", Infix: "SPEC", Number: 7, Anchor: "sec-3.1", Raw: "WL-SPEC-7#sec-3.1"}}},
		{"anchor before full stop", "See WL-SPEC-7#sec-3.1.", wl, []Hit{{Form: FormNode, Key: "WL", Infix: "SPEC", Number: 7, Anchor: "sec-3.1", Raw: "WL-SPEC-7#sec-3.1", Start: 4, End: 21}}},
		{"anchor dropped on rule", "WL-REQ-7#sec-3", wl, []Hit{{Form: FormNode, Key: "WL", Infix: "REQ", Number: 7, Raw: "WL-REQ-7"}}},
		{"rule fragment on doc", "see WL-SPEC-7#rule-12.", wl, []Hit{{Form: FormNode, Key: "WL", Infix: "SPEC", Number: 7, RuleFragment: 12, Raw: "WL-SPEC-7#rule-12"}}},
		{"rule fragment dropped on rule", "WL-REQ-7#rule-3", wl, []Hit{{Form: FormNode, Key: "WL", Infix: "REQ", Number: 7, Raw: "WL-REQ-7"}}},
		{"rule fragment needs digits", "WL-SPEC-7#rule-x", wl, []Hit{{Form: FormNode, Key: "WL", Infix: "SPEC", Number: 7, Raw: "WL-SPEC-7"}}},
		{"url rule fragment", "https://x.io/WL-SPEC-7#rule-12", wl, []Hit{{Form: FormURL, Key: "WL", Infix: "SPEC", Number: 7, RuleFragment: 12, Raw: "https://x.io/WL-SPEC-7#rule-12"}}},
		{"url spec rule fragment", "https://x.io/projects/worklode/spec/77#rule-4", wl, []Hit{{Form: FormURL, Key: "worklode", Infix: "SPEC", Number: 77, RuleFragment: 4, Raw: "https://x.io/projects/worklode/spec/77#rule-4"}}},
		{"keyword spec", "see spec 42 §3.", wl, []Hit{{Form: FormKeyword, Infix: "SPEC", Number: 42, Anchor: "sec-3", Raw: "spec 42 §3"}}},
		{"keyword adr", "per ADR 7 here", wl, []Hit{{Form: FormKeyword, Infix: "ADR", Number: 7, Raw: "ADR 7"}}},
		{"url task", "http://h:8080/tasks/WL-5.", wl, []Hit{{Form: FormURL, Key: "WL", Number: 5, Raw: "http://h:8080/tasks/WL-5"}}},
		{"url docs ref", "https://x.io/docs/ref/WL-PLAN-3#sec-2", wl, []Hit{{Form: FormURL, Key: "WL", Infix: "PLAN", Number: 3, Anchor: "sec-2", Raw: "https://x.io/docs/ref/WL-PLAN-3#sec-2"}}},
		{"url rules", "https://x.io/rules/WL-REQ-9", wl, []Hit{{Form: FormURL, Key: "WL", Infix: "REQ", Number: 9, Raw: "https://x.io/rules/WL-REQ-9"}}},
		{"url root ref", "https://x.io/WL-DEL-2", wl, []Hit{{Form: FormURL, Key: "WL", Infix: "DEL", Number: 2, Raw: "https://x.io/WL-DEL-2"}}},
		{"url relative root ref", "go to /WL-SPEC-2#sec-1 ok", wl, []Hit{{Form: FormURL, Key: "WL", Infix: "SPEC", Number: 2, Anchor: "sec-1", Raw: "/WL-SPEC-2#sec-1"}}},
		{"url rule", "https://x.io/projects/worklode/rule/12/3", wl, []Hit{{Form: FormURL, Key: "worklode", Infix: "RULE", Number: 12, Raw: "https://x.io/projects/worklode/rule/12/3"}}},
		{"url spec", "https://x.io/projects/worklode/spec/77#sec-4", wl, []Hit{{Form: FormURL, Key: "worklode", Infix: "SPEC", Number: 77, Anchor: "sec-4", Raw: "https://x.io/projects/worklode/spec/77#sec-4"}}},
		{"url plan", "https://x.io/projects/worklode/plan/5/2", wl, []Hit{{Form: FormURL, Key: "worklode", Infix: "PLAN", Number: 5, Raw: "https://x.io/projects/worklode/plan/5/2"}}},
		{"url task kind", "https://x.io/projects/worklode/bug/8", wl, []Hit{{Form: FormURL, Key: "worklode", Number: 8, Raw: "https://x.io/projects/worklode/bug/8"}}},
		{"url unknown kind", "https://x.io/projects/worklode/nope/8", wl, nil},
		{"markdown link", "[the spec](https://x.io/WL-SPEC-1) ok", wl, []Hit{{Form: FormURL, Key: "WL", Infix: "SPEC", Number: 1, LinkText: "the spec", Raw: "[the spec](https://x.io/WL-SPEC-1)"}}},
		{"fenced block", "```\nWL-1\n```\nWL-2", wl, []Hit{{Line: 4, Form: FormTask, Key: "WL", Number: 2, Raw: "WL-2"}}},
		{"inline code", "a `WL-1` b", wl, []Hit{{Form: FormTask, Key: "WL", Number: 1, Raw: "WL-1", Literal: true}}},
		{"code span across lines", "a `b WL-1\nc` WL-2", wl, []Hit{
			{Form: FormTask, Key: "WL", Number: 1, Raw: "WL-1", Literal: true},
			{Line: 2, Form: FormTask, Key: "WL", Number: 2, Raw: "WL-2"},
		}},
		{"blank line ends a code span", "a `b\n\nWL-1 `", wl, []Hit{{Line: 3, Form: FormTask, Key: "WL", Number: 1, Raw: "WL-1"}}},
		{"quoted", `the form ("WL-1") and “WL-2” but WL-3`, wl, []Hit{
			{Form: FormTask, Key: "WL", Number: 1, Raw: "WL-1", Literal: true},
			{Form: FormTask, Key: "WL", Number: 2, Raw: "WL-2", Literal: true},
			{Form: FormTask, Key: "WL", Number: 3, Raw: "WL-3"},
		}},
		{"section range", "WL-SPEC-7#sec-3–§4", wl, []Hit{{Form: FormNode, Key: "WL", Infix: "SPEC", Number: 7, Anchor: "sec-3", Raw: "WL-SPEC-7#sec-3", Literal: true}}},
		{"section list", "WL-SPEC-7#sec-3, #sec-5", wl, []Hit{{Form: FormNode, Key: "WL", Infix: "SPEC", Number: 7, Anchor: "sec-3", Raw: "WL-SPEC-7#sec-3", Literal: true}}},
		{"two section refs are no list", "WL-SPEC-7#sec-3, WL-SPEC-7#sec-5", wl, []Hit{
			{Form: FormNode, Key: "WL", Infix: "SPEC", Number: 7, Anchor: "sec-3", Raw: "WL-SPEC-7#sec-3"},
			{Form: FormNode, Key: "WL", Infix: "SPEC", Number: 7, Anchor: "sec-5", Raw: "WL-SPEC-7#sec-5"},
		}},
		{"two hits in order", "WL-2 then WL-SPEC-1", wl, []Hit{
			{Form: FormTask, Key: "WL", Number: 2, Raw: "WL-2"},
			{Form: FormNode, Key: "WL", Infix: "SPEC", Number: 1, Raw: "WL-SPEC-1"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Scan(tc.text, tc.keys)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d hits %+v, want %d", len(got), got, len(tc.want))
			}
			for i, w := range tc.want {
				g := got[i]
				if w.Line == 0 {
					w.Line = 1
				}
				if g.Raw != w.Raw || g.Line != w.Line || g.Form != w.Form || g.Key != w.Key || g.Infix != w.Infix ||
					g.Number != w.Number || g.Anchor != w.Anchor || g.RuleFragment != w.RuleFragment || g.LinkText != w.LinkText || g.Literal != w.Literal {
					t.Errorf("hit %d:\n got %+v\nwant %+v", i, g, w)
				}
			}
		})
	}
}

func TestScanOffsets(t *testing.T) {
	line := "x WL-SPEC-1 y"
	h := Scan(line, wl)[0]
	if line[h.Start:h.End] != "WL-SPEC-1" {
		t.Errorf("offsets %d:%d", h.Start, h.End)
	}
}

func TestScanEveryInfix(t *testing.T) {
	for _, k := range refkind.All {
		ref := fmt.Sprintf("WL-%s-4", k.Infix)
		got := Scan("see "+ref, wl)
		if len(got) != 1 || got[0].Infix != k.Infix || got[0].Number != 4 {
			t.Errorf("%s: %+v", ref, got)
		}
	}
}

func TestScanEveryTaskKindURL(t *testing.T) {
	for _, k := range ns.TaskKinds {
		got := Scan("https://x.io/projects/p/"+k+"/3", nil)
		if len(got) != 1 || got[0].Number != 3 {
			t.Errorf("kind %s: %+v", k, got)
		}
	}
}
