package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// groomPlanBody is an accepted-shaped plan covering 210-a#sec-2 with one
// mintable declaration: referrerPlanBody's task fence carries no kind, so it
// never reaches the accept path this test needs.
const groomPlanBody = `---
status: draft
covers:
  - 210-a.md#sec-2
---

# Covering plan

## Tasks

### Task 1 — Do it

` + "```yaml\nkind: feature\n```" + `

Do it.
`

// groomPlanReplanned is that plan after re-planning: one added declaration,
// which the re-accept mints, and the original left as it was.
const groomPlanReplanned = groomPlanBody + `
### Task 2 — Do it again

` + "```yaml\nkind: feature\n```" + `

Do it again.
`

// patchAndGroom runs the patch handler's transaction: PatchDoc, then
// MarkPlansStale over the plans it reported, in one commit.
func patchAndGroom(t *testing.T, s *Store, in DocPatchInput) (*model.DocPatchResult, int, error) {
	t.Helper()
	var res *model.DocPatchResult
	var marked int
	_, _, err := s.RecordDocEvent(t.Context(), "patch", "cli",
		fmt.Sprintf("doc-patch-%d", docEventSeq.Add(1)), "doc.patched", nil,
		func(tx *sql.Tx, eventID int64) error {
			d, p, err := PatchDoc(tx, s.Now(), in, eventID)
			if err != nil {
				return err
			}
			res = p
			marked, err = MarkPlansStale(tx, s.Now(), p.UnexecutedCoveringPlans, d.Slug, p.ChangedAnchors, eventID)
			return err
		})
	return res, marked, err
}

