package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// projectRepos runs `lode project list --json` and returns the repo mappings
// of the project with the given id.
func projectRepos(t *testing.T, id string) []model.RepoMapping {
	t.Helper()
	out, err := runLode(t, "project", "list", "--json")
	if err != nil {
		t.Fatalf("lode project list: %v\noutput: %s", err, out)
	}
	var resp model.ProjectListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	for _, p := range resp.Projects {
		if p.ID == id {
			return p.Repos
		}
	}
	t.Fatalf("project %s not in list output %q", id, out)
	return nil
}

// TestProjectRepoDoneState covers `lode project add-repo --done-state` and
// `lode project set-repo --done-state` end to end against a real server.
func TestProjectRepoDoneState(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	// runLode reuses rootCmd, so a flag value set by one call leaks into a
	// later call that omits it: exercise the omitted-flag case first.
	if out, err := runLode(t, "project", "add-repo", "proj", "acme/widgets"); err != nil {
		t.Fatalf("add-repo: %v\noutput: %s", err, out)
	}
	if out, err := runLode(t, "project", "add-repo", "proj", "acme/docs", "--done-state", "released"); err != nil {
		t.Fatalf("add-repo --done-state: %v\noutput: %s", err, out)
	}

	want := map[string]string{"acme/widgets": "merged", "acme/docs": "released"}
	for _, m := range projectRepos(t, "proj") {
		if want[m.Repo] != m.DoneState {
			t.Fatalf("repo %s done_state = %q, want %q", m.Repo, m.DoneState, want[m.Repo])
		}
		delete(want, m.Repo)
	}
	if len(want) != 0 {
		t.Fatalf("repos missing from project list: %v", want)
	}

	out, err := runLode(t, "project", "set-repo", "acme/widgets", "--done-state", "deployed_prod")
	if err != nil {
		t.Fatalf("set-repo: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "deployed_prod") {
		t.Fatalf("set-repo output = %q, want it to mention deployed_prod", out)
	}
	for _, m := range projectRepos(t, "proj") {
		if m.Repo == "acme/widgets" && m.DoneState != "deployed_prod" {
			t.Fatalf("acme/widgets done_state = %q, want deployed_prod", m.DoneState)
		}
	}

	// The server rejects an unknown state, and the CLI surfaces the failure.
	if out, err := runLode(t, "project", "set-repo", "acme/widgets", "--done-state", "bogus"); err == nil {
		t.Fatalf("set-repo bogus: want error, got nil\noutput: %s", out)
	}
	for _, m := range projectRepos(t, "proj") {
		if m.Repo == "acme/widgets" && m.DoneState != "deployed_prod" {
			t.Fatalf("acme/widgets done_state after rejected set = %q, want deployed_prod", m.DoneState)
		}
	}
}

// TestProjectCuratedCards covers `lode project focus-note` and `lode project
// decision` end to end: setting them lands in the store, --clear removes them,
// and a bare invocation (no value, no --clear) is a usage error.
func TestProjectCuratedCards(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()

	if out, err := runLode(t, "project", "focus-note", "proj", "--note", "Ship the cockpit", "--by", "alice"); err != nil {
		t.Fatalf("focus-note: %v\noutput: %s", err, out)
	}
	if out, err := runLode(t, "project", "decision", "proj",
		"--title", "Pick a datastore", "--accountable", "alice", "--rests-on", "blocked on benchmark"); err != nil {
		t.Fatalf("decision: %v\noutput: %s", err, out)
	}

	p, err := st.GetProject(ctx, "proj")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.FocusNote != "Ship the cockpit" || p.FocusPinnedBy != "alice" {
		t.Errorf("focus = {%q, %q}, want {Ship the cockpit, alice}", p.FocusNote, p.FocusPinnedBy)
	}
	if p.DecisionTitle != "Pick a datastore" || p.DecisionAccountable != "alice" ||
		p.DecisionReadiness != "blocked on benchmark" {
		t.Errorf("decision = {%q, %q, %q}", p.DecisionTitle, p.DecisionAccountable, p.DecisionReadiness)
	}

	// --clear removes each card.
	if out, err := runLode(t, "project", "focus-note", "proj", "--clear"); err != nil {
		t.Fatalf("focus-note --clear: %v\noutput: %s", err, out)
	}
	if out, err := runLode(t, "project", "decision", "proj", "--clear"); err != nil {
		t.Fatalf("decision --clear: %v\noutput: %s", err, out)
	}
	if p, err = st.GetProject(ctx, "proj"); err != nil {
		t.Fatalf("GetProject after clear: %v", err)
	}
	if p.FocusNote != "" || p.DecisionTitle != "" {
		t.Errorf("after clear focus_note=%q decision_title=%q, want both empty", p.FocusNote, p.DecisionTitle)
	}

	// A bare set with neither a value nor --clear is a usage error, not a
	// silent clear.
	if out, err := runLode(t, "project", "focus-note", "proj"); err == nil {
		t.Fatalf("focus-note with no --note/--clear: want error\noutput: %s", out)
	}
	if out, err := runLode(t, "project", "decision", "proj"); err == nil {
		t.Fatalf("decision with no --title/--clear: want error\noutput: %s", out)
	}
}

// --- lode project show ----------------------------------------------------

// detailServer serves GET /api/v1/projects/{id} from a canned body and records
// what it was asked for. The cost half of that endpoint is the API's to
// implement; these tests pin the CLI to the wire contract, not to a store.
type detailServer struct {
	mu    sync.Mutex
	body  string
	id    string
	query url.Values
}

func newDetailServer(t *testing.T, body string) *detailServer {
	t.Helper()
	d := &detailServer{body: body}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.id, d.query = r.PathValue("id"), r.URL.Query()
		body := d.body
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	return d
}

// projectDetailBody is the reference response from the spec: two priced days
// in one currency.
const projectDetailBody = `{
  "id": "worklode", "name": "Worklode", "key": "WL",
  "focus": ["correctness", "throughput"],
  "repos": [{"repo": "sunstoneinstitute/worklode", "done_state": "merged"}],
  "cost": {
    "days": [
      {"day": "2026-07-30", "currency": "USD", "input_tokens": 1234,
       "cache_write_5m_tokens": 40100, "cache_write_1h_tokens": 0,
       "cache_read_tokens": 900300, "output_tokens": 3100,
       "cost_amount": "0.412000", "unpriced_tokens": 0},
      {"day": "2026-07-31", "currency": "USD", "input_tokens": 1951,
       "cache_write_5m_tokens": 0, "cache_write_1h_tokens": 353979,
       "cache_read_tokens": 11779507, "output_tokens": 57641,
       "cost_amount": "10.880324", "unpriced_tokens": 0}
    ],
    "totals": [
      {"currency": "USD", "input_tokens": 3185, "cache_write_5m_tokens": 40100,
       "cache_write_1h_tokens": 353979, "cache_read_tokens": 12679807,
       "output_tokens": 60741, "cost_amount": "11.292324", "unpriced_tokens": 0}
    ]
  }
}`

func TestProjectShowHumanOutput(t *testing.T) {
	d := newDetailServer(t, projectDetailBody)
	setupRepoConfig(t, "worklode")

	out, err := runLode(t, "project", "show")
	if err != nil {
		t.Fatalf("lode project show: %v\noutput: %s", err, out)
	}
	want := "worklode (WL) — Worklode\n" +
		"focus: correctness, throughput\n" +
		"repos:\n" +
		"  sunstoneinstitute/worklode  done: merged\n" +
		"\n" +
		// Amounts render at two places; the stored values keep the micro-unit
		// precision the per-token rates were computed at.
		"cost, last 30 days: 11.29 USD\n" +
		"  2026-07-30  0.41   in 1.2k  cache-w 40.1k   cache-r 900.3k  out 3.1k\n" +
		"  2026-07-31  10.88  in 2.0k  cache-w 354.0k  cache-r 11.8M   out 57.6k\n"
	if out != want {
		t.Fatalf("project show output:\n%s\nwant:\n%s", out, want)
	}
	if d.id != "worklode" {
		t.Fatalf("server saw project id %q, want worklode", d.id)
	}
	// --days defaults to a bounded 30-day window, sent as YYYY-MM-DD.
	if from, to := d.query.Get("from"), d.query.Get("to"); len(from) != 10 || len(to) != 10 {
		t.Fatalf("cost window = from %q to %q, want two YYYY-MM-DD dates", from, to)
	}
}

func TestProjectShowJSONIsTheRawResponse(t *testing.T) {
	newDetailServer(t, projectDetailBody)
	setupRepoConfig(t, "worklode")

	out, err := runLode(t, "project", "show", "--json")
	if err != nil {
		t.Fatalf("lode project show --json: %v\noutput: %s", err, out)
	}
	var got, want map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if err := json.Unmarshal([]byte(projectDetailBody), &want); err != nil {
		t.Fatalf("decode reference body: %v", err)
	}
	if len(got) != len(want) || got["id"] != want["id"] {
		t.Fatalf("--json output = %v, want the server's response verbatim", got)
	}
	if _, ok := got["cost"]; !ok {
		t.Fatalf("--json output dropped the cost block: %v", got)
	}
}

func TestProjectShowWithoutUsage(t *testing.T) {
	newDetailServer(t, `{"id": "worklode", "name": "Worklode", "key": "WL",
	  "focus": [], "repos": [], "cost": {"days": [], "totals": []}}`)
	setupRepoConfig(t, "worklode")

	out, err := runLode(t, "project", "show")
	if err != nil {
		t.Fatalf("lode project show: %v\noutput: %s", err, out)
	}
	want := "worklode (WL) — Worklode\nfocus: (none)\n\ncost, last 30 days: none recorded\n"
	if out != want {
		t.Fatalf("project show output:\n%s\nwant:\n%s", out, want)
	}
}

// A day billed on a model with no price on file understates the headline
// total, so the shortfall is named rather than folded in at zero.
func TestProjectShowNamesUnpricedTokens(t *testing.T) {
	newDetailServer(t, `{"id": "worklode", "name": "Worklode", "key": "WL",
	  "focus": [], "repos": [],
	  "cost": {
	    "days": [{"day": "2026-07-31", "currency": "USD", "input_tokens": 100,
	      "cache_write_5m_tokens": 0, "cache_write_1h_tokens": 0,
	      "cache_read_tokens": 0, "output_tokens": 50,
	      "cost_amount": "0.001000", "unpriced_tokens": 12345}],
	    "totals": [{"currency": "USD", "input_tokens": 100,
	      "cache_write_5m_tokens": 0, "cache_write_1h_tokens": 0,
	      "cache_read_tokens": 0, "output_tokens": 50,
	      "cost_amount": "0.001000", "unpriced_tokens": 12345}]
	  }}`)
	setupRepoConfig(t, "worklode")

	out, err := runLode(t, "project", "show", "--days", "7")
	if err != nil {
		t.Fatalf("lode project show --days 7: %v\noutput: %s", err, out)
	}
	// A tenth of a cent is real spend, so it must not render as "0.00".
	if !strings.Contains(out, "cost, last 7 days: <0.01 USD") {
		t.Fatalf("output = %q, want the --days window in the cost header", out)
	}
	want := "note: 12.3k tokens from models with no price on file are excluded from the total.\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("output = %q, want it to end with %q", out, want)
	}
}

// --project names the project outright, overriding whatever the working
// directory resolves to.
func TestProjectShowProjectFlagOverridesResolution(t *testing.T) {
	d := newDetailServer(t, projectDetailBody)
	setupRepoConfig(t, "worklode")

	if out, err := runLode(t, "project", "show", "--project", "other"); err != nil {
		t.Fatalf("lode project show --project other: %v\noutput: %s", err, out)
	}
	if d.id != "other" {
		t.Fatalf("server saw project id %q, want other", d.id)
	}
}

// With nothing to resolve and no --project, the command explains how to scope
// itself and exits 0 rather than failing.
func TestProjectShowWithNoCurrentProject(t *testing.T) {
	newDetailServer(t, projectDetailBody)
	setupGitRepo(t, "") // no config, no remote

	out, err := runLode(t, "project", "show")
	if err != nil {
		t.Fatalf("lode project show unscoped: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "no current project") || !strings.Contains(out, "--project") {
		t.Fatalf("output = %q, want guidance on naming a project", out)
	}
}
