package store

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// patchSpecBody is an accepted-shaped spec with three plain-prose sections:
// the §8.4 rule split is about what an edit does to them, so none of them
// carries a code span, a wl: term or an acceptance-criteria heading that
// would fire a mechanical rule on its own.
const patchSpecBody = `---
status: accepted
issued: 2026-08-01
requires: 004-execution-backbone.md#sec-6
---

# In-place amendment

Intro prose.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.

## 3. Rules {#sec-3}

Rules body.
`

func reword(body string) string {
	return strings.Replace(body, "Scope body.", "Scope body, restated.", 1)
}

func rewordSec2(body string) string {
	return strings.Replace(body, "Model body.", "Model body, restated.", 1)
}

func addRequire(body string) string {
	return strings.Replace(body, "requires: 004-execution-backbone.md#sec-6",
		"requires:\n  - 004-execution-backbone.md#sec-6\n  - 022-metrics.md#sec-1", 1)
}

func narrowRule(body string) string {
	return strings.Replace(body, "Rules body.", "Rules body, narrowed to one case.", 1)
}

// patchDoc runs PatchDoc through RecordDocEvent, the way the API does.
func patchDoc(t *testing.T, s *Store, in DocPatchInput) (*model.Doc, *model.DocPatchResult, error) {
	t.Helper()
	var doc *model.Doc
	var res *model.DocPatchResult
	_, _, err := s.RecordDocEvent(t.Context(), "patch", "cli",
		fmt.Sprintf("doc-patch-%d", docEventSeq.Add(1)), "doc.patched", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			doc, res, err = PatchDoc(tx, s.Now(), in, eventID)
			return err
		})
	return doc, res, err
}

