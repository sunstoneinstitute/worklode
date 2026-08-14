---
status: draft
covers:
  - spec: docs/specs/032-project-cockpit.md#sec-2
    coverage: partial
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: partial
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
requires:
  - docs/plans/2026-08-09-project-cockpit-1-shell-and-projection.md
isRequiredBy:
  - docs/plans/2026-08-14-home-project-list.md
---

# Cockpit page-frame unification

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/` and `/projects/{id}` stop having two different layouts. Every
web page renders through the same `.shell` two-column frame (236px left
column + capped content): the seven global destinations move out of the
topbar's horizontal `nav.global` into a left **global sidebar**, the topbar
shrinks to brand + theme toggle + avatar, and project pages keep their
project sidebar and gain a `← All projects` back-link above the project
header. Below 880px the sidebar becomes a horizontal nav strip above the
content — reduced columns, not a compressed desktop grid (032 §10).

**Architecture:** this is layer-1 shell/chrome work — `internal/ui` templ
components and the design-system stylesheet, verified by `internal/api`
handler tests. No store work, no new routes, no new handlers: `web.go` and
`render.go` keep building the same views; only the components and their
tests change. That most tasks are handler-test-verifiable is expected for
this layer.

**Series.** Plan A of the four-plan series from the 2026-08-14 design brief
(Home as project list + cockpit frame unification). **A blocks
`docs/plans/2026-08-14-home-project-list.md` (plan D):** D's Home card grid
renders inside the `.shell` frame and global sidebar built here. A is
independent of plans B (`2026-08-14-project-crew-participants.md`) and C
(`2026-08-14-approvals-1-table-and-web-act.md`) and can execute in parallel
with them. As in B and C, this ordering is body prose rather than
`requires:`/`isRequiredBy:` frontmatter — the series files were authored in
parallel and a dangling reference fails `secmeta.py`; add the frontmatter
edges when all four files exist in one branch.

## Audit result: pages on a bare `.main`

Audited every `internal/ui/*.templ` (2026-08-14). Four render outside the
`.shell` frame and are converted by this plan:

- `board.templ` (Home `/` and Work `/work`) — bare `.main`, no left column
- `projects.templ` (`/projects`) — bare `.main`
- `placeholder.templ`, **global branch only** (`/intake`, `/reviews`,
  `/deliveries`, `/knowledge`) — bare `.main`; its project branch already
  renders `.shell` + project sidebar and is untouched
- `task.templ` (`/tasks/{id}`) — bare `.main`

Already on `.shell` and **not** converted: `cockpit.templ`,
`deliverables.templ`, `forms.templ` (its `formCard` wraps both creation
forms in `.shell` + project sidebar), and `placeholder.templ`'s project
branch. `layout.templ` is where the shell itself changes.

## Coverage notes

Why each `covers` level is what it is — each remainder is a deferred item
outside the whole series, so no `fullCoverageWith` sibling exists to name:

- **032 §2 `partial`** — this plan delivers the global navigation region
  (Task 1 rewords §2's opening to match the approved frame), the seven
  destinations in the sidebar, and the unified frame with the project
  back-link. The candidate-dossier local-navigation half of §2 has no
  dossiers to navigate (intake, 032 §5, is unbuilt), so §2 stays `partial`
  even after the frame lands.
- **032 §10 `partial`** — covers only the narrow-width behaviour of the
  chrome this plan builds: the shell collapse and the horizontal nav strips.
  §10's full WCAG 2.2 AA scope, async-result announcements, and the named
  narrow-width workflows stay governed but undischarged.
- **032 §11 `none`** — the standing rule: e2e tests drive the HTTP UI and
  API surfaces and never write directly to the store. The three-slice
  release content is not planned here.

## Global constraints

- **Exact spellings, quoted once.** The seven global destinations, in 032
  §2's order and spelling: **Home, Intake, Projects, Work, Reviews,
  Deliveries, Knowledge**. The eight project-local destinations: **Overview,
  Crew, Work, Deliverables, Reviews, Decisions, Documents, Activity**. All
  fifteen render as tight `>Name<` text markers (`globalLink`/`localLink`
  emit the label flush against the tags) and the web tests assert them by
  those markers. Back-link text: `All projects` (rendered with a `&larr;`).
- **Two tested invariants relocate and must not disappear.** (1) Every page
  that names a current destination carries exactly one
  `aria-current="page"` (`assertOneAriaCurrent`); `/tasks/{id}` and the
  new-task form name none today (zero markers) and that stays true. (2) The
  seven global and eight project-local `>Name<` markers keep rendering on
  the pages whose tests assert them. Any task that moves a marker or a
  landmark updates its assertions — `internal/api/web_test.go`,
  `internal/api/webform_test.go`, `e2e/cockpit_test.go`,
  `e2e/smoke_test.go` — in the same task as the change.
- **Breakpoints.** New: `max-width:880px` collapses `.shell` to one column
  and turns the sidebar into a horizontal nav strip (032 §10: reduced
  columns). Existing and untouched: `max-width:1080px` (decision rail folds
  under the canvas) and `max-width:640px` (launchgrid). All media queries
  live in `internal/ui/styles/app.tailwind.css` — point at it, never fork
  the values into components.
- **Metrics: none to add.** This plan adds no endpoint, background loop,
  outbound call, or store operation — spec 022 requires nothing new.
  `worklode_web_navigation_requests_total` keeps counting the unchanged
  destination set.
- **The templ/Tailwind toolchain is fixed** by 032 §12, already covered in
  full by `docs/plans/2026-08-10-cockpit-templ-htmx-tailwind.md`: edit only
  `.templ` sources and `app.tailwind.css`; `go generate ./...` (the one
  directive in `internal/ui/ui.go`, running `scripts/gen-web.sh`)
  regenerates the committed `*_templ.go` and `internal/ui/assets/app.css`;
  commit the generated files; never hand-edit `app.css`.
- **Dependency direction:** `internal/ui` imports nothing beyond stdlib,
  `internal/store`, and the templ runtime; `internal/api` imports
  `internal/ui`, never the reverse. This plan adds no imports anywhere.
- **`e2e/` drives public surfaces only** (HTTP pages) — never a store write.
- **Every task leaves `go test ./...` green** and ends with a commit.
  `internal/api` tests need Postgres with pgvector (default DSN in
  CLAUDE.md, override `TEST_POSTGRES_DSN`); a skipped run proved nothing.
- All tasks are specified for a Sonnet-tier implementer per
  `MODEL_SELECTION.md`; none carries a known open design decision. Escalate
  on the first sign the plan does not match the code.

**Read first:**
- `docs/specs/032-project-cockpit.md` §2, §10
- `internal/ui/layout.templ` — `Page`, `globalNav`, `globalLink`,
  `sidebar`, `localNav`
- `internal/ui/views.go` — `PageProps` and its ActiveGlobal contract
- `internal/ui/styles/app.tailwind.css` — the topbar/shell/nav rules and
  existing media queries
- `internal/api/web_test.go` — `assertShell`, `assertOneAriaCurrent`,
  `assertOrder`, `mainContent`, `TestGlobalNavOrder`,
  `TestGlobalDestinations`, `TestHomePage`, `TestProjectPage`,
  `TestProjectSections`, `TestAppCSSContent`
- `e2e/cockpit_test.go` — `assertOverviewSurface` (nav-landmark count);
  `e2e/smoke_test.go` — `assertWebPages`

## Tasks

### Task 1 — Reword 032 §2's opening: a navigation region, not a top bar

```yaml
kind: design
priority: high
skills: [ ]
blockedBy: [ ]
```

User-approved spec amendment. 032 is `status: draft`, so its anchors are not
frozen and this is an ordinary edit — no amendment ceremony, no `amends:`
frontmatter, no inline note, no renumbering.

In `docs/specs/032-project-cockpit.md` §2, replace exactly this opening
sentence:

```markdown
The global application navigation is a horizontal top bar with these primary
destinations:
```

with:

```markdown
The application presents a persistent global navigation region with these
primary destinations:
```

Everything else in §2 stays byte-identical: the seven numbered destinations
in their exact order and spelling, the project/dossier left-sidebar
paragraph, and the sentence "Global and local navigation must remain
visually and semantically distinct."

- [ ] Make the one-sentence edit; `git diff docs/specs/` shows only those
      two lines replaced.
- [ ] `./scripts/secfmt.py -l` — prints nothing, exit 0 (no numbering or
      anchor change).
- [ ] `./scripts/secmeta.py` — prints nothing on stdout, exit 0 (any
      `unresolved` cross-project shorthand on stderr is pre-existing and
      fine).
- [ ] `./scripts/secindex.py --check` — index still current (no heading
      changed).
- [ ] Commit: `Spec 032 §2: global navigation is a region, not a top bar`.

### Task 2 — Move the seven global destinations into a global sidebar

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

The atomic flip, and this plan's tracer. The topbar is shared chrome, so the
component change and every global page's conversion land in one commit —
staging it would either double or zero the `aria-current` count mid-flight.

**`internal/ui/layout.templ`:** delete the `<nav aria-label="Primary"
class="global">…</nav>` block from `Page`'s topbar (brand, theme toggle,
avatar stay), update the file-header comment (it still describes "brand +
primary global nav"), and add two components. `globalNav`/`globalLink` are
reused as-is, so the seven `>Name<` markers survive verbatim:

```templ
// globalSidebar renders the left column global pages share: the Primary
// nav landmark carrying the seven global destinations (moved here from the
// topbar), marking the one matching active as the current page.
templ globalSidebar(active string) {
	<div class="sidebar">
		<nav aria-label="Primary" class="global">
			@globalNav(active)
		</nav>
	</div>
}

// globalShell wraps a global (non-project) page's content in the unified
// two-column .shell frame: global sidebar left, content in .main > .canvas.
// Project pages build their own .shell with sidebar() instead.
templ globalShell(active string) {
	<div class="shell">
		@globalSidebar(active)
		<div class="main">
			<div class="canvas">
				{ children... }
			</div>
		</div>
	</div>
}
```

**Convert the three global-page templates** (each currently opens
`<div class="main"><div class="canvas">` directly under `@Page`): replace
that wrapper with `@globalShell(v.Page.ActiveGlobal)` and delete the two
closing `</div>`s. Files: `board.templ` (both Home and Work branches),
`projects.templ`, and `placeholder.templ`'s **global** (Project nil) branch.
The project branch of `placeholder.templ` is untouched. `board.templ` after:

```templ
templ Board(v BoardView) {
	@Page(v.Page) {
		@globalShell(v.Page.ActiveGlobal) {
			if v.IsHome {
				<h1>Home</h1>
				<h2>Current work</h2>
			} else {
				<h1>Work</h1>
			}
			// ...rest of the existing body, unchanged...
		}
	}
}
```

**`internal/ui/views.go`:** update `PageProps.ActiveGlobal`'s doc comment —
it now drives `globalShell`'s sidebar marking, not the topbar; a page with
no current destination (the task page) passes `""` and marks nothing.

**`internal/ui/styles/app.tailwind.css`:** restyle `nav.global` for the
sidebar — vertical like `nav.local`, dropping the topbar-only rules
(`overflow-x:auto;flex:1;scrollbar-width:none` and the
`nav.global::-webkit-scrollbar` line; keep the `.cnt` rules):

```css
/* nav.global lives in the left sidebar on global pages; the topbar carries
 * no navigation. Below 880px it becomes a horizontal strip (later media
 * query). */
