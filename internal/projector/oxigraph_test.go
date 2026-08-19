package projector_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphproj/graphtest"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/projector"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestProjectorEndToEnd is the plan's only end-to-end proof: a lifecycle
// event flows through the outbox, the projector renders and PUTs the whole
// project graph, and a real SPARQL endpoint answers the read-back — proving
// 006 §16 criterion 1 (event → projection → read-back), the §3 promise that
// wl:dependsOn+ is resolved query-time with no reasoner, and design call 4
// (abandoned tasks stay projected, not deleted).
//
// Oxigraph does not speak graph-server's branch-scoped Graph Store Protocol
// surface, so a small translating proxy stands between the projector's
// graphserver.Client and Oxigraph: it accepts the client's
// PUT /branches/main/graphs?graph=<iri> and forwards method, body, and
// Content-Type to Oxigraph's PUT /store?graph=<iri>, copying the status back.
// Everything upstream of the proxy — the client, the projector — is
// production code, unmodified.
func TestProjectorEndToEnd(t *testing.T) {
	base := graphtest.Endpoint(t)
	proxy := translatingOxigraphProxy(t, base)

	s := store.OpenTestStore(t)
	ctx := t.Context()

	suffix := randHex(t, 4)
	proj := "kg-" + suffix
	// projects_key_format requires ^[A-Z][A-Z0-9]{1,9}$, so the key is the
	// same random suffix, uppercased, rather than the lowercase project id.
	key := "KG" + strings.ToUpper(suffix)
	if err := s.CreateProject(ctx, proj, "KG End-to-End", key); err != nil {
		t.Fatalf("create project %s: %v", proj, err)
	}
	graph := iri.ProjectGraph(proj)
	t.Cleanup(func() { dropGraph(t, base, graph) })

	p := projector.New(s, graphserver.New(proxy.URL, nil), nil, 100)

	// 2. Seed tasks a, b, c and edges a blocks b, b blocks c.
	a := createTask(t, s, "e2e-a", proj, "task a")
	b := createTask(t, s, "e2e-b", proj, "task b")
	c := createTask(t, s, "e2e-c", proj, "task c")
	addEdge(t, s, "e2e-ab", a, b, "blocks")
	addEdge(t, s, "e2e-bc", b, c, "blocks")

	n, err := p.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want 1 project, nil", n, err)
	}

	// 3a. a's wl:taskState binds exactly one solution, "ready".
	assertSingleState(t, base, graph, a, "ready")

	// 3b. The property path <c> <wl:dependsOn>+ <a> binds: c depends on b
	// which depends on a, so the transitive closure reaches a with no
	// reasoner, purely as a query-time property path.
	pathRows := graphtest.Select(t, base, fmt.Sprintf(
		"SELECT ?x WHERE { GRAPH <%s> { <%s> <%s>+ ?x } FILTER(?x = <%s>) }",
		graph, iri.Task(c), iri.Term("dependsOn"), iri.Task(a)))
	if len(pathRows) != 1 {
		t.Fatalf("wl:dependsOn+ from c to a: %d solutions, want 1", len(pathRows))
	}

	// 3c. GROUP BY/COUNT: exactly one wl:inProject per projected task
	// (025 acceptance criterion 20's shape).
	assertOneInProjectEach(t, base, graph, 3)

	// 4. Transition a ready -> in_progress; whole-graph replace must leave
	// no stale state literal behind.
	transition(t, s, "e2e-a-start", a, "ready", "in_progress")
	n, err = p.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce after transition = %d, %v; want 1, nil", n, err)
	}
	assertSingleState(t, base, graph, a, "in_progress")

	// 5. Transition c ready -> abandoned; c must still be present with
	// wl:taskState "abandoned" (design call 4: abandoned tasks stay
	// projected, not deleted).
	transition(t, s, "e2e-c-abandon", c, "ready", "abandoned")
	n, err = p.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce after abandon = %d, %v; want 1, nil", n, err)
	}
	assertSingleState(t, base, graph, c, "abandoned")

	// The abandoned task must still carry exactly one wl:inProject too —
	// design call 4 means it stays a fully projected task, not a stub.
	assertOneInProjectEach(t, base, graph, 3)
}

