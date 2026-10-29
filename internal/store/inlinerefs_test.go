package store

import (
	"context"
	"database/sql"
	"errors"
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
			return deriveInlineRefs(tx, "p1", "rule", self, "body", &version, "", text, 0)
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
	derive(2, "x\nP1-REQ-1\n")
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
		if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "H", Body: model.Markdown(body)}, "stig"); err != nil {
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

// normalizeNode runs NormalizeNode in its own event's transaction.
func normalizeNode(t *testing.T, s *Store, kind model.NodeKind, id string) (bool, string, error) {
	t.Helper()
	var changed bool
	var diff string
	_, _, err := s.RecordDocEvent(t.Context(), "normalize", "cli",
		fmt.Sprintf("normalize-%d", docEventSeq.Add(1)), "refs.normalized", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			changed, diff, err = NormalizeNode(tx, s.Now(), "p1", kind, id, eventID)
			return err
		})
	return changed, diff, err
}

// taskCount counts tasks, so a test can see that a write minted none.
func taskCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestNormalizeAcceptedRule: an accepted rule normalizes to a new accepted
// version marked normalize, with no review task (WL-REQ-1855).
func TestNormalizeAcceptedRule(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Target", Body: "T.", Kind: "invariant"}, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Names", Body: "Names P1-REQ-1.\nAnd `P1-REQ-1`."}, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 2, false, "stig"); err != nil {
		t.Fatal(err)
	}
	before := taskCount(t, s)
	changed, diff, err := normalizeNode(t, s, "rule", strconv.FormatInt(ruleID(t, s, "P1", 2), 10))
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(diff, "-Names P1-REQ-1.") || !strings.Contains(diff, "+Names P1-RULE-1.") {
		t.Errorf("changed %v, diff:\n%s", changed, diff)
	}
	r, err := s.GetRule(ctx, "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != 2 || r.Status != "accepted" || r.Change != "normalize" || !strings.HasPrefix(string(r.Body), "\nNames P1-RULE-1.\nAnd `P1-REQ-1`.") {
		t.Errorf("v%d %s %q body %q", r.Version, r.Status, r.Change, r.Body)
	}
	if n := taskCount(t, s); n != before {
		t.Errorf("tasks %d -> %d; a normalization mints no review", before, n)
	}
	if changed, _, err := normalizeNode(t, s, "rule", strconv.FormatInt(ruleID(t, s, "P1", 2), 10)); err != nil || changed {
		t.Errorf("second run: changed %v, err %v", changed, err)
	}
}

// TestNormalizeRefusesForgedChange: a rewrite that is not a pure
// normalization of an accepted rule writes nothing and names the rule
// (WL-REQ-1855).
func TestNormalizeRefusesForgedChange(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Names", Body: "Names P1-7."}, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 1, false, "stig"); err != nil {
		t.Fatal(err)
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		return normalizeRule(tx, "p1", ruleID(t, s, "P1", 1), "Names", "\nNames P1-7, and more.\n")
	})
	if err == nil || !strings.Contains(err.Error(), "P1-REQ-1") {
		t.Fatalf("err = %v, want a refusal naming P1-REQ-1", err)
	}
	r, err := s.GetRule(ctx, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != 1 || r.Status != "accepted" {
		t.Errorf("v%d %s after a refused normalization", r.Version, r.Status)
	}
}