// staleEvents reads the doc.stale events recorded under one external id, with
// their payloads decoded.
func staleEvents(t *testing.T, s *Store, externalID string) []map[string]any {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT payload FROM events WHERE source = $1 AND external_id = $2 AND type = 'doc.stale'`,
		staleEventSource, externalID)
	if err != nil {
		t.Fatalf("read doc.stale events: %v", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// TestPlansStaleMetric: worklode_doc_operations_total{op="stale"} counts one
// per plan actually marked. MarkPlansStale runs inside the patch handler's
// transaction with no *Store, so the handler records the count afterwards —
// the pattern RecordPlanTasksMinted uses. Nil-safe with no registry.
func TestPlansStaleMetric(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)

	s.RecordPlansStale(2)
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("stale", "ok")); got != 2 {
		t.Fatalf(`docOps{op=stale,outcome=ok} = %v, want 2`, got)
	}
	s.RecordPlansStale(0)
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("stale", "ok")); got != 2 {
		t.Fatalf(`docOps{op=stale,outcome=ok} = %v after marking nothing, want 2`, got)
	}
	var m *storeMetrics
	m.docOp("stale", nil)
}

// TestPatchMarksUnexecutedPlansStale is 025 §8.6: an amendment that moves a
// section an accepted plan covers, with no claimed work behind it, leaves
// that plan stale rather than blocked. The mark is idempotent on
// (source, external_id), the plan's tasks stay claimable and say so, and
// re-accepting the re-planned document clears it.
func TestPatchMarksUnexecutedPlansStale(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := t.Context()

	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 210, Slug: "210-a",
		Body: patchSpecBody, CreatedBy: "stig", Status: "accepted",
	})
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 211, Slug: "211-cover",
		Body: groomPlanBody, CreatedBy: "stig",
	})
	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatalf("accept plan: %v", err)
	}
	if len(minted) != 1 {
		t.Fatalf("minted = %d tasks, want 1", len(minted))
	}
	accepted, err := s.GetDoc(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	extID := StaleExternalID(plan.Slug, accepted.Version)

	res, marked, err := patchAndGroom(t, s, DocPatchInput{
		ID: spec.ID, Body: rewordSec2(patchSpecBody), Note: "restated §2", ActorID: "stig",
	})
	if err != nil {
		t.Fatalf("PatchDoc: %v", err)
	}
	if !reflect.DeepEqual(res.UnexecutedCoveringPlans, []int64{plan.ID}) {
		t.Fatalf("UnexecutedCoveringPlans = %v, want [%d]", res.UnexecutedCoveringPlans, plan.ID)
	}
	if marked != 1 {
		t.Errorf("marked = %d plans, want 1", marked)
	}
	if got := docStatus(t, s, plan.ID); got != "stale" {
		t.Errorf("plan status = %q, want stale", got)
	}
	evs := staleEvents(t, s, extID)
	if len(evs) != 1 {
		t.Fatalf("doc.stale events = %d, want 1", len(evs))
	}
	if evs[0]["cause"] != "amended" || evs[0]["spec"] != "210-a" {
		t.Errorf("payload = %v, want cause amended on spec 210-a", evs[0])
	}
	if anchors := evs[0]["anchors"]; !reflect.DeepEqual(anchors, []any{"sec-2"}) {
		t.Errorf("payload anchors = %v, want [sec-2]", anchors)
	}

	// A second amendment of the same section reports nothing to mark — a
	// stale plan is no longer an accepted covering plan — and records no
	// second event.
	res2, marked2, err := patchAndGroom(t, s, DocPatchInput{
		ID: spec.ID, Body: rewordSec2(reword(patchSpecBody)), Note: "again", ActorID: "stig",
	})
	if err != nil {
		t.Fatalf("second PatchDoc: %v", err)
	}
	if len(res2.UnexecutedCoveringPlans) != 0 || marked2 != 0 {
		t.Errorf("second patch: plans = %v, marked = %d, want none", res2.UnexecutedCoveringPlans, marked2)
	}
	if got := docStatus(t, s, plan.ID); got != "stale" {
		t.Errorf("plan status = %q after the second patch, want stale", got)
	}

	// The (source, external_id) key is what makes that a no-op rather than
	// the accepted-only filter: told to mark the same plan at the same
	// version again, MarkPlansStale records nothing and marks nothing.
	setDocStatus(t, s, plan.ID, "accepted")
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		n, err := MarkPlansStale(tx, s.Now(), []int64{plan.ID}, "210-a", []string{"sec-2"}, 0)
		if n != 0 {
			t.Errorf("re-mark: marked = %d, want 0", n)
		}
		return err
	}); err != nil {
		t.Fatalf("re-mark: %v", err)
	}
	if evs := staleEvents(t, s, extID); len(evs) != 1 {
		t.Errorf("doc.stale events = %d after the re-mark, want 1", len(evs))
	}
	setDocStatus(t, s, plan.ID, "stale")

	// Claim-time flag: the plan's tasks stay claimable and the brief says the
	// text may predate the amendment (§8.6).
	if _, err := s.Claim(ctx, minted[0].ID, "stig", "wt-stale", 0); err != nil {
		t.Fatalf("claim %s: %v", minted[0].ID, err)
	}
	brief, err := s.Brief(ctx, minted[0].ID, BriefOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if brief.StalePlan != plan.Slug {
		t.Errorf("brief.StalePlan = %q, want %q", brief.StalePlan, plan.Slug)
	}

	// Re-acceptance clears the mark (§8.6). The re-planning edit bumps the
	// plan's version, so the accept event is a new one and its mint pass
	// picks up the added declaration while leaving the existing task alone.
	if _, err := updateDocBody(t, s, plan.ID, groomPlanReplanned); err != nil {
		t.Fatalf("re-plan edit: %v", err)
	}
	_, minted2, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatalf("re-accept the stale plan: %v", err)
	}
	if got := docStatus(t, s, plan.ID); got != "accepted" {
		t.Errorf("plan status = %q after re-acceptance, want accepted", got)
	}
	if len(minted2) != 1 {
		t.Errorf("re-accept minted %d tasks, want 1 (the new declaration only)", len(minted2))
	}
	if _, err := s.GetTask(ctx, minted[0].ID); err != nil {
		t.Errorf("the first minted task is gone: %v", err)
	}
	if brief, err := s.Brief(ctx, minted[0].ID, BriefOptions{}); err != nil {
		t.Fatal(err)
	} else if brief.StalePlan != "" {
		t.Errorf("brief.StalePlan = %q after re-acceptance, want none", brief.StalePlan)
	}
}

// candidateByID finds one StaleCandidateDocs row by doc id.
func candidateByID(cs []StaleCandidate, id int64) (StaleCandidate, bool) {
	for _, c := range cs {
		if c.DocID == id {
			return c, true
		}
	}
	return StaleCandidate{}, false
}

// TestStaleCandidateDocs is 025 §8.7's fact reader: one fixture per row of
// the truth table (accepted plan with/without a lease, accepted spec
// with/without an accepted covering plan, an ADR, a draft, a project with
// doc_staleness_days set), asserted separately. The threshold verdict is
// watcher.StaleAt's job, not this reader's, so nothing here checks a clock.
func TestStaleCandidateDocs(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := t.Context()

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, key, doc_staleness_days) VALUES ('p2','P2','P2',21)`); err != nil {
		t.Fatal(err)
	}

	// Accepted spec, the target of the covering plan below.
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 210, Slug: "210-a",
		Body: specBody, CreatedBy: "stig", Status: "accepted",
	})
	// Accepted spec with no covering plan at all.
	specAlone := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 220, Slug: "220-b",
		Body: specBody, CreatedBy: "stig", Status: "accepted",
	})
	// Accepted ADR: excluded from the reader regardless of status, matching
	// watcher.StaleAt.
	adr := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "adr", Number: 1, Slug: "001-adr",
		Body: specBody, CreatedBy: "stig", Status: "accepted",
	})
	// Draft spec: excluded, only accepted docs are candidates.
	draft := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 230, Slug: "230-draft",
		Body: specBody, CreatedBy: "stig",
	})
	// Accepted spec under a project with doc_staleness_days set.
	specP2 := mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "spec", Number: 240, Slug: "240-c",
		Body: specBody, CreatedBy: "stig", Status: "accepted",
	})

	// Plan covering spec 210-a#sec-2, taken through the real accept path so
	// its task gets plan_doc set and can be leased.
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 211, Slug: "211-cover",
		Body: groomPlanBody, CreatedBy: "stig",
	})
	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatalf("accept plan: %v", err)
	}
	if len(minted) != 1 {
		t.Fatalf("minted = %d tasks, want 1", len(minted))
	}

	cands, err := s.StaleCandidateDocs(ctx)
	if err != nil {
		t.Fatalf("StaleCandidateDocs: %v", err)
	}
	for i := 1; i < len(cands); i++ {
		if cands[i-1].DocID >= cands[i].DocID {
			t.Fatalf("candidates not ordered by doc id: %+v", cands)
		}
	}

	if _, ok := candidateByID(cands, adr.ID); ok {
		t.Errorf("ADR %d present, want excluded", adr.ID)
	}
	if _, ok := candidateByID(cands, draft.ID); ok {
		t.Errorf("draft %d present, want excluded", draft.ID)
	}

	planC, ok := candidateByID(cands, plan.ID)
	if !ok {
		t.Fatalf("plan %d absent, want candidate", plan.ID)
	}
	if planC.HasExecution {
		t.Errorf("plan.HasExecution = true before any claim, want false")
	}
	if planC.Kind != "plan" || planC.Slug != "211-cover" || planC.ProjectDays != 0 {
		t.Errorf("plan candidate = %+v, want kind plan, slug 211-cover, projectDays 0", planC)
	}

	specC, ok := candidateByID(cands, spec.ID)
	if !ok {
		t.Fatalf("spec %d absent, want candidate", spec.ID)
	}
	if !specC.HasExecution {
		t.Errorf("spec.HasExecution = false with an accepted covering plan, want true")
	}

	specAloneC, ok := candidateByID(cands, specAlone.ID)
	if !ok {
		t.Fatalf("spec %d absent, want candidate", specAlone.ID)
	}
	if specAloneC.HasExecution {
		t.Errorf("spec.HasExecution = true with no covering plan, want false")
	}

	specP2C, ok := candidateByID(cands, specP2.ID)
	if !ok {
		t.Fatalf("spec %d absent, want candidate", specP2.ID)
	}
	if specP2C.ProjectDays != 21 {
		t.Errorf("spec under p2: ProjectDays = %d, want 21 (doc_staleness_days)", specP2C.ProjectDays)
	}

	// Claim the plan's task: execution now happened, so HasExecution flips
	// even though the lease stays active (any lease row counts, §8.7).
	if _, err := s.Claim(ctx, minted[0].ID, "stig", "wt-stale-candidate", 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	cands2, err := s.StaleCandidateDocs(ctx)
	if err != nil {
		t.Fatalf("StaleCandidateDocs after claim: %v", err)
	}
	planC2, ok := candidateByID(cands2, plan.ID)
	if !ok {
		t.Fatalf("plan %d absent after claim, want candidate", plan.ID)
	}
	if !planC2.HasExecution {
		t.Errorf("plan.HasExecution = false after a claim, want true")
	}
}