nav.global{display:flex;flex-direction:column;gap:1px}
nav.global a{
  padding:8px 10px;border-radius:8px;color:var(--ink-2);font-weight:500;
  font-size:13.5px;display:flex;align-items:center;gap:10px
}
```

(`:hover`/`.active` rules stay as they are.) Also remove the `gap:20px`
topbar dependence if the nav's removal leaves spacing off — the topbar keeps
`.brand` + `.top-right` with `margin-left:auto`.

**Tests, `internal/api/web_test.go`** — first test of the new behaviour:

```go
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

// TestTopbarKeepsOnlyChrome checks the global destinations left the topbar
// (brand, theme toggle, avatar only — no nav landmark, no links) and that
// the seven destinations render in the sidebar column before the content.
func TestTopbarKeepsOnlyChrome(t *testing.T) {
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
	assertOrder(t, body, `<div class="sidebar">`, ">Home<", ">Knowledge<", `<div class="main">`)
}
```

Then the relocation updates, same task:

- `assertShell`: drop the `<nav aria-label="Primary"` marker (project pages
  no longer carry it — their left column is the project sidebar, and §2's
  distinctness rule is served by the two labelled navs never co-occurring);
  add `class="shell"` to the every-page marker list.
- `TestGlobalDestinations`: add
  `bodyContains(t, body, `+"`"+`<nav aria-label="Primary"`+"`"+`)` inside
  the loop — every global destination page carries the landmark in its
  sidebar.
- `TestGlobalNavOrder`, `TestHomePage`, `TestWorkPageOrgBoard`,
  `TestGlobalPlaceholdersAreHonest`: pass unchanged (markers relocated, not
  renamed; the sidebar renders only `<a>` links, so the no-form/no-button
  main-content checks still hold).
- `e2e/cockpit_test.go` `assertOverviewSurface`: the project page now has
  exactly **one** nav landmark — change the count check from 2 to 1, drop
  the `<nav aria-label="Primary"` assertion there, keep
  `<nav aria-label="Project"`. `e2e/smoke_test.go` `assertWebPages` is
  unchanged: Home still carries the Primary landmark.

- [ ] Red: `TestTopbarKeepsOnlyChrome` fails against the old layout; then
      the flip makes it green.
- [ ] `go generate ./...`; `git status --porcelain` shows the regenerated
      `*_templ.go` and `internal/ui/assets/app.css` — include them.
- [ ] `go test ./internal/api -count=1` with Postgres up — green.
- [ ] `go test ./...` green; `go test -race -count=1 -tags e2e ./e2e/ -run
      'TestProjectCockpitPublicSurface|TestFullChain'` — green.
- [ ] Commit: `Move global navigation from the topbar into a sidebar`.

### Task 3 — Task page joins the shell

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

`internal/ui/task.templ`: replace its bare
`<div class="main"><div class="canvas">` wrapper with `@globalShell("")` —
the task page is a cross-project detail page, not one of the seven
destinations, so nothing is marked current. That preserves today's
behaviour exactly (the topbar nav marked nothing on `/tasks/{id}` either):
zero `aria-current="page"` on this page, seven unmarked global links.

Extend `TestTaskPage` (`internal/api/web_test.go`), right after the
existing status check:

```go
body := rr.Body.String()
assertShell(t, body)
bodyContains(t, body, `<nav aria-label="Primary"`)
if got := strings.Count(body, `aria-current="page"`); got != 0 {
	t.Errorf(`aria-current count = %d, want 0 (no destination is current on a task page)`, got)
}
```

- [ ] Red (`assertShell` finds no `class="shell"`), then green.
- [ ] `go generate ./...`; commit the regenerated files.
- [ ] `go test ./internal/api -run TestTaskPage -count=1` green;
      `go test ./...` green.
- [ ] Commit: `Render the task page inside the unified shell`.

### Task 4 — "All projects" back-link on the project sidebar

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

`internal/ui/layout.templ`, `sidebar`: add the back-link as the first child
of `.sidebar`, above `.proj-head`:

```templ
<a class="backlink" href="/projects">&larr; All projects</a>
```

It is a plain link, never `aria-current` and never part of `nav.local`, so
the one-current-marker invariant is untouched. Style in
`app.tailwind.css`, next to the sidebar rules:

```css
.backlink{display:inline-flex;align-items:center;gap:6px;font-size:12.5px;
  font-weight:600;color:var(--ink-2);padding:4px 8px;margin-bottom:6px;
  border-radius:7px}