// patchedAnchors reads the anchors a document carries the §8.4 patched mark
// on, in document order.
func patchedAnchors(t *testing.T, s *Store, docID int64) []string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT anchor FROM doc_sections WHERE doc_id = $1 AND patched ORDER BY position`, docID)
	if err != nil {
		t.Fatalf("read patched sections: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

// TestPatchDocRuleSplit is 025 §8.3/§8.4's split: a mechanical rule refuses
// the edit outright and sends it to `lode doc revise`; anything left is the
// caller's own judgment, non-substantive with a note or substantive with the
// reviewers reopened.
func TestPatchDocRuleSplit(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	cases := []struct {
		name        string
		mutate      func(body string) string
		substantive bool
		note        string
		reviewers   []string
		wantErr     string // "" = lands
		wantPatched []string
	}{
		{"non-substantive wording with note", reword, false, "clarified §2", []string{"ada"}, "", nil},
		{"non-substantive without note", reword, false, "", []string{"ada"}, "note", nil},
		{"referenced section refused", rewordSec2, false, "n", []string{"ada"}, "referrer", nil},
		{"requires grows refused", addRequire, false, "n", []string{"ada"}, "new-dependency", nil},
		{"judged substantive marks patched", narrowRule, true, "", []string{"ada"}, "", []string{"sec-3"}},
		{"substantive with no reviewers refused", narrowRule, true, "", nil, "reviewers", nil},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// One spec per case, plus an accepted spec requiring its sec-2:
			// the referrer rule is a corpus fact, so it needs a corpus.
			spec := mustCreateDoc(t, s, DocInput{
				Project: "p1", Kind: "spec", Number: 100 + i*2, Slug: fmt.Sprintf("%03d-a", 100+i*2),
				Body: patchSpecBody, CreatedBy: "stig", Status: "accepted",
			})
			mustCreateDoc(t, s, DocInput{
				Project: "p1", Kind: "spec", Number: 101 + i*2, Slug: fmt.Sprintf("%03d-b", 101+i*2),
				Body:      referrerSpecBody(fmt.Sprintf("%03d-a", 100+i*2), "sec-2"),
				CreatedBy: "stig", Status: "accepted",
			})
			if len(tc.reviewers) > 0 {
				assignDocReviewers(t, s, spec.ID, tc.reviewers)
			}

			doc, res, err := patchDoc(t, s, DocPatchInput{
				ID: spec.ID, Body: tc.mutate(patchSpecBody),
				Substantive: tc.substantive, Note: tc.note, ActorID: "stig",
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to name %q", err, tc.wantErr)
				}
				// A refused patch changes nothing: the transaction rolled back.
				after, getErr := s.GetDoc(t.Context(), spec.ID)
				if getErr != nil {
					t.Fatal(getErr)
				}
				if after.Version != 1 || after.Body != patchSpecBody {
					t.Errorf("refused patch still moved the document: version %d", after.Version)
				}
				return
			}
			if err != nil {
				t.Fatalf("PatchDoc: %v", err)
			}
			// §7.3: the document stays accepted either way — a patch is not a
			// status change.
			if doc.Status != "accepted" {
				t.Errorf("status = %s, want accepted", doc.Status)
			}
			if doc.Version != 2 || res.NewVersion != 2 {
				t.Errorf("version = %d (result %d), want 2", doc.Version, res.NewVersion)
			}
			wantClass := "non-substantive"
			wantRule := "none"
			if tc.substantive {
				wantClass, wantRule = "substantive", "judged"
			}
			if res.Classification != wantClass || res.RuleFired != wantRule {
				t.Errorf("classification/rule = %s/%s, want %s/%s",
					res.Classification, res.RuleFired, wantClass, wantRule)
			}
			if got := patchedAnchors(t, s, spec.ID); !slices.Equal(got, tc.wantPatched) {
				t.Errorf("patched anchors = %v, want %v", got, tc.wantPatched)
			}

			// 025 §6 rule 5: last_revised_in moves on exactly the changed
			// sections.
			for _, sec := range docSections(t, s, spec.ID) {
				want := 1
				if slices.Contains(res.ChangedAnchors, sec.Anchor) {
					want = 2
				}
				if sec.LastRevisedIn != want {
					t.Errorf("#%s last_revised_in = %d, want %d", sec.Anchor, sec.LastRevisedIn, want)
				}
			}

			lanes := awaitingDocLanes(t, s, spec.ID)
			if tc.substantive {
				// The reviewers who approved the document owe a decision on
				// the version the patch made (§7.3).
				if !reflect.DeepEqual(lanes, []string{"ada@2"}) {
					t.Errorf("awaiting lanes = %v, want [ada@2]", lanes)
				}
			} else if len(lanes) != 0 {
				t.Errorf("awaiting lanes = %v, want none: a non-substantive patch reopens nothing", lanes)
			}

			notes, err := s.ListDocNotes(t.Context(), spec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.substantive {
				if len(notes) != 0 {
					t.Errorf("notes = %+v, want none on a substantive patch", notes)
				}
				return
			}
			// §8.5: the fixer's own record of what changed and why, anchored
			// at the first section the patch touched and naming them all.
			if len(notes) != 1 || notes[0].Anchor != res.ChangedAnchors[0] ||
				!strings.Contains(notes[0].Body, tc.note) ||
				!strings.Contains(notes[0].Body, res.ChangedAnchors[0]) {
				t.Errorf("notes = %+v, want one anchored at %s carrying %q and the changed anchors",
					notes, res.ChangedAnchors[0], tc.note)
			}
		})
	}
}

// awaitingDocLanes lists the open reviewer lanes on a document as
// "<actor>@<version>".
func awaitingDocLanes(t *testing.T, s *Store, docID int64) []string {
	t.Helper()
	rows, err := s.ListAwaitingApprovals(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if r.EntityKind != "doc" || r.EntityID != DocEntityID(docID) || r.RequiredActor == nil {
			continue
		}
		out = append(out, *r.RequiredActor+"@"+r.SubjectRevision)
	}
	slices.Sort(out)
	return out
}

// TestPatchDocReferrerExclusion covers the two halves of the §8.2 referrer
// question a patch asks that a plain reader does not: the plan the patching
// task was minted from does not block its own amendment, and an accepted
// covering plan nobody has claimed work from is reported rather than
// refused.
func TestPatchDocReferrerExclusion(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 200, Slug: "200-a",
		Body: patchSpecBody, CreatedBy: "stig", Status: "accepted",
	})
	own := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 201, Slug: "201-own",
		Body: referrerPlanBody("200-a", "sec-2"), CreatedBy: "stig", Status: "accepted",
	})
	idle := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 202, Slug: "202-idle",
		Body: referrerPlanBody("200-a", "sec-2"), CreatedBy: "stig", Status: "accepted",
	})
	planTask := func(plan *model.Doc, key, state string) *model.Task {
		t.Helper()
		task := createTask(t, s, taskTestNow, TaskInput{
			ProjectID: "p1", Title: key, Body: "body", Priority: "medium", Kind: "feature",
			CreatedBy: "stig", PlanDoc: plan.ID, PlanTaskKey: key,
		})
		walkTo(t, s, task.ID, state)
		return task
	}
	patching := planTask(own, "Task 1", "in_progress")
	planTask(idle, "Task 1", "ready")

	_, res, err := patchDoc(t, s, DocPatchInput{
		ID: spec.ID, Body: rewordSec2(patchSpecBody), Note: "restated §2",
		ActorID: "stig", TaskID: patching.ID,
	})
	if err != nil {
		t.Fatalf("PatchDoc: %v", err)
	}
	if !reflect.DeepEqual(res.UnexecutedCoveringPlans, []int64{idle.ID}) {
		t.Errorf("UnexecutedCoveringPlans = %v, want [%d]", res.UnexecutedCoveringPlans, idle.ID)
	}

	// A claimed task from another plan is a referrer, and refuses the patch.
	planTask(idle, "Task 2", "in_progress")
	if _, _, err := patchDoc(t, s, DocPatchInput{
		ID: spec.ID,
		Body: strings.Replace(rewordSec2(patchSpecBody),
			"Model body, restated.", "Model body, restated twice.", 1),
		Note:    "again",
		ActorID: "stig", TaskID: patching.ID,
	}); err == nil || !strings.Contains(err.Error(), "referrer") {
		t.Fatalf("err = %v, want a referrer refusal", err)
	}
}

// TestPatchDocKeepsEarlierPatchedMarks: every accepted-document write rebuilds
// the section rows, so the §8.4 mark a previous patch left has to survive one
// that touches a different section.
func TestPatchDocKeepsEarlierPatchedMarks(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 300, Slug: "300-a",
		Body: patchSpecBody, CreatedBy: "stig", Status: "accepted",
	})
	assignDocReviewers(t, s, spec.ID, []string{"ada"})

	if _, _, err := patchDoc(t, s, DocPatchInput{
		ID: spec.ID, Body: narrowRule(patchSpecBody), Substantive: true, ActorID: "stig",
	}); err != nil {
		t.Fatalf("first patch: %v", err)
	}
	if _, _, err := patchDoc(t, s, DocPatchInput{
		ID: spec.ID, Body: reword(narrowRule(patchSpecBody)), Note: "typo", ActorID: "stig",
	}); err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if got := patchedAnchors(t, s, spec.ID); !slices.Equal(got, []string{"sec-3"}) {
		t.Errorf("patched anchors = %v, want [sec-3]: the rebuild dropped an earlier mark", got)
	}
	// The refusal for an unchanged body: nothing to amend.
	if _, _, err := patchDoc(t, s, DocPatchInput{
		ID: spec.ID, Body: reword(narrowRule(patchSpecBody)), Note: "again", ActorID: "stig",
	}); err == nil || !strings.Contains(err.Error(), "nothing changed") {
		t.Fatalf("err = %v, want the nothing-changed refusal", err)
	}
}

// TestPatchDocMetricOutcomes: the §8.4 refusals are worth telling apart on
// worklode_doc_operations_total{op="patch"} — a mechanical gate refusing an
// edit is not the same event as a bug, and neither is a document with nobody
// left to re-approve it.
func TestPatchDocMetricOutcomes(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)

	s.RecordDocOp("patch", nil)
	s.RecordDocOp("patch", refusePatch("referrer", "doc 1 cannot be patched"))
	s.RecordDocOp("patch", &patchRefusal{rule: "no-reviewers", err: ErrInvalidInput})
	s.RecordDocOp("patch", errors.New("boom"))

	for _, tc := range []struct{ outcome string }{
		{"ok"}, {"refused-mechanical"}, {"no-reviewers"}, {"error"},
	} {
		if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("patch", tc.outcome)); got != 1 {
			t.Errorf(`docOps{op=patch,outcome=%s} = %v, want 1`, tc.outcome, got)
		}
	}
}

// decideDocLane approves one reviewer's open lane on a document, the way the
// cockpit's decide act does, and commits — the clear is a consequence of the
// decision, so the assertion after it reads committed rows.
func decideDocLane(t *testing.T, s *Store, docID int64, lane string) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	a, err := OpenApprovalForLane(tx, "doc", DocEntityID(docID), lane)
	if err != nil {
		t.Fatalf("open lane %q on doc %d: %v", lane, docID, err)
	}
	if _, err := DecideApproval(tx, DecideInput{
		ApprovalID: a.ID, Decision: "approve", ActorID: lane, Now: s.Now(),
	}); err != nil {
		t.Fatalf("decide lane %q on doc %d: %v", lane, docID, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestClearPatchedOnReapproval is the second half of 025 §7.3: the §8.4 marks
// come off only when every lane the patch reopened has approved. One of two
// reviewers is not the gate — that is the case a "clear on approve" that
// forgot to look at its neighbours would pass.
func TestClearPatchedOnReapproval(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	seedDocsActor(t, s, "bob")
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 170, Slug: "170-reapproval",
		Body: patchSpecBody, CreatedBy: "stig", Status: "accepted",
	})
	assignDocReviewers(t, s, spec.ID, []string{"ada", "bob"})

	if _, _, err := patchDoc(t, s, DocPatchInput{
		ID: spec.ID, Body: narrowRule(patchSpecBody), Substantive: true, ActorID: "stig",
	}); err != nil {
		t.Fatalf("PatchDoc: %v", err)
	}
	if got := patchedAnchors(t, s, spec.ID); !slices.Equal(got, []string{"sec-3"}) {
		t.Fatalf("patched anchors after the patch = %v, want [sec-3]", got)
	}

	decideDocLane(t, s, spec.ID, "ada")
	if got := patchedAnchors(t, s, spec.ID); !slices.Equal(got, []string{"sec-3"}) {
		t.Errorf("patched anchors with one lane still open = %v, want [sec-3]", got)
	}

	decideDocLane(t, s, spec.ID, "bob")
	if got := patchedAnchors(t, s, spec.ID); got != nil {
		t.Errorf("patched anchors after the last lane approved = %v, want none", got)
	}
}

// TestClearPatchedOnRevisionLanding: the other way a mark ends. A landed
// revision replaces the patched text with reviewed text, so nothing is
// "approved text, modified since" any more. The section rebuild carries the
// flag forward by design, so this is asserted rather than assumed.
func TestClearPatchedOnRevisionLanding(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 172, Slug: "172-revision-clears",
		Body: patchSpecBody, CreatedBy: "stig", Status: "accepted",
	})
	assignDocReviewers(t, s, spec.ID, []string{"ada"})

	if _, _, err := patchDoc(t, s, DocPatchInput{
		ID: spec.ID, Body: narrowRule(patchSpecBody), Substantive: true, ActorID: "stig",
	}); err != nil {
		t.Fatalf("PatchDoc: %v", err)
	}
	if got := patchedAnchors(t, s, spec.ID); !slices.Equal(got, []string{"sec-3"}) {
		t.Fatalf("patched anchors after the patch = %v, want [sec-3]", got)
	}

	if err := reviseDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, spec.ID, rewordSec2(narrowRule(patchSpecBody))); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if _, err := acceptRevision(t, s, spec.ID, "stig"); err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}
	if got := patchedAnchors(t, s, spec.ID); got != nil {
		t.Errorf("patched anchors after the revision landed = %v, want none", got)
	}
}