// TestNormalizeTaskBody: a task body is rewritten in place (WL-REQ-1855).
func TestNormalizeTaskBody(t *testing.T) {
	s := openDocStore(t)
	seedDocNoteTask(t, s, "P1-8")
	seedDocNoteTask(t, s, "P1-7")
	if _, err := s.db.ExecContext(t.Context(), `UPDATE tasks SET body = $1 WHERE id = 'P1-7'`, "Needs https://lode.example/P1-8 first.\nThen [it](https://lode.example/tasks/P1-8)."); err != nil {
		t.Fatal(err)
	}
	changed, _, err := normalizeNode(t, s, "task", "P1-7")
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	var body string
	if err := s.db.QueryRowContext(t.Context(), `SELECT body FROM tasks WHERE id = 'P1-7'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if want := "Needs P1-8 first.\nThen [it](P1-8)."; body != want {
		t.Errorf("body %q, want %q", body, want)
	}
}

// TestNormalizeAcceptedDoc: an accepted spec's own text normalizes to a new
// version that stays accepted, and lode doc versions marks it normalize
// (WL-REQ-1855).
func TestNormalizeAcceptedDoc(t *testing.T) {
	s := openDocStore(t)
	seedDocNoteTask(t, s, "P1-8")
	body := strings.Replace(ruleDocV1, "Intro.", "Intro names https://lode.example/P1-8.", 1)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: body, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	before := taskCount(t, s)
	changed, _, err := normalizeNode(t, s, "doc", strconv.FormatInt(d.ID, 10))
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	got, err := s.GetDoc(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "accepted" || !strings.Contains(string(got.Body), "Intro names P1-8.") || !strings.Contains(string(got.Body), "\nA.\n") {
		t.Errorf("%s body %q", got.Status, got.Body)
	}
	if n := taskCount(t, s); n != before {
		t.Errorf("tasks %d -> %d; a normalization mints no review", before, n)
	}
	versions, err := s.ListDocVersions(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) < 2 || versions[0].Version != got.Version || versions[0].Change != "normalize" || versions[1].Change != "edit" {
		t.Errorf("versions %+v", versions)
	}
}

// TestListInlineRefs: a task shows the doc that names it as an incoming ref
// and the doc shows it as outgoing, each with the far end's ref and title; a
// row at a version the source has moved past is not listed (WL-REQ-1852).
func TestListInlineRefs(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	seedDocNoteTask(t, s, "P1-7")
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a",
		Body: "---\nstatus: draft\n---\n# A\n\nSee P1-7.\n", CreatedBy: "stig"})
	doc := strconv.FormatInt(d.ID, 10)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO inline_refs (from_kind, from_id, field, from_version, to_kind, to_id, lines, source)
		 VALUES ('doc', $1, 'body', 99, 'task', 'P1-7', '{1}', 'derived')`, doc); err != nil {
		t.Fatal(err)
	}

	in, err := s.ListInlineRefs(ctx, "task", "P1-7", "in")
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 || in[0].FromKind != "doc" || in[0].FromID != doc || in[0].FromRef != "P1-SPEC-1" ||
		in[0].ToRef != "P1-7" || in[0].ToTitle != "T" || !slices.Equal(in[0].Lines, []int{3}) {
		t.Errorf("in = %+v", in)
	}
	out, err := s.ListInlineRefs(ctx, "doc", doc, "out")
	if err != nil || len(out) != 1 || out[0].ToKind != "task" {
		t.Errorf("out = %+v, %v", out, err)
	}
	none, err := s.ListInlineRefs(ctx, "task", "P1-7", "out")
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("none = %#v, %v", none, err)
	}
	if _, err := s.ListInlineRefs(ctx, "task", "P1-7", "sideways"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bad dir: %v", err)
	}
}

// deriveNode runs DeriveNode in its own event's transaction.
func deriveNode(t *testing.T, s *Store, kind model.NodeKind, id string) (model.RefDeriveNode, error) {
	t.Helper()
	var n model.RefDeriveNode
	_, _, err := s.RecordDocEvent(t.Context(), "derive", "cli",
		fmt.Sprintf("derive-%d", docEventSeq.Add(1)), "refs.derived", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			n, err = DeriveNode(tx, s.Now(), "p1", kind, id, eventID)
			return err
		})
	return n, err
}

// TestDeriveNodeTask: deriving a task whose rows were never written adds the
// reference rows, rewrites the link into plain form, names what resolves to
// nothing, and does nothing the second time (WL-REQ-1860).
func TestDeriveNodeTask(t *testing.T) {
	s := openDocStore(t)
	seedDocNoteTask(t, s, "P1-8")
	seedDocNoteTask(t, s, "P1-7")
	if _, err := s.db.ExecContext(t.Context(), `UPDATE tasks SET body = $1 WHERE id = 'P1-7'`, "Needs https://lode.example/P1-8 first.\nNot P1-99."); err != nil {
		t.Fatal(err)
	}
	n, err := deriveNode(t, s, "task", "P1-7")
	if err != nil {
		t.Fatal(err)
	}
	if n.Ref != "P1-7" || n.Rewrites != 1 || len(n.Added) != 1 || n.Added[0].ToRef != "P1-8" || len(n.Removed) != 0 ||
		!slices.Equal(n.Unresolved, []string{"P1-99"}) || !strings.Contains(n.Diff, "+Needs P1-8 first.") {
		t.Errorf("first run: %+v", n)
	}
	n, err = deriveNode(t, s, "task", "P1-7")
	if err != nil {
		t.Fatal(err)
	}
	if n.Rewrites != 0 || len(n.Added) != 0 || len(n.Removed) != 0 || n.Diff != "" {
		t.Errorf("second run: %+v", n)
	}
}

