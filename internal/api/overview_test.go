package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// overviewReadPaths are the five spec 007 reads, which share one permission
// and one authentication guard.
var overviewReadPaths = []string{
	"/api/v1/overview", "/api/v1/drift", "/api/v1/gaps",
	"/api/v1/frontier", "/api/v1/critical-path",
}

func TestOverviewEndpointsRequireAuth(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServer(t)
	for _, path := range overviewReadPaths {
		if rec := doReq(t, h, http.MethodGet, path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without token: %d; want 401", path, rec.Code)
		}
	}
	if rec := doReq(t, h, http.MethodPost, "/api/v1/derive", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /api/v1/derive without token: %d; want 401", rec.Code)
	}
}

// TestGraphBackedReadsWithoutGraphAre503: drift and gaps are answered from the
// knowledge graph, so an instance with no Config.Graph says so rather than
// pretending the org has neither violations nor gaps.
func TestGraphBackedReadsWithoutGraphAre503(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t) // the test config carries no graph client
	for _, path := range []string{"/api/v1/drift", "/api/v1/gaps"} {
		if rec := doReq(t, h, http.MethodGet, path, token, nil); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without graph: %d; want 503", path, rec.Code)
		}
	}
}

// TestBackboneReadsAnswerWithoutGraph is the other half: the frontier and the
// critical path are computed from the backbone, so they must answer on an
// instance with no graph configured. That is why NewServer always builds the
// overview service instead of leaving it nil.
func TestBackboneReadsAnswerWithoutGraph(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	for _, path := range []string{"/api/v1/frontier", "/api/v1/critical-path", "/api/v1/overview"} {
		rec := doReq(t, h, http.MethodGet, path, token, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s without graph: %d %s; want 200", path, rec.Code, rec.Body.String())
		}
	}
}

func TestFrontierEndpointOrdersAndAnnotates(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "low one", "priority": "low", "kind": "chore",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "critical one", "priority": "critical", "kind": "chore",
	})

	rec := doReq(t, h, http.MethodGet, "/api/v1/frontier", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("frontier: %d %s", rec.Code, rec.Body.String())
	}
	var resp model.FrontierList
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tasks) != 2 {
		t.Fatalf("frontier = %+v; want 2 tasks", resp.Tasks)
	}
	// WL-2 is the critical one, and the backbone's D9 order puts priority
	// ahead of age — the same order ClaimNext consumes.
	if resp.Tasks[0].ID != "WL-2" || resp.Tasks[0].Priority != "critical" {
		t.Fatalf("frontier[0] = %+v; want WL-2 (critical) first", resp.Tasks[0])
	}
	if resp.Tasks[1].ID != "WL-1" {
		t.Fatalf("frontier[1] = %+v; want WL-1 second", resp.Tasks[1])
	}
	// Nothing blocks anything here, so the annotations are the empty DAG's:
	// no fan-out, depth zero, nothing critical.
	for _, task := range resp.Tasks {
		if task.FanOut != 0 || task.Depth != 0 || task.IsCritical {
			t.Errorf("%s annotated %+v; want fan_out=0 depth=0 is_critical=false with no edges", task.ID, task)
		}
	}
}

// TestDeriveEndpointRequiresAdmin: running the derivers replaces org-wide
// named graphs and spends GitHub App calls across every repo, so it is
// permDeriveRun (admin), not the read permission the five GETs share.
func TestDeriveEndpointRequiresAdmin(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	worker := seedActor(t, st, "worker", "agent", "Worker", false)

	if rec := doReq(t, h, http.MethodPost, "/api/v1/derive", worker, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("derive as non-admin: %d %s; want 403", rec.Code, rec.Body.String())
	}
	// The same non-admin token reads the overview surface fine.
	for _, path := range overviewReadPaths {
		rec := doReq(t, h, http.MethodGet, path, worker, nil)
		if rec.Code == http.StatusForbidden {
			t.Errorf("%s as non-admin: 403; want the read to be permitted", path)
		}
	}
}

