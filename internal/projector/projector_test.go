package projector_test

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/projector"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// fakeGraphServer records PUT bodies per graph IRI and can be told to fail.
type fakeGraphServer struct {
	mu        sync.Mutex
	fail      bool
	failGraph map[string]bool // graph IRI → reject only that graph
	puts      map[string][]string
	attempts  map[string]int // graph IRI → PUTs seen, rejected ones included
}

// setFail is used instead of a bare field write because the test goroutine
// and the httptest handler goroutine race on fail otherwise (-race).
func (f *fakeGraphServer) setFail(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = v
}

// setFailGraph rejects PUTs to one graph only, which is how a test models the
// case this package exists to contain: one project graph-server will not
// accept while every other project is fine.
func (f *fakeGraphServer) setFailGraph(graph string, v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failGraph == nil {
		f.failGraph = map[string]bool{}
	}
	f.failGraph[graph] = v
}

func (f *fakeGraphServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/branches/main/graphs" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		g := r.URL.Query().Get("graph")
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.attempts == nil {
			f.attempts = map[string]int{}
		}
		f.attempts[g]++
		if f.fail || f.failGraph[g] {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if f.puts == nil {
			f.puts = map[string][]string{}
		}
		status := http.StatusNoContent
		if len(f.puts[g]) == 0 {
			status = http.StatusCreated
		}
		f.puts[g] = append(f.puts[g], string(body))
		w.WriteHeader(status)
	})
}

func (f *fakeGraphServer) last(graph string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b := f.puts[graph]; len(b) > 0 {
		return b[len(b)-1]
	}
	return ""
}

func (f *fakeGraphServer) count(graph string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.puts[graph])
}

// attemptCount is count's counterpart for rejected writes: how many PUTs the
// graph saw, whether or not they were accepted.
func (f *fakeGraphServer) attemptCount(graph string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts[graph]
}

func newProjector(t *testing.T) (*store.Store, *projector.Projector, *fakeGraphServer) {
	t.Helper()
	s := store.OpenTestStore(t)
	for _, p := range [][3]string{{"alpha", "Alpha", "AL"}, {"beta", "Beta", "BE"}} {
		if err := s.CreateProject(t.Context(), p[0], p[1], p[2]); err != nil {
			t.Fatalf("create project %s: %v", p[0], err)
		}
	}
	f := &fakeGraphServer{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return s, projector.New(s, graphserver.New(srv.URL, nil), nil, 100), f
}

// createTask creates a ready task through the outbox, as the API does.
func createTask(t *testing.T, s *store.Store, extID, project, title string) string {
	t.Helper()
	var id string
	_, _, err := s.RecordEvent(t.Context(), "cli", extID, "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := store.CreateTask(tx, time.Now().UTC(), store.TaskInput{
				ProjectID: project, Title: title, Priority: "medium", Kind: "feature",
			}, eventID)
			if err != nil {
				return err
			}
			id = task.ID
			return nil
		})
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return id
}

func TestRunOnceProjectsCreatedTask(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	id := createTask(t, s, "p1", "alpha", "wire the projector")
	store.AwaitCommitHorizon(t, s) // so this one run also checkpoints past it

	n, err := p.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want 1 project, nil", n, err)
	}
	doc := f.last(iri.ProjectGraph("alpha"))
	for _, want := range []string{
		"<" + iri.Task(id) + "> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <" + iri.Term("Task") + ">",
		"<" + iri.Task(id) + "> <" + iri.Term("taskState") + "> \"ready\"",
		"<" + iri.Task(id) + "> <" + iri.Term("taskKind") + "> <" + iri.Concept("feature") + ">",
		"<" + iri.Task(id) + "> <" + iri.Term("inProject") + "> <" + iri.Project("alpha") + ">",
		"<" + iri.Project("alpha") + "> <http://purl.org/dc/terms/title> \"Alpha\"",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("project graph missing %q\n%s", want, doc)
		}
	}

	// Checkpoint advanced past the settled batch: a second run is a no-op
	// with no new PUT.
	if n, err := p.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("second RunOnce = %d, %v; want 0, nil", n, err)
	}
	if got := f.count(iri.ProjectGraph("alpha")); got != 1 {
		t.Fatalf("PUTs after idempotent rerun = %d; want 1", got)
	}
}