// TestDeriveNodeRemovesStale: a row for a reference the text no longer makes
// is reported removed.
func TestDeriveNodeRemovesStale(t *testing.T) {
	s := openDocStore(t)
	seedDocNoteTask(t, s, "P1-8")
	seedDocNoteTask(t, s, "P1-7")
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO inline_refs (from_kind, from_id, field, to_kind, to_id, lines, source)
		 VALUES ('task', 'P1-7', 'body', 'task', 'P1-8', '{1}', 'derived')`); err != nil {
		t.Fatal(err)
	}
	n, err := deriveNode(t, s, "task", "P1-7")
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Removed) != 1 || n.Removed[0].ToRef != "P1-8" || len(n.Added) != 0 {
		t.Errorf("%+v", n)
	}
}

// TestDeriveTargets: refs, filters and all each select; nothing selected is
// invalid input.
func TestDeriveTargets(t *testing.T) {
	s := openDocStore(t)
	seedDocNoteTask(t, s, "P1-7")
	seedDocNoteTask(t, s, "P1-8")
	ctx := t.Context()
	got, _, err := s.DeriveTargets(ctx, "p1", model.RefDeriveParams{Kind: "task", Refs: []string{"P1-8"}})
	if err != nil || !slices.Equal(got, []string{"P1-8"}) {
		t.Errorf("refs: %v, %v", got, err)
	}
	got, _, err = s.DeriveTargets(ctx, "p1", model.RefDeriveParams{Kind: "task", All: true})
	if err != nil || len(got) != 2 {
		t.Errorf("all: %v, %v", got, err)
	}
	got, _, err = s.DeriveTargets(ctx, "p1", model.RefDeriveParams{Kind: "task", Status: []string{"merged"}})
	if err != nil || len(got) != 0 {
		t.Errorf("status: %v, %v", got, err)
	}
	if _, _, err = s.DeriveTargets(ctx, "p1", model.RefDeriveParams{Kind: "task"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("nothing selected: %v", err)
	}
	if _, _, err = s.DeriveTargets(ctx, "p1", model.RefDeriveParams{Kind: "task", Refs: []string{"P1-404"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown ref: %v", err)
	}
	if _, _, err = s.DeriveTargets(ctx, "p1", model.RefDeriveParams{Kind: "idea", All: true}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("idea: %v", err)
	}
}

// TestDeriveTargetsPages: Limit pages the selection in id order, Next
// resumes it, and the last page has no Next (WL-REQ-1860).
func TestDeriveTargetsPages(t *testing.T) {
	s := openDocStore(t)
	for _, id := range []string{"P1-7", "P1-8", "P1-9"} {
		seedDocNoteTask(t, s, id)
	}
	var got []string
	var pages int
	p := model.RefDeriveParams{Kind: "task", All: true, Limit: 2}
	for {
		ids, next, err := s.DeriveTargets(t.Context(), "p1", p)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		got = append(got, ids...)
		if next == "" {
			break
		}
		if next != ids[len(ids)-1] {
			t.Fatalf("next = %q, want last id of %v", next, ids)
		}
		p.After = next
	}
	if pages != 2 || !slices.Equal(got, []string{"P1-7", "P1-8", "P1-9"}) {
		t.Errorf("pages = %d, ids = %v", pages, got)
	}
}

// viaRows reads one field's rows as "kind:id via_doc via_anchor lines";
// version nil reads the unversioned rows.
func viaRows(t *testing.T, s *Store, kind model.NodeKind, id, field string, version *int) []string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT to_kind, to_id, coalesce(via_doc, ''), coalesce(via_anchor, ''), lines::text FROM inline_refs
		  WHERE from_kind = $1 AND from_id = $2 AND field = $3 AND from_version IS NOT DISTINCT FROM $4::int
		  ORDER BY to_kind, to_id, via_anchor`, kind, id, field, version)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k, i, d, a, l string
		if err := rows.Scan(&k, &i, &d, &a, &l); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s:%s %s %s %s", k, i, d, a, l))
	}
	return out
}

