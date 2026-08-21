package reconcile_test

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/reconcile"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// candidateOutcomes asserts the whole bounded outcome set at once, so a
// candidate double-counted under two labels fails the test.
func candidateOutcomes(t *testing.T, m *reconcile.Metrics, want map[string]float64) {
	t.Helper()
	for _, outcome := range []string{"repaired", "clean", "dry_run", "gather_error", "error"} {
		got := testutil.ToFloat64(m.Candidates().WithLabelValues(outcome))
		if got != want[outcome] {
			t.Errorf("poll_candidates{%s} = %v, want %v", outcome, got, want[outcome])
		}
	}
}

// TestPollRecordsMetrics: an applied run credits its candidate once, and
// counts the facts it actually repaired.
func TestPollRecordsMetrics(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedStaleTask(t, st)
	app := newFakeGitHub(t, mergedPRRoutes(taskID))

	m := reconcile.NewMetrics(prometheus.NewRegistry())
	res, err := reconcile.Poll(context.Background(), st, app, reconcile.Options{RunID: "run-m", Metrics: m})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(res.Repaired) != 1 {
		t.Fatalf("result = %+v; want 1 repair", res)
	}
	candidateOutcomes(t, m, map[string]float64{"repaired": 1})
	if got := testutil.ToFloat64(m.Repairs().WithLabelValues("pr")); got != 1 {
		t.Errorf("poll_repairs{pr} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.Repairs().WithLabelValues("commit")); got != 1 {
		t.Errorf("poll_repairs{commit} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.RepoErrors()); got != 0 {
		t.Errorf("poll_repo_errors = %v, want 0", got)
	}
}

// TestPollDryRunRecordsDryRunOutcome: a dry run reports the same repair but
// wrote nothing, so it must not be counted as repaired.
func TestPollDryRunRecordsDryRunOutcome(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedStaleTask(t, st)
	app := newFakeGitHub(t, mergedPRRoutes(taskID))

	m := reconcile.NewMetrics(prometheus.NewRegistry())
	res, err := reconcile.Poll(context.Background(), st, app,
		reconcile.Options{RunID: "run-m-dry", DryRun: true, Metrics: m})
	if err != nil {
		t.Fatalf("dry-run poll: %v", err)
	}
	if len(res.Repaired) != 1 {
		t.Fatalf("dry-run result = %+v; want the repair still reported", res)
	}
	candidateOutcomes(t, m, map[string]float64{"dry_run": 1})
	if got := testutil.ToFloat64(m.Repairs().WithLabelValues("pr")); got != 0 {
		t.Errorf("poll_repairs{pr} = %v after a dry run, want 0", got)
	}
}

// TestPollRecordsRepoGatherError: a repo whose GitHub reads fail is counted
// once, and its candidates land on gather_error rather than clean — "we
// could not look" must not read as "nothing to repair".
func TestPollRecordsRepoGatherError(t *testing.T) {
	st := store.OpenTestStore(t)
	seedStaleTask(t, st)
	// No /repos/acme/app route: the default-branch read 404s, so gatherRepo
	// fails before any fact is gathered.
	app := newFakeGitHub(t, map[string]string{})

	m := reconcile.NewMetrics(prometheus.NewRegistry())
	res, err := reconcile.Poll(context.Background(), st, app, reconcile.Options{RunID: "run-m-err", Metrics: m})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(res.Errors) != 1 || len(res.Repaired) != 0 {
		t.Fatalf("result = %+v; want 1 repo error and no repairs", res)
	}
	candidateOutcomes(t, m, map[string]float64{"gather_error": 1})
	if got := testutil.ToFloat64(m.RepoErrors()); got != 1 {
		t.Errorf("poll_repo_errors = %v, want 1", got)
	}
}

// TestPollNilMetrics: Options.Metrics is optional, so a nil one must not
// panic anywhere on the run path.
func TestPollNilMetrics(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedStaleTask(t, st)
	app := newFakeGitHub(t, mergedPRRoutes(taskID))

	if _, err := reconcile.Poll(context.Background(), st, app, reconcile.Options{RunID: "run-m-nil"}); err != nil {
		t.Fatalf("poll with nil metrics: %v", err)
	}
}
