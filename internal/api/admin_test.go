package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestCreateAndListProjects(t *testing.T) {
	_, h, token := newTestServer(t)

	rr := doReq(t, h, "POST", "/api/v1/projects", token, map[string]any{
		"id": "proj", "name": "Project", "key": "PROJ",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["id"] != "proj" || got["name"] != "Project" || got["key"] != "PROJ" {
		t.Fatalf("create project body = %v", got)
	}
	if repos, ok := got["repos"].([]any); !ok || len(repos) != 0 {
		t.Fatalf("create project repos = %v, want empty array", got["repos"])
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/proj/repos", token, map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add repo status = %d, body %s", rr.Code, rr.Body.String())
	}

	// Adding the same repo again (any project) is a conflict.
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/repos", token, map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("re-add repo status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/nosuch/repos", token, map[string]any{"repo": "acme/other"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("add repo to unknown project status = %d, want 404", rr.Code)
	}

	rr = doReq(t, h, "GET", "/api/v1/projects", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list projects status = %d, body %s", rr.Code, rr.Body.String())
	}
	var body projectListBody
	decodeInto(t, rr, &body)
	if len(body.Projects) != 1 {
		t.Fatalf("projects = %v, want 1", body.Projects)
	}
	p := body.Projects[0]
	if p.ID != "proj" || len(p.Repos) != 1 {
		t.Fatalf("project = %+v", p)
	}
	if p.Repos[0].Repo != "acme/widgets" || p.Repos[0].DoneState != "merged" {
		t.Fatalf("repo mapping = %+v, want acme/widgets/merged", p.Repos[0])
	}
}

// projectListBody is the decoded shape of GET /api/v1/projects.
type projectListBody struct {
	Projects []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Repos []struct {
			Repo      string `json:"repo"`
			DoneState string `json:"done_state"`
		} `json:"repos"`
	} `json:"projects"`
}

// listedDoneState returns the done_state GET /api/v1/projects reports for
// repo, or "" if the repo is not listed anywhere.
func listedDoneState(t *testing.T, h http.Handler, token, repo string) string {
	t.Helper()
	rr := doReq(t, h, "GET", "/api/v1/projects", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list projects status = %d, body %s", rr.Code, rr.Body.String())
	}
	var body projectListBody
	decodeInto(t, rr, &body)
	for _, p := range body.Projects {
		for _, m := range p.Repos {
			if m.Repo == repo {
				return m.DoneState
			}
		}
	}
	return ""
}

// projectDetailBody is the decoded shape of GET /api/v1/projects/{id}: the
// list-shape fields plus the cost window.
type projectDetailBody struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Key   string   `json:"key"`
	Focus []string `json:"focus"`
	Repos []struct {
		Repo      string `json:"repo"`
		DoneState string `json:"done_state"`
	} `json:"repos"`
	Cost projectCostBody `json:"cost"`
}

type projectCostBody struct {
	Days []struct {
		Day                string `json:"day"`
		Currency           string `json:"currency"`
		InputTokens        int64  `json:"input_tokens"`
		CacheWrite5mTokens int64  `json:"cache_write_5m_tokens"`
		CacheWrite1hTokens int64  `json:"cache_write_1h_tokens"`
		CacheReadTokens    int64  `json:"cache_read_tokens"`
		OutputTokens       int64  `json:"output_tokens"`
		CostAmount         string `json:"cost_amount"`
		UnpricedTokens     int64  `json:"unpriced_tokens"`
	} `json:"days"`
	Totals []struct {
		Currency   string `json:"currency"`
		CostAmount string `json:"cost_amount"`
	} `json:"totals"`
}

// projectCost GETs a project detail path (query string and all) and returns
// its cost half.
func projectCost(t *testing.T, h http.Handler, token, path string) projectCostBody {
	t.Helper()
	rr := doReq(t, h, "GET", path, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, body %s", path, rr.Code, rr.Body.String())
	}
	var body projectDetailBody
	decodeInto(t, rr, &body)
	return body.Cost
}

