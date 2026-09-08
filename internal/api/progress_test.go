package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// progressSpecBody is a spec with three anchored sections — the ones
// progressPlanBody covers at coverage none, full and partial.
const progressSpecBody = `---
status: draft
---

# Progress

## 0. Why {#sec-0}

Why body.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// progressPlanBody covers those three sections at none/full/partial and
// mints two tasks, so accepting it puts the plan in not_started (§1.1) and
// both owed sections in "Accepted, not started" (§1.2).
const progressPlanBody = `---
status: draft
covers:
  - spec: 066-progress.md#sec-0
    coverage: none
  - spec: 066-progress.md#sec-1
    coverage: full
  - spec: 066-progress.md#sec-2
    coverage: partial
---

# Progress plan

## Tasks

### Task 1 — First task

` + "```yaml" + `
kind: feature
priority: high
` + "```" + `

Do the first thing.

### Task 2 — Second task

` + "```yaml" + `
kind: feature
priority: medium
` + "```" + `

Do the second thing.
`

// seedProgressProject creates the project, its spec, and an accepted plan
// covering the spec. It returns the spec document, for tests that need its
// id.
func seedProgressProject(t *testing.T, h http.Handler, token, project string) model.Doc {
	t.Helper()
	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: project, Kind: "spec", Number: 66, Slug: "066-progress",
		Body: progressSpecBody,
	})
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: project, Kind: "plan", Slug: "066-progress-plan",
		Body: progressPlanBody,
	})
	rr := doReq(t, h, "POST", docPath(plan.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept plan status = %d, body %s", rr.Code, rr.Body.String())
	}
	var accepted model.AcceptDocResponse
	decodeInto(t, rr, &accepted)
	if len(accepted.Tasks) != 2 {
		t.Fatalf("accepting the plan minted %d tasks, want 2", len(accepted.Tasks))
	}
	return spec
}

// TestProgressPage: the page over real data. One spec covered by one accepted
// plan whose tasks are all unstarted lands in Active, draws one cell per owed
// section and none for the coverage-none section (§1.4), says so about the
// missing rally, and carries no percentage anywhere (§2.5).
func TestProgressPage(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedProgressProject(t, h, token, "proj")

	body := getPage(t, h, "/projects/proj/progress").Body.String()

	for _, want := range []string{
		"WL-SPEC-66", // the spec's ref, the row's only link
		"/docs/ref/WL-SPEC-66",
		">Active<",        // §1.3's first group
		"No active rally", // §2.1's band with nothing to show
	} {
		if !strings.Contains(body, want) {
			t.Errorf("progress page does not contain %q", want)
		}
	}

	// Two owed sections, both "accepted, not started"; the coverage-none
	// section is bound only and gets no cell at all (§1.4). The legend draws
	// one swatch per state, so count the strip's cells alone.
	strip := between(t, body, `<div class="strip">`, "</div>")
	if got := strings.Count(strip, "cell-not_started"); got != 2 {
		t.Errorf("strip has %d not_started cells, want 2 — strip was %s", got, strip)
	}
	if got := strings.Count(strip, `<i class="cell `); got != 2 {
		t.Errorf("strip has %d cells, want 2 (the bound-only section draws none) — strip was %s", got, strip)
	}
	if !strings.Contains(strip, "cell-partial") {
		t.Errorf("strip has no partial ring for the partially covered section — strip was %s", strip)
	}

	// §2.5: counts are counts. No count element carries a percentage.
	for _, block := range []string{`class="prog-counts"`, `class="prog-legend"`} {
		if seg := between(t, body, block, "</section>"); strings.Contains(seg, "%") {
			t.Errorf("a count element carries a percentage, which 032 §4 forbids: %s", seg)
		}
	}
}

// TestProgressPageNeedsASpec: a project with no spec has no Progress page and
// no sidebar entry pointing at one (§2, amending 056 §2).
func TestProgressPageNeedsASpec(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "bare")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/bare/progress", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /projects/bare/progress = %d, want 404", rec.Code)
	}

	overview := getPage(t, h, "/projects/bare").Body.String()
	if strings.Contains(overview, "/projects/bare/progress") {
		t.Error("a project with no spec still links to Progress from its sidebar")
	}
	if !strings.Contains(overview, "/projects/bare/work") {
		t.Error("the sidebar lost its Work entry")
	}
}

// TestProgressSidebarEntry: the entry appears once the project has a spec.
func TestProgressSidebarEntry(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedProgressProject(t, h, token, "proj")

	for _, page := range []string{"/projects/proj", "/projects/proj/work", "/projects/proj/progress"} {
		if !strings.Contains(getPage(t, h, page).Body.String(), "/projects/proj/progress") {
			t.Errorf("%s has no Progress entry in its sidebar", page)
		}
	}
}