// TestSectionRefKeepsResolution: a rule fragment targets the rule; a section
// fragment new in a write resolves against the arrangement; a later write
// keeps that resolution after the arrangement moves; a ref removed and added
// back resolves afresh; a section fragment that predates resolution targets the
// doc (WL-RULE-1849, WL-REQ-1852).
func TestSectionRefKeepsResolution(t *testing.T) {
	s := openDocStore(t)
	ctx := t.Context()
	body := "---\nstatus: draft\n---\n# A\n\n## 1. Group {#sec-1}\n\n### 1.1 Leaf {#sec-1.1}\n\nX.\n\n### 1.2 Other {#sec-1.2}\n\nY.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: body, CreatedBy: "stig"})
	if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Moved", Body: "Z."}, "stig"); err != nil {
		t.Fatal(err)
	}
	seedDocNoteTask(t, s, "P1-7")
	doc := strconv.FormatInt(d.ID, 10)
	leaf := strconv.FormatInt(ruleID(t, s, "P1", 1), 10)
	moved := strconv.FormatInt(ruleID(t, s, "P1", 3), 10)
	setBody := func(text string) {
		t.Helper()
		m := model.Markdown(text)
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			return UpdateTaskFields(tx, s.Now(), "P1-7", nil, &m, nil, nil, nil, nil, nil, nil, nil, nil)
		}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(step string, want ...string) {
		t.Helper()
		if got := viaRows(t, s, "task", "P1-7", "body", nil); !slices.Equal(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", step, got, want)
		}
	}

	// A rule fragment targets the rule, even one the doc does not arrange.
	setBody("See P1-SPEC-1#rule-3.")
	check("rule fragment", "rule:"+moved+" "+doc+"  {1}")

	// New section refs resolve against the arrangement.
	setBody("See P1-SPEC-1#sec-1.1 and P1-SPEC-1#sec-1.")
	check("new section refs", "doc:"+doc+"  sec-1 {1}", "rule:"+leaf+" "+doc+" sec-1.1 {1}")

	// The arrangement moves: sec-1.1 now holds another rule.
	if _, err := s.db.ExecContext(ctx, `UPDATE doc_rules SET rule_id = $2 WHERE doc_id = $1 AND rule_id = $3`, d.ID, moved, leaf); err != nil {
		t.Fatal(err)
	}
	setBody("Unrelated.\nSee P1-SPEC-1#sec-1.1 and P1-SPEC-1#sec-1.")
	check("after unrelated edit", "doc:"+doc+"  sec-1 {2}", "rule:"+leaf+" "+doc+" sec-1.1 {2}")

	// Removed, then added back: resolved afresh.
	setBody("Nothing.")
	check("removed")
	setBody("See P1-SPEC-1#sec-1.1.")
	check("added back", "rule:"+moved+" "+doc+" sec-1.1 {1}")

	// Text from before resolution (a legacy row with no via) keeps its
	// section ref on the doc through an unrelated edit and through derive.
	for _, q := range []string{
		`DELETE FROM inline_refs WHERE from_kind = 'task' AND from_id = 'P1-7'`,
		`UPDATE tasks SET body = 'See P1-SPEC-1#sec-1.2.' WHERE id = 'P1-7'`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := deriveNode(t, s, "task", "P1-7"); err != nil {
		t.Fatal(err)
	}
	check("derive on legacy text", "doc:"+doc+"  sec-1.2 {1}")
	if _, err := s.db.ExecContext(ctx, `UPDATE inline_refs SET via_anchor = NULL WHERE from_kind = 'task' AND from_id = 'P1-7'`); err != nil {
		t.Fatal(err)
	}
	setBody("Edited.\nSee P1-SPEC-1#sec-1.2.")
	check("edit of legacy text", "doc:"+doc+"  sec-1.2 {2}")
}

