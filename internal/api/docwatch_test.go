package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/watcher"
)

// watchSpecBody is the minimum a spec must be to pass CreateDoc's lint: an
// H1 for the title and numbered sections carrying {#sec-N} anchors.
const watchSpecBody = `---
status: draft
issued: 2026-08-01
---

# Documents in the backbone

Intro prose.

## 1. Scope {#sec-1}

Scope body.
`

// docWatchFixture is a store with one draft spec and a server holding just
// the two fields the handler reaches for. The server is built by hand rather
// than through NewServer: BackgroundCtx stays nil so no real loop runs, and
// the handler is then driven one event at a time, which takes every poll
// interval and every ack out of the test.
type docWatchFixture struct {
	st  *store.Store
	srv *server
	doc *model.Doc
	reg *prometheus.Registry
}

func newDocWatchFixture(t *testing.T) *docWatchFixture {
	t.Helper()
	ctx := t.Context()
	st := store.OpenTestStore(t)

	if err := st.CreateProject(ctx, "proj", "proj", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	// The mint's created_by; NewServer asserts this at boot, and a
	// hand-built server has to do the same.
	if err := st.EnsureServiceActor(ctx, watcherActorID, "doc-lifecycle watcher"); err != nil {
		t.Fatalf("ensure watcher actor: %v", err)
	}

	var doc *model.Doc
	_, _, err := st.RecordDocEvent(ctx, "create", "cli", "seed-doc", "doc.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			doc, err = store.CreateDoc(tx, st.Now(), store.DocInput{
				Project: "proj", Kind: "spec", Number: 25,
				Slug: "025-documents-in-the-backbone", Body: watchSpecBody,
				Assignee: "alice", CreatedBy: "alice",
			}, eventID)
			return err
		})
	if err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	reg := prometheus.NewRegistry()
	return &docWatchFixture{
		st:  st,
		srv: &server{st: st, log: slog.Default(), watcherMetrics: watcher.NewMetrics(reg)},
		doc: doc,
		reg: reg,
	}
}

// emit records one typed document event the way the API's own handlers do,
// through eventbus.Emit, so the payload the handler parses is the real shape
// rather than a hand-written row. version is what makes the external id
// distinct: two acceptances at the same version are one event by design.
func (f *docWatchFixture) emit(t *testing.T, ev eventbus.DomainEvent) store.Event {
	t.Helper()
	id, inserted, err := eventbus.Emit(t.Context(), f.st, "cli", ev, nil)
	if err != nil {
		t.Fatalf("emit %s: %v", ev.EventType(), err)
	}
	if !inserted {
		t.Fatalf("emit %s: external id %q already recorded", ev.EventType(), ev.ExternalID())
	}
	row, err := f.st.GetEvent(t.Context(), id)
	if err != nil {
		t.Fatalf("get event %d: %v", id, err)
	}
	return row
}

func (f *docWatchFixture) submitted(t *testing.T, version int) store.Event {
	t.Helper()
	return f.emit(t, eventbus.DocumentSubmitted{
		Doc: store.DocIRI(*f.doc), Actor: "alice", At: f.st.Now(), Version: version,
	})
}

func (f *docWatchFixture) accepted(t *testing.T, version int) store.Event {
	t.Helper()
	return f.emit(t, eventbus.DocumentAccepted{
		Doc: store.DocIRI(*f.doc), Actor: "alice", At: f.st.Now(),
		Version: version, From: "wlc:draft", To: "wlc:accepted",
	})
}

// handle runs the handler on one event and fails on any error.
func (f *docWatchFixture) handle(t *testing.T, ev store.Event) eventbus.Outcome {
	t.Helper()
	outcome, err := f.srv.handleDocLifecycle(t.Context(), ev)
	if err != nil {
		t.Fatalf("handler on event %d (%s): %v", ev.ID, ev.Type, err)
	}
	return outcome
}

// tasksAbout is the doc's review/design task set — the query 025 §1 says the
// set is, rather than a stored list.
func (f *docWatchFixture) tasksAbout(t *testing.T) []model.Task {
	t.Helper()
	tasks, err := f.st.ListTasks(t.Context(), store.TaskFilter{AboutDoc: f.doc.ID})
	if err != nil {
		t.Fatalf("list tasks about doc %d: %v", f.doc.ID, err)
	}
	return tasks
}

