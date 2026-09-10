package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

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
