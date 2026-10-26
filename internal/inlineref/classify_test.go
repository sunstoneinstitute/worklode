package inlineref

import (
	"slices"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestCandidates(t *testing.T) {
	for _, c := range []struct {
		from, to, fk, tk string
		want             []string
	}{
		{"task", "task", "", "", []string{"dependsOn", "childOf", "followUpTo", "duplicateOf"}},
		{"rule", "rule", "", "", []string{"refines", "needs", "constrains", "conflictsWith", "amends", "wasDerivedFrom"}},
		{"doc", "doc", "spec", "plan", []string{"requires", "wasDerivedFrom"}},
		{"doc", "doc", "plan", "plan", []string{"requires", "wasDerivedFrom", "blockedBy"}},
		{"task", "rule", "", "", []string{"governedBy"}},
		{"rule", "task", "", "", nil},
		{"doc", "rule", "", "", nil},
		{"milestone", "task", "", "", nil},
	} {
		if got := Candidates(model.NodeKind(c.from), model.NodeKind(c.to), c.fk, c.tk); !slices.Equal(got, c.want) {
			t.Errorf("Candidates(%s, %s, %s, %s) = %v, want %v", c.from, c.to, c.fk, c.tk, got, c.want)
		}
	}
}

func TestCue(t *testing.T) {
	task := Candidates("task", "task", "", "")
	rule := Candidates("rule", "rule", "", "")
	plans := Candidates("doc", "doc", "plan", "plan")
	for _, c := range []struct {
		line  string
		cands []string
		want  string
	}{
		{"This depends on WL-5.", task, "dependsOn"},
		{"Blocked By WL-5", task, "dependsOn"},
		{"requires task WL-5", task, "dependsOn"},
		{"A follow-up to WL-5", task, "followUpTo"},
		{"subtask of WL-5", task, "childOf"},
		{"Refines WL-REQ-3.", rule, "refines"},
		{"runs after WL-PLAN-4", plans, "blockedBy"},
		{"requires WL-SPEC-2", plans, "requires"},
		// No candidate's phrase: not a hit.
		{"governed by WL-REQ-3", task, ""},
		{"see WL-5", task, ""},
		// Word boundaries.
		{"independs onward WL-5", task, ""},
		{"unrefines WL-REQ-3", rule, ""},
		// An inverse phrase names no label.
		{"blocks WL-5", task, ""},
		{"depends on WL-5", nil, ""},
	} {
		if got := Cue(c.line, c.cands); got != c.want {
			t.Errorf("Cue(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}