// TestSectionRefKeepsResolutionAcrossVersions: a versioned field's next
// version keeps the resolution its previous version's rows hold.
func TestSectionRefKeepsResolutionAcrossVersions(t *testing.T) {
	s := openDocStore(t)
	ctx := t.Context()
	body := "---\nstatus: draft\n---\n# A\n\n## 1. Leaf {#sec-1}\n\nX.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: body, CreatedBy: "stig"})
	for _, h := range []string{"Moved", "Citer"} {
		if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: model.Markdown(h), Body: "Z."}, "stig"); err != nil {
			t.Fatal(err)
		}
	}
	doc := strconv.FormatInt(d.ID, 10)
	leaf := strconv.FormatInt(ruleID(t, s, "P1", 1), 10)
	moved := strconv.FormatInt(ruleID(t, s, "P1", 2), 10)
	citer := strconv.FormatInt(ruleID(t, s, "P1", 3), 10)
	derive := func(version int, prev, text string) {
		t.Helper()
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			return deriveInlineRefs(tx, "p1", "rule", citer, "body", &version, prev, text, 0)
		}); err != nil {
			t.Fatal(err)
		}
	}
	derive(1, "", "P1-SPEC-1#sec-1")
	if _, err := s.db.ExecContext(ctx, `UPDATE doc_rules SET rule_id = $2 WHERE doc_id = $1 AND rule_id = $3`, d.ID, moved, leaf); err != nil {
		t.Fatal(err)
	}
	derive(2, "P1-SPEC-1#sec-1", "Edited P1-SPEC-1#sec-1")
	v := 2
	if got, want := viaRows(t, s, "rule", citer, "body", &v), []string{"rule:" + leaf + " " + doc + " sec-1 {1}"}; !slices.Equal(got, want) {
		t.Errorf("v2: got %q, want %q", got, want)
	}
}

// normalizeSetup makes a spec P1-SPEC-1 whose sec-1.1 arranges P1-REQ-1 and
// whose group sec-1 arranges no rule, a third rule P1-REQ-3, and task P1-7.
// setBody writes the task body through its write path, which stores its rows.
func normalizeSetup(t *testing.T) (s *Store, docID int64, setBody func(string), body func() string) {
	t.Helper()
	s = openDocStore(t)
	ctx := t.Context()
	text := "---\nstatus: draft\n---\n# A\n\n## 1. Group {#sec-1}\n\n### 1.1 Leaf {#sec-1.1}\n\nX.\n\n### 1.2 Other {#sec-1.2}\n\nY.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: text, CreatedBy: "stig"})
	if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Moved", Body: "Z."}, "stig"); err != nil {
		t.Fatal(err)
	}
	seedDocNoteTask(t, s, "P1-7")
	setBody = func(text string) {
		t.Helper()
		m := model.Markdown(text)
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			return UpdateTaskFields(tx, s.Now(), "P1-7", nil, &m, nil, nil, nil, nil, nil, nil, nil, nil)
		}); err != nil {
			t.Fatal(err)
		}
	}
	body = func() string {
		t.Helper()
		var b string
		if err := s.db.QueryRowContext(ctx, `SELECT body FROM tasks WHERE id = 'P1-7'`).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	return s, d.ID, setBody, body
}

// TestNormalizeSectionRefFromRuleRow: a section ref whose stored row targets
// a rule is rewritten to the spec's #rule-<n> form, and its row still targets
// that rule (WL-REQ-1854).
func TestNormalizeSectionRefFromRuleRow(t *testing.T) {
	s, docID, setBody, body := normalizeSetup(t)
	setBody("See P1-SPEC-1#sec-1.1.")
	if changed, _, err := normalizeNode(t, s, "task", "P1-7"); err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if got, want := body(), "See P1-SPEC-1#rule-1."; got != want {
		t.Errorf("body %q, want %q", got, want)
	}
	leaf := strconv.FormatInt(ruleID(t, s, "P1", 1), 10)
	if got, want := viaRows(t, s, "task", "P1-7", "body", nil), []string{"rule:" + leaf + " " + strconv.FormatInt(docID, 10) + "  {1}"}; !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}
}

