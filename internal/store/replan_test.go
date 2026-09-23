package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// markStale flips a plan straight to status 'stale' without going through
// MarkPlansStale's event/doc.stale machinery — the tests here only need the
// row in that state, not the eventbus consequence.
func markStale(t *testing.T, s *Store, docID int64) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE docs SET status = 'stale' WHERE id = $1`, docID); err != nil {
		t.Fatalf("markStale %d: %v", docID, err)
	}
}

// release drops the lease ReplanNext (or Claim) took, the way a submit or an
// abandoned worktree would.
func release(t *testing.T, s *Store, taskID string) {
	t.Helper()
	if err := s.Release(context.Background(), taskID, "stig"); err != nil {
		t.Fatalf("release %s: %v", taskID, err)
	}
}

// TestReplanNextMintsAndClaimsOneDesignTask: a stale plan is handed out as a
// claimed design task about the plan; a second call finds the open task and
// claims that one rather than minting again; no stale plan means not claimed
// (S28).
func TestReplanNextMintsAndClaimsOneDesignTask(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}

	none, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", ActorID: "stig", TTL: time.Hour})
	if err != nil || none.Claimed {
		t.Fatalf("no stale plan: want not claimed, got %+v %v", none, err)
	}

	markStale(t, s, plan.ID)

	first, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", ActorID: "stig", TTL: time.Hour})
	if err != nil || !first.Claimed || first.Task == nil {
		t.Fatalf("stale plan: want a claimed task, got %+v %v", first, err)
	}
	task, err := s.GetTask(t.Context(), first.Task.ID)
	if err != nil || task.Kind != "design" || task.AboutDoc != plan.ID {
		t.Fatalf("want a design task about the plan: %+v %v", task, err)
	}
	if want := "Re-plan: " + plan.Title; task.Title != want {
		t.Errorf("title = %q, want %q (the §8.7 rule's title)", task.Title, want)
	}

	release(t, s, first.Task.ID)

	second, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", Plan: "P1-PLAN-1", ActorID: "stig", TTL: time.Hour})
	if err != nil || !second.Claimed || second.Task.ID != first.Task.ID {
		t.Fatalf("open design task exists: want it claimed again, got %+v %v", second, err)
	}
}

// TestReplanNextByRefRefusesNonStalePlan: naming a plan that exists but is
// not stale is a refusal, not a claim.
func TestReplanNextByRefRefusesNonStalePlan(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}

	_, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", Plan: "P1-PLAN-1", ActorID: "stig", TTL: time.Hour})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ReplanNext on an accepted plan by ref = %v, want ErrInvalidInput", err)
	}
}

// TestReplanNextByRefRefusesUnknownRef: naming a plan ref that resolves to
// nothing is ErrNotFound (a caller typo), not the same clean "no stale plan"
// answer the no-ref case gives when the project genuinely has none.
func TestReplanNextByRefRefusesUnknownRef(t *testing.T) {
	s := openDocStore(t)

	_, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", Plan: "P1-PLAN-99", ActorID: "stig", TTL: time.Hour})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReplanNext on an unknown ref = %v, want ErrNotFound", err)
	}
}

// TestReplanNextMetrics pins the worklode_claims_total{op="replan",...}
// series: "none" (no stale plan, no ref given), "invalid" (a named ref that
// is not a stale plan), "reused" (an open design task already existed), and
// "minted" (mintReplanTask actually created one). Every claimed outcome
// also counts under op="claim" — the same inner-Claim counting ClaimNext
// relies on (TestClaimNextMetrics).
func TestReplanNextMetrics(t *testing.T) {
	s := openDocStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", ActorID: "stig", TTL: time.Hour}); err != nil {
		t.Fatalf("no stale plan: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("replan", "none")); got != 1 {
		t.Fatalf("claims{replan,none} = %v, want 1", got)
	}

	if _, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", Plan: "P1-PLAN-1", ActorID: "stig", TTL: time.Hour}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("accepted plan by ref = %v, want ErrInvalidInput", err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("replan", "invalid")); got != 1 {
		t.Fatalf("claims{replan,invalid} = %v, want 1", got)
	}

	markStale(t, s, plan.ID)
	first, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", ActorID: "stig", TTL: time.Hour})
	if err != nil || !first.Claimed {
		t.Fatalf("stale plan: (%+v, %v), want claimed", first, err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("replan", "minted")); got != 1 {
		t.Fatalf("claims{replan,minted} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim", "ok")); got != 1 {
		t.Fatalf("claims{claim,ok} = %v, want 1 (ReplanNext's inner Claim counts under op=claim too)", got)
	}

	release(t, s, first.Task.ID)
	second, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", ActorID: "stig", TTL: time.Hour})
	if err != nil || !second.Claimed {
		t.Fatalf("reused: (%+v, %v), want claimed", second, err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("replan", "reused")); got != 1 {
		t.Fatalf("claims{replan,reused} = %v, want 1", got)
	}
}

// TestReplanNextConcurrentCallersShareOneMintedTask: two callers racing to
// re-plan the same stale plan end with exactly one design task minted about
// it — the loser's Claim call gets ErrLeased rather than a duplicate task
// (see task-4-report.md's concurrency section).
func TestReplanNextConcurrentCallersShareOneMintedTask(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	markStale(t, s, plan.ID)

	type outcome struct {
		res *ClaimNextResult
		err error
	}
	results := make(chan outcome, 2)
	// start gates both goroutines so they enter ReplanNext together instead
	// of however the scheduler happens to launch two `go` statements — a
	// gap there could let the first finish (and commit its mint) before the
	// second even begins, which would take the reuse path through the outer
	// OpenTaskForDoc and never exercise mintReplanTask's in-lock recheck at
	// all.
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			res, err := s.ReplanNext(context.Background(), ReplanOpts{ProjectID: "p1", ActorID: "stig", TTL: time.Hour})
			results <- outcome{res, err}
		}()
	}
	close(start)
	claimed := 0
	for range 2 {
		o := <-results
		if o.err != nil {
			if !errors.Is(o.err, ErrLeased) {
				t.Fatalf("ReplanNext race: %v", o.err)
			}
			continue
		}
		if o.res.Claimed {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("claimed = %d racing callers, want exactly 1", claimed)
	}

	open, err := s.OpenTaskForDoc(t.Context(), plan.ID, "design")
	if err != nil {
		t.Fatalf("OpenTaskForDoc: %v", err)
	}
	if open == "" {
		t.Fatal("want the open design task the winner claimed to still be findable")
	}

	minted, err := s.ListTasks(t.Context(), TaskFilter{AboutDoc: plan.ID, Kind: "design"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(minted) != 1 {
		t.Fatalf("design tasks about the plan = %d, want exactly 1 (no duplicate mint)", len(minted))
	}
}