// TestRunOnceProjectsDocuments covers WL-289: a document mutation dirties
// its project, and the project's documents render into their per-document
// declared graphs with the canonical node's facts — including
// prov:wasGeneratedBy naming the authoring task.
func TestRunOnceProjectsDocuments(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	taskID := createTask(t, s, "doc-t1", "alpha", "author the spec")
	if _, err := p.RunOnce(ctx); err != nil {
		t.Fatalf("drain task create: %v", err)
	}

	_, _, err := s.RecordEvent(ctx, "cli", "doc-c1", "doc.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, cerr := store.CreateDoc(tx, time.Now().UTC(), store.DocInput{
				Project: "alpha", Kind: "spec", Number: 1, Slug: "001-alpha-spec",
				Body:            "---\nstatus: draft\n---\n# Spec 1 — Alpha spec\n",
				GeneratedByTask: taskID,
			}, eventID)
			return cerr
		})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}

	n, err := p.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want 1 project, nil", n, err)
	}
	declared := f.last(iri.DeclaredGraph("001-alpha-spec"))
	subj := "<" + iri.Doc("001-alpha-spec") + ">"
	for _, want := range []string{
		subj + " <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <" + iri.Term("Spec") + ">",
		subj + " <" + graphproj.ProvWasGeneratedBy + "> <" + iri.Task(taskID) + ">",
		subj + " <" + graphproj.DCATVersion + "> \"1\"",
	} {
		if !strings.Contains(declared, want) {
			t.Errorf("declared graph missing %q\n%s", want, declared)
		}
	}
}

func TestCrossProjectEdgeProjectsBothGraphs(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	a := createTask(t, s, "p2", "alpha", "blocker")
	b := createTask(t, s, "p3", "beta", "blocked")
	if _, err := p.RunOnce(ctx); err != nil {
		t.Fatalf("drain creates: %v", err)
	}

	_, _, err := s.RecordEvent(ctx, "cli", "p4", "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.AddEdge(tx, time.Now().UTC(), a, b, "blocks", eventID)
		})
	if err != nil {
		t.Fatalf("add edge: %v", err)
	}

	n, err := p.RunOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("RunOnce after edge = %d, %v; want both projects", n, err)
	}
	if doc := f.last(iri.ProjectGraph("alpha")); !strings.Contains(doc,
		"<"+iri.Task(a)+"> <"+iri.Term("blocks")+"> <"+iri.Task(b)+">") {
		t.Errorf("alpha graph missing wl:blocks\n%s", doc)
	}
	if doc := f.last(iri.ProjectGraph("beta")); !strings.Contains(doc,
		"<"+iri.Task(b)+"> <"+iri.Term("dependsOn")+"> <"+iri.Task(a)+">") {
		t.Errorf("beta graph missing wl:dependsOn\n%s", doc)
	}
}

// TestFailedProjectQuarantinedAndRetried is WL-118's core contract: a project
// the graph server rejects no longer holds the watermark back — the checkpoint
// advances past its state_log rows and the project itself is remembered in
// graph_projection_failures, so the very next run re-attempts it (the first
// retry is immediate; see retryDelay).
func TestFailedProjectQuarantinedAndRetried(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	createTask(t, s, "p5", "alpha", "unlucky")
	store.AwaitCommitHorizon(t, s) // the checkpoint may only pass a settled batch

	f.setFail(true)
	if n, err := p.RunOnce(ctx); err == nil || n != 0 {
		t.Fatalf("RunOnce against a failing endpoint = %d, %v; want 0 and an error", n, err)
	}
	cp, err := s.ProjectionCheckpoint(ctx)
	if err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	if cp == 0 {
		t.Fatal("checkpoint stayed 0 after a per-project failure; it must advance past the batch")
	}
	fails, err := s.ProjectionFailures(ctx)
	if err != nil {
		t.Fatalf("read quarantine: %v", err)
	}
	if len(fails) != 1 || fails[0].ProjectID != "alpha" || fails[0].Attempts != 1 {
		t.Fatalf("quarantine = %+v; want one alpha row at attempt 1", fails)
	}
	if fails[0].LastError == "" {
		t.Error("quarantine row recorded no error text")
	}

	// The task's state_log rows are behind the watermark now, so only the
	// quarantine row can bring the project back — which is the point.
	f.setFail(false)
	if n, err := p.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("retry RunOnce = %d, %v; want 1, nil", n, err)
	}
	if got := f.count(iri.ProjectGraph("alpha")); got != 1 {
		t.Fatalf("accepted PUTs after recovery = %d; want 1", got)
	}
	if fails, err := s.ProjectionFailures(ctx); err != nil || len(fails) != 0 {
		t.Fatalf("quarantine after recovery = %+v, %v; want empty", fails, err)
	}
}

