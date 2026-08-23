package api_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
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

// assertShell checks the structural markers every page rendered through the
// Page shell component (layout.templ) must carry: the skip link, the
// two-column shell frame, the one main landmark, and the shared stylesheet —
// see docs/specs/032-project-cockpit.md §10.
func assertShell(t *testing.T, body string) {
	t.Helper()
	for _, want := range []string{
		`<html lang="en">`,
		`href="#main-content"`,
		`class="shell"`,
		`<main id="main-content"`,
		`href="/assets/app.css?v=`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing shell marker %q", want)
		}
	}
	if got := strings.Count(body, `<main id="main-content"`); got != 1 {
		t.Errorf("main landmark count = %d, want 1", got)
	}
}

// mainContent returns the page's <main id="main-content"> region — the page's
// own content, excluding the shared shell chrome (top bar, brand, theme
// toggle, avatar). Honest-placeholder checks scope their "no fabricated
// affordance" assertions to this region: the shell legitimately carries a
// theme-toggle <button>, but a placeholder's content must still render no
// form or button that would imply an unbuilt workflow exists.
func mainContent(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<main id="main-content"`)
	if i < 0 {
		t.Fatalf("body has no <main id=\"main-content\"> region:\n%s", body)
	}
	rest := body[i:]
	j := strings.Index(rest, "</main>")
	if j < 0 {
		t.Fatalf("body has no closing </main>:\n%s", body)
	}
	return rest[:j]
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

// navMarkers turns nav landmark labels into their opening-tag markers, for an
// ordered assertion.
func navMarkers(labels []string) []string {
	markers := make([]string, len(labels))
	for i, l := range labels {
		markers[i] = `<nav aria-label="` + l + `"`
	}
	return markers
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

// topbarRegion returns the <header class="topbar"> element's markup.
func topbarRegion(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<header class="topbar">`)
	if i < 0 {
		t.Fatalf("body has no topbar:\n%s", body)
	}
	rest := body[i:]
	j := strings.Index(rest, "</header>")
	if j < 0 {
		t.Fatalf("topbar not closed:\n%s", body)
	}
	return rest[:j]
}

// TestGlobalNavFollowsTopbar checks the global destinations render once in a
// second header row rather than in the page's sidebar. The topbar itself keeps
// only its brand and controls.
func TestGlobalNavFollowsTopbar(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := doReq(t, h, "GET", "/", "", nil).Body.String()
	header := topbarRegion(t, body)
	for _, want := range []string{`class="brand"`, `id="theme"`, `class="avatar"`} {
		if !strings.Contains(header, want) {
			t.Errorf("topbar missing %q", want)
		}
	}
	if strings.Contains(header, "<nav") || strings.Contains(header, "<a ") {
		t.Errorf("topbar still carries navigation:\n%s", header)
	}
	assertOrder(t, body, `</header>`, `<nav aria-label="Primary"`, ">Home<", ">Knowledge<", `<div class="shell" data-global="true">`, `<main id="main-content"`)
	if got := strings.Count(body, `<nav aria-label="Primary"`); got != 1 {
		t.Errorf("primary navigation count = %d, want 1", got)
	}
}

// TestGlobalNavOrder checks the primary nav renders the eight destinations
// in the exact order docs/specs/032-project-cockpit.md §2 requires: Home,
// Ideas, Intake, Projects, Work, Reviews, Deliveries, Knowledge.
func TestGlobalNavOrder(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := doReq(t, h, "GET", "/", "", nil).Body.String()
	assertOrder(t, body, ">Home<", ">Ideas<", ">Intake<", ">Projects<", ">Work<", ">Reviews<", ">Deliveries<", ">Knowledge<")
}