.backlink:hover{background:var(--surface-2);text-decoration:none}
```

Every `.shell` page that uses `sidebar()` inherits it: cockpit, project
placeholders, deliverables, both creation forms.

Tests: in `TestProjectPage`, after the existing local-nav `assertOrder`,
add `assertOrder(t, body, `+"`"+`class="backlink"`+"`"+`, "All projects",
">Overview<")` — the back-link renders above the project nav. In
`e2e/cockpit_test.go` `assertOverviewSurface`, assert the body contains
`All projects`. The existing `assertOneAriaCurrent` calls prove the count
stays 1.

- [ ] Red → green; `go generate ./...`; commit generated files.
- [ ] `go test ./internal/api -run 'TestProjectPage|TestProjectSections' -count=1`
      green; `go test ./...` green;
      `go test -race -count=1 -tags e2e ./e2e/ -run TestProjectCockpitPublicSurface`
      green.
- [ ] Commit: `Add the All-projects back-link to the project sidebar`.

### Task 5 — Below 880px the sidebar becomes a horizontal nav strip

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 4]
```

032 §10: narrow layouts use reduced columns, never a compressed desktop
grid. Add one media query to `internal/ui/styles/app.tailwind.css`, after
the existing `.main`/`.mode` rules (the 1080px and 640px queries stay
untouched):

