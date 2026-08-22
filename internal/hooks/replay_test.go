package hooks_test

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// newUnmappedEnv is newEnv without the repo mapping: deliveries for
// sunstoneinstitute/demo are recorded *.ignored until mapDemoRepo runs.
func newUnmappedEnv(t *testing.T) *env {
	t.Helper()
	st := store.OpenTestStore(t)
	return &env{
		dbEnv: dbEnv{st: st},
		h:     hooks.NewGitHubHandler(st, testSecret, slog.Default(), nil, nil, nil),
	}
}

func mapDemoRepo(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	if err := e.st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := e.st.AddRepo(ctx, "demo", demoRepo); err != nil {
		t.Fatalf("add repo: %v", err)
	}
}

func TestReplayAppliesIgnoredEventsAfterMapping(t *testing.T) {
	e := newUnmappedEnv(t)
	rr := deliverBody(t, e.h, "issues", "d-1", []byte(`{
		"action": "opened",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"issue": {"number": 7, "title": "late issue", "state": "open", "html_url": "u"}
	}`))
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ignored" {
		t.Fatalf("delivery: %d %s", rr.Code, rr.Body.String())
	}
	mapDemoRepo(t, e)

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 1 || res.StillUnmapped != 0 {
		t.Fatalf("replay result = %+v; want 1 candidate, 1 replayed", res)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM issues WHERE repo = 'sunstoneinstitute/demo' AND number = 7`); n != 1 {
		t.Fatalf("issue rows after replay = %d, want 1", n)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM events WHERE external_id = 'd-1' AND applied_at IS NOT NULL`); n != 1 {
		t.Fatalf("applied_at set = %d rows, want 1", n)
	}

	// Second replay: nothing left to do.
	res, err = hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}
	if res.Candidates != 0 || res.Replayed != 0 {
		t.Fatalf("second replay = %+v; want a no-op", res)
	}
}

// TestReplayProvenance: a replayed pull_request.opened produces the same
// state_log transition a live delivery would, attributed to the ORIGINAL
// event's id — the timeline reads "applied late", not "invented later".
func TestReplayProvenance(t *testing.T) {
	e := newUnmappedEnv(t)
	// The delivery arrives before the repo is mapped. Its head ref is WL-1-x,
	// which correlates to the first task seeded in project demo.
	rr := deliver(t, e.h, "pull_request", "d-pr", "pull_request_opened.json")
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ignored" {
		t.Fatalf("delivery: %d %s", rr.Code, rr.Body.String())
	}

	// ...then the repo is mapped and the task exists, claimed (in_progress).
	mapDemoRepo(t, e)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	if taskID != "WL-1" {
		t.Fatalf("seeded task id = %q; the fixture's WL-1-x head ref no longer correlates", taskID)
	}

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Replayed != 1 {
		t.Fatalf("replay result = %+v; want 1 replayed", res)
	}

	if got := e.taskState(t, taskID); got != "in_review" {
		t.Fatalf("task state after replay = %q, want in_review", got)
	}

	var originalID int64
	row := e.st.DBForTests().QueryRow(`SELECT id FROM events WHERE external_id = 'd-pr'`)
	if err := row.Scan(&originalID); err != nil {
		t.Fatalf("original event id: %v", err)
	}
	entries, err := e.st.StateLogForEntity(context.Background(), "task", taskID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no state_log entries for %s; want the in_review transition", taskID)
	}
	last := entries[len(entries)-1]
	if last.EventID != originalID {
		t.Fatalf("transition event_id = %d; want the original event %d", last.EventID, originalID)
	}
}

