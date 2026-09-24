package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// arranged is one row of a document's arrangement as the tests read it back.
type arranged struct {
	Position, Depth int
	Number          int64
	RuleVersion     int
	Anchor          string
	Status          string
}

func arrangementOf(t *testing.T, s *Store, docID int64) []arranged {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT dc.position, dc.depth, c.number, dc.rule_version, dc.anchor, c.status
		   FROM doc_rules dc JOIN rules c ON c.id = dc.rule_id
		  WHERE dc.doc_id = $1 ORDER BY dc.position`, docID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []arranged
	for rows.Next() {
		var a arranged
		if err := rows.Scan(&a.Position, &a.Depth, &a.Number, &a.RuleVersion, &a.Anchor, &a.Status); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func assertArrangement(t *testing.T, got, want []arranged) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("arrangement has %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

const ruleDocV1 = "---\nstatus: draft\n---\n# T\n\nIntro.\n\n## 1. One {#sec-1}\n\nA.\n\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n## 2. Two {#sec-2}\n\nC.\n"

// TestSyncRulesMintsVersionsAndKeepsIdentity: a create mints one rule per
// anchored section in order; an edit of a draft rewrites the changed rule's
// draft version in place, keeps the others untouched, and mints a new number for a new
// section; an anchor change with the same heading keeps the rule (S8 to
// S10, S20).
func TestSyncRulesMintsVersionsAndKeepsIdentity(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	assertArrangement(t, arrangementOf(t, s, d.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "draft"},
		{1, 3, 2, 1, "sec-1.1", "draft"},
		{2, 2, 3, 1, "sec-2", "draft"},
	})

	v2 := strings.Replace(ruleDocV1, "C.\n", "C changed.\n", 1) + "\n## 3. Three {#sec-3}\n\nD.\n"
	if _, err := updateDocBody(t, s, d.ID, v2); err != nil {
		t.Fatal(err)
	}
	assertArrangement(t, arrangementOf(t, s, d.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "draft"},
		{1, 3, 2, 1, "sec-1.1", "draft"},
		{2, 2, 3, 1, "sec-2", "draft"},
		{3, 2, 4, 1, "sec-3", "draft"},
	})

	v3 := strings.Replace(v2, "## 3. Three {#sec-3}", "## 2a. Three {#sec-2a}", 1)
	if _, err := updateDocBody(t, s, d.ID, v3); err != nil {
		t.Fatal(err)
	}
	got := arrangementOf(t, s, d.ID)
	if len(got) != 4 || got[3].Number != 4 || got[3].Anchor != "sec-2a" || got[3].RuleVersion != 1 {
		t.Errorf("anchor change with the same heading should keep rule 4 at v1: %+v", got)
	}

	var versions int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM rule_versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 4 {
		t.Errorf("rule_versions has %d rows, want 4 (four rules, draft edits rewrite in place)", versions)
	}
	c, err := s.GetRule(context.Background(), "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || !strings.Contains(c.Body, "C changed.") {
		t.Errorf("draft edit should rewrite v1 in place: %+v", c)
	}
}

// TestReviseRuleLocksAcceptedVersions: once a document is accepted its
// rules' versions are locked; landing a revision with changed text becomes
// the rule's version 2, accepted with the revision, and the v1 text is
// still readable (S10, S35).
func TestReviseRuleLocksAcceptedVersions(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := reviseDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	v2 := strings.Replace(ruleDocV1, "C.\n", "C changed.\n", 1)
	if err := updateRevision(t, s, d.ID, v2); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRevision(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	assertArrangement(t, arrangementOf(t, s, d.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "accepted"},
		{1, 3, 2, 1, "sec-1.1", "accepted"},
		{2, 2, 3, 2, "sec-2", "accepted"},
	})
	var v1 string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT body FROM rule_versions WHERE version = 1 AND rule_id = (SELECT id FROM rules WHERE number = 3)`).Scan(&v1); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v1, "changed") {
		t.Errorf("accepted v1 text must not change: %q", v1)
	}
}

// TestSyncRulesSkipsPlans: plans carry no anchors and arrange nothing in
// this increment.
func TestSyncRulesSkipsPlans(t *testing.T) {
	s := openDocStore(t)
	body := "---\nstatus: draft\ncovers: NO-SPEC\n---\n# P\n\n## Tasks\n\n### Task 1 — Do it\n\n```yaml\nkind: chore\n```\n\nText.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "p", Body: body, CreatedBy: "stig"})
	if got := arrangementOf(t, s, d.ID); len(got) != 0 {
		t.Errorf("plan arranged %d rules, want 0", len(got))
	}
}

