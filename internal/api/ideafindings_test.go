package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestIdeaFindingsAPI covers WL-REQ-1800's routes: a private idea's findings
// are 404 to everyone but its author, an admin included, and 409 to the author.
func TestIdeaFindingsAPI(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	admin := seedActor(t, st, "alice", "human", "Alice", true)
	bob := seedActor(t, st, "bob", "human", "Bob", false)
	createProject(t, st, "intake")
	h, metricsH, err := api.NewServer(st, api.Config{WebOpen: true, IntakeProject: "intake"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	idea := decodeIdea(t, doReq(t, h, "POST", "/api/v1/ideas", bob,
		map[string]any{"title": "t", "type": "general"}), http.StatusCreated)
	p := "/api/v1/ideas/" + idea.ID

	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", p + "/litmus-tests", nil},
		{"POST", p + "/litmus-tests", map[string]any{"date": "2026-10-01"}},
		{"GET", p + "/pre-research", nil},
		{"PUT", p + "/pre-research", map[string]any{"date": "2026-10-01"}},
	}
	for _, c := range routes {
		wantErr(t, doReq(t, h, c.method, c.path, admin, c.body), http.StatusNotFound, "")
		wantErr(t, doReq(t, h, c.method, c.path, bob, c.body), http.StatusConflict, "idea not shared")
		wantErr(t, doReq(t, h, c.method, "/api/v1/ideas/idea_nope"+strings.TrimPrefix(c.path, p), bob, c.body),
			http.StatusNotFound, "")
	}

	decodeIdea(t, doReq(t, h, "POST", p+"/share", bob, nil), http.StatusOK)

	var lts model.LitmusTestListResponse
	decodeBody(t, doReq(t, h, "GET", p+"/litmus-tests", admin, nil), http.StatusOK, &lts)
	if lts.LitmusTests == nil || len(lts.LitmusTests) != 0 {
		t.Fatalf("empty litmus = %#v", lts)
	}
	var lt model.LitmusTest
	decodeBody(t, doReq(t, h, "POST", p+"/litmus-tests", bob, map[string]any{
		"date": "2026-10-02", "summary": "passes", "recommendations": []string{"narrow"}}), http.StatusCreated, &lt)
	if lt.ID == "" || lt.Date != "2026-10-02" || lt.Summary != "passes" || !slices.Equal(lt.Recommendations, []string{"narrow"}) {
		t.Fatalf("created litmus = %+v", lt)
	}
	decodeBody(t, doReq(t, h, "GET", p+"/litmus-tests", admin, nil), http.StatusOK, &lts)
	if len(lts.LitmusTests) != 1 || lts.LitmusTests[0].ID != lt.ID {
		t.Fatalf("litmus list = %+v", lts)
	}

	var pr model.PreResearch
	decodeBody(t, doReq(t, h, "GET", p+"/pre-research", admin, nil), http.StatusOK, &pr)
	if pr.Findings == nil || len(pr.Findings) != 0 {
		t.Fatalf("empty pre-research = %+v", pr)
	}
	decodeBody(t, doReq(t, h, "PUT", p+"/pre-research", admin, map[string]any{
		"date": "2026-10-03", "findings": []string{"a", "b"}}), http.StatusOK, &pr)
	if !slices.Equal(pr.Findings, []string{"a", "b"}) {
		t.Fatalf("put pre-research = %+v", pr)
	}

	rr := doReq(t, h, "POST", p+"/litmus-tests", bob, map[string]any{"date": "soon"})
	var fe model.ErrorResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &fe)
	if rr.Code != http.StatusUnprocessableEntity || fe.Fields["date"] == "" {
		t.Fatalf("bad date = %d %s, want 422 with fields.date", rr.Code, rr.Body)
	}

	metrics := doReq(t, metricsH, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`worklode_ideas_writes_total{op="litmus"} 1`,
		`worklode_ideas_writes_total{op="pre_research"} 1`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}

// decodeBody decodes rr's body into v, failing the test on any other status.
func decodeBody(t *testing.T, rr *httptest.ResponseRecorder, want int, v any) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("status = %d, want %d; body %s", rr.Code, want, rr.Body)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rr.Body, err)
	}
}