// TestReplayDoesNotRegressNewerFacts is the WL-198 shape: a live delivery
// lands BETWEEN the backlogged .ignored event and the replay, so the replay
// re-applies a payload older than the facts already stored. The backlogged
// event is still replayed — correctness comes from the non-regressing upsert
// guard (see store.UpsertPR), not from excluding the event.
func TestReplayDoesNotRegressNewerFacts(t *testing.T) {
	e := newUnmappedEnv(t)

	const (
		t1 = "2026-07-19T10:00:00Z" // the backlogged "opened"
		t2 = "2026-07-19T12:00:00Z" // the live "closed as merged"
	)

	// Before the repo is mapped: pull_request.opened is recorded .ignored,
	// its apply never runs.
	rr := deliverBody(t, e.h, "pull_request", "d-open", []byte(`{
		"action": "opened",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"pull_request": {
			"number": 42, "title": "Fix crash on load", "state": "open", "merged": false,
			"body": "Fixes the crash.",
			"html_url": "https://github.com/sunstoneinstitute/demo/pull/42",
			"created_at": "`+t1+`", "updated_at": "`+t1+`",
			"merge_commit_sha": null, "merged_at": null,
			"head": {"ref": "feature/x", "sha": "abc1230000000000000000000000000000000000"}
		}
	}`))
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ignored" {
		t.Fatalf("backlogged delivery: %d %s", rr.Code, rr.Body.String())
	}

	mapDemoRepo(t, e)

	// Now mapped: the merge for the SAME PR arrives live and applies.
	rr = deliverBody(t, e.h, "pull_request", "d-merged", []byte(`{
		"action": "closed",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"pull_request": {
			"number": 42, "title": "Fix crash on load", "state": "closed", "merged": true,
			"body": "Fixes the crash.",
			"html_url": "https://github.com/sunstoneinstitute/demo/pull/42",
			"created_at": "`+t1+`", "updated_at": "`+t2+`",
			"merge_commit_sha": "def4560000000000000000000000000000000000",
			"merged_at": "`+t2+`",
			"head": {"ref": "feature/x", "sha": "abc1230000000000000000000000000000000000"}
		}
	}`))
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("live merge delivery: %d %s", rr.Code, rr.Body.String())
	}

	// Precondition: without a merged row first, the assertions below would
	// pass vacuously.
	assertPR42Merged(t, e, "before replay", t2)

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 1 || res.StillUnmapped != 0 {
		t.Fatalf("replay result = %+v; want 1 candidate, 1 replayed", res)
	}

	assertPR42Merged(t, e, "after replay", t2)

	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-open' AND applied_at IS NOT NULL`); n != 1 {
		t.Fatalf("replayed event applied_at set = %d rows, want 1", n)
	}
}

// assertPR42Merged requires the demo PR #42 row to carry the merge facts and
// the wantUpdated timestamp (RFC3339).
func assertPR42Merged(t *testing.T, e *env, when, wantUpdated string) {
	t.Helper()
	var state string
	var mergeSHA sql.NullString
	var mergedAt, updatedAt sql.NullTime
	if !e.rawQueryRow(t, []any{&state, &mergeSHA, &mergedAt, &updatedAt},
		`SELECT state, merge_sha, merged_at, updated_at FROM pull_requests
		 WHERE repo = $1 AND number = 42`, demoRepo) {
		t.Fatalf("%s: no pull_requests row for %s#42", when, demoRepo)
	}
	if state != "merged" {
		t.Fatalf("%s: state = %q, want merged (a stale payload regressed the row)", when, state)
	}
	if !mergeSHA.Valid || mergeSHA.String == "" {
		t.Fatalf("%s: merge_sha = %v, want it kept (a stale payload cleared it)", when, mergeSHA)
	}
	if !mergedAt.Valid {
		t.Fatalf("%s: merged_at is NULL, want it kept (a stale payload cleared it)", when)
	}
	want, err := time.Parse(time.RFC3339, wantUpdated)
	if err != nil {
		t.Fatalf("parse %q: %v", wantUpdated, err)
	}
	if !updatedAt.Valid || !updatedAt.Time.Equal(want) {
		t.Fatalf("%s: updated_at = %v, want %v (a stale payload moved the clock back)", when, updatedAt, want)
	}
}