// TestAcceptDocAcceptsItsRules: accepting a document accepts every rule it
// arranges and writes nothing else (S11).
func TestAcceptDocAcceptsItsRules(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig", Owner: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	got := arrangementOf(t, s, d.ID)
	if len(got) == 0 {
		t.Fatal("no rules arranged")
	}
	for _, a := range got {
		if a.Status != "accepted" {
			t.Errorf("rule %d status = %s, want accepted", a.Number, a.Status)
		}
	}
}

// TestSyncRulesKeepsIdentityAcrossAnchorRenumber: inserting a section
// shifts every anchor after it (`--update-section-anchors` and hand
// renumbering both rewrite anchors from tree position). Matching sections to
// prior rules one at a time in document order lets a later section's
// heading-fallback match steal an earlier section's rule once that
// earlier section's own anchor match already claimed a different rule —
// syncRules resolves matches over the whole document first so that can't
// happen.
func TestSyncRulesKeepsIdentityAcrossAnchorRenumber(t *testing.T) {
	s := openDocStore(t)
	v1 := "---\nstatus: draft\n---\n# T\n\n## 1. A {#sec-1}\n\nA.\n\n## 2. B {#sec-2}\n\nB.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: v1, CreatedBy: "stig"})

	v2 := "---\nstatus: draft\n---\n# T\n\n## 1. A {#sec-1}\n\nA.\n\n## 2. New {#sec-2}\n\nNew.\n\n## 3. B {#sec-3}\n\nB.\n"
	if _, err := updateDocBody(t, s, d.ID, v2); err != nil {
		t.Fatal(err)
	}

	got := arrangementOf(t, s, d.ID)
	byAnchor := map[string]arranged{}
	for _, a := range got {
		byAnchor[a.Anchor] = a
	}
	if b := byAnchor["sec-3"]; b.Number != 2 || b.RuleVersion != 1 {
		t.Errorf("B should keep rule 2 at v1: %+v", b)
	}
	if n := byAnchor["sec-2"]; n.Number != 3 || n.RuleVersion != 1 {
		t.Errorf("New should mint rule 3 at v1: %+v", n)
	}
}

// TestGetRule reads a rule by project key and number with its current
// text and the documents arranging it.
func TestGetRule(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	c, err := s.GetRule(context.Background(), "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Ref != "P1-RULE-2" || c.Heading != "Sub" || c.Body != "\nB.\n\n" || c.Status != "draft" || c.Version != 1 {
		t.Errorf("rule = %+v", c)
	}
	if len(c.ArrangedIn) != 1 || c.ArrangedIn[0].Doc != d.ID || c.ArrangedIn[0].DocRef != "P1-SPEC-1" ||
		c.ArrangedIn[0].Anchor != "sec-1.1" || c.ArrangedIn[0].Depth != 3 || c.ArrangedIn[0].Position != 1 {
		t.Errorf("arranged_in = %+v", c.ArrangedIn)
	}
	if _, err := s.GetRule(context.Background(), "P1", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing rule: err = %v, want ErrNotFound", err)
	}
}

