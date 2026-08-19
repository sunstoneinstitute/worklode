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

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/projector"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// fakeGraphServer records PUT bodies per graph IRI and can be told to fail.
type fakeGraphServer struct {
	mu   sync.Mutex
	fail bool
	puts map[string][]string // graph IRI → bodies, in arrival order
}

// setFail is used instead of a bare field write because the test goroutine
// and the httptest handler goroutine race on fail otherwise (-race).
func (f *fakeGraphServer) setFail(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = v
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
		if f.fail {
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

	// Checkpoint advanced: a second run is a no-op with no new PUT.
	if n, err := p.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("second RunOnce = %d, %v; want 0, nil", n, err)
	}
	if got := f.count(iri.ProjectGraph("alpha")); got != 1 {
		t.Fatalf("PUTs after idempotent rerun = %d; want 1", got)
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

func TestRunOnceLeavesCheckpointOnError(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	createTask(t, s, "p5", "alpha", "unlucky")

	f.setFail(true)
	if _, err := p.RunOnce(ctx); err == nil {
		t.Fatal("RunOnce against a failing endpoint returned nil error")
	}
	if cp, err := s.ProjectionCheckpoint(ctx); err != nil || cp != 0 {
		t.Fatalf("checkpoint after failure = %d, %v; must stay 0 for the retry", cp, err)
	}

	f.setFail(false)
	if n, err := p.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("retry RunOnce = %d, %v; want 1, nil", n, err)
	}
}