// TestOneFailingProjectDoesNotBlockAnother is the blast-radius property: alpha
// and beta are dirty in the same batch, graph-server rejects alpha's graph
// only, and beta must still be projected — before WL-118 the loop returned on
// alpha and beta was never attempted at all.
func TestOneFailingProjectDoesNotBlockAnother(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	createTask(t, s, "p6", "alpha", "poison")
	createTask(t, s, "p7", "beta", "innocent bystander")
	store.AwaitCommitHorizon(t, s) // the checkpoint may only pass a settled batch

	f.setFailGraph(iri.ProjectGraph("alpha"), true)
	n, err := p.RunOnce(ctx)
	if err == nil {
		t.Fatal("RunOnce with one failing project returned nil error")
	}
	if n != 1 {
		t.Fatalf("RunOnce projected %d graphs; want 1 (beta), alpha having failed", n)
	}
	if got := f.count(iri.ProjectGraph("beta")); got != 1 {
		t.Errorf("beta PUTs = %d; want 1 — a sibling's failure must not skip it", got)
	}
	if got := f.count(iri.ProjectGraph("alpha")); got != 0 {
		t.Errorf("alpha accepted PUTs = %d; want 0", got)
	}

	first, err := s.ProjectionFailures(ctx)
	if err != nil || len(first) != 1 {
		t.Fatalf("quarantine = %+v, %v; want one alpha row", first, err)
	}

	// beta is done: with the checkpoint past both, only alpha comes back.
	if n, err := p.RunOnce(ctx); n != 0 || err == nil {
		t.Fatalf("second RunOnce = %d, %v; want 0 and alpha's error", n, err)
	}
	// A repeat failure advances the attempt count but not the start of the
	// outage: how long alpha has been stuck is what an operator reads.
	again, err := s.ProjectionFailures(ctx)
	if err != nil || len(again) != 1 {
		t.Fatalf("quarantine after retry = %+v, %v; want one alpha row", again, err)
	}
	if again[0].Attempts != 2 {
		t.Errorf("attempts = %d; want 2", again[0].Attempts)
	}
	if !again[0].FirstFailedAt.Equal(first[0].FirstFailedAt) {
		t.Errorf("first_failed_at moved: %v -> %v", first[0].FirstFailedAt, again[0].FirstFailedAt)
	}
	if got := f.count(iri.ProjectGraph("beta")); got != 1 {
		t.Errorf("beta re-projected on alpha's retry: PUTs = %d; want 1", got)
	}
	if got := f.attemptCount(iri.ProjectGraph("alpha")); got != 2 {
		t.Errorf("alpha attempts = %d; want 2 (quarantine retry)", got)
	}
}

// TestQuarantineBacksOff pins the cadence: the first retry is immediate, the
// second waits, and the wait is skipped once the clock passes it.
func TestQuarantineBacksOff(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	base := time.Now().UTC()
	p.SetClock(func() time.Time { return base })
	createTask(t, s, "p8", "alpha", "persistently poisonous")
	store.AwaitCommitHorizon(t, s) // the checkpoint may only pass a settled batch
	alpha := iri.ProjectGraph("alpha")

	f.setFailGraph(alpha, true)
	for i := 1; i <= 2; i++ { // attempt 1 (dirty), attempt 2 (immediate retry)
		if _, err := p.RunOnce(ctx); err == nil {
			t.Fatalf("run %d against a rejecting graph returned nil error", i)
		}
	}
	if got := f.attemptCount(alpha); got != 2 {
		t.Fatalf("attempts after two runs = %d; want 2", got)
	}

	// Attempt 2 set a retryBase wait, so a third run at the same instant is
	// a no-op even with the graph server healthy again.
	f.setFailGraph(alpha, false)
	if n, err := p.RunOnce(ctx); n != 0 || err != nil {
		t.Fatalf("run inside the backoff = %d, %v; want 0, nil", n, err)
	}
	if got := f.attemptCount(alpha); got != 2 {
		t.Fatalf("attempts inside the backoff = %d; want 2 — the wait was ignored", got)
	}

	p.SetClock(func() time.Time { return base.Add(projector.RetryDelay(2) + time.Second) })
	if n, err := p.RunOnce(ctx); n != 1 || err != nil {
		t.Fatalf("run after the backoff = %d, %v; want 1, nil", n, err)
	}
	if fails, err := s.ProjectionFailures(ctx); err != nil || len(fails) != 0 {
		t.Fatalf("quarantine after recovery = %+v, %v; want empty", fails, err)
	}
}