func TestReplayDryRunWritesNothing(t *testing.T) {
	e := newUnmappedEnv(t)
	deliverBody(t, e.h, "issues", "d-1", []byte(`{
		"action": "opened",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"issue": {"number": 7, "title": "late issue", "state": "open", "html_url": "u"}
	}`))
	mapDemoRepo(t, e)

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run replay: %v", err)
	}
	if !res.DryRun || res.Candidates != 1 || res.Replayed != 1 {
		t.Fatalf("dry-run result = %+v; want 1 would-replay", res)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM issues`); n != 0 {
		t.Fatalf("dry-run wrote %d issue rows, want 0", n)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM events WHERE applied_at IS NOT NULL`); n != 0 {
		t.Fatalf("dry-run set applied_at on %d events, want 0", n)
	}
}

func TestReplaySkipsStillUnmapped(t *testing.T) {
	e := newUnmappedEnv(t)
	deliverBody(t, e.h, "issues", "d-1", []byte(`{
		"action": "opened",
		"repository": {"full_name": "never/mapped"},
		"issue": {"number": 1, "title": "x", "state": "open", "html_url": "u"}
	}`))

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 0 || res.StillUnmapped != 1 {
		t.Fatalf("replay result = %+v; want 1 still-unmapped, 0 replayed", res)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM events WHERE applied_at IS NULL`); n != 1 {
		t.Fatalf("still-unmapped event lost its NULL applied_at")
	}
}

// TestReplaySkipsSkillPushRows: a push.skills row with applied_at NULL —
// as an older binary would leave one mid rolling-deploy, since only the
// current webhook path marks it applied at ingestion — must not be routed
// through applyPush by the replayer. It is still a candidate and ends up
// applied (done, not stuck), but produces no ordinary-push effect.
func TestReplaySkipsSkillPushRows(t *testing.T) {
	e := newUnmappedEnv(t)
	mapDemoRepo(t, e)

	if _, err := e.st.DBForTests().Exec(
		`INSERT INTO events (source, external_id, type, payload, received_at)
		 VALUES ('github', 'd-skill', 'push.skills', $1, now())`,
		[]byte(`{"ref":"refs/heads/main","repository":{"full_name":"sunstoneinstitute/demo"}}`),
	); err != nil {
		t.Fatalf("insert push.skills row: %v", err)
	}

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 1 {
		t.Fatalf("replay result = %+v; want 1 candidate, 1 replayed", res)
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-skill' AND applied_at IS NOT NULL`); n != 1 {
		t.Fatalf("applied_at set = %d rows, want 1", n)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM task_commits`); n != 0 {
		t.Fatalf("task_commits after skill push replay = %d, want 0", n)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM main_commits`); n != 0 {
		t.Fatalf("main_commits after skill push replay = %d, want 0", n)
	}
}

// TestReplayRecordsMetrics: every candidate lands on exactly one bounded
// outcome label.
func TestReplayRecordsMetrics(t *testing.T) {
	e := newUnmappedEnv(t)
	deliverBody(t, e.h, "issues", "d-1", []byte(`{
		"action": "opened",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"issue": {"number": 7, "title": "late issue", "state": "open", "html_url": "u"}
	}`))
	deliverBody(t, e.h, "issues", "d-2", []byte(`{
		"action": "opened",
		"repository": {"full_name": "never/mapped"},
		"issue": {"number": 1, "title": "x", "state": "open", "html_url": "u"}
	}`))
	mapDemoRepo(t, e)

	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{Metrics: m})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Replayed != 1 || res.StillUnmapped != 1 {
		t.Fatalf("replay result = %+v; want 1 replayed, 1 still-unmapped", res)
	}

	for _, tc := range []struct {
		outcome string
		want    float64
	}{
		{"replayed", 1}, {"still_unmapped", 1}, {"dry_run", 0}, {"error", 0},
	} {
		got := testutil.ToFloat64(m.ReplayEvents().WithLabelValues(tc.outcome))
		if got != tc.want {
			t.Fatalf("replay_events{%s} = %v, want %v", tc.outcome, got, tc.want)
		}
	}
}