// minimalPlanBody is an accepted-shaped plan with no covers and no mintable
// task fence: the §8.7 sweep tests below only need a plan row past its
// staleness threshold with no leased task, and a plan's HasExecution check
// (StaleCandidateDocs) looks at leases, not covers.
const minimalPlanBody = `---
status: draft
---

# A plan with nothing covered

Nothing to cover.
`

// minimalPlanBodyRevised is minimalPlanBody after one re-planning edit, for
// TestSweepStaleDocsRevisionRearms.
const minimalPlanBodyRevised = `---
status: draft
---

# A plan with nothing covered

Nothing to cover. Revised.
`

// docStaleTestNow anchors the §8.7 sweep tests' injected clock.
var docStaleTestNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// openStaleSweepStore is openDocStore with a mutable clock, so a sweep test
// can create a document "now" and then move the clock forward past its
// staleness threshold before sweeping — the pattern openLeaseStore uses for
// the lease sweeper.
func openStaleSweepStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s := openDocStore(t)
	now := docStaleTestNow
	s.SetNowFunc(func() time.Time { return now })
	return s, &now
}

// TestSweepStaleDocsEmitsOnceThenSkipsUnchanged is 025 §8.7's idle path: an
// accepted plan with no execution crosses the (inclusive) instance-default
// threshold and the sweep emits one doc.stale event with cause "clock". Its
// status stays accepted — only the §8.6 amendment path flips a status, and
// only on plans. A second sweep at the same version emits nothing more.
func TestSweepStaleDocsEmitsOnceThenSkipsUnchanged(t *testing.T) {
	t.Parallel()
	s, now := openStaleSweepStore(t)
	ctx := t.Context()

	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "211-idle",
		Body: minimalPlanBody, CreatedBy: "stig", Status: "accepted",
	})
	*now = now.AddDate(0, 0, defaultDocStalenessDays) // inclusive threshold

	emitted, err := s.sweepStaleDocs(ctx)
	if err != nil {
		t.Fatalf("sweepStaleDocs: %v", err)
	}
	if emitted != 1 {
		t.Fatalf("emitted = %d, want 1", emitted)
	}
	if got := docStatus(t, s, plan.ID); got != "accepted" {
		t.Errorf("plan status = %q after a clock-fired stale, want accepted", got)
	}
	evs := staleEvents(t, s, StaleExternalID(plan.Slug, 1))
	if len(evs) != 1 {
		t.Fatalf("doc.stale events = %d, want 1", len(evs))
	}
	if evs[0]["cause"] != "clock" || evs[0]["doc"] != float64(plan.ID) {
		t.Errorf("payload = %v, want cause clock, doc %d", evs[0], plan.ID)
	}

	emitted2, err := s.sweepStaleDocs(ctx)
	if err != nil {
		t.Fatalf("second sweepStaleDocs: %v", err)
	}
	if emitted2 != 0 {
		t.Errorf("second sweep emitted = %d, want 0", emitted2)
	}
	if evs := staleEvents(t, s, StaleExternalID(plan.Slug, 1)); len(evs) != 1 {
		t.Errorf("doc.stale events after the second sweep = %d, want 1", len(evs))
	}
}

