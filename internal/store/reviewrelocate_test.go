package store

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// threadByID reads one thread row.
func threadByID(t *testing.T, s *Store, id int64) model.ReviewThread {
	t.Helper()
	th, err := threadsWhere(context.Background(), s.db, `t.id = $1`, id)
	if err != nil || len(th) != 1 {
		t.Fatalf("read thread %d: %v (%d rows)", id, err, len(th))
	}
	return th[0]
}

// rangeThread opens a bare range thread on quote in body (WL-REQ-1317).
func rangeThread(t *testing.T, s *Store, a model.ReviewAnchor, body, quote string) *model.ReviewThread {
	t.Helper()
	i := strings.Index(body, quote)
	if i < 0 {
		t.Fatalf("%q not in %q", quote, body)
	}
	a.ByteStart, a.ByteEnd, a.Quote = i, i+len(quote), quote
	th, err := openThread(t, s, 0, model.OpenThreadInput{Anchor: a, Intent: "note", Body: "look here"}, "stig")
	if err != nil {
		t.Fatalf("open thread on %q: %v", quote, err)
	}
	return th
}

// wantMoved checks a thread is open at version, covering quote at want's
// offset in body.
func wantMoved(t *testing.T, name string, got model.ReviewThread, version int, body, quote string) {
	t.Helper()
	a := got.Anchor
	if got.Status != "open" || a.Version != version || a.ByteStart != strings.Index(body, quote) ||
		a.ByteEnd != a.ByteStart+len(quote) || a.Quote != quote {
		t.Errorf("%s = %s v%d [%d,%d) %q, want open v%d on %q at %d in %q",
			name, got.Status, a.Version, a.ByteStart, a.ByteEnd, a.Quote, version, quote, strings.Index(body, quote), body)
	}
}

