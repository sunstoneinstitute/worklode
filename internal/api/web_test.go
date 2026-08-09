package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// bodyContains fails the test unless every want string appears in body,
// reporting all misses at once.
func bodyContains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q\n--- body ---\n%s", w, body)
		}
	}
}

// assertShell checks the structural markers every page rendered through
// layout.html must carry: the skip link, the one primary nav landmark, the
// one main landmark, and the shared stylesheet — see
// docs/specs/032-project-cockpit.md §10.
func assertShell(t *testing.T, body string) {
	t.Helper()
	for _, want := range []string{
		`<html lang="en">`,
		`href="#main-content"`,
		`<nav aria-label="Primary">`,
		`<main id="main-content"`,
		`href="/assets/app.css"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing shell marker %q", want)
		}
	}
	if got := strings.Count(body, `<main id="main-content"`); got != 1 {
		t.Errorf("main landmark count = %d, want 1", got)
	}
}

// assertOneAriaCurrent checks exactly one nav item (primary or project-local)
// is marked as the current page — the shell's binding accessibility
// constraint: never zero, never two.
func assertOneAriaCurrent(t *testing.T, body string) {
	t.Helper()
	if got := strings.Count(body, `aria-current="page"`); got != 1 {
		t.Errorf(`aria-current="page" count = %d, want 1`, got)
	}
}

// assertOrder checks every string in want appears in body, in that order
// (each search starts after the previous match).
func assertOrder(t *testing.T, body string, want ...string) {
	t.Helper()
	pos := 0
	for _, w := range want {
		idx := strings.Index(body[pos:], w)
		if idx < 0 {
			t.Errorf("body missing %q after position %d, in expected order %v", w, pos, want)
			return
		}
		pos += idx + len(w)
	}
}

// TestGlobalNavOrder checks the primary nav renders the seven destinations
// in the exact order docs/specs/032-project-cockpit.md §2 requires: Home,
// Intake, Projects, Work, Reviews, Deliveries, Knowledge.
func TestGlobalNavOrder(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := doReq(t, h, "GET", "/", "", nil).Body.String()
	assertOrder(t, body, ">Home<", ">Intake<", ">Projects<", ">Work<", ">Reviews<", ">Deliveries<", ">Knowledge<")
}

func TestGlobalDestinations(t *testing.T) {
	_, h, _ := newTestServer(t)

	for _, path := range []string{"/", "/intake", "/projects", "/work", "/reviews", "/deliveries", "/knowledge"} {
		t.Run(path, func(t *testing.T) {
			rr := doReq(t, h, "GET", path, "", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want 200; body %s", path, rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			assertShell(t, body)
			assertOneAriaCurrent(t, body)
		})
	}
}

// TestGlobalPlaceholdersAreHonest checks the four not-yet-implemented global
// destinations name their owning spec and render no form, button, or fake
// state implying the workflow exists.
func TestGlobalPlaceholdersAreHonest(t *testing.T) {
	_, h, _ := newTestServer(t)

	for _, tt := range []struct {
		path string
		want string
	}{
		{"/intake", "spec 032 §5"},
		{"/reviews", "spec 029 §7"},
		{"/deliveries", "spec 029 §3"},
		{"/knowledge", "specs 025"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			rr := doReq(t, h, "GET", tt.path, "", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want 200; body %s", tt.path, rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			bodyContains(t, body, tt.want)
			for _, forbidden := range []string{"<form", "<button"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("%s unexpectedly renders %q:\n%s", tt.path, forbidden, body)
				}
			}
		})
	}
}

func TestAssetsServedWithoutAuth(t *testing.T) {
	_, h, _ := newTestServer(t)

	for _, path := range []string{"/assets/app.css", "/assets/fonts/dm-sans-variable.ttf", "/assets/fonts/source-serif-4-variable.ttf", "/assets/fonts/dm-sans-OFL.txt", "/assets/fonts/source-serif-4-OFL.txt"} {
		rr := doReq(t, h, "GET", path, "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rr.Code)
		}
		if cc := rr.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
			t.Errorf("%s Cache-Control = %q, want bounded public cache", path, cc)
		}
	}
}

func TestAppCSSContent(t *testing.T) {
	_, h, _ := newTestServer(t)

	rr := doReq(t, h, "GET", "/assets/app.css", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("content-type = %q, want text/css", ct)
	}
	css := rr.Body.String()
	for _, want := range []string{
		"#0E1937", "#F4F4F4", "#FAD604", "#266680", "#46C5DE",
		"prefers-color-scheme: dark", ":focus-visible", "min-height: 44px",
		"@media (max-width: 64rem)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet missing %q", want)
		}
	}
}

func TestProjectSections(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	sections := map[string]string{
		"crew":         "spec 029 §6.1",
		"deliverables": "spec 029 §7",
		"reviews":      "spec 029 §7",
		"decisions":    "specs 028 and 029",
		"documents":    "specs 025 and 026",
		"activity":     "ordered event view",
	}
	for section, want := range sections {
		t.Run(section, func(t *testing.T) {
			rr := doReq(t, h, "GET", "/projects/proj/"+section, "", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			assertShell(t, body)
			assertOneAriaCurrent(t, body)
			bodyContains(t, body, "proj", want)
			for _, forbidden := range []string{"<form", "<button"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("section %s unexpectedly renders %q:\n%s", section, forbidden, body)
				}
			}
		})
	}

	rr := doReq(t, h, "GET", "/projects/proj/nosuchsection", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown section status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/projects/nosuch/crew", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown project section status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

func TestProjectPageVariantQueryParamIgnored(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	for _, path := range []string{"/projects/proj", "/projects/proj?variant=A", "/projects/proj?variant=B", "/projects/proj?variant=C"} {
		rr := doReq(t, h, "GET", path, "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200; body %s", path, rr.Code, rr.Body.String())
		}
		bodyContains(t, rr.Body.String(), "operations")
	}
}

func TestHomePage(t *testing.T) {
	_, h, _ := newTestServer(t)

	rr := doReq(t, h, "GET", "/", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	bodyContains(t, body, "<h1>Home</h1>", "Current work")
}

func TestWorkPageOrgBoard(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj1")
	createProject(t, st, "proj2")

	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj1", "title": "Leased task", "priority": "high", "kind": "feature",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim WL-1 status = %d, body %s", rr.Code, rr.Body.String())
	}

	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj1", "title": "Blocker task", "priority": "high", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj1", "title": "Blocked task", "priority": "medium", "kind": "bug",
	})
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-2/edges", token, map[string]any{"to": "WL-3", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add blocking edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	// A human-owned in-progress task: assigned to and started by "dana", an
	// actor distinct from "alice" (the leased task's holder) so the rendered
	// Assignee column can't be mistaken for the Holder column's value.
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj1", "title": "Human-owned task", "priority": "medium", "kind": "feature",
	})
	if err := st.CreateActor(context.Background(), "dana", "human", "Dana", false); err != nil {
		t.Fatalf("create actor dana: %v", err)
	}
	danaToken, err := st.CreateToken(context.Background(), "dana", "test token", nil)
	if err != nil {
		t.Fatalf("create token for dana: %v", err)
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-4/start", danaToken, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start WL-4 status = %d, body %s", rr.Code, rr.Body.String())
	}

	// A runtime failure, recorded directly through the store (as the
	// watcher would via POST /api/v1/runtime-events).
	seedEvent(t, st, "runtime-1", func(tx *sql.Tx, _ int64) error {
		_, err := store.InsertRuntimeEvent(tx, store.RuntimeEvent{
			Cluster: "prod-1", Kind: "crashloop", Workload: "app",
			Message: "CrashLoopBackOff on app", OccurredAt: st.Now(),
		})
		return err
	})

	// A fresh inbox issue.
	if err := st.AddRepo(context.Background(), "proj1", "acme/widgets"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	seedIssue(t, st, "acme/widgets", 1, "An untriaged issue")

	rr = doReq(t, h, "GET", "/work", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("work page status = %d, body %s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	bodyContains(t, body,
		"proj1", "proj2", // project names
		"Leased task", "alice", // in_progress task + holder actor
		"Blocked", "Blocked task", // the blocked bucket + the blocked task's title
		"CrashLoopBackOff on app", // recent-failures message
		"Inbox: 1 new issue",      // inbox count
		"Human-owned task",        // the human-owned in_progress task's title
		"<td>dana</td>",           // its Assignee column cell — proves the column renders the value, not just the header
	)
}

func TestTaskPage(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Add feature", "body": "do the thing", "priority": "high", "kind": "feature",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	const (
		repo     = "org/app"
		mergeSHA = "mergesha1"
	)
	seedEvent(t, st, "pr-open", func(tx *sql.Tx, _ int64) error {
		_, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: 7, Title: "Add feature", State: "open",
			HeadRef: "WL-1-add-feature", HeadSHA: "headsha1",
			URL: "https://github.com/org/app/pull/7", OpenedAt: st.Now(),
		}, "")
		return err
	})
	seedEvent(t, st, "pr-merge", func(tx *sql.Tx, _ int64) error {
		merged := st.Now()
		ms := mergeSHA
		_, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: 7, Title: "Add feature", State: "merged",
			HeadRef: "WL-1-add-feature", HeadSHA: "headsha1", MergeSHA: &ms,
			URL: "https://github.com/org/app/pull/7", OpenedAt: st.Now(), MergedAt: &merged,
		}, "")
		return err
	})
	seedEvent(t, st, "artifact", func(tx *sql.Tx, _ int64) error {
		_, err := store.CreateArtifact(tx, store.Artifact{
			Kind: "docker_image", Name: "reg/app", Version: "1.2.3",
			Repo: repo, SourceSHA: mergeSHA, BuiltAt: st.Now(),
		})
		return err
	})

	rr = doReq(t, h, "GET", "/tasks/WL-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	bodyContains(t, body,
		"WL-1", "Add feature", // id + title
		"in_progress",                // state
		"alice",                      // lease holder
		"do the thing",               // body
		"State change",               // timeline: state entry label
		"Pull request",               // timeline: pr entry label
		"org/app#7",                  // pr entry summary
		"Artifact",                   // timeline: artifact entry label
		"docker_image reg/app 1.2.3", // artifact entry summary
	)
	// WL-1 is leased but has no assignee: the "Assigned to" paragraph must
	// not render for it.
	if strings.Contains(body, "Assigned to") {
		t.Fatalf("unassigned task page unexpectedly shows an assignee:\n%s", body)
	}

	// A second, human-started task: assigned to and started by "erin"
	// without a lease. Holder must stay empty (no "Held by") while Assignee
	// renders — the Holder/Assignee distinction this feature exists to show.
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Human task", "priority": "medium", "kind": "feature",
	})
	if err := st.CreateActor(context.Background(), "erin", "human", "Erin", false); err != nil {
		t.Fatalf("create actor erin: %v", err)
	}
	erinToken, err := st.CreateToken(context.Background(), "erin", "test token", nil)
	if err != nil {
		t.Fatalf("create token for erin: %v", err)
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-2/start", erinToken, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start WL-2 status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", "/tasks/WL-2", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body = rr.Body.String()
	bodyContains(t, body, "Assigned to erin")
	if strings.Contains(body, "Held by") {
		t.Fatalf("human-started task page unexpectedly shows a lease holder:\n%s", body)
	}
	// The auto-assign wrote {"field":"assignee","old":"","new":"erin"}, which
	// summarizeStateChange renders as a "set to" line.
	bodyContains(t, body, "assignee set to erin")

	// Reassigning records the previous assignee, so the timeline renders the
	// old -> new form instead.
	if err := st.CreateActor(context.Background(), "frank", "human", "Frank", false); err != nil {
		t.Fatalf("create actor frank: %v", err)
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-2/assign", token, map[string]any{"assignee": "frank"})
	if rr.Code != http.StatusOK {
		t.Fatalf("reassign WL-2 status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", "/tasks/WL-2", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "assignee: erin -&gt; frank")

	rr = doReq(t, h, "GET", "/tasks/WL-99", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown task page status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

func TestTaskPageShowsProgress(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epic := createEpic(t, h, token, "proj", "Container")
	var childIDs []string
	for _, title := range []string{"A", "B"} {
		child := createTaskViaAPI(t, h, token, map[string]any{
			"project": "proj", "title": title, "priority": "medium", "kind": "feature",
			"parent": epic,
		})
		childIDs = append(childIDs, child["id"].(string))
	}

	rr := doReq(t, h, "GET", "/tasks/"+epic, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "(0/2 closed)", `href="/tasks/`+childIDs[0]+`"`)
}

func TestProjectPage(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	if err := st.AddRepo(context.Background(), "proj", "acme/widgets"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scoped task", "priority": "low", "kind": "chore",
	})

	rr := doReq(t, h, "GET", "/projects/proj", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("project page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	bodyContains(t, body,
		"proj", "acme/widgets", "Scoped task",
		`<link rel="canonical" href="/projects/proj">`, // cockpit projection's canonical url
		"operations",                     // the declared-evidence Operations mode
		"No governed decision is ready.", // the decision rail's Part-1 fallback
	)
	// Project local nav, in the exact order docs/specs/032-project-cockpit.md
	// §2 requires: Overview, Crew, Work, Deliverables, Reviews, Decisions,
	// Documents, Activity.
	assertOrder(t, body, ">Overview<", ">Crew<", ">Work<", ">Deliverables<", ">Reviews<", ">Decisions<", ">Documents<", ">Activity<")
	// The cockpit is a projection, never a stored workflow field: the page
	// must not render any of the retired/forbidden concepts. "Crew" and
	// "Deliverable(s)" are now legitimate project-local nav labels (checked
	// above), so only the concepts that would still be fabricated data stay
	// forbidden here.
	for _, forbidden := range []string{"completion_percentage", "Approval"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("project page unexpectedly renders %q:\n%s", forbidden, body)
		}
	}

	rr = doReq(t, h, "GET", "/projects/nosuch", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown project page status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}