// TestProgressExpandedRow: every row ships with its detail block (§2.3) and
// every cell with the text its tooltip shows (§2.4). progress.js has no test
// harness, so what a Go test holds is the DOM it drives: a .detail after each
// row, data-tip on each section and task cell, data-task on each task cell,
// and the script tag that reads them.
func TestProgressExpandedRow(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedProgressProject(t, h, token, "proj")

	body := getPage(t, h, "/projects/proj/progress").Body.String()

	for _, want := range []string{
		`src="/assets/progress.js`,               // the script that expands a row
		`aria-controls="d-WL-SPEC-66"`,           // the row names its own detail
		`<div class="detail" id="d-WL-SPEC-66">`, // rendered server-side, hidden by CSS
		"0 landed",                               // the plan's counts, still no percentage
		"2 open",
		"bound only", // §1.4's section: listed in the table, drawn nowhere
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expanded row does not contain %q", want)
		}
	}

	rows := strings.Count(body, `<div class="prog-row"`)
	if rows == 0 {
		t.Fatal("the page renders no spec row")
	}
	if got := strings.Count(body, `<div class="detail" `); got != rows {
		t.Errorf("%d rows but %d detail blocks — every row expands into one", rows, got)
	}

	// Both minted tasks get a cell, and each carries what progress.js reads:
	// the task id it pins on, and the line §2.4 shows.
	cells := regexp.MustCompile(`<i class="task task-[a-z]+"[^>]*>`).FindAllString(body, -1)
	if len(cells) != 2 {
		t.Fatalf("task strip has %d cells, want 2: %v", len(cells), cells)
	}
	for _, c := range cells {
		if !strings.Contains(c, `data-task="WL-`) || !strings.Contains(c, `data-tip="WL-`) {
			t.Errorf("task cell %s carries no data-task/data-tip pair", c)
		}
	}

	// Section cells carry their tooltip as data too — and no title attribute,
	// which would show a second, native tooltip over the same cell.
	strip := between(t, body, `<div class="strip">`, "</div>")
	for _, c := range regexp.MustCompile(`<i class="cell[^>]*>`).FindAllString(strip, -1) {
		if !strings.Contains(c, `data-tip="`) {
			t.Errorf("section cell %s carries no data-tip", c)
		}
	}
	if strings.Contains(strip, "title=") {
		t.Errorf("a section cell still carries a title attribute, so it shows two tooltips: %s", strip)
	}
}

// TestProgressRowFragment: GET .../progress/spec/{doc} renders exactly the
// one spec's row and its detail block (§5.2), through renderWeb, so it
// carries the page's own CSP (§4.4).
func TestProgressRowFragment(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := seedProgressProject(t, h, token, "proj")

	rr := doReq(t, h, "GET", fmt.Sprintf("/projects/proj/progress/spec/%d", spec.ID), "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET progress/spec/%d = %d, body %s", spec.ID, rr.Code, rr.Body)
	}
	if got := rr.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", got)
	}

	body := rr.Body.String()
	if got := strings.Count(body, `<div class="prog-row"`); got != 1 {
		t.Errorf("row fragment has %d .prog-row elements, want 1: %s", got, body)
	}
	if got := strings.Count(body, `<div class="detail" `); got != 1 {
		t.Errorf("row fragment has %d .detail elements, want 1: %s", got, body)
	}
	if !strings.Contains(body, "WL-SPEC-66") {
		t.Errorf("row fragment does not contain the spec's ref: %s", body)
	}
}

// TestProgressRowFragmentNotFound: a doc id that is not a live spec of this
// project 404s — a plan's own id, a spec of another project, and an id that
// names no document at all (§5.2).
func TestProgressRowFragmentNotFound(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "other")
	spec := seedProgressProject(t, h, token, "proj")
	otherSpec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "other", Kind: "spec", Number: 1, Slug: "001-other",
		Body: progressSpecBody,
	})
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "066-progress-plan-2",
		Body: progressPlanBody,
	})

	for name, doc := range map[string]int64{
		"a plan's own id":           plan.ID,
		"a spec of another project": otherSpec.ID,
		"an id naming nothing":      spec.ID + 10000,
	} {
		t.Run(name, func(t *testing.T) {
			rr := doReq(t, h, "GET", fmt.Sprintf("/projects/proj/progress/spec/%d", doc), "", nil)
			if rr.Code != http.StatusNotFound {
				t.Errorf("GET progress/spec/%d = %d, want 404", doc, rr.Code)
			}
		})
	}
}