func TestGlobalDestinations(t *testing.T) {
	_, h, _ := newTestServer(t)

	// Knowledge lands on /docs, the document corpus (spec 032 §2's
	// "documents and graph-backed expert views"); /knowledge redirects there.
	for _, path := range []string{"/", "/ideas", "/intake", "/projects", "/work", "/reviews", "/deliveries", "/docs"} {
		t.Run(path, func(t *testing.T) {
			rr := doReq(t, h, "GET", path, "", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want 200; body %s", path, rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			assertShell(t, body)
			assertOneAriaCurrent(t, body)
			bodyContains(t, body, `<nav aria-label="Primary"`)
		})
	}
}

// TestKnowledgeIsTheDocumentCorpus checks the Knowledge destination is the
// document corpus rather than a placeholder: the primary nav links to /docs,
// /docs marks Knowledge as the current page, and the spec's own spelling of
// the URL still resolves.
func TestKnowledgeIsTheDocumentCorpus(t *testing.T) {
	_, h, _ := newTestServer(t)

	body := doReq(t, h, "GET", "/", "", nil).Body.String()
	bodyContains(t, body, `href="/docs"`)
	if strings.Contains(body, `href="/knowledge"`) {
		t.Error("primary nav still links Knowledge at the placeholder URL")
	}

	docs := doReq(t, h, "GET", "/docs", "", nil).Body.String()
	assertOneAriaCurrent(t, docs)
	bodyContains(t, docs, `<a href="/docs" class="tab-secondary active" aria-current="page">`, `<span class="tab-label">Knowledge</span>`)

	rr := doReq(t, h, "GET", "/knowledge", "", nil)
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/docs" {
		t.Errorf("GET /knowledge = %d %q, want 302 to /docs", rr.Code, rr.Header().Get("Location"))
	}
}

// TestGlobalPlaceholdersAreHonest checks the not-yet-implemented global
// destinations name their owning spec and render no form, button, or fake
// state implying the workflow exists.
func TestGlobalPlaceholdersAreHonest(t *testing.T) {
	_, h, _ := newTestServer(t)

	for _, tt := range []struct {
		path string
		want string
	}{
		{"/ideas", "spec 032 §5"},
		{"/intake", "spec 032 §5"},
		{"/deliveries", "spec 029 §3"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			rr := doReq(t, h, "GET", tt.path, "", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want 200; body %s", tt.path, rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			bodyContains(t, body, tt.want)
			main := mainContent(t, body)
			for _, forbidden := range []string{"<form", "<button"} {
				if strings.Contains(main, forbidden) {
					t.Fatalf("%s unexpectedly renders %q in its main content:\n%s", tt.path, forbidden, body)
				}
			}
		})
	}
}