// TestRuleVersionsAndGovernedTasks: the history lists every version newest
// first, one version reads with its own text, and the rule detail carries
// the tasks governed by it.
func TestRuleVersionsAndGovernedTasks(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := reviseDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := updateRevision(t, s, d.ID, strings.Replace(ruleDocV1, "C.\n", "C changed.\n", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRevision(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	vs, err := s.ListRuleVersions(context.Background(), "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].Version != 2 || vs[1].Version != 1 || vs[0].Heading != "Two" {
		t.Errorf("versions = %+v", vs)
	}
	v1, err := s.GetRuleVersion(context.Background(), "P1", 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Version != 1 || strings.Contains(v1.Body, "changed") || v1.Ref != "P1-RULE-3" {
		t.Errorf("v1 = %+v", v1)
	}
	if _, err := s.GetRuleVersion(context.Background(), "P1", 3, 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing version: %v, want ErrNotFound", err)
	}

	task := createTask(t, s, s.Now(), TaskInput{ProjectID: "p1", Title: "Governed", Kind: "feature", Priority: "medium", CreatedBy: "stig"})
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		id, err := RuleIDByRef(tx, "P1", 3)
		if err != nil {
			return err
		}
		return Govern(tx, task.ID, id, "manual", false)
	}); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetRule(context.Background(), "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.GovernedTasks) != 1 || c.GovernedTasks[0].ID != task.ID || c.GovernedTasks[0].Source != "manual" || c.GovernedTasks[0].RuleVersion != 2 {
		t.Errorf("governed tasks = %+v", c.GovernedTasks)
	}
}

// TestEditRuleDraftRewritesInPlace: editing a rule of a draft document
// regenerates the document body with only that section changed and rewrites
// the rule's draft version in place (S14, S35).
func TestEditRuleDraftRewritesInPlace(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	docID, err := editRule(t, s, "P1", 2, model.EditRuleInput{Heading: "Subsection", Body: "\nB changed.\n\n"}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if docID != d.ID {
		t.Errorf("docID = %d, want %d", docID, d.ID)
	}
	got, err := s.GetDoc(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(ruleDocV1, "### 1.1 Sub {#sec-1.1}\n\nB.\n", "### 1.1 Subsection {#sec-1.1}\n\nB changed.\n", 1)
	if got.Body != want {
		t.Errorf("body after rule edit:\n%s\nwant:\n%s", got.Body, want)
	}
	c, err := s.GetRule(context.Background(), "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || c.Heading != "Subsection" || c.Body != "\nB changed.\n\n" || c.Status != "draft" {
		t.Errorf("rule after edit = %+v", c)
	}
	assertArrangement(t, arrangementOf(t, s, d.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "draft"},
		{1, 3, 2, 1, "sec-1.1", "draft"},
		{2, 2, 3, 1, "sec-2", "draft"},
	})
}

// TestEditRuleAcceptedGoesThroughRevision: editing a rule of an accepted
// document opens (or updates) the candidate revision with the regenerated
// body; the rule itself does not move until the revision lands (R2, S13,
// S35).
func TestEditRuleAcceptedGoesThroughRevision(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := editRule(t, s, "P1", 3, model.EditRuleInput{Heading: "Two", Body: "\nC changed.\n\n"}, "stig"); err != nil {
		t.Fatal(err)
	}
	rev, err := s.GetDocRevision(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rev.Body, "C changed.") || strings.Contains(rev.Body, "\nC.\n") {
		t.Errorf("revision body:\n%s", rev.Body)
	}
	c, err := s.GetRule(context.Background(), "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || c.Body != "\nC.\n" || c.Status != "accepted" {
		t.Errorf("rule must not move before the revision lands: %+v", c)
	}
	// A second edit updates the same open revision rather than failing on
	// ErrRevisionExists.
	if _, err := editRule(t, s, "P1", 1, model.EditRuleInput{Heading: "One", Body: "\nA changed.\n\n"}, "stig"); err != nil {
		t.Fatal(err)
	}
	rev, _ = s.GetDocRevision(context.Background(), d.ID)
	if !strings.Contains(rev.Body, "A changed.") || !strings.Contains(rev.Body, "C changed.") {
		t.Errorf("second edit lost the first: %s", rev.Body)
	}
	if _, err := acceptRevision(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetRule(context.Background(), "P1", 3)
	// sec-2 is the document's last section, so its body ends in a single
	// newline the way Parse produced it, whatever the caller sent.
	if c.Version != 2 || c.Body != "\nC changed.\n" || c.Status != "accepted" {
		t.Errorf("after landing: %+v", c)
	}
}

// TestEditRuleNormalisesBodyWhitespace: a body with neither a leading nor a
// trailing newline still renders a document that re-parses to the same
// sections and anchors. Document.Bytes writes the heading and the body with
// nothing between them, so an unnormalised body runs into the next heading,
// drops that rule from the arrangement and orphans it.
func TestEditRuleNormalisesBodyWhitespace(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	want := []arranged{
		{0, 2, 1, 1, "sec-1", "draft"},
		{1, 3, 2, 1, "sec-1.1", "draft"},
		{2, 2, 3, 1, "sec-2", "draft"},
	}

	// A middle section: a blank line must still separate it from the next
	// heading.
	if _, err := editRule(t, s, "P1", 2, model.EditRuleInput{Heading: "Sub", Body: "B tight."}, "stig"); err != nil {
		t.Fatal(err)
	}
	assertArrangement(t, arrangementOf(t, s, d.ID), want)
	assertSameAnchors(t, s, d.ID, "sec-1", "sec-1.1", "sec-2")
	c, err := s.GetRule(context.Background(), "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Body != "\nB tight.\n\n" {
		t.Errorf("middle rule body = %q, want %q", c.Body, "\nB tight.\n\n")
	}

	// The last section: no heading follows, so no trailing blank line is
	// added and the document still ends in one newline.
	if _, err := editRule(t, s, "P1", 3, model.EditRuleInput{Heading: "Two", Body: "C tight."}, "stig"); err != nil {
		t.Fatal(err)
	}
	assertArrangement(t, arrangementOf(t, s, d.ID), want)
	assertSameAnchors(t, s, d.ID, "sec-1", "sec-1.1", "sec-2")
	c, err = s.GetRule(context.Background(), "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if c.Body != "\nC tight.\n" {
		t.Errorf("last rule body = %q, want %q", c.Body, "\nC tight.\n")
	}
	got, err := s.GetDoc(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got.Body, "## 2. Two {#sec-2}\n\nC tight.\n") {
		t.Errorf("document body after editing the last rule:\n%q", got.Body)
	}
}

// assertSameAnchors re-parses the stored document body and checks its
// anchors, in order.
func assertSameAnchors(t *testing.T, s *Store, docID int64, want ...string) {
	t.Helper()
	d, err := s.GetDoc(context.Background(), docID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := designdoc.Parse([]byte(d.Body))
	if err != nil {
		t.Fatalf("re-parse of the regenerated body failed: %v\n%s", err, d.Body)
	}
	var got []string
	for _, sec := range parsed.Sections {
		got = append(got, sec.Anchor)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("anchors after edit = %v, want %v\nbody:\n%s", got, want, d.Body)
	}
}

// TestEditRuleRefusals: an unknown rule is ErrNotFound; a rule arranged
// in no document is ErrInvalidInput (R3).
func TestEditRuleRefusals(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, err := editRule(t, s, "P1", 99, model.EditRuleInput{Heading: "x", Body: "y"}, "stig"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown rule: %v", err)
	}
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM doc_rules WHERE doc_id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := editRule(t, s, "P1", 1, model.EditRuleInput{Heading: "x", Body: "y"}, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unarranged rule: %v, want ErrInvalidInput", err)
	}
}

// editRule runs EditRule through RecordDocEvent against the arranging
// document, the way the API handler does.
func editRule(t *testing.T, s *Store, key string, number int64, in model.EditRuleInput, actor string) (int64, error) {
	t.Helper()
	var docID int64
	_, _, err := s.RecordDocEvent(t.Context(), "rule_edit", "cli",
		fmt.Sprintf("rule-edit-%d", docEventSeq.Add(1)), "doc.rule_edited", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			docID, err = EditRule(tx, s.Now(), key, number, in, actor, eventID)
			return err
		})
	return docID, err
}

// TestSetRuleMeta sets owner and tags (round-tripping a tag needing
// text[] quoting), clears the owner alone with an empty string while tags
// survive untouched, clears the tags alone while the cleared owner survives
// untouched, and refuses an unknown rule with ErrNotFound (S15).
func TestSetRuleMeta(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	ctx := context.Background()
	id := ruleID(t, s, "P1", 1)
	owner, tags := "stig", []string{"storage", "search", "a,b", `he "said"`}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return SetRuleMeta(tx, id, model.RuleMetaInput{Owner: &owner, Tags: &tags})
	}); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetRule(ctx, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Owner != "stig" || len(c.Tags) != 4 || c.Tags[0] != "storage" ||
		c.Tags[2] != "a,b" || c.Tags[3] != `he "said"` {
		t.Errorf("after set: owner %q tags %q", c.Owner, c.Tags)
	}
	none := ""
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return SetRuleMeta(tx, id, model.RuleMetaInput{Owner: &none})
	}); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetRule(ctx, "P1", 1)
	if c.Owner != "" || len(c.Tags) != 4 {
		t.Errorf("owner cleared, tags kept: owner %q tags %v", c.Owner, c.Tags)
	}
	empty := []string{}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return SetRuleMeta(tx, id, model.RuleMetaInput{Tags: &empty})
	}); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetRule(ctx, "P1", 1)
	if len(c.Tags) != 0 || c.Owner != "" {
		t.Errorf("tags cleared, owner kept: owner %q tags %v", c.Owner, c.Tags)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return SetRuleMeta(tx, 999999, model.RuleMetaInput{Owner: &owner})
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown rule: %v", err)
	}
}

