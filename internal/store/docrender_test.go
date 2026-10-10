package store

import (
	"context"
	"strings"
	"testing"
)

// TestRenderBody: an arranged rule's text and a spec heading's text replace
// the stored copy at their anchors; the preamble and an unanchored section
// are template text and stay; a body that already agrees is returned as is.
func TestRenderBody(t *testing.T) {
	body := "Intro.\n\n## 1. Head {#sec-1}\n\n### 1.1 Old {#sec-1.1}\n\nOld text.\n\n## Sources\n\nA list.\n"
	texts := map[string]arrangedText{
		"sec-1":   {heading: "Renamed head"},
		"sec-1.1": {heading: "New", body: "\nNew text.\n", rule: true},
	}
	got := renderBody(body, texts)
	want := "Intro.\n\n## 1. Renamed head {#sec-1}\n\n### 1.1 New {#sec-1.1}\n\nNew text.\n\n## Sources\n\nA list.\n"
	if got != want {
		t.Errorf("rendered:\n%q\nwant:\n%q", got, want)
	}
	if again := renderBody(want, texts); again != want {
		t.Errorf("an agreeing body changed:\n%q", again)
	}
}

// TestGetDocRendersArrangedRuleText: a spec's text comes from the rule
// version its arrangement holds, not from the stored body (WL-SPEC-77
// §19.5), and keeps the stored template text around it.
func TestGetDocRendersArrangedRuleText(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, err := s.db.ExecContext(ctx,
		`UPDATE rule_versions SET heading = 'Second', body = E'\nC from the rule.\n'
		  WHERE rule_id = $1 AND version = 1`, ruleID(t, s, "P1", 3)); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDoc(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Intro.", "## 2. Second {#sec-2}", "C from the rule."} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("rendered body lacks %q:\n%s", want, got.Body)
		}
	}
	v, err := s.GetDocVersion(ctx, d.ID, got.Version)
	if err != nil {
		t.Fatal(err)
	}
	if v.Body != got.Body {
		t.Errorf("current version body:\n%s\nwant the document's:\n%s", v.Body, got.Body)
	}
}
