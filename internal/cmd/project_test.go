package cmd

import (
	"context"
	"encoding/json"
	"io"
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

func TestProjectDoctorRendersReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/doctor" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"repos": [{
				"repo": "acme/app", "project": "demo",
				"app_installed": null,
				"mapped_at": "2026-07-30T00:00:00Z",
				"last_event_at": null, "event_types": [],
				"unapplied_events": 3, "stale": true
			}, {
				"repo": "acme/slow", "project": "demo",
				"app_installed": null, "app_error": "context deadline exceeded",
				"mapped_at": "2026-07-30T00:00:00Z",
				"last_event_at": null, "event_types": [],
				"unapplied_events": 0, "stale": true
			}, {
				"repo": "acme/gone", "project": "demo",
				"app_installed": false, "app_error": "github app is not installed on this repo",
				"mapped_at": "2026-07-30T00:00:00Z",
				"last_event_at": null, "event_types": [],
				"unapplied_events": 0, "stale": true
			}],
			"unmapped_senders": [{"repo": "acme/unmapped", "events": 2, "last_event_at": "2026-07-29T00:00:00Z"}]
		}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")

	out, err := runLode(t, "project", "doctor")
	if err != nil {
		t.Fatalf("project doctor: %v\n%s", err, out)
	}
	// A null app_installed renders as unchecked either way, but the reason
	// separates "no App configured" from "the check did not finish"; only a
	// false one may read as NOT INSTALLED.
	for _, want := range []string{
		"acme/app", "STALE", "acme/unmapped",
		"unchecked (no GitHub App configured)",
		"unchecked (context deadline exceeded)",
		"NOT INSTALLED (github app is not installed on this repo)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	out, err = runLode(t, "project", "doctor", "--json")
	if err != nil {
		t.Fatalf("project doctor --json: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"stale": true`) && !strings.Contains(out, `"stale":true`) {
		t.Fatalf("--json output does not round-trip stale:\n%s", out)
	}
}

// TestProjectCrewAdd covers `lode project crew add` end to end against a real
// server: the default role, an explicit role, the lead flag, and a refusal
// surfacing as a CLI error.
func TestProjectCrewAdd(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	for _, id := range []string{"ada", "bob"} {
		if err := st.CreateActor(ctx, id, "human", strings.ToUpper(id[:1])+id[1:], false); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}

	// runLode reuses rootCmd, so a flag set by one call leaks into a later
	// call that omits it: exercise the omitted-flag case first.
	out, err := runLode(t, "project", "crew", "add", "proj", "bob")
	if err != nil {
		t.Fatalf("crew add: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "added bob to project proj as member") {
		t.Fatalf("crew add output = %q, want it to name the default role", out)
	}

	out, err = runLode(t, "project", "crew", "add", "proj", "ada", "--role", "editor", "--lead")
	if err != nil {
		t.Fatalf("crew add --role --lead: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "as editor") || !strings.Contains(out, "ada is the project lead") {
		t.Fatalf("crew add output = %q, want the role and the lead line", out)
	}

	// --json prints the server's own body, with every role the member holds.
	out, err = runLode(t, "project", "crew", "add", "proj", "ada", "--role", "reporter", "--json")
	if err != nil {
		t.Fatalf("crew add --json: %v\noutput: %s", err, out)
	}
	var member model.CrewMember
	if err := json.Unmarshal([]byte(out), &member); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if member.Actor != "ada" || !member.Lead || strings.Join(member.Roles, ",") != "editor,reporter" {
		t.Fatalf("member = %+v, want ada, lead, [editor reporter]", member)
	}

	// A refused add is an error the CLI surfaces, not a silent success.
	if out, err := runLode(t, "project", "crew", "add", "proj", "bob", "--role", "member"); err == nil {
		t.Fatalf("duplicate role: want an error, got nil\noutput: %s", out)
	}
	if out, err := runLode(t, "project", "crew", "add", "proj", "nosuch"); err == nil {
		t.Fatalf("unknown actor: want an error, got nil\noutput: %s", out)
	}

	crew, err := st.ListParticipants(ctx, "proj")
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(crew) != 2 {
		t.Fatalf("crew = %+v, want 2 members", crew)
	}
}

// TestProjectCrewList covers `lode project crew <project>` (no subcommand):
// the roster renders as a table (name, roles comma-joined, a lead marker),
// --json prints the server's envelope verbatim, and an empty roster still
// prints the header row rather than erroring.
func TestProjectCrewList(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	for _, id := range []string{"ada", "bob"} {
		if err := st.CreateActor(ctx, id, "human", strings.ToUpper(id[:1])+id[1:], false); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
	if out, err := runLode(t, "project", "crew", "add", "proj", "ada", "--role", "editor", "--lead"); err != nil {
		t.Fatalf("seed lead: %v\noutput: %s", err, out)
	}
	if out, err := runLode(t, "project", "crew", "add", "proj", "bob", "--role", "reporter"); err != nil {
		t.Fatalf("seed member: %v\noutput: %s", err, out)
	}

	out, err := runLode(t, "project", "crew", "proj")
	if err != nil {
		t.Fatalf("crew list: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "ada") || !strings.Contains(out, "editor") || !strings.Contains(out, "lead") {
		t.Fatalf("crew list output = %q, want ada/editor/lead", out)
	}
	if !strings.Contains(out, "bob") || !strings.Contains(out, "reporter") {
		t.Fatalf("crew list output = %q, want bob/reporter", out)
	}

	out, err = runLode(t, "project", "crew", "proj", "--json")
	if err != nil {
		t.Fatalf("crew list --json: %v\noutput: %s", err, out)
	}
	var resp model.ParticipantListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if len(resp.Participants) != 2 {
		t.Fatalf("participants = %+v, want 2", resp.Participants)
	}
}

// TestProjectCrewDispatch checks the parent `crew` command's own RunE (the
// listing form) does not swallow the `add`/`remove` subcommands: cobra must
// still dispatch to them when the first argument names one, which is the one
// thing giving the parent its own RunE risks silently breaking.
func TestProjectCrewDispatch(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "ada", "human", "Ada", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}

	if out, err := runLode(t, "project", "crew", "add", "proj", "ada"); err != nil {
		t.Fatalf("crew add: %v\noutput: %s", err, out)
	} else if !strings.Contains(out, "added ada to project proj") {
		t.Fatalf("crew add output = %q, want the add subcommand's own message, not a listing", out)
	}

	crew, err := st.ListParticipants(ctx, "proj")
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(crew) != 1 {
		t.Fatalf("crew = %+v, want ada added by the subcommand", crew)
	}

	if out, err := runLode(t, "project", "crew", "remove", "proj", "ada"); err != nil {
		t.Fatalf("crew remove: %v\noutput: %s", err, out)
	} else if !strings.Contains(out, "removed ada from project proj") {
		t.Fatalf("crew remove output = %q, want the remove subcommand's own message, not a listing", out)
	}

	crew, err = st.ListParticipants(ctx, "proj")
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(crew) != 0 {
		t.Fatalf("crew = %+v, want ada removed by the subcommand", crew)
	}
}

// TestProjectCrewRemove covers `lode project crew remove` end to end: the
// open-work guard's item list reaches the terminal verbatim, the lead cannot
// be removed, and a clean removal drops every role the member held.
func TestProjectCrewRemove(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	for _, id := range []string{"ada", "bob"} {
		if err := st.CreateActor(ctx, id, "human", strings.ToUpper(id[:1])+id[1:], false); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
	if out, err := runLode(t, "project", "crew", "add", "proj", "ada", "--role", "editor", "--lead"); err != nil {
		t.Fatalf("seed lead: %v\noutput: %s", err, out)
	}
	if out, err := runLode(t, "project", "crew", "add", "proj", "bob", "--role", "reporter"); err != nil {
		t.Fatalf("seed member: %v\noutput: %s", err, out)
	}

	task, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "proj", Title: "bob's open task", Body: "b",
		Priority: "medium", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, _, err := c.AssignTask(ctx, task.ID, "bob"); err != nil {
		t.Fatalf("assign task: %v", err)
	}

	// The guard's item list is what the person has to act on, so it reaches
	// the terminal exactly as the server wrote it.
	out, err := runLode(t, "project", "crew", "remove", "proj", "bob")
	if err == nil {
		t.Fatalf("open work: want an error, got nil\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), task.ID+" (task, ready)") {
		t.Fatalf("error = %v, want it to name %s (task, ready)", err, task.ID)
	}

	// The lead cannot go while handoff is unimplemented.
	if out, err := runLode(t, "project", "crew", "remove", "proj", "ada"); err == nil {
		t.Fatalf("lead removal: want an error, got nil\noutput: %s", out)
	} else if !strings.Contains(err.Error(), "lead handoff is not implemented") {
		t.Fatalf("lead removal error = %v, want it to say why", err)
	}

	if _, _, err := c.UnassignTask(ctx, task.ID); err != nil {
		t.Fatalf("unassign task: %v", err)
	}
	out, err = runLode(t, "project", "crew", "remove", "proj", "bob")
	if err != nil {
		t.Fatalf("crew remove: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "removed bob from project proj") {
		t.Fatalf("output = %q, want it to name the member and the project", out)
	}

	crew, err := st.ListParticipants(ctx, "proj")
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(crew) != 1 || crew[0].ActorID != "ada" {
		t.Fatalf("crew = %+v, want only ada", crew)
	}

	// Removing them again is an error, not a silent success.
	if out, err := runLode(t, "project", "crew", "remove", "proj", "bob"); err == nil {
		t.Fatalf("second removal: want an error, got nil\noutput: %s", out)
	}

	// --json is honored like the sibling 204-returning set-repo command: the
	// server's empty body means printRaw writes nothing, not the prose message.
	if out, err := runLode(t, "project", "crew", "add", "proj", "bob", "--role", "reporter"); err != nil {
		t.Fatalf("re-seed bob: %v\noutput: %s", err, out)
	}
	out, err = runLode(t, "project", "crew", "remove", "proj", "bob", "--json")
	if err != nil {
		t.Fatalf("crew remove --json: %v\noutput: %s", err, out)
	}
	if out != "" {
		t.Fatalf("crew remove --json output = %q, want empty (204 has no body)", out)
	}
}
