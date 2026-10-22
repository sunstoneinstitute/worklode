package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// inlineRefRows reads one field's rows at version as "kind:id source lines".
func inlineRefRows(t *testing.T, s *Store, fromID, field string, version int) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT to_kind, to_id, source, lines::text FROM inline_refs
		  WHERE from_kind = 'rule' AND from_id = $1 AND field = $2 AND from_version = $3
		  ORDER BY to_kind, to_id`, fromID, field, version)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind, id, source, lines string
		if err := rows.Scan(&kind, &id, &source, &lines); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s:%s %s %s", kind, id, source, lines))
	}
	return out
}

// TestDeriveInlineRefs: a rule's field resolves task, section-with-rule,
// section-without-rule, milestone and self refs into grouped rows; a draft
// rewrite replaces them, a new version keeps the old version's rows, a
// manual row survives and takes the lines of a duplicate (WL-REQ-1851,
// WL-REQ-1852).
func TestDeriveInlineRefs(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	body := "---\nstatus: draft\n---\n# A\n\n## 1. Group {#sec-1}\n\n### 1.1 Leaf {#sec-1.1}\n\nX.\n\n### 1.2 Other {#sec-1.2}\n\nY.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: body, CreatedBy: "stig"})
	seedDocNoteTask(t, s, "P1-7")
	var mile string
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		m, err := CreateMilestone(tx, s.Now(), "p1", "M", 0, "ada")
		mile = m.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	leaf := strconv.FormatInt(ruleID(t, s, "P1", 1), 10)
	self := strconv.FormatInt(ruleID(t, s, "P1", 2), 10)
	doc := strconv.FormatInt(d.ID, 10)

	derive := func(version int, text string) {
		t.Helper()
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			return deriveInlineRefs(tx, "p1", "rule", self, "body", &version, text)
		}); err != nil {
			t.Fatal(err)
		}
	}
	text := "See P1-7, P1-SPEC-1#sec-1.1, P1-SPEC-1#sec-1 and " + mile + ".\n" +
		"Itself P1-REQ-2, nothing P1-REQ-99, and P1-7 twice: `P1-7`.\n"
	derive(1, text)
	want := []string{
		"doc:" + doc + " derived {1}",
		"milestone:" + mile + " derived {1}",
		"rule:" + leaf + " derived {1}",
		"task:P1-7 derived {1,2,2}",
	}
	if got := inlineRefRows(t, s, self, "body", 1); !slices.Equal(got, want) {
		t.Errorf("v1 rows:\n got %q\nwant %q", got, want)
	}

	// A draft rewrite of version 1 replaces its derived rows.
	derive(1, "Only P1-7.\n")
	if got, want := inlineRefRows(t, s, self, "body", 1), []string{"task:P1-7 derived {1}"}; !slices.Equal(got, want) {
		t.Errorf("after draft rewrite: got %q, want %q", got, want)
	}

	// A new version adds rows and keeps version 1's.
	derive(2, "Now "+mile+".\n")
	if got, want := inlineRefRows(t, s, self, "body", 1), []string{"task:P1-7 derived {1}"}; !slices.Equal(got, want) {
		t.Errorf("v1 after bump: got %q, want %q", got, want)
	}
	if got, want := inlineRefRows(t, s, self, "body", 2), []string{"milestone:" + mile + " derived {1}"}; !slices.Equal(got, want) {
		t.Errorf("v2: got %q, want %q", got, want)
	}

	// A manual row survives a derivation that does not name its target, and
	// takes the lines of one that does.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO inline_refs (from_kind, from_id, field, from_version, to_kind, to_id, lines, source)
		 VALUES ('rule', $1, 'body', 2, 'rule', $2, '{}', 'manual')`, self, leaf); err != nil {
		t.Fatal(err)
	}
	derive(2, "Nothing.\n")
	if got, want := inlineRefRows(t, s, self, "body", 2), []string{"rule:" + leaf + " manual {}"}; !slices.Equal(got, want) {
		t.Errorf("manual after unrelated derive: got %q, want %q", got, want)
	}
	derive(2, "x\nP1-SPEC-1#sec-1.1\n")
	if got, want := inlineRefRows(t, s, self, "body", 2), []string{"rule:" + leaf + " manual {2}"}; !slices.Equal(got, want) {
		t.Errorf("manual after naming derive: got %q, want %q", got, want)
	}
}

// TestRuleWritesDeriveInlineRefs: lode rule edit, an accepted rule's next
// version and a standalone add write the rule's heading and body rows
// (WL-REQ-1852).
func TestRuleWritesDeriveInlineRefs(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	one := strconv.FormatInt(ruleID(t, s, "P1", 1), 10)
	three := strconv.FormatInt(ruleID(t, s, "P1", 3), 10)
	if _, err := s.EditRule(ctx, "P1", 1, model.EditRuleInput{Heading: "Heading P1-REQ-3", Body: "Body P1-REQ-3.\n"}, "stig"); err != nil {
		t.Fatal(err)
	}
	if got, want := inlineRefRows(t, s, one, "heading", 1), []string{"rule:" + three + " derived {1}"}; !slices.Equal(got, want) {
		t.Errorf("heading rows: got %q, want %q", got, want)
	}
	got := inlineRefRows(t, s, one, "body", 1)
	if len(got) != 1 || !strings.HasPrefix(got[0], "rule:"+three+" derived") {
		t.Errorf("body rows: %q", got)
	}
}

// TestRuleReferencesAcrossVersions: lode rule add derives references, a
// manual references edge carries to the next version, and the older version
// still shows its own references (WL-REQ-1640, WL-REQ-1852).
func TestRuleReferencesAcrossVersions(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	for _, body := range []string{"B.", "Names P1-REQ-1."} {
		if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "H", Body: body}, "stig"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Third", Body: "C."}, "stig"); err != nil {
		t.Fatal(err)
	}
	edges := func(r *model.Rule) []string {
		var out []string
		for _, e := range r.Edges {
			if e.From == r.Ref {
				out = append(out, e.Type+" "+e.To+" "+e.Source)
			}
		}
		return out
	}
	r, err := s.GetRule(ctx, "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := edges(r), []string{"references P1-REQ-1 derived"}; !slices.Equal(got, want) {
		t.Fatalf("after add: %q, want %q", got, want)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return LinkRules(tx, ruleID(t, s, "P1", 2), ruleID(t, s, "P1", 3), "references")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 2, false, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EditRule(ctx, "P1", 2, model.EditRuleInput{Heading: "H", Body: "No refs now."}, "stig"); err != nil {
		t.Fatal(err)
	}
	if r, err = s.GetRule(ctx, "P1", 2); err != nil {
		t.Fatal(err)
	}
	if got, want := edges(r), []string{"references P1-REQ-3 manual"}; r.Version != 2 || !slices.Equal(got, want) {
		t.Errorf("v%d: %q, want %q", r.Version, got, want)
	}
	if r, err = s.GetRuleVersion(ctx, "P1", 2, 1); err != nil {
		t.Fatal(err)
	}
	if got, want := edges(r), []string{"references P1-REQ-1 derived", "references P1-REQ-3 manual"}; !slices.Equal(got, want) {
		t.Errorf("v1: %q, want %q", got, want)
	}
}