// TestNormalizeSectionRefFromDocRow: a section ref whose stored row targets
// the doc is left as written (WL-REQ-1854).
func TestNormalizeSectionRefFromDocRow(t *testing.T) {
	s, _, setBody, body := normalizeSetup(t)
	setBody("See P1-SPEC-1#sec-1.")
	if changed, _, err := normalizeNode(t, s, "task", "P1-7"); err != nil || changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if got, want := body(), "See P1-SPEC-1#sec-1."; got != want {
		t.Errorf("body %q, want %q", got, want)
	}
}

// TestNormalizeSectionRefAfterMove: when the arrangement moved since the row
// was written, the rewrite names the stored rule, not the one arranged at the
// anchor today (WL-REQ-1854).
func TestNormalizeSectionRefAfterMove(t *testing.T) {
	s, docID, setBody, body := normalizeSetup(t)
	setBody("See P1-SPEC-1#sec-1.1.")
	if _, err := s.db.ExecContext(t.Context(), `UPDATE doc_rules SET rule_id = $2 WHERE doc_id = $1 AND rule_id = $3`,
		docID, ruleID(t, s, "P1", 3), ruleID(t, s, "P1", 1)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := normalizeNode(t, s, "task", "P1-7"); err != nil {
		t.Fatal(err)
	}
	if got, want := body(), "See P1-SPEC-1#rule-1."; got != want {
		t.Errorf("body %q, want %q", got, want)
	}
}

// TestNormalizeAcceptedRuleSectionRef: inlineref.Verify refuses a section
// ref rewritten to another rule, and accepts the rewrite to the rule its
// stored row targets, because #rule-<n> resolves to that rule (WL-REQ-1854,
// WL-REQ-1855).
func TestNormalizeAcceptedRuleSectionRef(t *testing.T) {
	s, _, _, _ := normalizeSetup(t)
	ctx := t.Context()
	if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Cites", Body: "See P1-SPEC-1#sec-1.1."}, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 4, false, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return normalizeRule(tx, "p1", ruleID(t, s, "P1", 4), "Cites", "\nSee P1-SPEC-1#rule-3.\n")
	}); err == nil || !strings.Contains(err.Error(), "referenced targets changed") {
		t.Fatalf("rewrite to another rule: err = %v, want targets changed", err)
	}
	if changed, _, err := normalizeNode(t, s, "rule", strconv.FormatInt(ruleID(t, s, "P1", 4), 10)); err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	r, err := s.GetRule(ctx, "P1", 4)
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != 2 || r.Change != "normalize" || !strings.Contains(string(r.Body), "See P1-SPEC-1#rule-1.") {
		t.Errorf("v%d %q body %q", r.Version, r.Change, r.Body)
	}
}

// TestRuleFragmentManualAndMissing: a derived <doc>#rule-<n> to a rule a
// manual row already targets adds its lines to the manual row whatever its
// via (WL-REQ-1852); a #rule-<n> naming no rule of the doc's project writes
// no row and is reported unresolved (WL-RULE-1849, WL-REQ-1851).
func TestRuleFragmentManualAndMissing(t *testing.T) {
	s := openDocStore(t)
	body := "---\nstatus: draft\n---\n# A\n\n## 1. Leaf {#sec-1}\n\nX.\n"
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: body, CreatedBy: "stig"})
	seedDocNoteTask(t, s, "P1-7")
	leaf := strconv.FormatInt(ruleID(t, s, "P1", 1), 10)
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO inline_refs (from_kind, from_id, field, to_kind, to_id, lines, source)
		 VALUES ('task', 'P1-7', 'body', 'rule', $1, '{}', 'manual')`, leaf); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE tasks SET body = $1 WHERE id = 'P1-7'`,
		"See P1-REQ-1.\nAnd P1-SPEC-1#rule-1.\nNot P1-SPEC-1#rule-99."); err != nil {
		t.Fatal(err)
	}
	n, err := deriveNode(t, s, "task", "P1-7")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := viaRows(t, s, "task", "P1-7", "body", nil), []string{"rule:" + leaf + "   {1,2}"}; !slices.Equal(got, want) {
		t.Errorf("rows:\n got %q\nwant %q", got, want)
	}
	if !slices.Equal(n.Unresolved, []string{"P1-SPEC-1#rule-99"}) {
		t.Errorf("unresolved: got %q", n.Unresolved)
	}
}