// translatingOxigraphProxy bridges the production graphserver.Client's
// branch-scoped Graph Store Protocol writes to Oxigraph's plain GSP surface.
func translatingOxigraphProxy(t *testing.T, oxigraphBase string) *httptest.Server {
	t.Helper()
	wantPath := "/branches/" + projector.Branch + "/graphs"
	mux := http.NewServeMux()
	mux.HandleFunc(wantPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "unexpected method "+r.Method, http.StatusMethodNotAllowed)
			return
		}
		graphIRI := r.URL.Query().Get("graph")
		fwd, err := http.NewRequestWithContext(r.Context(), http.MethodPut,
			oxigraphBase+"/store?graph="+url.QueryEscape(graphIRI), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fwd.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		resp, err := http.DefaultClient.Do(fwd)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// randHex returns n bytes of crypto/rand as a hex string, for a project id
// unique enough to isolate this run's graph on a shared Oxigraph instance.
func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("crypto/rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// dropGraph deletes the named graph directly against the SPARQL endpoint
// (graphtest.PutGraph's own cleanup does not apply here: this test's writes
// go through the translating proxy, not graphtest.PutGraph).
func dropGraph(t *testing.T, base, graphIRI string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, base+"/store?graph="+url.QueryEscape(graphIRI), nil)
	if err != nil {
		t.Fatalf("build DELETE for %s: %v", graphIRI, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", graphIRI, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	// 404 is fine: the graph may never have been written if the test failed
	// before the first RunOnce.
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE %s: HTTP %d", graphIRI, resp.StatusCode)
	}
}

// addEdge adds a typed edge through the outbox, mirroring createTask.
func addEdge(t *testing.T, s *store.Store, extID, from, to, typ string) {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", extID, "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.AddEdge(tx, time.Now().UTC(), from, to, typ, eventID)
		})
	if err != nil {
		t.Fatalf("add edge %s %s %s: %v", from, typ, to, err)
	}
}

// transition moves a task from -> to through the outbox, mirroring
// createTask and addEdge.
func transition(t *testing.T, s *store.Store, extID, taskID, from, to string) {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", extID, "task.transition", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.Transition(tx, time.Now().UTC(), taskID, from, to, eventID)
		})
	if err != nil {
		t.Fatalf("transition %s %s -> %s: %v", taskID, from, to, err)
	}
}

// assertSingleState queries wl:taskState for task and requires exactly one
// solution binding want — proving both the read-back (criterion 1) and that
// a whole-graph replace leaves no stale literal behind after a transition.
func assertSingleState(t *testing.T, base, graph, task, want string) {
	t.Helper()
	rows := graphtest.Select(t, base, fmt.Sprintf(
		"SELECT ?state WHERE { GRAPH <%s> { <%s> <%s> ?state } }",
		graph, iri.Task(task), iri.Term("taskState")))
	if len(rows) != 1 {
		t.Fatalf("%s wl:taskState: %d solutions, want exactly 1: %v", task, len(rows), rows)
	}
	if got := rows[0]["state"]; got != want {
		t.Errorf("%s wl:taskState = %q, want %q", task, got, want)
	}
}

// assertOneInProjectEach proves 025 acceptance criterion 20's shape: every
// projected task binds exactly one wl:inProject, via GROUP BY/COUNT.
func assertOneInProjectEach(t *testing.T, base, graph string, wantTasks int) {
	t.Helper()
	rows := graphtest.Select(t, base, fmt.Sprintf(
		"SELECT ?task (COUNT(?p) AS ?n) WHERE { GRAPH <%s> { ?task <%s> ?p } } GROUP BY ?task",
		graph, iri.Term("inProject")))
	if len(rows) != wantTasks {
		t.Fatalf("%d tasks bind wl:inProject, want %d: %v", len(rows), wantTasks, rows)
	}
	for _, r := range rows {
		if r["n"] != "1" {
			t.Errorf("%s binds %s wl:inProject values, want 1", r["task"], r["n"])
		}
	}
}
