package corpusindex

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestDocHeader checks the exact format spec 040 §4.3 quotes.
func TestDocHeader(t *testing.T) {
	doc := model.Doc{ProjectKey: "WL", Kind: "spec", Number: 25, Title: "Documents in the backbone"}
	got := DocHeader(doc, "15.2", "The ordered log")
	want := `WL-SPEC-25 "Documents in the backbone" — §15.2 The ordered log`
	if got != want {
		t.Errorf("DocHeader = %q, want %q", got, want)
	}
}

// TestDocHeaderNoNumber covers a plan heading (no section number) and the
// whole-document case (no heading at all).
func TestDocHeaderNoNumber(t *testing.T) {
	doc := model.Doc{ProjectKey: "WL", Kind: "plan", Number: 2, Title: "A Plan"}
	if got, want := DocHeader(doc, "", "Tasks"), `WL-PLAN-2 "A Plan" — Tasks`; got != want {
		t.Errorf("DocHeader with heading only = %q, want %q", got, want)
	}
	if got, want := DocHeader(doc, "", ""), `WL-PLAN-2 "A Plan"`; got != want {
		t.Errorf("DocHeader with no section = %q, want %q", got, want)
	}
}

// TestTaskHeader checks the exact format spec 040 §4.3 quotes.
func TestTaskHeader(t *testing.T) {
	task := model.Task{ID: "WL-142", Kind: "feature", State: "in_progress", Title: "Fix the thing"}
	got := TaskHeader(task)
	want := "WL-142 [feature/in_progress] Fix the thing"
	if got != want {
		t.Errorf("TaskHeader = %q, want %q", got, want)
	}
}

// TestSkillHeader checks the exact format spec 040 §4.3 quotes.
func TestSkillHeader(t *testing.T) {
	skill := model.Skill{Name: "test-driven-development", Description: "Write the test before the code"}
	got := SkillHeader(skill)
	want := "skill: test-driven-development — Write the test before the code"
	if got != want {
		t.Errorf("SkillHeader = %q, want %q", got, want)
	}
}