// TestReviewsPageListsAwaitingApprovals checks the Reviews destination is
// the real awaiting-approvals queue (spec 029 §7.1), not a placeholder: a
// seeded PR-kind approval shows its title and entity id, and the page still
// carries exactly one aria-current="page" marker.
func TestReviewsPageListsAwaitingApprovals(t *testing.T) {
	st, h, _ := newTestServer(t)
	seedAwaitingPRApproval(t, st, "acme/site#7", "Fix the widget")

	rr := doReq(t, h, "GET", "/reviews", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("reviews page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	bodyContains(t, body, "Fix the widget", "acme/site#7", `aria-current="page"`)
	assertOneAriaCurrent(t, body)
}

// TestReviewsPageEmptyIsHonest checks an empty queue states that honestly
// rather than showing a fabricated record.
func TestReviewsPageEmptyIsHonest(t *testing.T) {
	_, h, _ := newTestServer(t)

	body := doReq(t, h, "GET", "/reviews", "", nil).Body.String()
	bodyContains(t, body, "No approvals are waiting.")
}

// TestShellReferencesHTMX asserts the shell references the self-hosted,
// dormant HTMX asset — no CDN, no hx-* behavior (that's spec 032 §11) — and
// that /assets/htmx.min.js is served unauthenticated like the other assets.
func TestShellReferencesHTMX(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := doReq(t, h, "GET", "/", "", nil).Body.String()
	if !strings.Contains(body, `src="/assets/htmx.min.js?v=`) {
		t.Error("shell does not reference self-hosted HTMX")
	}
	rr := doReq(t, h, "GET", "/assets/htmx.min.js", "", nil)
	if rr.Code != 200 {
		t.Errorf("GET /assets/htmx.min.js = %d, want 200 (no auth redirect)", rr.Code)
	}
}

// TestShellReferencesThemeToggle asserts the shell wires the self-hosted
// theme toggle: it renders the #theme button, references /assets/theme.js, and
// that script is served unauthenticated like the other assets.
func TestShellReferencesThemeToggle(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := doReq(t, h, "GET", "/", "", nil).Body.String()
	if !strings.Contains(body, `id="theme"`) {
		t.Error("shell does not render the theme-toggle button")
	}
	if !strings.Contains(body, `src="/assets/theme.js?v=`) {
		t.Error("shell does not reference the self-hosted theme-toggle script")
	}
	rr := doReq(t, h, "GET", "/assets/theme.js", "", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("GET /assets/theme.js = %d, want 200 (no auth redirect)", rr.Code)
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

func TestShellFingerprintsStylesheet(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := doReq(t, h, "GET", "/", "", nil).Body.String()
	const prefix = `href="/assets/app.css?v=`
	start := strings.Index(body, prefix)
	if start < 0 {
		t.Fatalf("shell has no fingerprinted stylesheet URL: %s", body)
	}
	version := body[start+len(prefix):]
	version = version[:strings.IndexByte(version, '"')]

	rr := doReq(t, h, "GET", "/assets/app.css?v="+version, "", nil)
	sum := sha256.Sum256(rr.Body.Bytes())
	if want := hex.EncodeToString(sum[:6]); version != want {
		t.Fatalf("stylesheet version = %q, want content fingerprint %q", version, want)
	}
}

// TestTailwindSourceNotServed asserts the Tailwind build source
// (internal/ui/styles/app.tailwind.css) is not reachable under /assets/ —
// it lives outside the embedded, served tree so un-minified build source is
// never exposed; only the generated internal/ui/assets/app.css is served.
func TestTailwindSourceNotServed(t *testing.T) {
	_, h, _ := newTestServer(t)

	rr := doReq(t, h, "GET", "/assets/app.tailwind.css", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestAppCSSContent checks the served stylesheet — built from internal/ui's
// design-system source (ported verbatim from the cockpit design prototype,
// docs/mockups/cockpit/index.html) — carries the brand palette, both the
// light and dark token blocks, and a sample of the shell/component rules.
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
		"prefers-color-scheme:dark", ":focus-visible", "--ink:", "--accent:",
		".topbar", "@media (max-width:1080px)",
		"max-width:880px", ".backlink",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet missing %q", want)
		}
	}
}

// TestEveryPageRendersTheShell sweeps every web page and asserts the
// unified frame: exactly one .shell grid, one main landmark, the page's exact
// set of nav landmarks in render order followed by the main landmark
// (Primary alone on global pages, Primary then Project on project pages,
// content always after both — WL-51's WCAG 2.4.1 fix, pinned per page rather
// than only on Home), no nav landmark nested inside main content, and — on
// every page that names a current destination — exactly one
// aria-current="page". The task page and the new-task form name none (their
// left column marks nothing), so they assert zero.
func TestEveryPageRendersTheShell(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Swept task", "priority": "low", "kind": "chore",
	})

	global := []string{"Primary"}
	project := []string{"Primary", "Project"}
	pages := []struct {
		path       string
		navs       []string // every nav landmark's aria-label, in render order
		hasCurrent bool
	}{
		{"/", global, true}, {"/intake", global, true},
		{"/projects", global, true}, {"/work", global, true},
		{"/reviews", global, true}, {"/deliveries", global, true},
		{"/docs", global, true},
		{"/projects/proj", project, true}, {"/projects/proj/crew", project, true},
		{"/projects/proj/reviews", project, true}, {"/projects/proj/decisions", project, true},
		{"/projects/proj/documents", project, true}, {"/projects/proj/activity", project, true},
		{"/projects/proj/deliverables", project, true},
		{"/projects/proj/deliverables/new", project, true},
		{"/projects/proj/tasks/new", project, false},
		{"/tasks/WL-1", global, false},
	}
	for _, page := range pages {
		t.Run(page.path, func(t *testing.T) {
			rr := doReq(t, h, "GET", page.path, "", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			if got := strings.Count(body, `<div class="shell"`); got != 1 {
				t.Errorf("shell count = %d, want 1", got)
			}
			if got := strings.Count(body, `<main id="main-content"`); got != 1 {
				t.Errorf("main landmark count = %d, want 1", got)
			}
			if got := strings.Count(body, "<nav aria-label="); got != len(page.navs) {
				t.Errorf("nav landmark count = %d, want %d (%v)", got, len(page.navs), page.navs)
			}
			for _, label := range page.navs {
				bodyContains(t, body, `<nav aria-label="`+label+`"`)
			}
			assertOrder(t, body, append(navMarkers(page.navs), `<main id="main-content"`)...)
			want := 0
			if page.hasCurrent {
				want = 1
			}
			if got := strings.Count(body, `aria-current="page"`); got != want {
				t.Errorf(`aria-current="page" count = %d, want %d`, got, want)
			}
			if main := mainContent(t, body); strings.Contains(main, "<nav") {
				t.Errorf("main content still contains a nav landmark:\n%s", main)
			}
		})
	}
}

func TestProjectSections(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	// Deliverables and Crew are both absent on purpose: they are built
	// destinations now, with their own pages (see webform_test.go and
	// TestCrewPage below).
	sections := map[string]string{
		"reviews":   "spec 029 §7",
		"decisions": "specs 025 and 029",
		"documents": "specs 025 and 026",
		"activity":  "ordered event view",
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
			main := mainContent(t, body)
			for _, forbidden := range []string{"<form", "<button"} {
				if strings.Contains(main, forbidden) {
					t.Fatalf("section %s unexpectedly renders %q in its main content:\n%s", section, forbidden, body)
				}
			}
		})
	}

	rr := doReq(t, h, "GET", "/projects/proj/nosuchsection", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown section status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/projects/nosuch/reviews", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown project section status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestCrewPage checks the Crew destination: an empty roster renders the
// honest "No Crew yet" state with no fabricated record, a populated roster
// shows every member with their role labels and exactly one Lead badge
// (032 §6), and an unknown project 404s the same way every other project
// route does. The roster is seeded through the real write path, so what the
// page shows is what a Crew add actually stores.
func TestCrewPage(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/projects/proj/crew", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	bodyContains(t, body, "No Crew yet")
	if strings.Contains(body, "Crew arrives with project participants") {
		t.Error("the crew destination still renders its old placeholder message")
	}
	// The add-member form is the page's one write affordance (029 §6.1).
	bodyContains(t, mainContent(t, body), `<form method="post" action="/projects/proj/crew">`, `name="actor"`, `name="role"`, `name="lead"`)

	seedCrewActors(t, st, "ada", "bob")
	for _, add := range []map[string]any{
		{"actor": "ada", "role": "editor", "lead": true},
		{"actor": "bob", "role": "reporter"},
		{"actor": "bob", "role": "data-scientist"},
	} {
		if rr := doReq(t, h, "POST", "/api/v1/projects/proj/participants", token, add); rr.Code != http.StatusCreated {
			t.Fatalf("seed %v status = %d; body %s", add, rr.Code, rr.Body.String())
		}
	}

	rr = doReq(t, h, "GET", "/projects/proj/crew", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("populated roster status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body = rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	main := mainContent(t, body)
	if strings.Contains(main, "No Crew yet") {
		t.Error("a populated roster still renders the empty state")
	}
	// Both members, by display name and id, with their role labels. bob's
	// two roles are folded into one row, sorted.
	bodyContains(t, main, "Ada Person", "ada", "editor",
		"Bob Person", "bob", "data-scientist, reporter")
	// Exactly one Lead badge: 032 §6's accountable human is one person.
	if n := strings.Count(main, ">Lead</span>"); n != 1 {
		t.Fatalf("Lead badges = %d, want exactly 1:\n%s", n, main)
	}

	if rr := doReq(t, h, "GET", "/projects/nosuch/crew", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", rr.Code)
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
		// The mode is a pure projection of declared facts: every ?variant
		// still renders the Operations (mode B) canvas, never a mode the
		// query asked for.
		bodyContains(t, rr.Body.String(), `data-panel="B"`, "Operations")
	}
}

// TestHomePage covers Home's open-mode degradation (constraints: no actor,
// LODE_WEB_OPEN): every project, newest activity first, and never a role
// badge or a signal line — those claim a relationship an anonymous viewer
// does not have.
func TestHomePage(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj1")
	createProject(t, st, "proj2")

	// A driven clock, because task timestamps are truncated to the second:
	// two tasks created in the same wall-clock second would tie, and the tie
	// break is project id ascending — which is the order this test is about
	// proving Home does not use.
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	st.SetNowFunc(func() time.Time { return now })

	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj1", "title": "Older work", "priority": "medium", "kind": "feature",
	})
	now = now.Add(time.Hour)
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj2", "title": "Newer work", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	bodyContains(t, body, "<h1>Home</h1>")
	if strings.Contains(body, "Current work") {
		t.Errorf("open-mode Home still renders the retired board framing %q", "Current work")
	}
	assertOrder(t, body, `href="/projects/proj2"`, `href="/projects/proj1"`)

	main := mainContent(t, body)
	for _, unwanted := range []string{"You lead", "awaiting you", "You are on this project", ">Lead<", ">Member<"} {
		if strings.Contains(main, unwanted) {
			t.Errorf("open-mode Home claims a viewer relationship it cannot know: %q\n%s", unwanted, main)
		}
	}
}

// TestHomePageActorTiers drives Home as a logged-in person and pins the tier
// order from the plan's global constraints: approvals awaiting you, then
// projects you lead, then projects you are on. The seeded activity runs the
// other way round (pawait is the oldest, pmember the newest), so a page that
// sorted by activity alone would render the exact reverse.
func TestHomePageActorTiers(t *testing.T) {
	st, h, iss := newOIDCServer(t, api.Config{})
	token := seedActor(t, st, "alice", "human", "Alice", true)

	createProject(t, st, "pawait")
	createProject(t, st, "plead")
	createProject(t, st, "pmember")

	// Driven clock: task timestamps truncate to the second, so activity has
	// to be spaced deliberately for "tier beats recency" to mean anything.
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	st.SetNowFunc(func() time.Time { return now })

	awaitTask := createTaskViaAPI(t, h, token, map[string]any{
		"project": "pawait", "title": "Awaiting work", "priority": "medium", "kind": "feature",
	})["id"].(string)

	// Seeded before the other two projects' tasks so pawait stays the least
	// recently active project whatever the PR upsert touches.
	seedEvent(t, st, "home-approval", func(tx *sql.Tx, _ int64) error {
		if _, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: "acme/widgets", Number: 7, Title: "Awaiting PR", State: "open",
			HeadRef: awaitTask + "-awaiting", HeadSHA: "shaawait",
			URL: "https://github.com/acme/widgets/pull/7", OpenedAt: st.Now(),
		}, ""); err != nil {
			return err
		}
		role := "science-leads"
		return store.InsertAwaitingApproval(tx, st.Now(), "pr",
			store.PREntityID("acme/widgets", 7), "shaawait", &role, nil)
	})

	now = now.Add(time.Hour)
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "plead", "title": "Lead work", "priority": "medium", "kind": "feature",
	})
	now = now.Add(time.Hour)
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "pmember", "title": "Member work", "priority": "medium", "kind": "feature",
	})

	// The groups claim is what reaches the approval's required_role: the
	// awaiting tier is earned by the group, not by being named personally.
	iss.TokenClaims = map[string]any{
		"preferred_username": "grace", "name": "Grace", "aud": iss.ClientID,
		"groups": []string{"user", "science-leads"},
	}
	session := webLogin(t, h, "grace")

	// Crew rows come after login: the actor row is minted by the callback.
	seedEvent(t, st, "home-crew", func(tx *sql.Tx, eventID int64) error {
		if err := store.AddParticipant(tx, st.Now(), "plead", "grace", "engineer", true, "alice", eventID); err != nil {
			return err
		}
		return store.AddParticipant(tx, st.Now(), "pmember", "grace", "engineer", false, "alice", eventID)
	})

	rr := withSession(t, h, "GET", "/", session, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	bodyContains(t, body, "<h1>Home</h1>")

	main := mainContent(t, body)
	assertOrder(t, main,
		`href="/projects/pawait"`, "1 approval awaiting you",
		`href="/projects/plead"`, "You lead this project",
		`href="/projects/pmember"`, "You are on this project",
	)
	// One Lead badge and one Member badge: the awaiting card is a
	// non-member's, so it carries no role at all.
	if got := strings.Count(main, ">Lead<"); got != 1 {
		t.Errorf("Lead badge count = %d, want 1\n%s", got, main)
	}
	if got := strings.Count(main, ">Member<"); got != 1 {
		t.Errorf("Member badge count = %d, want 1\n%s", got, main)
	}
}