```css
/* Below 880px the shell collapses to one column (spec 032 §10: reduced
 * columns, not a squeezed grid): the sidebar — global or project — becomes
 * a horizontal nav strip above the content. */
@media (max-width:880px){
  .shell{grid-template-columns:1fr}
  .sidebar{border-right:0;border-bottom:1px solid var(--line);padding:10px 14px}
  nav.global,nav.local{flex-direction:row;overflow-x:auto;scrollbar-width:none;
    margin-top:0;border-top:0;padding-top:0}
  nav.global::-webkit-scrollbar,nav.local::-webkit-scrollbar{display:none}
  nav.global a,nav.local a{white-space:nowrap}
  .proj-head{display:flex;align-items:baseline;gap:10px;padding:0 0 8px}
  .proj-name{margin:0;font-size:16px}
}
```

No templ change. The markup order (sidebar first, then `.main`) already
puts the strip above the content in the collapsed grid.

Test: extend `TestAppCSSContent`'s `want` list
(`internal/api/web_test.go`) with `"max-width:880px"` and `".backlink"` —
the served stylesheet is the committed generated artifact, so this catches
a forgotten `go generate` too.

- [ ] Red (served CSS lacks the query) → `go generate ./...` → green;
      commit the regenerated `app.css`.
- [ ] `go test ./internal/api -run TestAppCSSContent -count=1` green;
      `go test ./...` green.