// TestProgressSummaryFragment: GET .../progress/summary renders the band,
// counts, bar/legend and footer's slot — the markup the full page draws
// outside its spec groups — through renderWeb, so it too carries
// frame-ancestors 'none' (§4.4, §5.2).
func TestProgressSummaryFragment(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedProgressProject(t, h, token, "proj")

	rr := doReq(t, h, "GET", "/projects/proj/progress/summary", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET progress/summary = %d, body %s", rr.Code, rr.Body)
	}
	if got := rr.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", got)
	}

	body := rr.Body.String()
	for _, want := range []string{
		"prog-band",
		`class="prog-counts"`,
		`class="prog-bar"`,
		`class="prog-legend"`,
		`class="prog-footer`,
		"No active rally",
		">Active<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("summary fragment does not contain %q: %s", want, body)
		}
	}
	// The fragment stops at the summary: no spec row and no detail block, both
	// of which the row fragment owns (§5.2).
	if strings.Contains(body, `class="prog-row"`) {
		t.Error("summary fragment renders a spec row, which is the row fragment's job")
	}
}

// TestProgressSummaryFragmentFixedHeights holds §5.4 rule 1: the band, the
// counts, the section bar and the footer have fixed heights, so a live swap
// of any of them moves nothing else on the page. The band and footer keep
// their reserved slot's height whether or not there is anything to say
// (h-12, h-14); the counts tiles hold theirs against a wrapping label
// (h-20); the section bar renders in its own card regardless of content.
func TestProgressSummaryFragmentFixedHeights(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedProgressProject(t, h, token, "proj")

	rr := doReq(t, h, "GET", "/projects/proj/progress/summary", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET progress/summary = %d, body %s", rr.Code, rr.Body)
	}

	body := rr.Body.String()
	for _, want := range []string{
		`class="card prog-band h-12"`,
		`class="card pad prog-count h-20"`,
		`class="card" aria-labelledby="prog-bar-h"`,
		`class="prog-footer h-14"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("summary fragment does not contain %q: %s", want, body)
		}
	}
}

// TestProgressSummaryFragmentNeedsASpec: a project with no spec 404s here
// too, matching the full page (§2, §5.2).
func TestProgressSummaryFragmentNeedsASpec(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "bare")

	rr := doReq(t, h, "GET", "/projects/bare/progress/summary", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET progress/summary = %d, want 404", rr.Code)
	}
}

// TestProgressDraftPlanAction: a draft plan's line and a draft spec's row
// each carry the two-step Accept button (§3.1, §3.2). progress.js has no test
// harness, so what a Go test holds is the DOM the script drives: a button.act
// carrying the route it posts to and the reason it is disabled.
//
// This server is the open instance (WebOpen), so the viewer is nobody: the
// button is disabled with the reason rather than hidden (§3), because the
// store admits only the document's owner. The enabled shape is
// TestProgressAcceptActionByViewer's, in internal/ui, where a viewer can be
// named without a login provider.
func TestProgressDraftPlanAction(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 66, Slug: "066-progress",
		Body: progressSpecBody,
	})
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "066-progress-plan",
		Body: progressPlanBody,
	})

	body := getPage(t, h, "/projects/proj/progress").Body.String()

	line := between(t, body, `<div class="plan">`, "</div>")
	for _, want := range []string{
		`class="act"`,
		`data-route="accept"`,
		`data-reason="sign in to accept"`,
		`disabled`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the draft plan's line does not contain %q — line was %s", want, line)
		}
	}
	// A disabled button carries no body and no confirmation sentence: there
	// is nothing for the script to send.
	if strings.Contains(line, "data-body=") {
		t.Errorf("a disabled Accept button still carries the body it would send: %s", line)
	}
	// Two buttons: the draft plan's line and the draft spec's row (§3.2).
	if got := strings.Count(body, `data-route="accept"`); got != 2 {
		t.Errorf("the page renders %d accept buttons, want 2 (the draft plan and the draft spec)", got)
	}
}

// TestProgressReviewActionDisabled: spec 059's routes do not exist in
// today's routeGuards, so hasReviewSurface reports false and every spec row
// and plan line renders its Review button disabled with that reason
// (WL-SPEC-66 §3.3). The enabled shape, once a route lands, is
// TestHasReviewSurfaceOnceRegistered's, in internal/api's own package —
// this test only holds today's rendering to today's table.
func TestProgressReviewActionDisabled(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 66, Slug: "066-progress",
		Body: progressSpecBody,
	})
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "066-progress-plan",
		Body: progressPlanBody,
	})

	body := getPage(t, h, "/projects/proj/progress").Body.String()

	const reason = `data-reason="Review surface (spec 059) not yet built"`
	if got := strings.Count(body, `data-route="review"`); got != 2 {
		t.Errorf("the page renders %d review buttons, want 2 (the spec row and the plan line): %s", got, body)
	}
	if got := strings.Count(body, reason); got != 2 {
		t.Errorf("the page renders %d disabled review reasons, want 2: %s", got, body)
	}
}

// between returns the substring of body from the first occurrence of open
// through the next close, for assertions scoped to one block of markup.
func between(t *testing.T, body, open, close string) string {
	t.Helper()
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("markup %q not found on the page", open)
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("markup %q has no closing %q", open, close)
	}
	return rest[:j]
}