// TestReplayBatchIsBoundedAndResumes: the candidate read is a batch, so one
// run cannot pull an org-sized backlog of payloads into memory. A full batch
// says so, and the next run continues after it rather than repeating it.
func TestReplayBatchIsBoundedAndResumes(t *testing.T) {
	e := newUnmappedEnv(t)
	for _, id := range []string{"d-1", "d-2", "d-3"} {
		deliverBody(t, e.h, "issues", id, []byte(`{
			"action": "opened",
			"repository": {"full_name": "sunstoneinstitute/demo"},
			"issue": {"number": 7, "title": "late issue", "state": "open", "html_url": "u"}
		}`))
	}
	mapDemoRepo(t, e)

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{Limit: 2})
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if res.Candidates != 2 || res.Replayed != 2 || !res.Truncated {
		t.Fatalf("first batch = %+v; want 2 candidates, 2 replayed, truncated", res)
	}

	res, err = hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{Limit: 2})
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 1 || res.Truncated {
		t.Fatalf("second batch = %+v; want the remaining 1, not truncated", res)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM events WHERE applied_at IS NULL`); n != 0 {
		t.Fatalf("%d events still unapplied; batching skipped some", n)
	}
}

// TestReplayCapsReportedErrors: Errors becomes the reconcile response body's
// replay section, so a backlog where every apply fails must report a bounded
// list and count the rest rather than growing the response without limit.
func TestReplayCapsReportedErrors(t *testing.T) {
	e := newUnmappedEnv(t)
	mapDemoRepo(t, e)

	const overflow = 2
	// A payload that is valid jsonb but not a delivery envelope fails at
	// parse, which is the cheapest way to make every candidate an error.
	if _, err := e.st.DBForTests().Exec(
		`INSERT INTO events (source, external_id, type, payload, received_at)
		 SELECT 'github', 'd-bad-' || i, 'issues.opened.ignored', '{"repository": 5}'::jsonb, now()
		 FROM generate_series(1, $1) i`,
		hooks.MaxReplayErrorsForTest+overflow,
	); err != nil {
		t.Fatalf("seed unparseable events: %v", err)
	}

	res, err := hooks.Replay(context.Background(), e.st,
		hooks.ReplayOptions{Limit: hooks.MaxReplayErrorsForTest + overflow + 10})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(res.Errors) != hooks.MaxReplayErrorsForTest {
		t.Fatalf("reported %d errors, want the cap of %d", len(res.Errors), hooks.MaxReplayErrorsForTest)
	}
	if res.ErrorsOmitted != overflow {
		t.Fatalf("errors_omitted = %d, want %d", res.ErrorsOmitted, overflow)
	}
}

// TestReplayFilesCatalogEvidenceAfterDeclaration is WL-256's fix: a catalog
// delivery that matched no declaration when it arrived is a replay candidate,
// and the run that follows the declaration files the stored fact against it.
func TestReplayFilesCatalogEvidenceAfterDeclaration(t *testing.T) {
	e := newCatalogEnv(t)
	if got := ackStatus(t, catalogDeliver(t, e.h, "d-early",
		catalogBody(catalogArtifact, "published"))); got != "unrouted" {
		t.Fatalf("delivery before the declaration acked %q, want unrouted", got)
	}

	// The declaration shows up afterwards — the ordering the ack promised is
	// recoverable.
	id := e.seedDeliverable(t, "casualties", catalogArtifact)

	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{Metrics: m})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 1 || res.StillUnmapped != 0 {
		t.Fatalf("replay result = %+v; want 1 candidate, 1 replayed", res)
	}
	if got := e.evidenceRows(t, "deliverable", id); got != 1 {
		t.Fatalf("evidence rows after replay = %d, want 1", got)
	}
	// Provenance: the evidence points at the ORIGINAL delivery, and that
	// delivery is now marked applied, so it leaves the candidate set.
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM artifact_evidence ae
		   JOIN events ev ON ev.id = ae.event_id
		  WHERE ev.external_id = 'd-early' AND ev.applied_at IS NOT NULL`); n != 1 {
		t.Fatalf("evidence joined to the applied original event = %d rows, want 1", n)
	}
	if got := testutil.ToFloat64(m.CatalogEvidence().WithLabelValues("published", "deliverable")); got != 1 {
		t.Errorf("catalog_evidence{published,deliverable} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.ReplayEvents().WithLabelValues("replayed")); got != 1 {
		t.Errorf("replay_events{replayed} = %v, want 1", got)
	}

	// Second replay: nothing left to do, and no second evidence row.
	res, err = hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}
	if res.Candidates != 0 {
		t.Fatalf("second replay = %+v; want a no-op", res)
	}
	if got := e.evidenceRows(t, "deliverable", id); got != 1 {
		t.Fatalf("evidence rows after the second replay = %d, want 1", got)
	}
}