// TestDeriveWithoutGraphIs503: an admin on an instance with no graph endpoint
// is refused for the deployment's reason, not their own.
func TestDeriveWithoutGraphIs503(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t) // admin "alice", no graph client
	rec := doReq(t, h, http.MethodPost, "/api/v1/derive", token, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("derive as admin without graph: %d %s; want 503", rec.Code, rec.Body.String())
	}
}

// TestOverviewRollWithoutGraph checks the roll-up reports the graph is off
// rather than reporting zero drift, and still counts the backbone frontier.
func TestOverviewRollWithoutGraph(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "a task", "priority": "medium", "kind": "chore",
	})

	rec := doReq(t, h, http.MethodGet, "/api/v1/overview?project=proj", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("overview: %d %s", rec.Code, rec.Body.String())
	}
	var o model.Overview
	if err := json.Unmarshal(rec.Body.Bytes(), &o); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if o.GraphEnabled {
		t.Errorf("graph_enabled = true; want false with no graph configured")
	}
	if o.FrontierSize != 1 {
		t.Errorf("frontier_size = %d; want 1", o.FrontierSize)
	}
}

// WL-354 regression: a chain whose work is entirely finished is not the
// critical path. Depth stays historical; criticality follows the open
// subgraph (spec 007 §4's closed-task rule).
func TestCriticalPathExcludesFinishedChains(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	// WL-1 blocks WL-2 blocks WL-3 blocks WL-4.
	for i := 1; i <= 4; i++ {
		createTaskViaAPI(t, h, token, map[string]any{
			"project": "proj", "title": "chain", "priority": "medium", "kind": "chore",
		})
	}
	for _, e := range [][2]string{{"WL-1", "WL-2"}, {"WL-2", "WL-3"}, {"WL-3", "WL-4"}} {
		rr := doReq(t, h, http.MethodPost, "/api/v1/tasks/"+e[0]+"/edges", token,
			map[string]any{"type": "blocks", "to": e[1]})
		if rr.Code != http.StatusCreated && rr.Code != http.StatusOK {
			t.Fatalf("edge %v: %d %s", e, rr.Code, rr.Body.String())
		}
	}

	// Abandon is the one public one-step close, and taskClosed treats it
	// exactly like the live case's deployed_prod: closed is closed.
	closeTask := func(id string) {
		t.Helper()
		rr := doReq(t, h, http.MethodPost, "/api/v1/tasks/"+id+"/abandon", token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("abandon %s: %d %s", id, rr.Code, rr.Body.String())
		}
	}

	fetch := func() model.CriticalPath {
		t.Helper()
		rr := doReq(t, h, http.MethodGet, "/api/v1/critical-path", token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("critical-path: %d %s", rr.Code, rr.Body.String())
		}
		var cp model.CriticalPath
		if err := json.Unmarshal(rr.Body.Bytes(), &cp); err != nil {
			t.Fatal(err)
		}
		return cp
	}

	// Fully open: the whole chain is critical.
	if cp := fetch(); len(cp.Tasks) != 4 || cp.MaxDepth != 3 {
		t.Fatalf("open chain: %d tasks max depth %d, want 4/3", len(cp.Tasks), cp.MaxDepth)
	}

	// Close the first two. The path is the open remainder, with historical
	// depths: WL-3 at depth 2, WL-4 at depth 3.
	closeTask("WL-1")
	closeTask("WL-2")
	cp := fetch()
	if len(cp.Tasks) != 2 {
		t.Fatalf("half-closed chain: tasks = %+v, want WL-3 and WL-4", cp.Tasks)
	}
	depths := map[string]int{}
	for _, task := range cp.Tasks {
		depths[task.ID] = task.Depth
	}
	if depths["WL-3"] != 2 || depths["WL-4"] != 3 {
		t.Fatalf("historical depths = %v, want WL-3:2 WL-4:3", depths)
	}

	// Close the rest: the exact live case — every node deployed-or-merged.
	// The card reports no chain at all rather than finished work.
	closeTask("WL-3")
	closeTask("WL-4")
	if cp := fetch(); len(cp.Tasks) != 0 {
		t.Fatalf("fully closed chain still reported: %+v", cp.Tasks)
	}
}