// abandon closes a task the way the state machine does, so OpenTaskForDoc
// stops counting it.
func (f *docWatchFixture) abandon(t *testing.T, taskID string) {
	t.Helper()
	_, _, err := f.st.RecordEvent(t.Context(), "cli", "abandon-"+taskID, "task.updated", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.Transition(tx, f.st.Now(), taskID, "ready", "abandoned", eventID)
		})
	if err != nil {
		t.Fatalf("abandon %s: %v", taskID, err)
	}
}

// wantActions asserts one cell of worklode_watcher_actions_total.
func (f *docWatchFixture) wantActions(t *testing.T, rule, outcome string, want float64) {
	t.Helper()
	got := testutil.ToFloat64(f.srv.watcherMetrics.Actions().WithLabelValues(rule, outcome))
	if got != want {
		t.Errorf("worklode_watcher_actions_total{rule=%q,outcome=%q} = %v, want %v", rule, outcome, got, want)
	}
}

func TestDocWatchMintsReviewOnSubmit(t *testing.T) {
	f := newDocWatchFixture(t)

	if got := f.handle(t, f.submitted(t, 1)); got != eventbus.OutcomeApplied {
		t.Errorf("outcome = %q, want %q", got, eventbus.OutcomeApplied)
	}

	tasks := f.tasksAbout(t)
	if len(tasks) != 1 {
		t.Fatalf("tasks about the doc = %d, want 1: %+v", len(tasks), tasks)
	}
	got := tasks[0]
	for _, c := range []struct{ name, got, want string }{
		{"kind", got.Kind, "review"},
		{"state", got.State, "ready"},
		{"project", got.Project, f.doc.Project},
		{"created_by", got.CreatedBy, watcherActorID},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if got.AboutDoc != f.doc.ID {
		t.Errorf("about_doc = %d, want %d", got.AboutDoc, f.doc.ID)
	}
	if !strings.Contains(got.Title, f.doc.Title) {
		t.Errorf("title = %q, want it to name the document %q", got.Title, f.doc.Title)
	}
	f.wantActions(t, "review-on-submit", "applied", 1)
	f.wantActions(t, "review-on-submit", "suppressed", 0)
}

// TestDocWatchRedeliveryMintsOnce is idempotency layer 1 on its own: the same
// event row handled twice. The open-task guard would also stop the second
// mint, so the assertion is deliberately about the event, not the task —
// the second run's RecordEvent hits (source, external_id) and skips apply
// before any guard is consulted.
func TestDocWatchRedeliveryMintsOnce(t *testing.T) {
	f := newDocWatchFixture(t)
	ev := f.submitted(t, 1)

	f.handle(t, ev)
	f.handle(t, ev)

	tasks := f.tasksAbout(t)
	if len(tasks) != 1 {
		t.Fatalf("tasks about the doc = %d, want 1: %+v", len(tasks), tasks)
	}
	// One state_log entry means apply ran once, which is the layer being
	// tested: two RecordEvent calls, one insert. The entry also names the
	// mint event, so the assertions below reach it without listing the log
	// (ListEvents is horizon-bounded; GetEvent by id is not).
	entries, err := f.st.StateLogForEntity(t.Context(), "task", tasks[0].ID)
	if err != nil {
		t.Fatalf("state log for %s: %v", tasks[0].ID, err)
	}
	if len(entries) != 1 {
		t.Fatalf("state log for %s = %+v, want exactly the mint entry", tasks[0].ID, entries)
	}
	mint, err := f.st.GetEvent(t.Context(), entries[0].EventID)
	if err != nil {
		t.Fatalf("get mint event %d: %v", entries[0].EventID, err)
	}
	wantExtID := "doc-lifecycle:review-on-submit:" + strconv.FormatInt(ev.ID, 10)
	if mint.Source != watcherEventSource || mint.ExternalID != wantExtID {
		t.Errorf("mint event = (%q, %q), want (%q, %q)",
			mint.Source, mint.ExternalID, watcherEventSource, wantExtID)
	}
	if !strings.Contains(string(mint.Payload), eventIRI(ev.ID)) {
		t.Errorf("payload %s does not carry prov:wasInformedBy %s", mint.Payload, eventIRI(ev.ID))
	}
}

// TestDocWatchSuppressionCycle walks 025 §15.4's whole cycle for the
// plan-on-accept rule: mint, suppress-with-a-note while the design task is
// open, and mint again once it closes — because sections accepted since the
// last plan do need planning.
func TestDocWatchSuppressionCycle(t *testing.T) {
	f := newDocWatchFixture(t)

	f.handle(t, f.accepted(t, 1))
	tasks := f.tasksAbout(t)
	if len(tasks) != 1 || tasks[0].Kind != "design" {
		t.Fatalf("after the first acceptance: %+v, want one design task", tasks)
	}
	design := tasks[0].ID

	// A second acceptance while it is open: absorbed, not minted.
	second := f.accepted(t, 2)
	if got := f.handle(t, second); got != eventbus.OutcomeSuppressed {
		t.Errorf("outcome = %q, want %q", got, eventbus.OutcomeSuppressed)
	}
	if tasks := f.tasksAbout(t); len(tasks) != 1 {
		t.Fatalf("after the suppressed acceptance: %d tasks, want still 1: %+v", len(tasks), tasks)
	}
	entries, err := f.st.StateLogForEntity(t.Context(), "task", design)
	if err != nil {
		t.Fatalf("state log for %s: %v", design, err)
	}
	// jsonb round-trips with a space after the colon.
	wantAbsorbed := fmt.Sprintf(`"absorbed_event": %d`, second.ID)
	if !slicesContainsSubstring(entries, wantAbsorbed) {
		t.Errorf("state log for %s = %+v, want an entry containing %s", design, entries, wantAbsorbed)
	}
	f.wantActions(t, "plan-on-accept", "suppressed", 1)

	// Closed guard, so the next acceptance is real work again.
	f.abandon(t, design)
	if got := f.handle(t, f.accepted(t, 3)); got != eventbus.OutcomeApplied {
		t.Errorf("outcome after abandon = %q, want %q", got, eventbus.OutcomeApplied)
	}
	tasks = f.tasksAbout(t)
	if len(tasks) != 2 {
		t.Fatalf("after the third acceptance: %d tasks, want 2: %+v", len(tasks), tasks)
	}
	f.wantActions(t, "plan-on-accept", "applied", 2)
}

// TestDocWatchIgnoresVendorEvents: the log's dotted population is not RDF
// (025 §15.2) and no rule speaks about it, so it is acked untouched.
func TestDocWatchIgnoresVendorEvents(t *testing.T) {
	f := newDocWatchFixture(t)

	id, _, err := f.st.RecordEvent(t.Context(), "github", "delivery-1", "push",
		[]byte(`{"ref":"refs/heads/main"}`), nil)
	if err != nil {
		t.Fatalf("record push event: %v", err)
	}
	ev, err := f.st.GetEvent(t.Context(), id)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}

	if got := f.handle(t, ev); got != eventbus.OutcomeApplied {
		t.Errorf("outcome = %q, want %q", got, eventbus.OutcomeApplied)
	}
	if tasks := f.tasksAbout(t); len(tasks) != 0 {
		t.Fatalf("tasks about the doc = %d, want 0: %+v", len(tasks), tasks)
	}
	f.wantActions(t, "review-on-submit", "applied", 0)
	f.wantActions(t, "plan-on-accept", "applied", 0)
}

// TestDocWatchUnknownSubjectIsAnError: a wl:subject naming no document is
// returned as an error, not swallowed as an applied no-op. The loop then
// retries it head-of-line, which is right while the row is merely not
// visible yet; a genuinely deleted document is `lode event seek`'s problem.
func TestDocWatchUnknownSubjectIsAnError(t *testing.T) {
	f := newDocWatchFixture(t)

	ev := f.emit(t, eventbus.DocumentSubmitted{
		Doc: "wlid:doc/spec-proj-999", Actor: "alice", At: f.st.Now(), Version: 1,
	})
	_, err := f.srv.handleDocLifecycle(t.Context(), ev)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want store.ErrNotFound", err)
	}
	if tasks := f.tasksAbout(t); len(tasks) != 0 {
		t.Fatalf("tasks about the doc = %d, want 0: %+v", len(tasks), tasks)
	}
}

func slicesContainsSubstring(entries []store.StateLogEntry, want string) bool {
	for _, e := range entries {
		if strings.Contains(e.Change, want) {
			return true
		}
	}
	return false
}