// TestHomePageEmptyState covers the second required degradation: a signed-in
// actor on no project, with nothing awaiting them, is told so and pointed at
// the portfolio — never given a card for a project they are not on.
func TestHomePageEmptyState(t *testing.T) {
	st, h, iss := newOIDCServer(t, api.Config{})
	token := seedActor(t, st, "alice", "human", "Alice", true)
	createProject(t, st, "proj1")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj1", "title": "Somebody else's work", "priority": "medium", "kind": "feature",
	})

	iss.TokenClaims = map[string]any{
		"preferred_username": "grace", "name": "Grace", "aud": iss.ClientID,
		"groups": []string{"user"},
	}
	session := webLogin(t, h, "grace")

	rr := withSession(t, h, "GET", "/", session, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)

	main := mainContent(t, body)
	bodyContains(t, main, "You are not on any project yet.", `href="/projects"`, "Browse all projects")
	if strings.Contains(main, "homecard") {
		t.Errorf("empty state renders a project card:\n%s", main)
	}
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
		"Assignee dana",           // its assignee, rendered on the row — proves the value renders, distinct from the holder
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
		_, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: 7, Title: "Add feature", State: "open",
			HeadRef: "WL-1-add-feature", HeadSHA: "headsha1",
			URL: "https://github.com/org/app/pull/7", OpenedAt: st.Now(),
		}, "")
		return err
	})
	seedEvent(t, st, "pr-merge", func(tx *sql.Tx, _ int64) error {
		merged := st.Now()
		ms := mergeSHA
		_, _, err := store.UpsertPR(tx, store.PullRequest{
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
	assertShell(t, body)
	bodyContains(t, body, `<nav aria-label="Primary"`)
	if got := strings.Count(body, `aria-current="page"`); got != 0 {
		t.Errorf(`aria-current count = %d, want 0 (no destination is current on a task page)`, got)
	}
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

// TestTaskPageTimelineTypeIsAnAccessibleIcon asserts the Timeline "Type"
// column (WL-298) renders each row's type as an icon, not the old plain-text
// label, while keeping that label reachable two ways: the native title
// tooltip for desktop hover, and an aria-label — on an element carrying
// role="img" so assistive tech announces the label instead of trying to
// describe the SVG — for touch and screen readers, since a title attribute
// alone is not reliably exposed on touch.
func TestTaskPageTimelineTypeIsAnAccessibleIcon(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Add feature", "body": "do the thing", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/tasks/WL-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()

	// The task's own creation writes a "state" timeline entry, so a fresh
	// task's page already carries one icon to check without seeding a PR,
	// CI run, or other fact.
	bodyContains(t, body,
		`class="ticon" role="img" title="State change" aria-label="State change"`,
		`<svg`, `aria-hidden="true"`,
	)
	// The old plain-text cell rendered the label as element content; the
	// icon cell must not also render it there, only in the two attributes
	// above — otherwise the type column is back to spending the horizontal
	// space this task exists to save.
	if strings.Contains(body, "<td>State change</td>") {
		t.Errorf("timeline type cell still renders the old plain-text label:\n%s", body)
	}
}

// TestTaskPageRendersSourceLink asserts a task with a linked PR/CI fact
// renders a source-native "Open source" link to that fact's own URL, marked
// rel="noreferrer" — the timeline evidence Task 4 preserves.
func TestTaskPageRendersSourceLink(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Linked", "priority": "medium", "kind": "feature",
	})
	const url = "https://github.com/org/app/pull/9"
	seedEvent(t, st, "pr-good", func(tx *sql.Tx, _ int64) error {
		_, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: "org/app", Number: 9, Title: "Linked", State: "open",
			HeadRef: "WL-1-linked", HeadSHA: "sha-good",
			URL: url, OpenedAt: st.Now(),
		}, "")
		return err
	})

	rr := doReq(t, h, "GET", "/tasks/WL-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), `href="`+url+`" rel="noreferrer"`, "Open source")
}

// TestTaskPageEscapesHostileTimelineURL asserts a source URL with an unsafe
// scheme (e.g. javascript:) never reaches the rendered href verbatim:
// templ's own href sanitizer (github.com/a-h/templ's SafeURL, applied
// automatically to every <a href=...> expression by the generated code —
// see internal/ui/task.templ) neutralizes it into "about:invalid#TemplFailedSanitizationURL",
// since ui.TimelineRow.URL is rendered as a plain string. This is templ's
// equivalent safety net to html/template's contextual autoescaping (which
// used to substitute the different placeholder "#ZgotmplZ" for the same
// class of hostile URL) — same guarantee (never rendered verbatim), a
// different library's placeholder token.
func TestTaskPageEscapesHostileTimelineURL(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Hostile link", "priority": "medium", "kind": "feature",
	})
	const hostile = "javascript:alert(document.cookie)"
	seedEvent(t, st, "pr-hostile", func(tx *sql.Tx, _ int64) error {
		_, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: "org/app", Number: 1, Title: "Hostile", State: "open",
			HeadRef: "WL-1-hostile", HeadSHA: "sha-hostile",
			URL: hostile, OpenedAt: st.Now(),
		}, "")
		return err
	})

	rr := doReq(t, h, "GET", "/tasks/WL-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, `href="`+hostile+`"`) {
		t.Fatalf("hostile URL rendered verbatim in href, want escaped/rejected:\n%s", body)
	}
	bodyContains(t, body, "about:invalid#TemplFailedSanitizationURL")
}