// TestGetProject covers GET /api/v1/projects/{id}: the project's own fields,
// the empty cost window a project with no recorded usage has, and 404 on an
// unknown id.
func TestGetProject(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	if err := st.AddRepo(context.Background(), "proj", "acme/widgets"); err != nil {
		t.Fatalf("add repo: %v", err)
	}

	rr := doReq(t, h, "GET", "/api/v1/projects/proj", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get project status = %d, body %s", rr.Code, rr.Body.String())
	}
	var body projectDetailBody
	decodeInto(t, rr, &body)
	if body.ID != "proj" || body.Key != "WL" {
		t.Fatalf("project = %+v", body)
	}
	if len(body.Repos) != 1 || body.Repos[0].Repo != "acme/widgets" {
		t.Fatalf("repos = %+v", body.Repos)
	}

	// A project with no usage reports empty arrays, not nulls: a client
	// iterating days must not have to special-case a missing window.
	cost, _ := decodeMap(t, rr)["cost"].(map[string]any)
	for _, field := range []string{"days", "totals"} {
		if arr, ok := cost[field].([]any); !ok || len(arr) != 0 {
			t.Fatalf("cost.%s = %v, want empty array", field, cost[field])
		}
	}

	rr = doReq(t, h, "GET", "/api/v1/projects/nosuch", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown project status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestGetProjectCostWindow covers the from/to bounds on the cost window.
func TestGetProjectCostWindow(t *testing.T) {
	st, h, token := newTestServer(t)
	endSessionWithUsage(t, st, h, token, []map[string]any{sonnetUsagePrevDay, sonnetUsage})

	for name, tc := range map[string]struct {
		query string
		days  []string
		total string
	}{
		"unbounded":  {"", []string{"2026-07-30", "2026-07-31"}, "10.000000"},
		"from only":  {"?from=2026-07-31", []string{"2026-07-31"}, "9.000000"},
		"to only":    {"?to=2026-07-30", []string{"2026-07-30"}, "1.000000"},
		"both ends":  {"?from=2026-07-31&to=2026-07-31", []string{"2026-07-31"}, "9.000000"},
		"past usage": {"?from=2026-08-01", nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			cost := projectCost(t, h, token, "/api/v1/projects/proj"+tc.query)
			if len(cost.Days) != len(tc.days) {
				t.Fatalf("days = %+v, want %v", cost.Days, tc.days)
			}
			for i, want := range tc.days {
				if cost.Days[i].Day != want {
					t.Fatalf("days[%d] = %q, want %q", i, cost.Days[i].Day, want)
				}
			}
			if tc.total == "" {
				if len(cost.Totals) != 0 {
					t.Fatalf("totals = %+v, want none", cost.Totals)
				}
				return
			}
			if len(cost.Totals) != 1 || cost.Totals[0].CostAmount != tc.total {
				t.Fatalf("totals = %+v, want a single %s", cost.Totals, tc.total)
			}
		})
	}

	rr := doReq(t, h, "GET", "/api/v1/projects/proj?from=31-07-2026", token, nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed from status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

// TestAddRepoDoneState covers the optional done_state field on POST
// /api/v1/projects/{id}/repos.
func TestAddRepoDoneState(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "POST", "/api/v1/projects", token, map[string]any{"id": "proj", "name": "Project", "key": "PROJ"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/proj/repos", token,
		map[string]any{"repo": "acme/widgets", "done_state": "deployed_prod"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add repo status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["done_state"]; got != "deployed_prod" {
		t.Fatalf("add repo response done_state = %v, want deployed_prod", got)
	}
	if got := listedDoneState(t, h, token, "acme/widgets"); got != "deployed_prod" {
		t.Fatalf("stored done_state = %q, want deployed_prod", got)
	}

	// An invalid done_state is rejected and maps nothing.
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/repos", token,
		map[string]any{"repo": "acme/other", "done_state": "bogus"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("add repo bogus done_state status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if got := listedDoneState(t, h, token, "acme/other"); got != "" {
		t.Fatalf("acme/other was mapped with done_state %q despite 422", got)
	}

	// Omitting done_state leaves the mapping at the schema default.
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/repos", token, map[string]any{"repo": "acme/third"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add repo without done_state status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := listedDoneState(t, h, token, "acme/third"); got != "merged" {
		t.Fatalf("default done_state = %q, want merged", got)
	}
}

// TestPatchRepoDoneState covers PATCH /api/v1/repos/{owner}/{name}.
func TestPatchRepoDoneState(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "POST", "/api/v1/projects", token, map[string]any{"id": "proj", "name": "Project", "key": "PROJ"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/repos", token, map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add repo status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "PATCH", "/api/v1/repos/acme/widgets", token, map[string]any{"done_state": "released"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("patch repo status = %d, want 204; body %s", rr.Code, rr.Body.String())
	}
	if got := listedDoneState(t, h, token, "acme/widgets"); got != "released" {
		t.Fatalf("done_state = %q, want released", got)
	}

	rr = doReq(t, h, "PATCH", "/api/v1/repos/acme/widgets", token, map[string]any{"done_state": "bogus"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("patch bogus done_state status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if got := listedDoneState(t, h, token, "acme/widgets"); got != "released" {
		t.Fatalf("done_state after rejected patch = %q, want released", got)
	}

	rr = doReq(t, h, "PATCH", "/api/v1/repos/acme/widgets", token, map[string]any{})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("patch without done_state status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["error"]; got != "done_state is required" {
		t.Fatalf("patch without done_state error = %v, want \"done_state is required\"", got)
	}

	rr = doReq(t, h, "PATCH", "/api/v1/repos/acme/nosuch", token, map[string]any{"done_state": "released"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("patch unmapped repo status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestPatchProjectFocus covers PATCH /api/v1/projects/{id}: focus is echoed
// back on success, an invalid concern entry is 422, and a missing project is
// 404.
func TestPatchProjectFocus(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "POST", "/api/v1/projects", token, map[string]any{"id": "proj", "name": "Project", "key": "PROJ"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["focus"]; got == nil {
		t.Fatalf("create project focus missing: %v", decodeMap(t, rr))
	} else if arr, ok := got.([]any); !ok || len(arr) != 0 {
		t.Fatalf("create project focus = %v, want empty array", got)
	}

	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj", token, map[string]any{
		"focus": []string{"security", "completeness"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch focus status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	focus, ok := got["focus"].([]any)
	if !ok || len(focus) != 2 || focus[0] != "security" || focus[1] != "completeness" {
		t.Fatalf("focus = %v, want [security completeness]", got["focus"])
	}

	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj", token, map[string]any{"focus": []string{"nonsense"}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid concern status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "PATCH", "/api/v1/projects/nosuch", token, map[string]any{"focus": []string{"security"}})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing project status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj", token, map[string]any{})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
}

// patchCockpitCards fetches the cockpit's curated cards for project id.
func patchCockpitCards(t *testing.T, h http.Handler, token, id string) pinnedFocusDecode {
	t.Helper()
	rr := doReq(t, h, "GET", "/api/v1/projects/"+id+"/cockpit", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("cockpit status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got pinnedFocusDecode
	decodeInto(t, rr, &got)
	return got
}

// TestPatchProjectCuratedCards covers PATCH /api/v1/projects/{id} setting the
// pinned-focus and next-decision cards: one combined PATCH sets both (visible
// via the cockpit), an empty focus_note/decision_title clears each, and a body
// carrying only a companion field (no trigger) is a 422.
func TestPatchProjectCuratedCards(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	if err := st.CreateActor(context.Background(), "stig", "human", "Stig Bakken", true); err != nil {
		t.Fatalf("create actor stig: %v", err)
	}

	// A single PATCH sets ranking focus, pinned focus, and next decision.
	rr := doReq(t, h, "PATCH", "/api/v1/projects/proj", token, map[string]any{
		"focus":                []string{"security"},
		"focus_note":           "Ship the cockpit",
		"focus_pinned_by":      "stig",
		"decision_title":       "Pick a datastore",
		"decision_accountable": "stig",
		"decision_readiness":   "blocked on benchmark",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("combined patch status = %d, body %s", rr.Code, rr.Body.String())
	}

	got := patchCockpitCards(t, h, token, "proj")
	if got.PinnedFocus == nil || got.PinnedFocus.Note != "Ship the cockpit" {
		t.Fatalf("pinned_focus = %#v, want the note set", got.PinnedFocus)
	}
	if by := got.PinnedFocus.PinnedBy; by == nil || by.ID != "stig" || by.Name != "Stig Bakken" {
		t.Errorf("pinned_by = %#v, want stig/Stig Bakken", by)
	}
	if got.NextDecision == nil || got.NextDecision.Title != "Pick a datastore" ||
		got.NextDecision.Accountable != "stig" || got.NextDecision.Readiness != "blocked on benchmark" {
		t.Errorf("next_decision = %#v, want populated", got.NextDecision)
	}

	// An empty focus_note clears the pinned-focus card; the decision is
	// untouched because decision_title is absent from this body.
	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj", token, map[string]any{"focus_note": ""})
	if rr.Code != http.StatusOK {
		t.Fatalf("clear focus_note status = %d, body %s", rr.Code, rr.Body.String())
	}
	got = patchCockpitCards(t, h, token, "proj")
	if got.PinnedFocus != nil {
		t.Errorf("pinned_focus = %#v, want nil after clear", got.PinnedFocus)
	}
	if got.NextDecision == nil {
		t.Errorf("next_decision = nil, want it left intact by a focus-only clear")
	}

	// An empty decision_title clears the next-decision card.
	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj", token, map[string]any{"decision_title": ""})
	if rr.Code != http.StatusOK {
		t.Fatalf("clear decision_title status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got = patchCockpitCards(t, h, token, "proj"); got.NextDecision != nil {
		t.Errorf("next_decision = %#v, want nil after clear", got.NextDecision)
	}

	// A body with only a companion field (no focus_note/decision_title trigger)
	// changes nothing and is a clean 422.
	rr = doReq(t, h, "PATCH", "/api/v1/projects/proj", token, map[string]any{"focus_pinned_by": "stig"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("companion-only patch status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	// A missing project 404s through the same path as focus.
	rr = doReq(t, h, "PATCH", "/api/v1/projects/nosuch", token, map[string]any{"focus_note": "x"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing project status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateProjectValidation(t *testing.T) {
	_, h, token := newTestServer(t)
	for name, body := range map[string]map[string]any{
		"missing id":   {"name": "n"},
		"missing name": {"id": "i"},
	} {
		t.Run(name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/projects", token, body)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCreateProjectKeyValidation(t *testing.T) {
	_, h, token := newTestServer(t)
	// missing key
	rr := doReq(t, h, "POST", "/api/v1/projects", token,
		map[string]any{"id": "p1", "name": "P1"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing key status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	// malformed key (lowercase)
	rr = doReq(t, h, "POST", "/api/v1/projects", token,
		map[string]any{"id": "p2", "name": "P2", "key": "wl"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad key status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	// duplicate key -> 409
	rr = doReq(t, h, "POST", "/api/v1/projects", token,
		map[string]any{"id": "p3", "name": "P3", "key": "WL"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("first WL status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/projects", token,
		map[string]any{"id": "p4", "name": "P4", "key": "WL"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate WL status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateActorAndTokenLifecycle(t *testing.T) {
	_, h, token := newTestServer(t)

	rr := doReq(t, h, "POST", "/api/v1/actors", token, map[string]any{
		"id": "bob", "kind": "agent", "display_name": "Bob",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create actor status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr); got["id"] != "bob" || got["kind"] != "agent" {
		t.Fatalf("create actor body = %v", got)
	}

	rr = doReq(t, h, "POST", "/api/v1/actors", token, map[string]any{"id": "x", "kind": "nonsense"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad kind status = %d, want 422", rr.Code)
	}

	rr = doReq(t, h, "POST", "/api/v1/actors/bob/tokens", token, map[string]any{"description": "bob's token"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create token status = %d, body %s", rr.Code, rr.Body.String())
	}
	tok, _ := decodeMap(t, rr)["token"].(string)
	if !strings.HasPrefix(tok, "wl_") {
		t.Fatalf("token = %q, want wl_ prefix", tok)
	}

	rr = doReq(t, h, "POST", "/api/v1/actors/nosuch/tokens", token, map[string]any{})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("token for unknown actor status = %d, want 404", rr.Code)
	}

	// The new token authenticates.
	rr = doReq(t, h, "GET", "/api/v1/tasks", tok, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("auth with new token status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "DELETE", "/api/v1/tokens", token, map[string]any{"token": tok})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks", tok, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("auth with revoked token status = %d, want 401", rr.Code)
	}

	rr = doReq(t, h, "DELETE", "/api/v1/tokens", token, map[string]any{"token": tok})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("revoke already-revoked status = %d, want 404", rr.Code)
	}
}

// TestAdminGatedEndpoints verifies the management endpoints reject non-admin
// tokens with 403 (any bearer token could previously mint an admin token —
// privilege escalation) while admin tokens pass, and that non-gated
// endpoints stay open to non-admins.
func TestAdminGatedEndpoints(t *testing.T) {
	st, h, adminToken := newTestServer(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "worker", "agent", "Worker", false); err != nil {
		t.Fatalf("create non-admin actor: %v", err)
	}
	workerToken, err := st.CreateToken(ctx, "worker", "worker token", nil)
	if err != nil {
		t.Fatalf("create worker token: %v", err)
	}

	gated := []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", "/api/v1/projects", map[string]any{"id": "p2", "name": "P2", "key": "P2"}},
		{"PATCH", "/api/v1/projects/p2", map[string]any{"focus": []string{"security"}}},
		{"POST", "/api/v1/projects/p2/repos", map[string]any{"repo": "acme/other"}},
		{"PATCH", "/api/v1/repos/acme/other", map[string]any{"done_state": "released"}},
		{"POST", "/api/v1/actors", map[string]any{"id": "eve", "kind": "agent"}},
		{"POST", "/api/v1/actors/worker/tokens", map[string]any{}},
		{"DELETE", "/api/v1/tokens", map[string]any{"token": workerToken}},
	}
	for _, ep := range gated {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			rr := doReq(t, h, ep.method, ep.path, workerToken, ep.body)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("non-admin status = %d, want 403; body %s", rr.Code, rr.Body.String())
			}
			if got := decodeMap(t, rr)["error"]; got != "admin required" {
				t.Fatalf("error = %v, want admin required", got)
			}
		})
	}

	// The same calls succeed with the admin token (in gated order: project
	// first so the repo add has a target).
	for _, ep := range gated {
		rr := doReq(t, h, ep.method, ep.path, adminToken, ep.body)
		if rr.Code >= 400 {
			t.Fatalf("admin %s %s status = %d, body %s", ep.method, ep.path, rr.Code, rr.Body.String())
		}
	}

	// Non-admin tokens keep read access and task/inbox/board endpoints.
	// (workerToken was revoked above by the admin DELETE — mint a new one.)
	workerToken2, err := st.CreateToken(ctx, "worker", "worker token 2", nil)
	if err != nil {
		t.Fatalf("create second worker token: %v", err)
	}
	for _, ep := range []struct{ method, path string }{
		{"GET", "/api/v1/projects"},
		{"GET", "/api/v1/tasks"},
		{"GET", "/api/v1/inbox"},
		{"GET", "/api/v1/board"},
	} {
		rr := doReq(t, h, ep.method, ep.path, workerToken2, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("non-admin %s %s status = %d, want 200; body %s", ep.method, ep.path, rr.Code, rr.Body.String())
		}
	}
}

// TestImportRouteRequiresAdmin covers the one admin-gated route
// TestAdminGatedEndpoints cannot fold into its shared gated slice: POST
// /api/v1/inbox/import needs s.appAuth configured to succeed, which
// newTestServer's zero-value Config does not provide, so an admin-token call
// would 503 before ever exercising requireAdmin — breaking that test's
// shared "admin succeeds" loop for every other route in it. This test
// isolates the assertion that actually matters for the admin gate: a
// non-admin token must be rejected with 403 before importInbox ever runs,
// proving requireAdmin is still wired on this route (rather than relying on
// importInbox's own checks to reject it). It goes through the real mux —
// s.auth(requireAdmin(s.importInbox)) — unlike inbox_import_test.go's
// fixtures, which call s.importInbox directly and so never exercise this
// wrapper.
func TestImportRouteRequiresAdmin(t *testing.T) {
	st, h, _ := newTestServer(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "worker", "agent", "Worker", false); err != nil {
		t.Fatalf("create non-admin actor: %v", err)
	}
	workerToken, err := st.CreateToken(ctx, "worker", "worker token", nil)
	if err != nil {
		t.Fatalf("create worker token: %v", err)
	}

	rr := doReq(t, h, "POST", "/api/v1/inbox/import", workerToken, map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["error"]; got != "admin required" {
		t.Fatalf("error = %v, want admin required", got)
	}
}

// TestCreateActorAdminFlag checks the admin flag round-trips through POST
// /api/v1/actors into the store.
func TestCreateActorAdminFlag(t *testing.T) {
	st, h, token := newTestServer(t)
	rr := doReq(t, h, "POST", "/api/v1/actors", token, map[string]any{
		"id": "root", "kind": "human", "display_name": "Root", "admin": true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create admin actor status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["admin"]; got != true {
		t.Fatalf("response admin = %v, want true", got)
	}
	a, err := st.GetActor(context.Background(), "root")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if !a.Admin {
		t.Fatalf("stored actor admin = false, want true")
	}
}

func TestInboxListPromoteDismiss(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	if err := st.AddRepo(context.Background(), "proj", "acme/widgets"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	seedIssue(t, st, "acme/widgets", 1, "Fix the frobnicator")

	rr := doReq(t, h, "GET", "/api/v1/inbox", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list inbox status = %d, body %s", rr.Code, rr.Body.String())
	}
	var listBody struct {
		Issues []struct {
			Repo        string `json:"repo"`
			Number      int64  `json:"number"`
			Title       string `json:"title"`
			TriageState string `json:"triage_state"`
		} `json:"issues"`
	}
	decodeInto(t, rr, &listBody)
	if len(listBody.Issues) != 1 || listBody.Issues[0].TriageState != "new" {
		t.Fatalf("issues = %+v", listBody.Issues)
	}

	rr = doReq(t, h, "GET", "/api/v1/inbox?state=promoted", token, nil)
	decodeInto(t, rr, &listBody)
	if len(listBody.Issues) != 0 {
		t.Fatalf("promoted issues before promote = %+v, want none", listBody.Issues)
	}

	// Promote without a title: defaults to the issue's title.
	rr = doReq(t, h, "POST", "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "high", "kind": "bug",
		"applies_to_versions": []string{"v1.2"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("promote status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["title"] != "Fix the frobnicator" || got["project"] != "proj" || got["priority"] != "high" {
		t.Fatalf("promoted task = %v", got)
	}
	if got["id"] != "WL-1" {
		t.Fatalf("promoted task id = %v, want WL-1", got["id"])
	}

	// A second promote of the same issue fails: no longer 'new'.
	rr = doReq(t, h, "POST", "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "high", "kind": "bug",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("re-promote status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	// Unmapped repo -> 404.
	seedIssue(t, st, "acme/unmapped", 5, "Orphan issue")
	rr = doReq(t, h, "POST", "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/unmapped", "number": 5, "priority": "low", "kind": "chore",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("promote unmapped repo status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}

	// Dismiss a fresh issue.
	seedIssue(t, st, "acme/widgets", 2, "Not worth doing")
	rr = doReq(t, h, "POST", "/api/v1/inbox/dismiss", token, map[string]any{"repo": "acme/widgets", "number": 2})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("dismiss status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/inbox/dismiss", token, map[string]any{"repo": "acme/widgets", "number": 2})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("re-dismiss status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
}

func TestInboxPromoteValidation(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "POST", "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "nonsense", "kind": "bug",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad priority status = %d, want 422", rr.Code)
	}
}

func TestBoard(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Ready one", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Will be blocked", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Claimed", "priority": "medium", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "In review", "priority": "low", "kind": "chore"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Container", "priority": "high", "kind": "epic"})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add blocking edge status = %d", rr.Code)
	}

	// WL-1 becomes a child of the epic, so the board must carry its parent.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-5", "type": "child_of"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add child_of edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-3/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim WL-3 status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-4/claim", token, map[string]any{"worktree": "host:/wt-2"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim WL-4 status = %d, body %s", rr.Code, rr.Body.String())
	}
	moveToReview(t, st, "WL-4")

	rr = doReq(t, h, "GET", "/api/v1/board?project=proj", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board status = %d, body %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Projects []struct {
			ID         string `json:"id"`
			InProgress []struct {
				ID     string `json:"id"`
				Holder *struct {
					ActorID   string `json:"actor_id"`
					ExpiresAt string `json:"expires_at"`
				} `json:"holder"`
			} `json:"in_progress"`
			InReview []struct {
				ID string `json:"id"`
			} `json:"in_review"`
			Ready []struct {
				ID     string `json:"id"`
				Parent string `json:"parent"`
			} `json:"ready"`
			Blocked []struct {
				ID string `json:"id"`
			} `json:"blocked"`
		} `json:"projects"`
		RecentFailures []any `json:"recent_failures"`
	}
	decodeInto(t, rr, &body)
	if len(body.Projects) != 1 {
		t.Fatalf("projects = %+v", body.Projects)
	}
	p := body.Projects[0]
	if len(p.Ready) != 2 || p.Ready[0].ID != "WL-1" || p.Ready[1].ID != "WL-5" {
		t.Fatalf("ready = %+v", p.Ready)
	}
	if p.Ready[0].Parent != "WL-5" {
		t.Fatalf("ready[0] parent = %q, want WL-5", p.Ready[0].Parent)
	}
	if p.Ready[1].Parent != "" {
		t.Fatalf("the epic reported a parent of %q, want none", p.Ready[1].Parent)
	}
	if len(p.Blocked) != 1 || p.Blocked[0].ID != "WL-2" {
		t.Fatalf("blocked = %+v", p.Blocked)
	}
	if len(p.InProgress) != 1 || p.InProgress[0].ID != "WL-3" {
		t.Fatalf("in_progress = %+v", p.InProgress)
	}
	if p.InProgress[0].Holder == nil || p.InProgress[0].Holder.ActorID != "alice" {
		t.Fatalf("in_progress holder = %+v", p.InProgress[0].Holder)
	}
	if len(p.InReview) != 1 || p.InReview[0].ID != "WL-4" {
		t.Fatalf("in_review = %+v", p.InReview)
	}
	// project filter set -> recent_failures omitted (nil, not empty array).
	if body.RecentFailures != nil {
		t.Fatalf("recent_failures with project filter = %v, want omitted", body.RecentFailures)
	}

	// No project filter: recent_failures included (possibly empty).
	rr = doReq(t, h, "GET", "/api/v1/board", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board (all) status = %d, body %s", rr.Code, rr.Body.String())
	}
	var allBody struct {
		Projects       []any `json:"projects"`
		RecentFailures []any `json:"recent_failures"`
	}
	decodeInto(t, rr, &allBody)
	if allBody.RecentFailures == nil {
		t.Fatalf("recent_failures without project filter = nil, want present (possibly empty)")
	}
}

// TestBoardInProgressWithoutLease covers the board's lease-lookup error
// handling: an in_progress task with no active lease (in_review ->
// in_progress via the review flow, no claim) must render with no holder, not
// fail. The other half of that handling — a real DB error surfacing as 500
// via mapStoreErr — is impractical to force through the public API and is
// covered by code-path match with getTask.
func TestBoardInProgressWithoutLease(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Reopened", "priority": "high", "kind": "bug"})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}
	moveToReview(t, st, "WL-1")
	// Review sends it back to in_progress (rework); the original lease was not renewed.
	_, _, err := st.RecordEvent(context.Background(), "github", "rework-WL-1", "task.reworked", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := st.Now()
			if err := store.CloseActiveLease(tx, now, "WL-1"); err != nil {
				return err
			}
			return store.Transition(tx, now, "WL-1", "in_review", "in_progress", eventID)
		})
	if err != nil {
		t.Fatalf("move WL-1 back to in_progress without lease: %v", err)
	}

	rr = doReq(t, h, "GET", "/api/v1/board?project=proj", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board status = %d, body %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Projects []struct {
			InProgress []struct {
				ID     string `json:"id"`
				Holder *struct {
					ActorID string `json:"actor_id"`
				} `json:"holder"`
			} `json:"in_progress"`
		} `json:"projects"`
	}
	decodeInto(t, rr, &body)
	if len(body.Projects) != 1 || len(body.Projects[0].InProgress) != 1 {
		t.Fatalf("board = %+v", body.Projects)
	}
	ip := body.Projects[0].InProgress[0]
	if ip.ID != "WL-1" || ip.Holder != nil {
		t.Fatalf("in_progress = %+v, want WL-1 with no holder", ip)
	}
}

func TestBoardUnknownProject(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/board?project=nosuch", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// TestBoardAcrossProjectsGroupsCorrectly asserts that the org-wide board
// (no project filter) correctly regroups the single, unscoped
// ListProjectWorkFacts read back into each project's own bucket — a task
// never lands under the wrong project, and a project with no tasks still
// gets an entry with empty buckets rather than being dropped.
func TestBoardAcrossProjectsGroupsCorrectly(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proja")
	createProject(t, st, "projb")
	createProject(t, st, "projc") // no tasks at all

	createTaskViaAPI(t, h, token, map[string]any{"project": "proja", "title": "A ready", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "projb", "title": "B ready", "priority": "medium", "kind": "feature"})

	rr := doReq(t, h, "GET", "/api/v1/board", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board status = %d, body %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Projects []struct {
			ID    string `json:"id"`
			Ready []struct {
				ID string `json:"id"`
			} `json:"ready"`
			InProgress []any `json:"in_progress"`
			InReview   []any `json:"in_review"`
			Blocked    []any `json:"blocked"`
		} `json:"projects"`
	}
	decodeInto(t, rr, &body)

	byID := make(map[string]int, len(body.Projects))
	for i, p := range body.Projects {
		byID[p.ID] = i
	}
	for _, id := range []string{"proja", "projb", "projc"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("project %s missing from board; got %+v", id, body.Projects)
		}
	}

	a := body.Projects[byID["proja"]]
	if len(a.Ready) != 1 || a.Ready[0].ID != "PROJA-1" {
		t.Fatalf("proja ready = %+v, want [PROJA-1]", a.Ready)
	}
	b := body.Projects[byID["projb"]]
	if len(b.Ready) != 1 || b.Ready[0].ID != "PROJB-1" {
		t.Fatalf("projb ready = %+v, want [PROJB-1]", b.Ready)
	}
	c := body.Projects[byID["projc"]]
	if len(c.Ready) != 0 || len(c.InProgress) != 0 || len(c.InReview) != 0 || len(c.Blocked) != 0 {
		t.Fatalf("projc (no tasks) = %+v, want every bucket empty", c)
	}
}

func TestGetTaskIncludesLease(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Leased", "priority": "high", "kind": "feature"})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	if got := decodeMap(t, rr)["lease"]; got != nil {
		t.Fatalf("lease before claim = %v, want absent", got)
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	lease, ok := got["lease"].(map[string]any)
	if !ok {
		t.Fatalf("lease after claim missing: %v", got)
	}
	if lease["actor_id"] != "alice" || lease["task_id"] != "WL-1" {
		t.Fatalf("lease = %v", lease)
	}
}

// seedIssue inserts a fresh (triage_state='new') inbox issue directly via
// the store, as a webhook delivery would.
func seedIssue(t *testing.T, st *store.Store, repo string, number int64, title string) {
	t.Helper()
	err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		return store.UpsertIssue(tx, store.Issue{
			Repo: repo, Number: number, Title: title, State: "open",
			URL: "https://github.com/" + repo + "/issues/" + strconv.FormatInt(number, 10),
		})
	})
	if err != nil {
		t.Fatalf("seed issue %s#%d: %v", repo, number, err)
	}
}