- [ ] Commit: `Collapse the shell to a nav strip below 880px`.

### Task 6 — Shell-unification sweep test

```yaml
kind: chore
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3, 4]
```

Make the audit durable: one sweep over every web page asserting the unified
frame, so the next page cannot regress to a bare `.main`. New test in
`internal/api/web_test.go`:

```go
// TestEveryPageRendersTheShell sweeps every web page and asserts the
// unified frame: exactly one .shell grid, one main landmark, and — on every
// page that names a current destination — exactly one aria-current="page".
// The task page and the new-task form name none (their left column marks
// nothing), so they assert zero.
func TestEveryPageRendersTheShell(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Swept task", "priority": "low", "kind": "chore",
	})

	pages := []struct {
		path       string
		hasCurrent bool
	}{
		{"/", true}, {"/intake", true}, {"/projects", true}, {"/work", true},
		{"/reviews", true}, {"/deliveries", true}, {"/knowledge", true},
		{"/projects/proj", true}, {"/projects/proj/crew", true},
		{"/projects/proj/reviews", true}, {"/projects/proj/decisions", true},
		{"/projects/proj/documents", true}, {"/projects/proj/activity", true},
		{"/projects/proj/deliverables", true},
		{"/projects/proj/deliverables/new", true},
		{"/projects/proj/tasks/new", false},
		{"/tasks/WL-1", false},
	}
	for _, page := range pages {
		t.Run(page.path, func(t *testing.T) {
			rr := doReq(t, h, "GET", page.path, "", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			if got := strings.Count(body, `<div class="shell">`); got != 1 {
				t.Errorf("shell count = %d, want 1", got)
			}
			if got := strings.Count(body, `<main id="main-content"`); got != 1 {
				t.Errorf("main landmark count = %d, want 1", got)
			}
			want := 0
			if page.hasCurrent {
				want = 1
			}
			if got := strings.Count(body, `aria-current="page"`); got != want {
				t.Errorf(`aria-current="page" count = %d, want %d`, got, want)
			}
		})
	}
}
```