// TestDirtyProjectBypassesBackoff: new task activity is the event most likely
// to clear a content-specific rejection, so it re-attempts immediately rather
// than waiting out the backoff.
func TestDirtyProjectBypassesBackoff(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	base := time.Now().UTC()
	p.SetClock(func() time.Time { return base })
	alpha := iri.ProjectGraph("alpha")

	createTask(t, s, "p9", "alpha", "bad content")
	store.AwaitCommitHorizon(t, s) // the checkpoint may only pass a settled batch
	f.setFailGraph(alpha, true)
	for i := 1; i <= 2; i++ {
		if _, err := p.RunOnce(ctx); err == nil {
			t.Fatalf("run %d returned nil error", i)
		}
	}

	// In backoff now. New activity in the project must not have to wait.
	f.setFailGraph(alpha, false)
	createTask(t, s, "p10", "alpha", "content fixed")
	if n, err := p.RunOnce(ctx); n != 1 || err != nil {
		t.Fatalf("RunOnce over a re-dirtied quarantined project = %d, %v; want 1, nil", n, err)
	}
	if got := f.count(alpha); got != 1 {
		t.Fatalf("accepted PUTs = %d; want 1", got)
	}
}

func TestRetryDelayCurve(t *testing.T) {
	for _, tc := range []struct {
		attempts int
		want     time.Duration
	}{
		{0, 0}, {1, 0}, {2, time.Minute}, {3, 2 * time.Minute},
		{4, 4 * time.Minute}, {6, 16 * time.Minute},
		{7, 30 * time.Minute}, {50, 30 * time.Minute},
	} {
		if got := projector.RetryDelay(tc.attempts); got != tc.want {
			t.Errorf("RetryDelay(%d) = %v; want %v", tc.attempts, got, tc.want)
		}
	}
}

// TestJitteredDelaySpreadsTheHerd covers the retry spread: a global
// graph-server outage fails every project at the same instant, so without
// jitter their next_attempt_at values cluster and each cap boundary fires a
// full render and PUT for every project at once.
func TestJitteredDelaySpreadsTheHerd(t *testing.T) {
	// The floor holds: half the window is never jittered away, so a project
	// that keeps failing is not re-attempted sooner than half its delay.
	for _, attempts := range []int{2, 3, 4, 7, 50} {
		ceiling := projector.RetryDelay(attempts)
		for _, r := range []float64{0, 0.5, 0.999999} {
			got := projector.JitteredDelay(attempts, r)
			if got < ceiling/2 || got > ceiling {
				t.Errorf("JitteredDelay(%d, %v) = %v; want within [%v, %v]",
					attempts, r, got, ceiling/2, ceiling)
			}
		}
	}

	// The immediate first re-attempt stays immediate: there is nothing to
	// spread, and delaying it would cost the transient-failure recovery the
	// zero delay exists for.
	for _, attempts := range []int{0, 1} {
		if got := projector.JitteredDelay(attempts, 0.9); got != 0 {
			t.Errorf("JitteredDelay(%d, 0.9) = %v; want 0", attempts, got)
		}
	}

	// Distinct draws land on distinct times, which is the whole point.
	if a, b := projector.JitteredDelay(7, 0.1), projector.JitteredDelay(7, 0.9); a == b {
		t.Errorf("two jitter draws produced the same delay %v; the herd is not spread", a)
	}
}

// TestQuarantineRetryIsJittered covers the wiring: the row the projector
// writes carries the jittered delay, not the ceiling.
func TestQuarantineRetryIsJittered(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	base := time.Now().UTC()
	p.SetClock(func() time.Time { return base })
	p.SetJitter(func() float64 { return 0 })
	createTask(t, s, "j1", "alpha", "fail me")

	// Two failures: the first schedules an immediate retry, the second the
	// first real backoff.
	f.setFail(true)
	for range 2 {
		if _, err := p.RunOnce(ctx); err == nil {
			t.Fatal("RunOnce succeeded against a failing graph server")
		}
	}

	rows, err := s.ProjectionFailures(ctx)
	if err != nil {
		t.Fatalf("ProjectionFailures: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("quarantined projects = %d, want 1", len(rows))
	}
	// Compared with a tolerance because the column's resolution is coarser
	// than the clock's; the assertion still separates the floor from the
	// ceiling, which are 30s apart at this attempt count.
	want := base.Add(projector.JitteredDelay(rows[0].Attempts, 0))
	if drift := rows[0].NextAttemptAt.UTC().Sub(want.UTC()); drift > time.Second || drift < -time.Second {
		t.Errorf("next_attempt_at = %v, want %v (jitter 0 is the floor, half of the %v ceiling)",
			rows[0].NextAttemptAt.UTC(), want, projector.RetryDelay(rows[0].Attempts))
	}
}