func TestTaskPageShowsProgress(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	container := createContainer(t, h, token, "proj", "Container")
	var childIDs []string
	for _, title := range []string{"A", "B"} {
		child := createTaskViaAPI(t, h, token, map[string]any{
			"project": "proj", "title": title, "priority": "medium", "kind": "feature",
			"parent": container,
		})
		childIDs = append(childIDs, child["id"].(string))
	}

	rr := doReq(t, h, "GET", "/tasks/"+container, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "(0/2 closed)", `href="/tasks/`+childIDs[0]+`"`)
}

// TestTaskPageShowsFollowUps checks both directions of the provenance edge
// render on the task page: the origin lists its follow-ups, the follow-up
// names its origin.
func TestTaskPageShowsFollowUps(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Origin", "priority": "medium", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Loose end", "priority": "medium", "kind": "chore",
		"follow_up_to": "WL-1",
	})

	rr := doReq(t, h, "GET", "/tasks/WL-2", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("follow-up page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "Follow-up to", `/tasks/WL-1`)

	rr = doReq(t, h, "GET", "/tasks/WL-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("origin page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "Follow-ups", `/tasks/WL-2`)
}

// TestTaskPageShowsDuplicates checks both directions of the duplicate edge
// render on the task page: the canonical task lists its duplicates, the
// duplicate names its canonical task.
func TestTaskPageShowsDuplicates(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Canonical", "priority": "medium", "kind": "bug",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Filed twice", "priority": "medium", "kind": "bug",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-2/edges", token,
		map[string]any{"to": "WL-1", "type": "duplicate_of"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/tasks/WL-2", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("duplicate page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "Duplicate of", `/tasks/WL-1`)

	rr = doReq(t, h, "GET", "/tasks/WL-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("canonical page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "Duplicates", `/tasks/WL-2`)
}

// TestTaskPageRendersEdgeChangeSummary pins summarizeStateChange's "edge"
// case: store.AddEdge/RemoveEdge log {"field":"edge","op",...}, not the
// {"field","old","new"} shape every other state_log row uses, so a naive
// decode used to render a blank "edge set to " line. Both the add and the
// remove must render a non-blank, non-generic summary naming both endpoints.
func TestTaskPageRendersEdgeChangeSummary(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Blocker", "priority": "medium", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Blocked", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/tasks/WL-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, "edge set to") {
		t.Fatalf("task page renders the blank field/old/new fallback for an edge change:\n%s", body)
	}
	bodyContains(t, body, "edge added: WL-1 blocks WL-2")

	rr = doReq(t, h, "DELETE", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("remove edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/tasks/WL-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "edge removed: WL-1 blocks WL-2")
}

func TestProjectPage(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	if err := st.AddRepo(context.Background(), "proj", "acme/widgets"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	if err := st.SetProjectFocus(context.Background(), "proj", []string{"security", "usability"}); err != nil {
		t.Fatalf("set project focus: %v", err)
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
		"proj", "Scoped task",
		`<link rel="canonical" href="/projects/proj">`, // cockpit projection's canonical url
		`data-panel="B"`,      // the Operations (mode B) canvas
		"Operations",          // the declared-evidence Operations mode banner
		"Active work",         // the Operations work card renders the ready task
		"Automation boundary", // the decision rail's honest automation-boundary card
	)
	// Mode B has neither a repositories panel nor a ranking-focus list (its
	// Definition-of-done panel has no backing data and is omitted too), so
	// neither a mapped repo nor the project's steering concerns may leak into
	// the rendered canvas — both remain covered by the JSON cockpit contract,
	// not this page (WL-164).
	for _, absent := range []string{"acme/widgets", "security", "usability"} {
		if strings.Contains(body, absent) {
			t.Errorf("project page unexpectedly rendered %q:\n%s", absent, body)
		}
	}
	// Project local nav, in the exact order docs/specs/032-project-cockpit.md
	// §2 requires: Overview, Crew, Work, Deliverables, Reviews, Decisions,
	// Documents, Activity.
	assertOrder(t, body, ">Overview<", ">Crew<", ">Work<", ">Deliverables<", ">Reviews<", ">Decisions<", ">Documents<", ">Activity<")
	assertOrder(t, body, `class="backlink"`, "All projects", ">Overview<")
	// The cockpit is a projection, never a stored workflow field: the page
	// must not render any of the retired/forbidden concepts. "Crew" and
	// "Deliverable(s)" are now legitimate project-local nav labels (checked
	// above), so only the concepts that would still be fabricated data stay
	// forbidden here. "completion" also catches "completion_percentage"; "%"
	// catches any percentage-based health/progress readout.
	for _, forbidden := range []string{"%", "completion", "project health", "Approval"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("project page unexpectedly renders %q:\n%s", forbidden, body)
		}
	}

	rr = doReq(t, h, "GET", "/projects/nosuch", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown project page status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestProjectPageOwnerAndDelegateCopy asserts the rendered Overview page's
// Active-work row shows real owner/delegate names, distinguishing the human
// accountable owner (Dana) from the agent that holds the lease (Agent One)
// via the "Agent One · on behalf of Dana" who-line — never the bare actor id,
// never conflating the two roles.
func TestProjectPageOwnerAndDelegateCopy(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	ctx := context.Background()
	if err := st.CreateActor(ctx, "dana", "human", "Dana", false); err != nil {
		t.Fatalf("create actor dana: %v", err)
	}
	agentToken := seedActor(t, st, "agent-one", "agent", "Agent One", false)
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Owned and delegated", "priority": "medium", "kind": "feature",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/assign", token, map[string]any{"assignee": "dana"})
	if rr.Code != http.StatusOK {
		t.Fatalf("assign status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", agentToken, map[string]any{"worktree": "host:/wt-agent-one"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/projects/proj", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("project page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "Agent One", "on behalf of Dana")
}

// TestDriftPageRendersWithoutGraph holds the drift board's two standing
// properties on a deployment with no graph-server: the backbone-authoritative
// half still renders (200), the graph-backed half says so honestly instead of
// erroring, and the whole page stays read-only — spec 007's read surface has
// no act to offer, so it must render no mutation affordance at all.
func TestDriftPageRendersWithoutGraph(t *testing.T) {
	_, h, _ := newTestServer(t)
	rec := doReq(t, h, http.MethodGet, "/drift", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /drift: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "knowledge graph not configured") {
		t.Fatalf("page without graph must say so:\n%s", rec.Body.String())
	}
	// Read-only by construction: no form, no POST affordance.
	if strings.Contains(rec.Body.String(), "<form") {
		t.Fatal("drift page contains a mutation affordance")
	}
}
