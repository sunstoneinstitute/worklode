package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRenderBody: an arranged rule's text and a spec heading's text replace
// the stored copy at their anchors; the preamble and an unanchored section
// are template text and stay; a body that already agrees is returned as is.
func TestRenderBody(t *testing.T) {
	body := "Intro.\n\n## 1. Head {#sec-1}\n\n### 1.1 Old {#sec-1.1}\n\nOld text.\n\n## Sources\n\nA list.\n"
	texts := []arrangedText{
		{heading: "Renamed head", depth: 2},
		{heading: "New", body: "\nNew text.\n", rule: true, ref: "P1-REQ-4", depth: 3},
	}
	got := renderBody(body, texts)
	want := "Intro.\n\n## 1. Renamed head {#sec-1}\n\n### 1.1 New (P1-REQ-4) {#sec-1.1}\n\nNew text.\n\n## Sources\n\nA list.\n"
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
	for _, want := range []string{"Intro.", "## 2. Second (P1-REQ-3) {#sec-2}", "C from the rule."} {
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

// TestRenderBodyNumbersByPosition: the k-th placeholder shows the k-th
// entry, numbered and anchored from its place whatever the stored anchor
// says (WL-REQ-165), and an entry with no placeholder renders at the end.
func TestRenderBodyNumbersByPosition(t *testing.T) {
	body := "Intro.\n\n## 3. One {#sec-3}\n\n## 3a Two {#sec-3a}\n"
	texts := []arrangedText{
		{heading: "One", body: "\nA.\n", rule: true, ref: "P1-REQ-1", depth: 2},
		{heading: "Two", body: "\nB.\n", rule: true, ref: "P1-REQ-2", depth: 2},
		{heading: "Three", body: "\nC.\n", rule: true, ref: "P1-RULE-3", depth: 2},
		{heading: "Open questions", depth: 2, slug: "sec-open-questions"},
	}
	want := "Intro.\n\n## 1. One (P1-REQ-1) {#sec-1}\n\nA.\n\n## 2. Two (P1-REQ-2) {#sec-2}\n\nB.\n\n" +
		"## 3. Three (P1-RULE-3) {#sec-3}\n\nC.\n\n## Open questions {#sec-open-questions}\n"
	if got := renderBody(body, texts); got != want {
		t.Errorf("rendered:\n%q\nwant:\n%q", got, want)
	}
}

// TestSpecWriteStoresTemplate: a spec write keeps template text and each
// entry's heading line in docs.body and moves rule text out of it; reads
// render the full text back (WL-REQ-1295, WL-REQ-1299).
func TestSpecWriteStoresTemplate(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	body := "# T\n\nIntro.\n\n## 1. One {#sec-1}\n\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n#### Example\n\nE.\n\n## 2. Two {#sec-2}\n\nC.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: body, CreatedBy: "stig"})
	var stored string
	if err := s.db.QueryRowContext(ctx, `SELECT body FROM docs WHERE id = $1`, d.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	want := "# T\n\nIntro.\n\n## 1. One {#sec-1}\n\n### 1.1 Sub {#sec-1.1}\n#### Example\n\nE.\n\n## 2. Two {#sec-2}\n"
	if stored != want {
		t.Errorf("stored body:\n%q\nwant:\n%q", stored, want)
	}
	if plain(d.Body) != body {
		t.Errorf("rendered body:\n%q\nwant:\n%q", d.Body, body)
	}
	edited := strings.Replace(body, "C.", "C, edited.", 1)
	got, err := updateDocBody(t, s, d.ID, edited)
	if err != nil {
		t.Fatal(err)
	}
	if plain(got.Body) != edited {
		t.Errorf("rendered after edit:\n%q", got.Body)
	}
	list, err := s.ListDocs(ctx, DocFilter{Project: "p1"})
	if err != nil || len(list) != 1 || plain(list[0].Body) != edited {
		t.Errorf("ListDocs body:\n%+v, err %v", list, err)
	}
}

// TestSpecWriteGate: a spec write is refused when it carries a heading
// within the anchor depth that has no anchor, naming it; a deeper one is
// rule content, and a plan is exempt (WL-REQ-1295).
func TestSpecWriteGate(t *testing.T) {
	s := openDocStore(t)
	bad := "Intro.\n\n## 1. One {#sec-1}\n\nA.\n\n## Sources\n\nA list.\n"
	if _, err := createDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "bad", Body: bad, CreatedBy: "stig"}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), `"## Sources"`) {
		t.Errorf("create with ## Sources: %v, want ErrInvalidInput naming it", err)
	}
	ok := "Intro.\n\n## 1. One {#sec-1}\n\nA.\n\n#### Deep\n\nD.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "ok", Body: ok, CreatedBy: "stig"})
	if _, err := updateDocBody(t, s, d.ID, ok+"\n### Unanchored\n\nX.\n"); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), `"### Unanchored"`) {
		t.Errorf("edit with ### Unanchored: %v, want ErrInvalidInput naming it", err)
	}
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := reviseDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := updateRevision(t, s, d.ID, ok+"\n## Sources\n"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("revision with ## Sources: %v, want ErrInvalidInput", err)
	}
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", Body: "# P\n\n## Notes\n\nfree.\n", CreatedBy: "stig"})
}
