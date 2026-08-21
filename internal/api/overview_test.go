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
	_, h, token := newTestServer(t) // admin "alice", no graph client
	rec := doReq(t, h, http.MethodPost, "/api/v1/derive", token, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("derive as admin without graph: %d %s; want 503", rec.Code, rec.Body.String())
	}
}

// TestOverviewRollWithoutGraph checks the roll-up reports the graph is off
// rather than reporting zero drift, and still counts the backbone frontier.
func TestOverviewRollWithoutGraph(t *testing.T) {
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
