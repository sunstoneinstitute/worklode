package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// reconcilerSpecBody mirrors internal/store/rules_test.go's ruleDocV1
// fixture: sec-1 is rule 1, its child sec-1.1 is rule 2, sec-2 is
// rule 3.
const reconcilerSpecBody = "---\nstatus: draft\n---\n# T\n\nIntro.\n\n## 1. One {#sec-1}\n\nA.\n\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n## 2. Two {#sec-2}\n\nC.\n"

// reconcilerFixture is a store seeded with one project (key WL) and one
// accepted-shape spec carrying three rules, plus a bare *server holding
// just the fields handleSpecReconcile reaches for. Built by hand rather than
// through NewServer, matching docWatchFixture: no background loop runs, and
// the handler is driven one event at a time.
type reconcilerFixture struct {
	st  *store.Store
	srv *server
}

func newReconcilerFixture(t *testing.T) *reconcilerFixture {
	t.Helper()
	ctx := context.Background()
	st := store.OpenTestStore(t)

	if err := st.CreateProject(ctx, "wl", "wl", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	_, _, err := st.RecordEvent(ctx, "cli", "seed-doc", "doc.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := store.CreateDoc(tx, st.Now(), store.DocInput{
				Project: "wl", Kind: "spec", Number: 1, Slug: "t", Body: reconcilerSpecBody,
			}, eventID)
			return err
		})
	if err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	reg := prometheus.NewRegistry()
	return &reconcilerFixture{
		st:  st,
		srv: &server{st: st, log: slog.Default(), reconcilerMetrics: newReconcilerMetrics(reg)},
	}
}

// createTask makes a planless task in project WL and returns it, Branch set.
func (f *reconcilerFixture) createTask(t *testing.T) *model.Task {
	t.Helper()
	var task *model.Task
	_, _, err := f.st.RecordEvent(context.Background(), "cli", "seed-task:"+t.Name(), "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			task, err = store.CreateTask(tx, f.st.Now(), store.TaskInput{
				ProjectID: "wl", Title: "t", Kind: "feature", Priority: "medium",
			}, eventID)
			return err
		})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// governedBy reads a task's governing rule refs and sources.
func (f *reconcilerFixture) governedBy(t *testing.T, taskID string) []model.TaskGovernance {
	t.Helper()
	gov, err := f.st.GovernedBy(context.Background(), taskID)
	if err != nil {
		t.Fatalf("governed by %s: %v", taskID, err)
	}
	return gov
}