// TestSweepStaleDocsProjectOverrideFiresEarlier is 025 §8.7's per-project
// override: projects.doc_staleness_days shortens the threshold for
// documents under that project, leaving the instance default in force for
// every other project's documents.
func TestSweepStaleDocsProjectOverrideFiresEarlier(t *testing.T) {
	t.Parallel()
	s, now := openStaleSweepStore(t)
	ctx := t.Context()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, key, doc_staleness_days) VALUES ('p2','P2','P2',5)`); err != nil {
		t.Fatal(err)
	}

	defaultSpec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 210, Slug: "210-default",
		Body: specBody, CreatedBy: "stig", Status: "accepted",
	})
	overrideSpec := mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "spec", Number: 210, Slug: "210-override",
		Body: specBody, CreatedBy: "stig", Status: "accepted",
	})

	*now = now.AddDate(0, 0, 10) // past p2's 5-day override, short of p1's 30-day default

	emitted, err := s.sweepStaleDocs(ctx)
	if err != nil {
		t.Fatalf("sweepStaleDocs: %v", err)
	}
	if emitted != 1 {
		t.Fatalf("emitted = %d, want 1 (override spec only)", emitted)
	}
	if evs := staleEvents(t, s, StaleExternalID(overrideSpec.Slug, 1)); len(evs) != 1 {
		t.Errorf("override spec doc.stale events = %d, want 1", len(evs))
	}
	if evs := staleEvents(t, s, StaleExternalID(defaultSpec.Slug, 1)); len(evs) != 0 {
		t.Errorf("default-threshold spec doc.stale events = %d, want 0 (not yet stale)", len(evs))
	}
}

// TestSweepStaleDocsRevisionRearms is 025 §8.7's version key: grooming a
// document at one version leaves it re-armable. A later revision bumps the
// plan's version and re-arms the clock from the revision's updated_at, not
// the original.
func TestSweepStaleDocsRevisionRearms(t *testing.T) {
	t.Parallel()
	s, now := openStaleSweepStore(t)
	ctx := t.Context()

	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "211-rearm",
		Body: minimalPlanBody, CreatedBy: "stig", Status: "accepted",
	})
	*now = now.AddDate(0, 0, defaultDocStalenessDays)
	emitted, err := s.sweepStaleDocs(ctx)
	if err != nil || emitted != 1 {
		t.Fatalf("first sweep: emitted = %d, err = %v, want 1, nil", emitted, err)
	}

	if _, err := updateDocBody(t, s, plan.ID, minimalPlanBodyRevised); err != nil {
		t.Fatalf("revise plan: %v", err)
	}
	emitted2, err := s.sweepStaleDocs(ctx)
	if err != nil {
		t.Fatalf("sweep right after the revision: %v", err)
	}
	if emitted2 != 0 {
		t.Errorf("sweep right after the revision emitted = %d, want 0 (not stale yet)", emitted2)
	}

	*now = now.AddDate(0, 0, defaultDocStalenessDays)
	emitted3, err := s.sweepStaleDocs(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if emitted3 != 1 {
		t.Fatalf("second sweep emitted = %d, want 1 (version 2)", emitted3)
	}
	if evs := staleEvents(t, s, StaleExternalID(plan.Slug, 2)); len(evs) != 1 {
		t.Errorf("doc.stale events at version 2 = %d, want 1", len(evs))
	}
	if evs := staleEvents(t, s, StaleExternalID(plan.Slug, 1)); len(evs) != 1 {
		t.Errorf("doc.stale events at version 1 = %d, want 1 (unchanged)", len(evs))
	}
}

// TestSweepStaleDocsExecutedPlanNeverFires is 025 §8.7's execution guard: a
// plan with a claimed task is never a candidate, no matter how long past the
// threshold the clock moves.
func TestSweepStaleDocsExecutedPlanNeverFires(t *testing.T) {
	t.Parallel()
	s, now := openStaleSweepStore(t)
	ctx := t.Context()

	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 210, Slug: "210-a",
		Body: specBody, CreatedBy: "stig", Status: "accepted",
	})
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 211, Slug: "211-cover",
		Body: groomPlanBody, CreatedBy: "stig",
	})
	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatalf("accept plan: %v", err)
	}
	if len(minted) != 1 {
		t.Fatalf("minted = %d tasks, want 1", len(minted))
	}
	if _, err := s.Claim(ctx, minted[0].ID, "stig", "wt-never-stale", 0); err != nil {
		t.Fatalf("claim: %v", err)
	}

	*now = now.AddDate(0, 0, defaultDocStalenessDays*10)
	emitted, err := s.sweepStaleDocs(ctx)
	if err != nil {
		t.Fatalf("sweepStaleDocs: %v", err)
	}
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0 (executed plan)", emitted)
	}
	accepted, err := s.GetDoc(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if evs := staleEvents(t, s, StaleExternalID(plan.Slug, accepted.Version)); len(evs) != 0 {
		t.Errorf("doc.stale events = %d, want 0", len(evs))
	}
}

// withdrawDoc runs WithdrawDoc through RecordDocEvent, the way the API's
// handler does, so the metric and the doc.withdrawn event are exercised too.
func withdrawDoc(t *testing.T, s *Store, id int64, justification string) (*model.Doc, error) {
	t.Helper()
	var out *model.Doc
	payload, err := EventPayload(map[string]any{"doc": id, "justification": justification})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.RecordDocEvent(t.Context(), "withdraw", "cli",
		fmt.Sprintf("doc-withdraw-%d", docEventSeq.Add(1)), "doc.withdrawn", payload,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = WithdrawDoc(tx, s.Now(), id, eventID)
			return err
		})
	return out, err
}

// TestWithdrawDocTransitions is 025 §8.7's close verb: accepted and stale go
// to withdrawn, and the three statuses that are not withdrawable are refused
// with ErrBadTransition rather than silently accepted. The event is recorded
// with its justification and the op is counted.
func TestWithdrawDocTransitions(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)

	accepted := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "820-accepted",
		Body: minimalPlanBody, CreatedBy: "stig", Status: "accepted",
	})
	d, err := withdrawDoc(t, s, accepted.ID, "superseded by a different approach")
	if err != nil {
		t.Fatalf("withdraw accepted: %v", err)
	}
	if d.Status != "withdrawn" {
		t.Errorf("returned status = %q, want withdrawn", d.Status)
	}
	if got := docStatus(t, s, accepted.ID); got != "withdrawn" {
		t.Errorf("stored status = %q, want withdrawn", got)
	}

	stale := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "820-stale",
		Body: minimalPlanBody, CreatedBy: "stig", Status: "accepted",
	})
	setDocStatus(t, s, stale.ID, "stale")
	if _, err := withdrawDoc(t, s, stale.ID, "not worth re-planning"); err != nil {
		t.Fatalf("withdraw stale: %v", err)
	}
	if got := docStatus(t, s, stale.ID); got != "withdrawn" {
		t.Errorf("stale doc status = %q, want withdrawn", got)
	}

	// A draft is deleted, not withdrawn; a superseded document is already
	// resolved; a withdrawn one already is what the call asks for.
	for _, status := range []string{"draft", "superseded", "withdrawn"} {
		doc := mustCreateDoc(t, s, DocInput{
			Project: "p1", Kind: "plan", Slug: "820-" + status,
			Body: minimalPlanBody, CreatedBy: "stig",
		})
		setDocStatus(t, s, doc.ID, status)
		if _, err := withdrawDoc(t, s, doc.ID, "why not"); !errors.Is(err, ErrBadTransition) {
			t.Errorf("withdraw a %s doc: err = %v, want ErrBadTransition", status, err)
		}
		if got := docStatus(t, s, doc.ID); got != status {
			t.Errorf("refused %s doc moved to %q, want it left alone", status, got)
		}
	}

	// Two withdrawals landed and three were refused.
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("withdraw", "ok")); got != 2 {
		t.Errorf(`docOps{op=withdraw,outcome=ok} = %v, want 2`, got)
	}
	if got := testutil.ToFloat64(s.metrics.docOps.WithLabelValues("withdraw", "error")); got != 3 {
		t.Errorf(`docOps{op=withdraw,outcome=error} = %v, want 3`, got)
	}

	var payload map[string]any
	var raw []byte
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT payload FROM events WHERE type = 'doc.withdrawn'
		  AND payload->>'doc' = $1`, fmt.Sprint(accepted.ID)).Scan(&raw); err != nil {
		t.Fatalf("read doc.withdrawn event: %v", err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["justification"] != "superseded by a different approach" {
		t.Errorf("payload = %v, want the justification recorded", payload)
	}
}

// TestUnresolvedDocsOlderThan is 025 §8.7's unresolved set: accepted specs
// and plans nothing has executed, filtered by how long they have sat. The
// --older-than boundary is inclusive, and an executed document is out however
// old it is — the same "executed" predicate the staleness sweep applies.
func TestUnresolvedDocsOlderThan(t *testing.T) {
	t.Parallel()
	s, now := openStaleSweepStore(t)
	ctx := t.Context()

	// Three accepted plans nothing has executed, created 31, 30 and 29 days
	// before the read, plus one whose minted task was claimed.
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "821-old",
		Body: minimalPlanBody, CreatedBy: "stig", Status: "accepted",
	})
	*now = now.AddDate(0, 0, 1)
	boundary := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "821-boundary",
		Body: minimalPlanBody, CreatedBy: "stig", Status: "accepted",
	})
	*now = now.AddDate(0, 0, 1)
	fresh := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "821-fresh",
		Body: minimalPlanBody, CreatedBy: "stig", Status: "accepted",
	})
	executed := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Number: 822, Slug: "822-executed",
		Body: groomPlanBody, CreatedBy: "stig",
	})
	_, minted, err := acceptDoc(t, s, executed.ID, "stig")
	if err != nil {
		t.Fatalf("accept plan: %v", err)
	}
	if _, err := s.Claim(ctx, minted[0].ID, "stig", "wt-822", 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	*now = now.AddDate(0, 0, 29)

	all, err := s.UnresolvedDocs(ctx, "p1", "", 0)
	if err != nil {
		t.Fatalf("UnresolvedDocs: %v", err)
	}
	if got := docSlugs(all); !reflect.DeepEqual(got, []string{"821-old", "821-boundary", "821-fresh"}) {
		t.Errorf("unresolved = %v, want the three unexecuted plans oldest first", got)
	}

	// 30 days is inclusive: the document updated exactly 30 days ago is in,
	// the one updated 29 days ago is out.
	bounded, err := s.UnresolvedDocs(ctx, "p1", "", 30)
	if err != nil {
		t.Fatalf("UnresolvedDocs(30d): %v", err)
	}
	if got := docSlugs(bounded); !reflect.DeepEqual(got, []string{"821-old", "821-boundary"}) {
		t.Errorf("unresolved 30d = %v, want %v and %v", got, old.Slug, boundary.Slug)
	}
	if got := docSlugs(mustUnresolved(t, s, "p1", "", 31)); !reflect.DeepEqual(got, []string{"821-old"}) {
		t.Errorf("unresolved 31d = %v, want only %v", got, old.Slug)
	}
	if got := docSlugs(mustUnresolved(t, s, "p1", "", 32)); len(got) != 0 {
		t.Errorf("unresolved 32d = %v, want none", got)
	}
	if got := docSlugs(mustUnresolved(t, s, "p1", "spec", 0)); len(got) != 0 {
		t.Errorf("unresolved --kind spec = %v, want none (every unexecuted doc here is a plan)", got)
	}

	// Withdrawing one takes it out of the set: that is what the verb is for.
	if _, err := withdrawDoc(t, s, fresh.ID, "not going to happen"); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if got := docSlugs(mustUnresolved(t, s, "p1", "", 0)); !reflect.DeepEqual(got, []string{"821-old", "821-boundary"}) {
		t.Errorf("unresolved after a withdrawal = %v, want the withdrawn plan gone", got)
	}
}

func mustUnresolved(t *testing.T, s *Store, project, kind string, days int) []model.Doc {
	t.Helper()
	docs, err := s.UnresolvedDocs(t.Context(), project, kind, days)
	if err != nil {
		t.Fatalf("UnresolvedDocs(%q, %d): %v", kind, days, err)
	}
	return docs
}

func docSlugs(docs []model.Doc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.Slug
	}
	return out
}