(The new-task form passes `""` as its sidebar-active key today — that
zero is existing behaviour this plan documents, not changes. If any listed
path 404s or double-renders, that is a real finding: fix the page, not the
test.)

- [ ] `go test ./internal/api -run TestEveryPageRendersTheShell -count=1`
      — every subtest green.
- [ ] `go test ./...` green.
- [ ] Commit: `Sweep-test the unified shell across every web page`.

### Task 7 — Visual verification at desktop, strip, and mobile widths

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [2, 3, 4, 5]
```

Handler tests cannot see layout. Verify visually with **headless
Playwright** (never a personal browser), against the compose stack:

```bash
export LODE_BOOTSTRAP_TOKEN=wl_$(openssl rand -hex 20)
docker compose up -d --build --wait
LODE_SERVER=http://localhost:8080 LODE_TOKEN=$LODE_BOOTSTRAP_TOKEN \
  go run . project add demo --name "Demo project" --key DEMO
npx --yes playwright install chromium
mkdir -p /tmp/frame-shots && cd /tmp/frame-shots
for vp in 1440,900 860,900 390,844; do
  for page in "" projects projects/demo; do
    npx --yes playwright screenshot --viewport-size="$vp" \
      --full-page "http://localhost:8080/$page" \
      "shot-${vp/,/x}-${page:-home}.png"
  done
done
```

(Each screenshot command prints `Capturing screenshot into shot-….png`.)
Inspect all nine screenshots and confirm:

- **1440x900:** every page shows the two-column shell — left sidebar
  (global nav on `/` and `/projects`; back-link + project header + local
  nav on `/projects/demo`), content capped at the same width and padding on
  all three.
- **860x900** (below the 880px breakpoint): one column; the sidebar renders
  as a horizontal nav strip above the content; nothing is a shrunken
  desktop grid.
- **390x844** (mobile): still one column; the nav strip scrolls
  horizontally within itself; the page body has no horizontal scrollbar
  (screenshot content fills exactly the viewport width).

Fix any defect found in this task (a pure CSS/markup fix belongs here; a
design gap is an escalation, not an improvisation). Tear down with
`docker compose down -v`.

- [ ] Nine screenshots captured and inspected; the three checks above hold.
- [ ] If fixes were needed: `go generate ./...`, `go test ./...` green,
      commit `Fix narrow-width shell layout defects`; otherwise nothing to
      commit.

### Task 8 — Full verification and comment alignment

```yaml
kind: chore
priority: medium
skills:
  - superpowers:verification-before-completion
blockedBy: [7]
```

The closing sweep:

- `grep -rn "top bar\|topbar" internal/ui internal/api --include='*.go' --include='*.templ'`
  — update any comment still claiming the topbar carries the primary nav
  (e.g. `web.go`'s package comment, `views.go`'s `PageProps` if missed);
  code references to the `.topbar` chrome itself stay.
- `go generate ./...` then `git status --porcelain` — empty, proving the
  committed artifacts are current; `./scripts/check-generated.sh` agrees.
- `./scripts/secfmt.py -l` and `./scripts/secmeta.py` — silent, exit 0.
- `go test ./...` — green.
- `go test -race -count=1 -tags e2e ./e2e/` — green
  (`ok  github.com/sunstoneinstitute/worklode/e2e`).

- [ ] All five checks pass with the outputs stated.
- [ ] Commit any comment fixes: `Align shell comments with the sidebar
      navigation`; otherwise nothing to commit.