// governedEvents counts the task.governed events the reconciler recorded for
// a task, which is what tells a per-link external id from a per-delivery one.
func (f *reconcilerFixture) governedEvents(t *testing.T, taskID string) int {
	t.Helper()
	var n int
	if err := f.st.Tx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT count(*) FROM events WHERE type = 'task.governed' AND payload->>'task' = $1`, taskID).Scan(&n)
	}); err != nil {
		t.Fatalf("count task.governed for %s: %v", taskID, err)
	}
	return n
}

// govern links taskID to rule number in project WL with the given source,
// bypassing the gate — used to set up the "a plan already governs this task"
// case.
func (f *reconcilerFixture) govern(t *testing.T, taskID string, ruleNumber int64, source string) {
	t.Helper()
	if err := f.st.Tx(context.Background(), func(tx *sql.Tx) error {
		id, err := store.RuleIDByRef(tx, "WL", ruleNumber)
		if err != nil {
			return err
		}
		return store.Govern(tx, taskID, id, source, false)
	}); err != nil {
		t.Fatalf("govern %s by rule %d: %v", taskID, ruleNumber, err)
	}
}

// prEvent builds a github pull_request.opened event for a branch and body.
func prEvent(id int64, headRef, body string) store.Event {
	payload, _ := json.Marshal(map[string]any{
		"action":       "opened",
		"pull_request": map[string]any{"body": body, "head": map[string]any{"ref": headRef}},
	})
	return store.Event{ID: id, Source: "github", Type: "pull_request.opened", Payload: payload}
}

// pushEvent builds a github push event naming ref and carrying commits in
// the order the payload lists them (oldest first, matching GitHub's own
// ordering).
func pushEvent(id int64, ref string, commitMessages ...string) store.Event {
	commits := make([]map[string]any, len(commitMessages))
	for i, m := range commitMessages {
		commits[i] = map[string]any{"id": fmt.Sprintf("c%d", i), "message": m}
	}
	payload, _ := json.Marshal(map[string]any{"ref": "refs/heads/" + ref, "commits": commits})
	return store.Event{ID: id, Source: "github", Type: "push", Payload: payload}
}

func TestSpecReconcilerGovernsAPlanlessTask(t *testing.T) {
	f := newReconcilerFixture(t)
	task := f.createTask(t)
	ctx := context.Background()

	out, err := f.srv.handleSpecReconcile(ctx, prEvent(1001, task.Branch, "Adds it.\n\nSpec: WL-RULE-1\n"))
	if err != nil || out != eventbus.OutcomeApplied {
		t.Fatalf("first delivery: %v %v", out, err)
	}
	gov := f.governedBy(t, task.ID)
	if len(gov) != 1 || gov[0].Rule != "WL-RULE-1" || gov[0].Source != "gate" {
		t.Fatalf("governed_by = %+v", gov)
	}

	// Redelivery of the same event is absorbed.
	out, err = f.srv.handleSpecReconcile(ctx, prEvent(1001, task.Branch, "Spec: WL-RULE-1\n"))
	if err != nil || out != eventbus.OutcomeSuppressed {
		t.Fatalf("redelivery: %v %v", out, err)
	}
	if got := testutil.ToFloat64(f.srv.reconcilerMetrics.outcomes.WithLabelValues("already")); got != 1 {
		t.Errorf("already metric = %v, want 1", got)
	}

	// A section ref resolves to the rule at that anchor.
	out, err = f.srv.handleSpecReconcile(ctx, prEvent(1002, task.Branch, "Spec: WL-SPEC-1 sec-1.1\n"))
	if err != nil || out != eventbus.OutcomeApplied {
		t.Fatalf("section ref: %v %v", out, err)
	}
	gov = f.governedBy(t, task.ID)
	if len(gov) != 2 {
		t.Fatalf("after section ref: %+v", gov)
	}
}

// TestSpecReconcilerLinksOncePerTrailer covers the case that made the log
// grow: two different deliveries (a push after the pull request opened)
// carrying the same trailer for the same task. The second is counted as
// already governed and writes no second task.governed event.
func TestSpecReconcilerLinksOncePerTrailer(t *testing.T) {
	f := newReconcilerFixture(t)
	task := f.createTask(t)
	ctx := context.Background()

	out, err := f.srv.handleSpecReconcile(ctx, prEvent(7001, task.Branch, "Adds it.\n\nSpec: WL-RULE-1\n"))
	if err != nil || out != eventbus.OutcomeApplied {
		t.Fatalf("pull request: %v %v", out, err)
	}
	out, err = f.srv.handleSpecReconcile(ctx, pushEvent(7002, task.Branch, "more\n\nSpec: WL-RULE-1\n"))
	if err != nil || out != eventbus.OutcomeSuppressed {
		t.Fatalf("later push with the same trailer: %v %v", out, err)
	}
	if got := testutil.ToFloat64(f.srv.reconcilerMetrics.outcomes.WithLabelValues("already")); got != 1 {
		t.Errorf("already metric = %v, want 1", got)
	}
	if gov := f.governedBy(t, task.ID); len(gov) != 1 || gov[0].Rule != "WL-RULE-1" {
		t.Fatalf("governed_by = %+v", gov)
	}
	if n := f.governedEvents(t, task.ID); n != 1 {
		t.Errorf("task.governed events = %d, want 1", n)
	}
}

func TestSpecReconcilerLeavesPlannedAndNoneAlone(t *testing.T) {
	f := newReconcilerFixture(t)
	task := f.createTask(t)
	ctx := context.Background()

	out, _ := f.srv.handleSpecReconcile(ctx, prEvent(2001, task.Branch, "Spec: none refactor\n"))
	if out != eventbus.OutcomeSuppressed {
		t.Errorf("none: %v", out)
	}
	out, _ = f.srv.handleSpecReconcile(ctx, prEvent(2002, task.Branch, "no trailer\n"))
	if out != eventbus.OutcomeSuppressed {
		t.Errorf("no trailer: %v", out)
	}
	out, _ = f.srv.handleSpecReconcile(ctx, prEvent(2003, "feature/no-task", "Spec: WL-RULE-1\n"))
	if out != eventbus.OutcomeSuppressed {
		t.Errorf("no task: %v", out)
	}
	out, _ = f.srv.handleSpecReconcile(ctx, prEvent(2004, task.Branch, "Spec: WL-RULE-999\n"))
	if out != eventbus.OutcomeSuppressed {
		t.Errorf("unknown rule: %v", out)
	}
	if gov := f.governedBy(t, task.ID); len(gov) != 0 {
		t.Fatalf("nothing should be linked yet: %+v", gov)
	}

	// A plan link blocks the gate.
	f.govern(t, task.ID, 1, "plan")
	out, _ = f.srv.handleSpecReconcile(ctx, prEvent(2005, task.Branch, "Spec: WL-RULE-2\n"))
	if out != eventbus.OutcomeSuppressed {
		t.Errorf("planned task: %v", out)
	}
	if gov := f.governedBy(t, task.ID); len(gov) != 1 {
		t.Fatalf("plan link only: %+v", gov)
	}

	for _, o := range []string{"none", "no_trailer", "no_task", "unknown_target", "planned"} {
		if got := testutil.ToFloat64(f.srv.reconcilerMetrics.outcomes.WithLabelValues(o)); got != 1 {
			t.Errorf("%s metric = %v, want 1", o, got)
		}
	}
}

func TestSpecReconcilerReadsPushCommits(t *testing.T) {
	f := newReconcilerFixture(t)
	task := f.createTask(t)
	ctx := context.Background()

	out, err := f.srv.handleSpecReconcile(ctx, pushEvent(3001, task.Branch,
		"first\n\nSpec: WL-RULE-1\n", "second\n\nSpec: WL-RULE-2\n"))
	if err != nil || out != eventbus.OutcomeApplied {
		t.Fatalf("push: %v %v", out, err)
	}
	gov := f.governedBy(t, task.ID)
	if len(gov) != 1 || gov[0].Rule != "WL-RULE-2" {
		t.Fatalf("the final commit's trailer wins: %+v", gov)
	}
}

func TestSpecReconcilerMalformedTrailer(t *testing.T) {
	f := newReconcilerFixture(t)
	task := f.createTask(t)
	ctx := context.Background()

	out, err := f.srv.handleSpecReconcile(ctx, prEvent(4001, task.Branch, "Spec: not-a-ref\n"))
	if err != nil || out != eventbus.OutcomeSuppressed {
		t.Fatalf("malformed: %v %v", out, err)
	}
	if got := testutil.ToFloat64(f.srv.reconcilerMetrics.outcomes.WithLabelValues("malformed")); got != 1 {
		t.Errorf("malformed metric = %v, want 1", got)
	}
	if gov := f.governedBy(t, task.ID); len(gov) != 0 {
		t.Fatalf("nothing should be linked: %+v", gov)
	}
}

// TestSpecReconcilerIgnoresNonGithub confirms the loop is a no-op on the
// doc-lifecycle events flowing through the same log.
func TestSpecReconcilerIgnoresNonGithub(t *testing.T) {
	f := newReconcilerFixture(t)
	ctx := context.Background()
	out, err := f.srv.handleSpecReconcile(ctx, store.Event{ID: 5001, Source: "cli", Type: "task.created", Payload: []byte(`{}`)})
	if err != nil || out != eventbus.OutcomeSuppressed {
		t.Fatalf("non-github event: %v %v", out, err)
	}
}

// TestSpecReconcilerIgnoresUnmappedRepo confirms a delivery from a repo with
// no project mapping (typed "pull_request.opened.ignored" by
// internal/hooks/github.go) governs nothing, even when its branch happens to
// look like a real task's.
func TestSpecReconcilerIgnoresUnmappedRepo(t *testing.T) {
	f := newReconcilerFixture(t)
	task := f.createTask(t)
	ctx := context.Background()

	ev := prEvent(6001, task.Branch, "Spec: WL-RULE-1\n")
	ev.Type = "pull_request.opened.ignored"
	out, err := f.srv.handleSpecReconcile(ctx, ev)
	if err != nil || out != eventbus.OutcomeSuppressed {
		t.Fatalf("ignored delivery: %v %v", out, err)
	}
	if gov := f.governedBy(t, task.ID); len(gov) != 0 {
		t.Fatalf("nothing should be linked: %+v", gov)
	}
}
