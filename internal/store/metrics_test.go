package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestWithMetricsRegisters asserts WithMetrics registers the store's
// instruments: the DB pool collector and the active-lease collector.
func TestWithMetricsRegisters(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := OpenTestStore(t, WithMetrics(reg))
	if s.metrics == nil {
		t.Fatal("WithMetrics did not set s.metrics")
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var names []string
	for _, f := range families {
		names = append(names, f.GetName())
	}
	joined := strings.Join(names, "\n")
	for _, want := range []string{"go_sql_open_connections", "worklode_leases_active"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("registry missing %s; got:\n%s", want, joined)
		}
	}
}

// TestClaimOutcomeMapping asserts the sentinel-error → label mapping.
func TestClaimOutcomeMapping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "ok"},
		{ErrLeased, "leased"},
		{ErrBlocked, "blocked"},
		{ErrNotFound, "not_found"},
		{ErrBadTransition, "error"},
	} {
		if got := claimOutcome(tc.err); got != tc.want {
			t.Fatalf("claimOutcome(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// TestDecisionMetrics drives AddDecision and EditDecision through a store
// with metrics attached and asserts worklode_decisions_total counts both ops
// by outcome, refusals included.
func TestDecisionMetrics(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	posed := model.DecisionInput{Key: "x-distribution", Question: "Do we ship X?", ResponseType: "yes_no"}
	if _, err := s.AddDecision(ctx, task.ID, "stig", posed); err != nil {
		t.Fatalf("pose decision: %v", err)
	}
	if _, err := s.AddDecision(ctx, task.ID, "stig", posed); !errors.Is(err, ErrDecisionExists) {
		t.Fatalf("duplicate pose err = %v, want ErrDecisionExists", err)
	}
	q := "Do we ship X in October?"
	if _, err := s.EditDecision(ctx, task.ID, "x-distribution", "stig",
		model.DecisionInput{Question: q}); err != nil {
		t.Fatalf("edit decision: %v", err)
	}
	if _, err := s.EditDecision(ctx, task.ID, "nope", "stig",
		model.DecisionInput{Question: q}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("edit unknown row err = %v, want ErrNotFound", err)
	}

	if _, err := s.RecordDecision(ctx, task.ID, "x-distribution", "stig",
		model.DecisionAnswer{Value: "yes"}); err != nil {
		t.Fatalf("record decision: %v", err)
	}
	if _, err := s.RecordDecision(ctx, task.ID, "x-distribution", "stig",
		model.DecisionAnswer{Value: "no"}); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("re-answer err = %v, want ErrBadTransition", err)
	}

	for _, tc := range []struct{ op, outcome string }{
		{"pose", "ok"}, {"pose", "refused"}, {"edit", "ok"}, {"edit", "refused"},
		{"answer", "ok"}, {"answer", "refused"},
	} {
		if got := testutil.ToFloat64(s.metrics.decisions.WithLabelValues(tc.op, tc.outcome)); got != 1 {
			t.Errorf("decisions{%s,%s} = %v, want 1", tc.op, tc.outcome, got)
		}
	}
	if !strings.Contains(gatheredNames(t, reg), "worklode_decisions_total") {
		t.Error("worklode_decisions_total is not registered")
	}
}

// gatheredNames joins the metric family names a registry reports.
func gatheredNames(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var names []string
	for _, f := range families {
		names = append(names, f.GetName())
	}
	return strings.Join(names, "\n")
}

// TestStoreMetricsNilSafe asserts a store opened without WithMetrics (nil
// storeMetrics) records nothing and does not panic.
func TestStoreMetricsNilSafe(t *testing.T) {
	t.Parallel()
	var m *storeMetrics
	m.claim("claim", "ok")
	m.renew("ok")
	m.release("ok")
	m.expire(3)
	m.sweeperRun(nil)
	m.sweeperRun(errors.New("boom"))
	m.docGroomRun(nil)
	m.docGroomRun(errors.New("boom"))
	m.emitStaleDocs(2)
	m.projectWorkRead(nil)
	m.projectWorkRead(errors.New("boom"))
	m.instruction("enqueue", "ok")
	m.deliverInstructions(3)
	m.decision("pose", "ok")
	m.escalation("minted")
	m.gap("recorded")
	m.fix("started", "recorded")
}

// TestEscalationMetrics drives the three outcomes of EscalateTask —
// minted, joined and error — through a store with metrics attached.
func TestEscalationMetrics(t *testing.T) {
	t.Parallel()
	s := openEscalateStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()

	plan := mustCreateDoc(t, s, DocInput{
		Project: "horndb", Kind: "plan", Number: 9, Slug: "009-plan",
		Body: "---\nstatus: accepted\n---\n\n# A plan\n", CreatedBy: "alice",
	})
	for i, worktree := range []string{"wt-a", "wt-b"} {
		task := createTask(t, s, taskTestNow, defaultTaskInput())
		if _, err := s.Claim(ctx, task.ID, "stig", worktree, 0); err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if _, err := s.EscalateTask(ctx, EscalateInput{
			TaskID: task.ID, To: "plan", DocID: plan.ID,
			Reason: "under-specified", ActorID: "stig",
		}); err != nil {
			t.Fatalf("escalate %d: %v", i, err)
		}
	}
	// No lease: the error arm.
	orphan := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := s.EscalateTask(ctx, EscalateInput{
		TaskID: orphan.ID, To: "plan", DocID: plan.ID,
		Reason: "under-specified", ActorID: "stig",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("escalate without a lease err = %v, want ErrNotFound", err)
	}

	for _, outcome := range []string{"minted", "joined", "error"} {
		if got := testutil.ToFloat64(s.metrics.escalations.WithLabelValues(outcome)); got != 1 {
			t.Errorf("escalations{%s} = %v, want 1", outcome, got)
		}
	}
	if !strings.Contains(gatheredNames(t, reg), "worklode_task_escalations_total") {
		t.Error("worklode_task_escalations_total is not registered")
	}
}

// TestLeaseMetricsCounters drives claim/renew/release/expire through a store
// with metrics attached and asserts the counters.
func TestLeaseMetricsCounters(t *testing.T) {
	t.Parallel()
	s, now := openLeaseStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	// Claim ok, then a second claim → leased.
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt-a", 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt-b", 0); !errors.Is(err, ErrLeased) {
		t.Fatalf("second claim err = %v, want ErrLeased", err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim", "ok")); got != 1 {
		t.Fatalf("claims{claim,ok} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim", "leased")); got != 1 {
		t.Fatalf("claims{claim,leased} = %v, want 1", got)
	}

	// Active-lease collector sees the one live lease.
	if got := testutil.ToFloat64(&leaseCollector{db: s.db.DB, now: s.Now}); got != 1 {
		t.Fatalf("worklode_leases_active = %v, want 1", got)
	}

	// Renew by a non-holder → error; by the holder → ok.
	if _, err := s.Renew(ctx, task.ID, "nobody", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renew by non-holder err = %v, want ErrNotFound", err)
	}
	if _, err := s.Renew(ctx, task.ID, "stig", 0); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.renewals.WithLabelValues("error")); got != 1 {
		t.Fatalf("renewals{error} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.renewals.WithLabelValues("ok")); got != 1 {
		t.Fatalf("renewals{ok} = %v, want 1", got)
	}

	// Release ok.
	if err := s.Release(ctx, task.ID, "stig"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.releases.WithLabelValues("ok")); got != 1 {
		t.Fatalf("releases{ok} = %v, want 1", got)
	}

	// Re-claim, then expire it: expiries counts 1.
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt-a", time.Second); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	*now = now.Add(time.Hour)
	n, err := s.ExpireLeases(ctx, *now)
	if err != nil || n != 1 {
		t.Fatalf("ExpireLeases = (%d, %v), want (1, nil)", n, err)
	}
	if got := testutil.ToFloat64(s.metrics.expiries); got != 1 {
		t.Fatalf("expiries = %v, want 1", got)
	}
}

// TestClaimNextMetrics: an empty ready set records claim_next/none; a
// successful pickup records claim_next/ok (plus its internal claim/ok).
func TestClaimNextMetrics(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()

	res, err := s.ClaimNext(ctx, ClaimNextOpts{ActorID: "stig", Worktree: "host:/wt-x"})
	if err != nil || res.Claimed {
		t.Fatalf("ClaimNext on empty set = (%+v, %v), want unclaimed, nil", res, err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim_next", "none")); got != 1 {
		t.Fatalf("claims{claim_next,none} = %v, want 1", got)
	}

	createTask(t, s, leaseTestNow, defaultTaskInput())
	res, err = s.ClaimNext(ctx, ClaimNextOpts{ActorID: "stig", Worktree: "host:/wt-x"})
	if err != nil || !res.Claimed {
		t.Fatalf("ClaimNext = (%+v, %v), want claimed, nil", res, err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim_next", "ok")); got != 1 {
		t.Fatalf("claims{claim_next,ok} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim", "ok")); got != 1 {
		t.Fatalf("claims{claim,ok} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim_next", "none")); got != 1 {
		t.Fatalf("claims{claim_next,none} after success = %v, want 1", got)
	}
}

// TestClaimNextDryRunRecordsNothing: a dry run is a read, not a claim
// attempt, so it must not touch the claim counters.
func TestClaimNextDryRunRecordsNothing(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()

	res, err := s.ClaimNext(ctx, ClaimNextOpts{ActorID: "stig", Worktree: "host:/wt-x", DryRun: true})
	if err != nil || res.Claimed {
		t.Fatalf("dry-run ClaimNext = (%+v, %v), want unclaimed, nil", res, err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim_next", "none")); got != 0 {
		t.Fatalf("claims{claim_next,none} after dry run = %v, want 0", got)
	}
}

// TestInstructionMetrics drives EnqueueInstruction and
// ClaimPendingInstructionsForActor through a store with metrics attached and
// asserts the op/outcome counters plus the delivered-count counter.
func TestInstructionMetrics(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt-a")

	if _, err := s.EnqueueInstruction(ctx, lease.TaskID, "stig", "steer this"); err != nil {
		t.Fatalf("enqueue instruction: %v", err)
	}
	if _, err := s.EnqueueInstruction(ctx, "no-such-task", "stig", "steer this"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("enqueue instruction on unknown task err = %v, want ErrNotFound", err)
	}
	if got := testutil.ToFloat64(s.metrics.instructions.WithLabelValues("enqueue", "ok")); got != 1 {
		t.Fatalf("instructions{enqueue,ok} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.instructions.WithLabelValues("enqueue", "error")); got != 1 {
		t.Fatalf("instructions{enqueue,error} = %v, want 1", got)
	}

	delivered, err := s.ClaimPendingInstructionsForActor(ctx, "stig")
	if err != nil || len(delivered) != 1 {
		t.Fatalf("claim pending instructions = (%v, %v), want 1 delivered, nil", delivered, err)
	}
	if got := testutil.ToFloat64(s.metrics.instructions.WithLabelValues("claim", "ok")); got != 1 {
		t.Fatalf("instructions{claim,ok} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.instructionsDelivered); got != 1 {
		t.Fatalf("instructions_delivered = %v, want 1", got)
	}

	// A second claim finds nothing pending: instructions{claim,ok} still
	// increments (the operation succeeded), but instructions_delivered does
	// not move.
	if delivered, err := s.ClaimPendingInstructionsForActor(ctx, "stig"); err != nil || len(delivered) != 0 {
		t.Fatalf("second claim = (%v, %v), want 0 delivered, nil", delivered, err)
	}
	if got := testutil.ToFloat64(s.metrics.instructions.WithLabelValues("claim", "ok")); got != 2 {
		t.Fatalf("instructions{claim,ok} after second claim = %v, want 2", got)
	}
	if got := testutil.ToFloat64(s.metrics.instructionsDelivered); got != 1 {
		t.Fatalf("instructions_delivered after empty claim = %v, want 1", got)
	}
}

// TestProjectWorkReadMetrics drives a real ListProjectWorkFacts call through
// a store with metrics attached (proving the outcome="ok" counter is wired
// into the method itself), then exercises projectWorkRead's error-label
// mapping directly — parallel to TestClaimOutcomeMapping — since a genuine
// DB failure is impractical to force through the public method. It also
// asserts the metric carries only the outcome label: never a project or
// task id, which would be unbounded.
func TestProjectWorkReadMetrics(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()

	if _, err := s.ListProjectWorkFacts(ctx, "horndb"); err != nil {
		t.Fatalf("ListProjectWorkFacts: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.projectWorkReads.WithLabelValues("ok")); got != 1 {
		t.Fatalf("project_work_reads{ok} = %v, want 1", got)
	}

	s.metrics.projectWorkRead(errors.New("boom"))
	if got := testutil.ToFloat64(s.metrics.projectWorkReads.WithLabelValues("error")); got != 1 {
		t.Fatalf("project_work_reads{error} = %v, want 1", got)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "worklode_project_work_reads_total" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			for _, lp := range metric.GetLabel() {
				if lp.GetName() != "outcome" {
					t.Fatalf("worklode_project_work_reads_total has unexpected label %q, want only outcome", lp.GetName())
				}
			}
		}
	}
}

// TestSplitSymbol pins the linker-symbol parsing behind the pkg and func
// labels: the package keeps its repo-relative path, a receiver is dropped,
// and every closure reports as the function that declared it.
func TestSplitSymbol(t *testing.T) {
	t.Parallel()
	const mod = "github.com/sunstoneinstitute/worklode/"
	for _, tc := range []struct{ sym, pkg, fn string }{
		{mod + "internal/store.(*Store).ListProjectWorkFacts", "internal/store", "ListProjectWorkFacts"},
		{mod + "internal/store.blockedByEdgeOrPlan", "internal/store", "blockedByEdgeOrPlan"},
		{mod + "internal/store.(*Store).sweepStaleDocs.func1", "internal/store", "sweepStaleDocs"},
		{mod + "internal/store.(*Store).x.func1.2", "internal/store", "x"},
		{mod + "internal/skillstore.Upsert", "internal/skillstore", "Upsert"},
		// A name the compiler would never emit, and a genuine method whose
		// name merely starts with "func": neither may be mistaken for a
		// closure tail.
		{"main", "unknown", "unknown"},
		{mod + "internal/store.(*Store).funcs", "internal/store", "funcs"},
	} {
		pkg, fn := splitSymbol(tc.sym)
		if pkg != tc.pkg || fn != tc.fn {
			t.Errorf("splitSymbol(%q) = (%q, %q), want (%q, %q)", tc.sym, pkg, fn, tc.pkg, tc.fn)
		}
	}
}

// TestQueryMetricsLabelCaller drives one query through the metered pool and
// asserts it is attributed to the store function that issued it. This is what
// pins queryCallerSkip: a wrong skip lands on meteredDB or on the caller's
// caller, and the expected series is simply absent.
func TestQueryMetricsLabelCaller(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := OpenTestStore(t, WithMetrics(reg))

	// A miss returns after exactly one QueryRowContext, so the count is
	// exact rather than a floor.
	if _, err := s.GetTask(t.Context(), "WL-404"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTask on a missing id: err = %v, want ErrNotFound", err)
	}

	if got := testutil.ToFloat64(s.metrics.queries.WithLabelValues("internal/store", "GetTask")); got != 1 {
		t.Errorf("worklode_store_queries_total{pkg=internal/store,func=GetTask} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.querySeconds.WithLabelValues("internal/store", "GetTask")); got <= 0 {
		t.Errorf("worklode_store_query_seconds_total{...,func=GetTask} = %v, want > 0", got)
	}
}