// TestCatalogRoutedDeliveryIsMarkedApplied: a delivery that routed at arrival
// is done, so it must not linger as a replay candidate. That marker is what
// keeps the candidate set finite rather than every catalog delivery ever.
func TestCatalogRoutedDeliveryIsMarkedApplied(t *testing.T) {
	e := newCatalogEnv(t)
	e.seedDeliverable(t, "casualties", catalogArtifact)

	if got := ackStatus(t, catalogDeliver(t, e.h, "d-live",
		catalogBody(catalogArtifact, "published"))); got != "ok" {
		t.Fatalf("ack = %q, want ok", got)
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-live' AND applied_at IS NOT NULL`); n != 1 {
		t.Fatalf("applied_at set on the routed delivery = %d rows, want 1", n)
	}

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Candidates != 0 {
		t.Fatalf("replay result = %+v; want no candidates", res)
	}
}

// TestReplayLeavesStillUnroutedCatalogDelivery: a delivery nothing declares
// yet is counted as still unrouted and left unapplied, so the declaration can
// still arrive tomorrow. Nothing is written for it in the meantime.
func TestReplayLeavesStillUnroutedCatalogDelivery(t *testing.T) {
	e := newCatalogEnv(t)
	catalogDeliver(t, e.h, "d-orphan", catalogBody("gs://nobody/declares-this", "published"))

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 0 || res.StillUnmapped != 1 {
		t.Fatalf("replay result = %+v; want 1 candidate, 0 replayed, 1 still unrouted", res)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM artifact_evidence`); n != 0 {
		t.Fatalf("wrote %d evidence row(s) for an undeclared address, want 0", n)
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-orphan' AND applied_at IS NULL`); n != 1 {
		t.Fatalf("still-unrouted delivery left unapplied = %d rows, want 1", n)
	}
}

// TestReplayDryRunLeavesCatalogDeliveryUnapplied: --dry-run reports what it
// would attempt and writes nothing — no evidence, no marker.
func TestReplayDryRunLeavesCatalogDeliveryUnapplied(t *testing.T) {
	e := newCatalogEnv(t)
	catalogDeliver(t, e.h, "d-dry", catalogBody(catalogArtifact, "published"))
	id := e.seedDeliverable(t, "casualties", catalogArtifact)

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 1 {
		t.Fatalf("dry run = %+v; want 1 candidate it would replay", res)
	}
	if got := e.evidenceRows(t, "deliverable", id); got != 0 {
		t.Fatalf("dry run wrote %d evidence row(s), want 0", got)
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-dry' AND applied_at IS NULL`); n != 1 {
		t.Fatalf("dry run marked the delivery applied; want it left as a candidate")
	}
}

// TestReplayReportsUnusableCatalogPayload: a stored payload the current
// validation rejects is reported and skipped, not retried forever as an
// apply that cannot succeed.
func TestReplayReportsUnusableCatalogPayload(t *testing.T) {
	e := newCatalogEnv(t)
	if _, err := e.st.DBForTests().Exec(
		`INSERT INTO events (source, external_id, type, payload, received_at)
		 VALUES ('catalog', 'd-bad', 'catalog.published', '{"artifact":"gs://x","state":"vanished"}'::jsonb, now())`,
	); err != nil {
		t.Fatalf("seed unusable catalog event: %v", err)
	}

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Replayed != 0 || len(res.Errors) != 1 {
		t.Fatalf("replay result = %+v; want 0 replayed and 1 reported error", res)
	}
}