// TestBodyWriteRelocates: every body write carries each open range thread
// to the text it now sits in, or marks it obsolete (WL-REQ-1317). Bodies are
// normalised before they are stored, so lines that must survive are
// blank-line-separated paragraphs, and the wrapped paragraph is a legacy
// body written straight to the row.
func TestBodyWriteRelocates(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})

	// A draft rule: rewritten in place, so T stays at version 1.
	r, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Free", Body: "a\n\nb\n\ntarget := 1\n\nc\n"}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	T := rangeThread(t, s, model.ReviewAnchor{Rule: r.ID, Version: r.Version}, string(r.Body), "target := 1")
	if err := editRule(t, s, "P1", r.Number, model.EditRuleInput{Heading: "Free", Body: "x\n\ny\n\na\n\nb\n\ntarget := 1\n\nc\n"}, "stig"); err != nil {
		t.Fatal(err)
	}
	r2, err := s.GetRule(ctx, "P1", r.Number)
	if err != nil {
		t.Fatal(err)
	}
	got := threadByID(t, s, T.ID)
	wantMoved(t, "T after insert", got, r.Version, string(r2.Body), "target := 1")
	if got.Anchor.DocAnchor != T.Anchor.DocAnchor {
		t.Errorf("T doc_anchor = %q, want %q", got.Anchor.DocAnchor, T.Anchor.DocAnchor)
	}
	if err := editRule(t, s, "P1", r.Number, model.EditRuleInput{Heading: "Free", Body: "a\n\nb\n\nc\n"}, "stig"); err != nil {
		t.Fatal(err)
	}
	if got := threadByID(t, s, T.ID); got.Status != "obsolete" || got.ResolvedAt == nil {
		t.Errorf("T after delete = %s resolved_at=%v, want obsolete with resolved_at set", got.Status, got.ResolvedAt)
	}
	var status string
	var informed bool
	if err := s.db.QueryRow(
		`SELECT payload->>'wl:threadStatus', payload ? 'prov:wasInformedBy' FROM events
		  WHERE type = 'wl:ReviewThreadResolved' AND payload->>'wl:subject' = $1`,
		iri.CURIE(iri.ReviewThread(T.ID))).Scan(&status, &informed); err != nil {
		t.Fatalf("read T's resolved event: %v", err)
	}
	if status != "obsolete" || !informed {
		t.Errorf("T's event status=%q informed=%v, want obsolete informed by the edit", status, informed)
	}

	// An accepted rule: the edit is version 2, U follows it, v1 is untouched.
	if _, _, err := acceptDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetRule(ctx, "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	U := rangeThread(t, s, model.ReviewAnchor{Rule: c.ID, Version: c.Version}, string(c.Body), "C.")
	if err := editRule(t, s, "P1", 3, model.EditRuleInput{Heading: "Two", Body: "\nNew lead.\n\nC.\n"}, "stig"); err != nil {
		t.Fatal(err)
	}
	c2, err := s.GetRule(ctx, "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, "U after accepted edit", threadByID(t, s, U.ID), c.Version+1, string(c2.Body), "C.")
	var v1 string
	if err := s.db.QueryRow(`SELECT body FROM rule_versions WHERE rule_id = $1 AND version = $2`, c.ID, c.Version).Scan(&v1); err != nil || v1 != string(c.Body) {
		t.Errorf("v%d body = %q, %v; want %q", c.Version, v1, err, c.Body)
	}

	// A plan: the next version, with P moved and Q, spanning a soft line
	// break of a legacy wrapped paragraph, found once the paragraph joins.
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "p", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# Plan\n\nShip the quote here.\n"})
	legacy := "# Plan\n\nShip the quote here.\n\none\ntwo\nthree\nfour\n"
	if _, err := s.db.Exec(`UPDATE docs SET body = $2 WHERE id = $1`, plan.ID, legacy); err != nil {
		t.Fatal(err)
	}
	P := rangeThread(t, s, model.ReviewAnchor{Doc: plan.ID, Version: plan.Version}, legacy, "Ship the quote here.")
	Q := rangeThread(t, s, model.ReviewAnchor{Doc: plan.ID, Version: plan.Version}, legacy, "two\nthree")
	plan2, err := updateDocBody(t, s, plan.ID, "# Plan\n\nLead one.\n\nLead two.\n\nShip the quote here.\n\none\ntwo\nthree\nfour\n")
	if err != nil {
		t.Fatal(err)
	}
	wantMoved(t, "P after plan edit", threadByID(t, s, P.ID), plan.Version+1, string(plan2.Body), "Ship the quote here.")
	wantMoved(t, "Q after the paragraph joined", threadByID(t, s, Q.ID), plan.Version+1, string(plan2.Body), "two three")

	// An accepted spec amended in place: D on template text follows the
	// patch to the next version; a bare section note never moves.
	acc := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Number: 100, Slug: "100-a",
		Body: patchSpecBody, CreatedBy: "stig", Status: "accepted"})
	var tmpl string
	if err := s.db.QueryRow(`SELECT body FROM docs WHERE id = $1`, acc.ID).Scan(&tmpl); err != nil {
		t.Fatal(err)
	}
	i := strings.Index(tmpl, "Intro prose.")
	var D int64
	if err := s.db.QueryRow(
		`INSERT INTO review_threads (doc_id, version, byte_start, byte_end, quote, intent, status, author_id, created_at)
		 VALUES ($1, $2, $3, $4, 'Intro prose.', 'note', 'open', 'stig', now()) RETURNING id`,
		acc.ID, acc.Version, i, i+len("Intro prose.")).Scan(&D); err != nil {
		t.Fatal(err)
	}
	note, err := addDocNote(t, s, acc.ID, model.AddDocNoteInput{Anchor: "sec-2", Body: "a section note"}, "ada")
	if err != nil {
		t.Fatal(err)
	}
	cur, err := s.GetDoc(ctx, acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := patchDoc(t, s, DocPatchInput{ID: acc.ID, Note: "lead in", ActorID: "stig",
		Body: strings.Replace(plain(string(cur.Body)), "Intro prose.", "New lead.\n\nIntro prose.", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT body FROM docs WHERE id = $1`, acc.ID).Scan(&tmpl); err != nil {
		t.Fatal(err)
	}
	wantMoved(t, "D after patch", threadByID(t, s, D), acc.Version+1, tmpl, "Intro prose.")
	if got := threadByID(t, s, note.ID); got.Status != "open" || got.Anchor.Version != 0 || got.Anchor.DocAnchor != "sec-2" {
		t.Errorf("bare note = %+v, want open on sec-2 with no version", got)
	}
}

// bodyWriters are the only functions that may write a document's or a
// rule's text: each relocates the open threads on it (WL-REQ-1317).
// storeSpecTemplate settles the text a writer above it relocates to, and
// insertRule creates version 1, which no thread can name yet.
var bodyWriters = map[string]bool{
	"updateDocBodyPinned": true, "publishPatch": true, "AcceptRevision": true, "normalizeDoc": true,
	"reviseRule": true, "bumpRuleVersion": true, "storeSpecTemplate": true, "insertRule": true,
}

// TestOnlyRelocatingWritersWriteBodies fails on a new body writer until it
// relocates and joins bodyWriters, the way TestOnlyBumpFunctionsWriteVersions
// holds version writes.
func TestOnlyRelocatingWritersWriteBodies(t *testing.T) {
	write := regexp.MustCompile(`(?i)UPDATE docs SET[^;` + "`" + `]*?\bbody\s*=|INSERT INTO rule_versions\b|UPDATE rule_versions\b`)
	funcDecl := regexp.MustCompile(`(?m)^func (?:\([^)]*\) )?(\w+)`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		decls := funcDecl.FindAllSubmatchIndex(src, -1)
		for _, m := range write.FindAllIndex(src, -1) {
			fn := ""
			for _, d := range decls {
				if d[0] > m[0] {
					break
				}
				fn = string(src[d[2]:d[3]])
			}
			if !bodyWriters[fn] {
				t.Errorf("%s: %s writes %q; write bodies through a writer that relocates threads (WL-REQ-1317)", f, fn, src[m[0]:m[1]])
			}
		}
	}
}