// TestListRules: a document filter returns its arrangement in order, a
// status filter narrows, an unknown status is refused, and the unfiltered
// list runs by number with bodies left empty.
func TestListRules(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	d1 := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: ruleDocV1, CreatedBy: "stig", Owner: "stig"})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "b", Body: "---\nstatus: draft\n---\n# B\n\n## 1. Other {#sec-1}\n\nD.\n", CreatedBy: "stig"})

	all, err := s.ListRules(ctx, RuleFilter{Project: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, c := range all {
		refs = append(refs, c.Ref)
		if c.Body != "" {
			t.Errorf("%s: body %q, want empty on a list", c.Ref, c.Body)
		}
	}
	if got := strings.Join(refs, " "); got != "P1-RULE-1 P1-RULE-2 P1-RULE-3 P1-RULE-4" {
		t.Errorf("all = %s", got)
	}

	inDoc, err := s.ListRules(ctx, RuleFilter{Doc: d1.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(inDoc) != 3 || inDoc[1].Heading != "Sub" || inDoc[1].ArrangedIn[0].Anchor != "sec-1.1" {
		t.Errorf("doc filter = %+v", inDoc)
	}

	if _, _, err := acceptDoc(t, s, d1.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.ListRules(ctx, RuleFilter{Project: "p1", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].Heading != "Other" {
		t.Errorf("draft filter = %+v", drafts)
	}

	if _, err := s.ListRules(ctx, RuleFilter{Status: "bogus"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bogus status: err = %v, want ErrInvalidInput", err)
	}
}
