---
status: draft
covers:
  - docs/specs/032-project-cockpit.md#sec-0
  - docs/specs/032-project-cockpit.md#sec-1
  - docs/specs/032-project-cockpit.md#sec-2
  - docs/specs/032-project-cockpit.md#sec-3
  - docs/specs/032-project-cockpit.md#sec-4
  - docs/specs/032-project-cockpit.md#sec-10
  - docs/specs/032-project-cockpit.md#sec-11
---
# Project Cockpit 1/4: Shell and Projection Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the stable application shell and an honest project Overview that
projects existing Worklode facts into a shared JSON/HTML cockpit without adding
a second status model or pretending that the unimplemented research lifecycle
already exists.

**Architecture:** `internal/store` provides one bulk, UI-neutral project-work
reader. `internal/api/cockpit.go` maps those governed facts into a normalized
read model shared by `GET /api/v1/projects/{id}/cockpit` and
`GET /projects/{id}`; server-rendered templates progressively disclose outcome,
work, and evidence. Embedded CSS and fonts preserve the single-binary deployment
and the existing bearer/session authentication boundaries remain unchanged.

**Tech Stack:** Go 1.26, `database/sql` with Postgres 17 + pgvector,
`net/http`, `html/template`, embedded static assets, Prometheus client, and the
existing Go e2e harness. No new Go, JavaScript, or browser-runtime dependencies.

## Global Constraints

- Do not execute this plan until `docs/specs/032-project-cockpit.md` is
  accepted. It is draft when this plan is written.
- The cockpit is a projection. Add no cockpit table, editable project-health
  field, completion percentage, workflow column, duplicated delivery state, or
  persisted UI mode.
- Preserve the four evidence categories exactly: **declared**,
  **user-reported**, **observed**, and **recommended**. Part 1 has no
  AI-produced recommendation and must not label any current fact recommended.
- Global navigation is, in order: Home, Intake, Projects, Work, Reviews,
  Deliveries, Knowledge. Project navigation is, in order: Overview, Crew,
  Work, Deliverables, Reviews, Decisions, Documents, Activity.
- `/projects/{id}` is the canonical project URL. Query parameters, including
  prototype `variant=A|B|C`, never choose a lifecycle mode.
- Current project rows have no spec-029 intake or stage facts and therefore
  select **Operations**. Editorial decision and Approved launch are pure,
  tested modes that remain unreachable until Part 2 supplies their governed
  inputs.
- Render at most one primary next decision. In Part 1 `next_decision` is
  `null`: blocked and review work may be secondary concerns, but are not
  promoted into a governed decision before specs 028/029 supply decision
  objects and authority.
- The existing `projects.focus` value is task-ranking configuration and must
  be labelled **Ranking focus**. It is not spec 032's pinned governed object;
  `pinned_focus` remains `null` until Part 2 has a project lead who can
  authorize the mutation.
- Missing intake, Crew, deliverable, approval, automation, document-store, and
  Morning Brief capabilities render as explicit unavailable copy with their
  owning spec section. Do not render sample data, fake counts, disabled action
  buttons, or empty widgets that imply the workflow exists.
- Target WCAG 2.2 AA: semantic landmarks, labelled controls, keyboard access,
  visible focus, no colour-only meaning, minimum pointer targets, and a narrow
  layout that uses one reading-order column rather than a compressed grid.
- Use the Sunstone branding skill assets and contrast-safe pairs: Dark Blue
  `#0E1937`, Cool Grey Light `#F4F4F4`, Logo Yellow `#FAD604`, light-mode
  links `#266680`, dark-mode links `#46C5DE`, DM Sans for interface text, and
  Source Serif 4 for headings. Self-host fonts and their OFL licences.
- Normalize JSON collections to `[]`, never `null`. Optional governed objects
  are JSON `null`, never dummy records.
- Every new endpoint or meaningful store read adds or extends a bounded
  `worklode_*` metric with tests. Never label metrics with project or task ids.
- Store tests must run against reachable Postgres with pgvector. A skipped
  local store test is not verification.
- e2e tests create state only through HTTP API, signed webhooks, or other
  public surfaces. They never write directly through `internal/store`.
- This part adds no migration and no web mutation. Existing `webAuth` behavior,
  including open development mode when no provider is configured, is unchanged.
- Run `go test ./... -count=1`, `go vet ./...`, and the tagged e2e suite before
  claiming completion.

---

## File Map

| File | Responsibility |
| --- | --- |
| `internal/store/project_work.go` | Bulk, UI-neutral project task facts: hierarchy, blockers, lease, and current-state provenance. |
| `internal/store/project_work_test.go` | Postgres tests for scoping, ordering, optional facts, and provenance. |
| `internal/store/metrics.go` | Nil-safe metric for the new project-work read. |
| `internal/api/cockpit.go` | Stable cockpit JSON model, lifecycle-mode selection, evidence mapping, and shared assembler. |
| `internal/api/cockpit_internal_test.go` | Pure tests for mode and evidence classification. |
| `internal/api/cockpit_test.go` | Authenticated endpoint and database-backed assembly tests. |
| `internal/api/web.go` | Embedded assets, shell/page data, global/local route handlers, and HTML rendering. |
| `internal/api/server.go` | Cockpit API, web, section, and static-asset routes plus parsed templates. |
| `internal/api/metrics.go` | Bounded cockpit projection and navigation counters. |
| `internal/api/templates/layout.html` | Stable global application shell and shared landmarks. |
| `internal/api/templates/project.html` | Project Overview: outcome, work, decision rail, and evidence disclosure. |
| `internal/api/templates/placeholder.html` | Honest global/project destination placeholder with no simulated state. |
| `internal/api/templates/projects.html` | Cross-project list using canonical cockpit links. |
| `internal/api/assets/app.css` | Sunstone light/dark tokens, responsive cockpit layout, and focus/target rules. |
| `internal/api/assets/fonts/*` | DM Sans and Source Serif 4 variable fonts plus OFL licences. |
| `internal/api/admin.go` | Legacy board assembly refactored onto the same project-work reader. |
| `internal/api/timeline.go` | Preserve source-native URLs in timeline entries used by task evidence. |
| `internal/api/templates/task.html` | Render source links already present in PR/CI timeline facts. |
| `e2e/cockpit_test.go` | Public-surface tracer proving API and HTML agree over real governed events. |
| `e2e/smoke_test.go` | Move board assertions to `/work` and include the new shell routes. |

## Scope and Series Boundary

This is Part 1 of four independently testable releases:

1. shell and projection foundation (this plan);
2. intake, promotion, Approved launch, Enter Research, and Crew;
3. governed deliverables and exact-revision review lanes; and
4. bounded unattended execution, Morning Brief, and the full three-slice
   acceptance journey.

Part 1 deliberately projects only facts present in migrations 0001–0010. It
does not implement domain state owned by specs 025, 028, or 029. Its output is
still useful: every existing engineering project receives a readable,
accessible Operations cockpit, and the later parts extend the assembler's
inputs without replacing the shell, URL, or response shape.

## Spec Coverage

| Spec section | Coverage |
| --- | --- |
| §0 boundary | Tasks 1–5 build one human-facing projection without redefining domain entities. |
| §1 fact/evidence layers | Tasks 1, 3, and 4 define the shared projection, state provenance, and progressive evidence disclosure. |
| §2 navigation | Task 2 implements both navigation systems and canonical URLs. |
| §3 lifecycle modes | Task 1 defines and exhaustively tests all three modes; only Operations is reachable from the current schema. |
| §4 focus/next decision | Task 1 fixes the single nullable decision slot; Task 4 shows secondary blockers. Lead-authorized pinning and governed decisions remain in Part 2. |
| §5 intake/promotion | Part 2; Task 2 provides only an honest Intake destination. |
| §6 people/agents | Task 4 distinguishes owner from delegate; Crew membership and maintenance remain in Part 2. |
| §7 deliverables/review | Task 4 deep-links existing GitHub evidence; governed deliverables and exact-revision approval lanes remain in Part 3. |
| §8 automation | Part 4; Task 2 provides only an honest destination. |
| §9 Home/Morning Brief | Task 2 makes Home the landing destination and labels the current board accurately; Morning Brief state and cursor remain in Part 4. |
| §10 accessibility | Tasks 2 and 5 implement and verify the structural, responsive, and keyboard baseline. |
| §11 first release | Task 5 proves the Part-1 tracer. The connected three-slice journey remains the Part-4 acceptance gate. |
| §12 non-goals | Global constraints forbid dashboards, workflow builders, sprint state, and embedded specialist-tool replacements. |

## Tasks

### Task 1 — Ship the minimal cockpit tracer through API and HTML

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:** create `internal/api/cockpit.go`,
`internal/api/cockpit_internal_test.go`, and `internal/api/cockpit_test.go`;
modify `internal/api/server.go`, `internal/api/web.go`,
`internal/api/templates/project.html`, `internal/api/metrics.go`,
`internal/api/metrics_internal_test.go`, and `internal/api/web_test.go`.

**Interfaces:**

- Consumes: `(*store.Store).GetProject`, `ListRepos`, `ProjectCost`, and the
  existing `(*server).assembleBoard(context.Context, string)`.
- Produces: `cockpitProjection`, `selectMode(modeFacts) cockpitMode`,
  `(*server).assembleProjectCockpit(context.Context, string)
  (*cockpitProjection, error)`, and authenticated
  `GET /api/v1/projects/{id}/cockpit`. Tasks 3 and 4 preserve this wire
  contract while replacing the provisional board-shaped input.

- [ ] **Step 1: Write pure lifecycle-mode tests**

Create `internal/api/cockpit_internal_test.go` with the complete mode table:

```go
func TestSelectMode(t *testing.T) {
    tests := []struct {
        name string
        in   modeFacts
        want cockpitMode
    }{
        {"candidate", modeFacts{IntakeCandidate: true}, modeEditorialDecision},
        {"promoted launch", modeFacts{PromotedFromIntake: true}, modeApprovedLaunch},
        {"entered research", modeFacts{PromotedFromIntake: true, EnteredResearch: true}, modeOperations},
        {"ordinary project", modeFacts{}, modeOperations},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := selectMode(tt.in); got != tt.want {
                t.Fatalf("selectMode(%+v) = %q, want %q", tt.in, got, tt.want)
            }
        })
    }
}
```

- [ ] **Step 2: Run the pure test and verify the missing symbols fail**

Run:

```bash
go test ./internal/api -run TestSelectMode -count=1
```

Expected: compile failure naming `modeFacts`, `cockpitMode`, or `selectMode`.

- [ ] **Step 3: Add the lifecycle types and governed selector**

Start `internal/api/cockpit.go` with these exact names and values:

```go
type cockpitMode string

const (
    modeEditorialDecision cockpitMode = "editorial_decision"
    modeApprovedLaunch    cockpitMode = "approved_launch"
    modeOperations        cockpitMode = "operations"
)

type modeFacts struct {
    IntakeCandidate    bool
    PromotedFromIntake bool
    EnteredResearch    bool
}

func selectMode(f modeFacts) cockpitMode {
    if f.IntakeCandidate {
        return modeEditorialDecision
    }
    if f.PromotedFromIntake && !f.EnteredResearch {
        return modeApprovedLaunch
    }
    return modeOperations
}

// modeFactsForProject remains all-false until Part 2 stores spec-029
// promotion and Enter Research decisions. A current project is therefore an
// ordinary Operations project; query parameters are intentionally absent.
func modeFactsForProject(store.Project) modeFacts { return modeFacts{} }
```

- [ ] **Step 4: Run the pure test and verify it passes**

Run:

```bash
go test ./internal/api -run TestSelectMode -count=1
```

Expected: PASS.

- [ ] **Step 5: Write the authenticated API contract tests**

In `internal/api/cockpit_test.go`, use the existing `newTestServer` fixture to
seed one project and task. Decode the response and assert this normalized
top-level shape (the test should compare typed fields, then marshal once to
check `[]` versus `null`):

```json
{
  "canonical_url": "/projects/proj",
  "project": {"id": "proj", "name": "Project", "key": "WL"},
  "mode": {
    "name": "operations",
    "basis": {
      "category": "declared",
      "summary": "Existing Worklode project; no intake lifecycle facts are present"
    }
  },
  "pinned_focus": null,
  "ranking_focus": [],
  "next_decision": null,
  "work": {
    "in_progress": [],
    "in_review": [],
    "ready": [],
    "blocked": []
  },
  "secondary_concerns": [],
  "repositories": [],
  "cost": {"days": [], "totals": []}
}
```

Add cases for missing bearer token (401), unknown project (404), a ready task,
and forbidden keys `completion_percentage`, `health`, `stage`, and `variant`.

- [ ] **Step 6: Run the endpoint test and verify the route is absent**

Run:

```bash
go test ./internal/api -run 'TestProjectCockpit|TestSelectMode' -count=1
```

Expected: `TestProjectCockpit` fails with 404 while `TestSelectMode` passes.

- [ ] **Step 7: Define the stable projection and provisional assembler**

Use explicit JSON tags and initialized slices. Keep decision and pin as
pointers so the type can never represent multiple primary decisions:

```go
type evidenceSummary struct {
    Category string `json:"category"`
    Summary  string `json:"summary"`
}

type cockpitModeJSON struct {
    Name  cockpitMode     `json:"name"`
    Basis evidenceSummary `json:"basis"`
}

type cockpitProjectJSON struct {
    ID   string `json:"id"`
    Name string `json:"name"`
    Key  string `json:"key"`
}

type actorSummary struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

type focusJSON struct {
    ObjectType string        `json:"object_type"`
    ObjectID   string        `json:"object_id"`
    Note       string        `json:"note"`
    PinnedBy   *actorSummary `json:"pinned_by"`
    PinnedAt   time.Time     `json:"pinned_at"`
}

type decisionActionJSON struct {
    Label  string `json:"label"`
    Effect string `json:"effect"`
    URL    string `json:"url"`
}

type evidenceReferenceJSON struct {
    Label    string `json:"label"`
    URL      string `json:"url"`
    Category string `json:"category"`
}

type decisionJSON struct {
    Title            string                  `json:"title"`
    Accountable      string                  `json:"accountable"`
    Subject          string                  `json:"subject"`
    Readiness        string                  `json:"readiness"`
    Actions          []decisionActionJSON    `json:"actions"`
    Evidence         []evidenceReferenceJSON `json:"evidence"`
    ContraryEvidence []evidenceReferenceJSON `json:"contrary_evidence"`
}

type secondaryConcernJSON struct {
    Kind     string          `json:"kind"`
    Title    string          `json:"title"`
    URL      string          `json:"url"`
    Evidence evidenceSummary `json:"evidence"`
}

type repositoryJSON struct {
    Repo           string          `json:"repo"`
    DoneState      string          `json:"done_state"`
    StatusEvidence evidenceSummary `json:"status_evidence"`
}

type cockpitWorkItem struct {
    ID             string           `json:"id"`
    Title          string           `json:"title"`
    Priority       string           `json:"priority"`
    State          string           `json:"state"`
    Blocked        bool             `json:"blocked"`
    URL            string           `json:"url"`
    Owner          *actorSummary    `json:"owner"`
    Delegate       *actorSummary    `json:"delegate"`
    StatusEvidence evidenceSummary  `json:"status_evidence"`
}

type cockpitWork struct {
    InProgress []cockpitWorkItem `json:"in_progress"`
    InReview   []cockpitWorkItem `json:"in_review"`
    Ready      []cockpitWorkItem `json:"ready"`
    Blocked    []cockpitWorkItem `json:"blocked"`
}

type cockpitProjection struct {
    CanonicalURL      string                `json:"canonical_url"`
    Project           cockpitProjectJSON    `json:"project"`
    Mode              cockpitModeJSON       `json:"mode"`
    PinnedFocus       *focusJSON            `json:"pinned_focus"`
    RankingFocus      []string              `json:"ranking_focus"`
    NextDecision      *decisionJSON         `json:"next_decision"`
    Work              cockpitWork           `json:"work"`
    SecondaryConcerns []secondaryConcernJSON `json:"secondary_concerns"`
    Repositories      []repositoryJSON      `json:"repositories"`
    Cost              projectCostJSON       `json:"cost"`
}
```

In `assembleProjectCockpit`, initialize `RankingFocus`, all four work slices,
`SecondaryConcerns`, `Repositories`, both decision evidence slices when a
decision exists, and both cost slices with concrete empty slices. Adapt the
scoped board as a provisional task source, and map `store.ProjectCost` into an
always-present cost object with empty `days` and `totals` arrays.

- [ ] **Step 8: Register the API and shared web assembly path**

Add this route beside the existing project API routes:

```go
mux.Handle("GET /api/v1/projects/{id}/cockpit", s.auth(s.projectCockpit))
```

The handler calls `assembleProjectCockpit`, maps store errors with
`mapStoreErr`, and writes the projection with `writeJSON`. Change
`projectPage` to call the same assembler; it must not call its own HTTP API.

- [ ] **Step 9: Add the bounded projection metric**

Extend the current API metric owner with
`worklode_cockpit_projection_requests_total{surface,outcome}`. Values are
exactly `surface=api|web` and `outcome=ok|not_found|error`:

```go
func cockpitOutcome(err error) string {
    if err == nil {
        return "ok"
    }
    if errors.Is(err, store.ErrNotFound) {
        return "not_found"
    }
    return "error"
}

func (s *server) observeCockpitProjection(surface string, err error) {
    if s.cockpitProjections == nil {
        return
    }
    s.cockpitProjections.WithLabelValues(surface, cockpitOutcome(err)).Inc()
}
```

Observe exactly once per attempted projection in API and web handlers. Add
`prometheus/testutil` assertions for registration, success, not-found, and
error; do not add id labels.

- [ ] **Step 10: Render the minimal truthful Overview**

Update `project.html` to render the project name/key, Operations mode and its
basis, linked work groups, repository facts, and one decision rail containing
“No governed decision is ready.” Add:

```html
<link rel="canonical" href="{{.Cockpit.CanonicalURL}}">
<aside aria-label="Next decision">
  {{if .Cockpit.NextDecision}}
    <h2>{{.Cockpit.NextDecision.Title}}</h2>
  {{else}}
    <h2>Next decision</h2>
    <p>No governed decision is ready.</p>
  {{end}}
</aside>
```

Do not render a stage, Crew, deliverable, approval, percentage, or automation
sample.

- [ ] **Step 11: Run focused API and web tests**

Run:

```bash
go test ./internal/api -run 'Cockpit|ProjectPage|Metrics|SelectMode' -count=1
```

Expected: PASS against reachable Postgres.

- [ ] **Step 12: Commit the tracer**

```bash
git add internal/api/cockpit.go internal/api/cockpit_internal_test.go internal/api/cockpit_test.go internal/api/server.go internal/api/web.go internal/api/templates/project.html internal/api/metrics.go internal/api/metrics_internal_test.go internal/api/web_test.go
git commit -m "feat: add the project cockpit tracer"
```

### Task 2 — Establish the accessible application and project shell

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - sunstone-core:branding
blockedBy: [1]
```

**Files:** create `internal/api/assets/app.css`,
`internal/api/assets/fonts/dm-sans-variable.ttf`,
`internal/api/assets/fonts/source-serif-4-variable.ttf`, both `OFL.txt` files,
`internal/api/templates/placeholder.html`, and
`internal/api/templates/projects.html`; modify `internal/api/web.go`,
`internal/api/server.go`, `internal/api/templates/layout.html`,
`internal/api/templates/project.html`, `internal/api/templates/board.html`,
`internal/api/metrics.go`, `internal/api/metrics_internal_test.go`,
`internal/api/web_test.go`, and `e2e/smoke_test.go`.

**Interfaces:**

- Consumes: Task 1's `cockpitProjection`, current `boardPage`,
  `(*store.Store).ListProjects`, and the installed branding skill assets.
- Produces: embedded `/assets/` files, `basePage.ActiveGlobal`, all seven
  global destinations, the eight-item project navigation, and allow-listed
  honest placeholder pages. Task 4 fills the Overview body without changing
  the shell contract.

- [ ] **Step 1: Write structural shell tests first**

Add a shared helper to `web_test.go`:

```go
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
```

Table-test `/`, `/intake`, `/projects`, `/work`, `/reviews`, `/deliveries`,
and `/knowledge`; each must return 200, pass `assertShell`, and set exactly one
`aria-current="page"`. Add tests for every project section, unknown section
404, unknown project 404, and `?variant=A|B|C` leaving the mode unchanged.

- [ ] **Step 2: Write asset and responsive-contract tests**

Test `/assets/app.css` and both fonts without authentication. Assert CSS
content type, a bounded public cache header, and these exact strings:

```go
for _, want := range []string{
    "#0E1937", "#F4F4F4", "#FAD604", "#266680", "#46C5DE",
    "prefers-color-scheme: dark", ":focus-visible", "min-height: 44px",
    "@media (max-width: 64rem)",
} {
    if !strings.Contains(css, want) {
        t.Errorf("stylesheet missing %q", want)
    }
}
```

- [ ] **Step 3: Run the shell tests and verify new routes fail**

Run:

```bash
go test ./internal/api -run 'Shell|Navigation|Assets|ProjectSection' -count=1
```

Expected: failures for unregistered routes and missing embedded assets.

- [ ] **Step 4: Embed and serve the assets**

Extend `web.go`:

```go
//go:embed templates/*.html assets
var webFS embed.FS

func (s *server) assetHandler() http.Handler {
    assets, err := fs.Sub(webFS, "assets")
    if err != nil {
        panic(err)
    }
    return http.StripPrefix("/assets/", http.FileServer(http.FS(assets)))
}
```

Register `GET /assets/` outside `webAuth`; styles and fonts contain no project
data and must not redirect to login. Wrap the handler to set
`Cache-Control: public, max-age=3600` and record the navigation metric from
Step 9.

- [ ] **Step 5: Copy the approved font assets and licences**

Copy exactly these installed skill files into the named repository paths:

```text
assets/fonts/DM_Sans/DMSans-VariableFont_opsz,wght.ttf
assets/fonts/DM_Sans/OFL.txt
assets/fonts/Source_Serif_4/SourceSerif4-VariableFont_opsz,wght.ttf
assets/fonts/Source_Serif_4/OFL.txt
```

Rename the TTF destinations to `dm-sans-variable.ttf` and
`source-serif-4-variable.ttf`; keep distinct licence filenames
`dm-sans-OFL.txt` and `source-serif-4-OFL.txt`.

- [ ] **Step 6: Move brand tokens and layout rules into `app.css`**

Start from the branding skill's `examples/sunstone-theme.css`. Use these font
faces and the spec's responsive breakpoint:

```css
@font-face {
  font-family: "DM Sans";
  src: url("/assets/fonts/dm-sans-variable.ttf") format("truetype-variations");
  font-weight: 100 1000;
  font-style: normal;
  font-display: swap;
}
@font-face {
  font-family: "Source Serif 4";
  src: url("/assets/fonts/source-serif-4-variable.ttf") format("truetype-variations");
  font-weight: 200 900;
  font-style: normal;
  font-display: swap;
}
.skip-link:focus { transform: translateY(0); }
:focus-visible { outline: 3px solid var(--sunstone-yellow); outline-offset: 3px; }
a, button, input, select, summary { min-height: 44px; }
.cockpit-grid { display: grid; grid-template-columns: 14rem minmax(0, 1fr) 20rem; }
@media (max-width: 64rem) {
  .cockpit-grid { display: flex; flex-direction: column; }
  .project-nav ul { display: flex; flex-wrap: wrap; }
}
```

Use light teal links and dark cyan links exactly as the branding skill
requires. Every badge contains visible text; colour is decorative only.

- [ ] **Step 7: Replace `layout.html` with the stable shell**

The common template must contain one primary nav and one main landmark:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <link rel="stylesheet" href="/assets/app.css">
</head>
<body>
  <a class="skip-link" href="#main-content">Skip to content</a>
  <header>
    <nav aria-label="Primary">
      <a href="/" {{if eq .ActiveGlobal "home"}}aria-current="page"{{end}}>Home</a>
      <a href="/intake" {{if eq .ActiveGlobal "intake"}}aria-current="page"{{end}}>Intake</a>
      <a href="/projects" {{if eq .ActiveGlobal "projects"}}aria-current="page"{{end}}>Projects</a>
      <a href="/work" {{if eq .ActiveGlobal "work"}}aria-current="page"{{end}}>Work</a>
      <a href="/reviews" {{if eq .ActiveGlobal "reviews"}}aria-current="page"{{end}}>Reviews</a>
      <a href="/deliveries" {{if eq .ActiveGlobal "deliveries"}}aria-current="page"{{end}}>Deliveries</a>
      <a href="/knowledge" {{if eq .ActiveGlobal "knowledge"}}aria-current="page"{{end}}>Knowledge</a>
    </nav>
  </header>
  <main id="main-content">{{template "content" .}}</main>
</body>
</html>
```

Remove the 30-second meta refresh. Unannounced whole-page replacement is not
compatible with this shell's accessibility contract.

- [ ] **Step 8: Register global and local destinations**

Add `ActiveGlobal string` to `basePage`. Keep `/` as Home and render the
existing board under “Current work”; `/work` renders the same board as the
task-oriented destination. `/projects` lists `ListProjects` results with
canonical cockpit links.

Use one allow-list for project sections:

```go
var projectSections = map[string]string{
    "crew": "Crew arrives with project participants in spec 029 §6.1.",
    "deliverables": "Deliverables arrive with spec 029 §7.",
    "reviews": "Governed approval reviews arrive with spec 029 §7.",
    "decisions": "Research decisions arrive with specs 028 and 029.",
    "documents": "Backbone documents arrive with specs 025 and 026.",
    "activity": "Project activity arrives when the ordered event view is implemented.",
}
```

Unknown keys return 404. Each placeholder loads the project first, renders the
same project header/navigation, names the missing capability, and has no
`form`, `button`, count, or fake record.

- [ ] **Step 9: Add the bounded navigation metric**

Register `worklode_web_navigation_requests_total{destination,outcome}` with
destinations exactly `home|intake|projects|work|reviews|deliveries|knowledge|project_section|asset`
and outcomes `ok|not_found|error`. Add a nil-safe observer and tests for one
successful route, one missing project section, and one asset response.

- [ ] **Step 10: Run API and updated smoke tests**

Run:

```bash
go test ./internal/api -count=1
go test -race -count=1 -tags e2e ./e2e/ -run TestSmoke
```

Expected: PASS. Existing root-board assertions now target `/work`; `/` asserts
the Home heading and shared shell.

- [ ] **Step 11: Perform the manual accessibility baseline**

At 1280px, 768px, and 360px verify: no horizontal page scroll; skip link is
first in tab order and becomes visible; both navigation landmarks are distinct;
focus remains visible; targets are usable; light/dark themes keep approved
contrast pairs; headings use Source Serif 4 and UI text uses DM Sans.

- [ ] **Step 12: Commit the shell**

```bash
git add internal/api/assets internal/api/templates internal/api/web.go internal/api/server.go internal/api/metrics.go internal/api/metrics_internal_test.go internal/api/web_test.go e2e/smoke_test.go
git commit -m "feat: add the accessible cockpit shell"
```

### Task 3 — Add bulk project-work facts and status provenance

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:** create `internal/store/project_work.go` and
`internal/store/project_work_test.go`; modify `internal/store/metrics.go`,
`internal/store/metrics_test.go`, `internal/api/admin.go`,
`internal/api/admin_test.go`, and `internal/api/cockpit.go`.

**Interfaces:**

- Consumes: `store.Task`, `store.Lease`, `blockedCondition`, `closedStates`,
  task ordering from `ListTasks`, `state_log`, and `events`.
- Produces: `store.EventFact`, `store.TaskRef`, `store.ProjectWorkFact`, and
  `(*store.Store).ListProjectWorkFacts(context.Context, string)
  ([]store.ProjectWorkFact, error)`. Task 4 is the only product-language
  mapper; the store types remain UI-neutral.

- [ ] **Step 1: Write failing store tests for the complete read contract**

In `project_work_test.go`, seed two projects through existing test helpers and
assert:

```go
facts, err := st.ListProjectWorkFacts(ctx, "project-a")
if err != nil { t.Fatal(err) }
if len(facts) != 3 { t.Fatalf("len = %d, want 3", len(facts)) }
if facts[0].Task.ID != criticalID { t.Errorf("first = %s, want %s", facts[0].Task.ID, criticalID) }
if facts[1].Parent == nil || facts[1].Parent.ID != parentID { t.Errorf("parent = %#v", facts[1].Parent) }
if len(facts[2].OpenBlockers) != 1 || facts[2].OpenBlockers[0].ID != blockerID { t.Errorf("blockers = %#v", facts[2].OpenBlockers) }
if facts[0].Lease == nil || facts[0].Lease.ActorID != agentID { t.Errorf("lease = %#v", facts[0].Lease) }
if facts[0].StateEvent == nil || facts[0].StateEvent.Source != "github" { t.Errorf("state event = %#v", facts[0].StateEvent) }
```

Add cases proving `projectID == ""` returns both projects; a new task has
`StateEvent == nil`; the newest state row wins by `at DESC, id DESC`; a closed
blocker disappears; and released leases are absent.

- [ ] **Step 2: Run the store tests and verify the method is missing**

Run against reachable pgvector Postgres:

```bash
go test ./internal/store -run TestListProjectWorkFacts -count=1
```

Expected: compile failure for `ListProjectWorkFacts` and its types.

- [ ] **Step 3: Define the UI-neutral fact types**

Create `project_work.go`:

```go
type EventFact struct {
    ID     int64
    Source string
    Type   string
    At     time.Time
}

type TaskRef struct {
    ID    string
    Title string
    State string
}

type ProjectWorkFact struct {
    Task         Task
    Parent       *TaskRef
    OpenBlockers []TaskRef
    Lease        *Lease
    StateEvent   *EventFact
}

func (f ProjectWorkFact) Blocked() bool { return len(f.OpenBlockers) > 0 }
```

- [ ] **Step 4: Implement the bounded bulk reader**

Run the task/parent/lease/state-event query and blocker query in one read
transaction. Reuse the exact task column order from `scanTask`; preserve
`ListTasks` ordering:

```sql
SELECT t.id, t.project_id, t.title, t.body, t.priority, t.kind, t.state,
       t.concern, t.assignee, t.needs_decomposition, t.created_by,
       t.created_at, t.updated_at, t.skills,
       parent.id, parent.title, parent.state,
       l.id, l.task_id, l.actor_id, l.worktree, l.acquired_at, l.renewed_at,
       l.expires_at, l.released_at,
       se.id, se.source, se.type, se.at
FROM tasks t
LEFT JOIN task_edges pe
  ON pe.from_task = t.id AND pe.type = 'child_of'
LEFT JOIN tasks parent ON parent.id = pe.to_task
LEFT JOIN leases l ON l.task_id = t.id AND l.released_at IS NULL
LEFT JOIN LATERAL (
  SELECT sl.id, e.source, e.type, sl.at
  FROM state_log sl
  JOIN events e ON e.id = sl.event_id
  WHERE sl.entity_kind = 'task'
    AND sl.entity_id = t.id
    AND sl.change->>'field' = 'state'
  ORDER BY sl.at DESC, sl.id DESC
  LIMIT 1
) se ON true
WHERE ($1 = '' OR t.project_id = $1)
ORDER BY CASE t.priority
           WHEN 'critical' THEN 0 WHEN 'high' THEN 1
           WHEN 'medium' THEN 2 ELSE 3
         END,
         split_part(t.id, '-', 1),
         CAST(split_part(t.id, '-', 2) AS INTEGER);
```

The second query selects open `blocks` edges for the same project using
`closedStates`; append `TaskRef`s to a map keyed by dependent id, then assign
`OpenBlockers` as initialized empty slices. Return empty `[]`, not nil.

- [ ] **Step 5: Add the store-read metric**

Extend `storeMetrics` with
`worklode_project_work_reads_total{outcome="ok"|"error"}`. Observe once when
`ListProjectWorkFacts` returns; keep the method nil-safe:

```go
func (m *storeMetrics) projectWorkRead(err error) {
    if m == nil { return }
    outcome := "ok"
    if err != nil { outcome = "error" }
    m.projectWorkReads.WithLabelValues(outcome).Inc()
}
```

Test both labels with `prometheus/testutil` and ensure no project label exists.

- [ ] **Step 6: Run the store tests and verify all fact cases pass**

```bash
go test ./internal/store -run 'ProjectWork|Metrics' -count=1
```

Expected: PASS against reachable Postgres.

- [ ] **Step 7: Refactor the legacy board onto the shared reader**

Replace the `ListTasks` + `BlockedTaskIDs` + per-task parent/lease assembly in
`assembleBoard` with `ListProjectWorkFacts`. Preserve `boardResponse`, bucket
names, task ordering, holder strings, parent ids, and store-wide
`RecentFailures`. Project cockpit code must not copy `RecentFailures`: runtime
events cannot currently be scoped to a project.

- [ ] **Step 8: Run board regression tests**

```bash
go test ./internal/api -run 'Board|Cockpit' -count=1
```

Expected: existing board JSON and HTML tests pass unchanged, and cockpit API
tests still pass with the new reader.

- [ ] **Step 9: Commit the shared fact reader**

```bash
git add internal/store/project_work.go internal/store/project_work_test.go internal/store/metrics.go internal/store/metrics_test.go internal/api/admin.go internal/api/admin_test.go internal/api/cockpit.go
git commit -m "feat: add project work facts with provenance"
```

### Task 4 — Complete accountability and evidence disclosure

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3]
```

**Files:** modify `internal/api/cockpit.go`,
`internal/api/cockpit_internal_test.go`, `internal/api/cockpit_test.go`,
`internal/api/timeline.go`, `internal/api/timeline_test.go`,
`internal/api/web.go`, `internal/api/templates/project.html`,
`internal/api/templates/task.html`, and `internal/api/web_test.go`.

**Interfaces:**

- Consumes: Task 3's `ListProjectWorkFacts`, `(*store.Store).GetActor`,
  `ListRepos`, `ProjectCost`, and existing timeline PR/CI facts.
- Produces: final Part-1 `cockpitProjection` values for owner, delegate,
  evidence, blockers, Ranking focus, repositories, and cost; source-native
  links on task evidence. Task 5 treats this API/HTML behavior as fixed.

- [ ] **Step 1: Write exhaustive evidence-classification tests**

Add these cases to `cockpit_internal_test.go`:

```go
func TestStateEvidence(t *testing.T) {
    tests := []struct {
        source, eventType string
        hasEvent          bool
        want              evidenceCategory
    }{
        {"", "", false, evidenceDeclared},
        {"github", "pull_request", true, evidenceObserved},
        {"flux", "kustomization.applied", true, evidenceObserved},
        {"watcher", "pod.crashloop", true, evidenceObserved},
        {"system", "lease.expired", true, evidenceObserved},
        {"cli", "lease.claimed", true, evidenceObserved},
        {"cli", "task.started", true, evidenceUserReported},
        {"cli", "task.stopped", true, evidenceUserReported},
        {"cli", "task.updated", true, evidenceUserReported},
    }
    for _, tt := range tests {
        if got := stateEvidence(tt.source, tt.eventType, tt.hasEvent); got != tt.want {
            t.Errorf("stateEvidence(%q, %q, %v) = %q, want %q", tt.source, tt.eventType, tt.hasEvent, got, tt.want)
        }
    }
}
```

Also pin display labels exactly `Declared`, `User-reported`, `Observed`, and
`Recommended`.

- [ ] **Step 2: Run the evidence test and verify it fails**

```bash
go test ./internal/api -run TestStateEvidence -count=1
```

Expected: compile failure for `evidenceCategory` or `stateEvidence`.

- [ ] **Step 3: Implement the evidence enum and mapping**

```go
type evidenceCategory string

const (
    evidenceDeclared     evidenceCategory = "declared"
    evidenceUserReported evidenceCategory = "user_reported"
    evidenceObserved     evidenceCategory = "observed"
    evidenceRecommended  evidenceCategory = "recommended"
)

func stateEvidence(source, eventType string, hasEvent bool) evidenceCategory {
    if !hasEvent { return evidenceDeclared }
    switch source {
    case "github", "flux", "watcher", "system":
        return evidenceObserved
    case "cli":
        if strings.HasPrefix(eventType, "lease.") {
            return evidenceObserved
        }
        return evidenceUserReported
    default:
        return evidenceDeclared
    }
}
```

`Label()` maps `user_reported` to `User-reported`; do not derive labels by
replacing underscores.

- [ ] **Step 4: Write owner/delegate and normalized-collection tests**

Seed human Dana as task assignee and Agent One as lease holder. Assert Dana is
the owner and Agent One the delegate. Repeat with a human lease holder and
assert `delegate == null`. Add cases for a missing display name falling back to
actor id, blocked task references, an observed GitHub state, a user-reported
human start, no cost rows, and no repos.

- [ ] **Step 5: Resolve actors once and assemble work from bulk facts**

Use one per-projection cache:

```go
actors := map[string]*store.Actor{}
resolveActor := func(id string) (*store.Actor, error) {
    if id == "" { return nil, nil }
    if a, ok := actors[id]; ok { return a, nil }
    a, err := s.st.GetActor(ctx, id)
    if err != nil { return nil, err }
    actors[id] = a
    return a, nil
}
```

`owner` comes only from `Task.Assignee`. `delegate` comes only from an
unreleased lease whose actor kind is `agent`. A human or service lease remains
technical evidence and is never labelled a delegate or Crew member.

- [ ] **Step 6: Fill the three disclosure layers without inventing state**

Map bulk facts into Running (`in_progress`), Awaiting review (`in_review`),
Ready (`ready`), and Blocked groups. Each row includes textual state, evidence
category, exact source/type/id/time when present, owner, delegate, and task URL.
Use open blocker refs as secondary concerns; leave `next_decision` and
`pinned_focus` nil.

Map repositories as declared `repo` + `done_state` facts. Map
`ProjectCost(ctx, id, now-30d, now)` without recomputing totals and include
`unpriced_tokens`. Initialize all nested collections before returning.

- [ ] **Step 7: Render outcome, work, evidence, and decision rail**

Use semantic regions in `project.html`:

```html
<div class="cockpit-grid">
  <nav class="project-nav" aria-label="Project">{{template "project-nav" .}}</nav>
  <div class="cockpit-canvas">
    <section aria-labelledby="outcome-heading">
      <h1 id="outcome-heading">{{.Cockpit.Project.Name}}</h1>
      <p>{{.Cockpit.Project.Key}} · Operations</p>
      <p>{{.Cockpit.Mode.Basis.Summary}}</p>
    </section>
    <section id="work" aria-labelledby="work-heading">
      <h2 id="work-heading">Current work</h2>
      <h3>Running</h3>
      <ul>{{range .Cockpit.Work.InProgress}}<li><a href="{{.URL}}">{{.ID}} — {{.Title}}</a>: {{.State}} ({{.StatusEvidence.Category}})</li>{{else}}<li>None</li>{{end}}</ul>
      <h3>Awaiting review</h3>
      <ul>{{range .Cockpit.Work.InReview}}<li><a href="{{.URL}}">{{.ID}} — {{.Title}}</a>: {{.State}} ({{.StatusEvidence.Category}})</li>{{else}}<li>None</li>{{end}}</ul>
      <h3>Ready</h3>
      <ul>{{range .Cockpit.Work.Ready}}<li><a href="{{.URL}}">{{.ID}} — {{.Title}}</a>: {{.State}} ({{.StatusEvidence.Category}})</li>{{else}}<li>None</li>{{end}}</ul>
      <h3>Blocked</h3>
      <ul>{{range .Cockpit.Work.Blocked}}<li><a href="{{.URL}}">{{.ID}} — {{.Title}}</a>: {{.State}} ({{.StatusEvidence.Category}})</li>{{else}}<li>None</li>{{end}}</ul>
    </section>
    <details>
      <summary>Evidence</summary>
      <p>Ranking focus: {{range .Cockpit.RankingFocus}}{{.}} {{else}}none{{end}}</p>
      <ul>{{range .Cockpit.Repositories}}<li>{{.Repo}}: {{.DoneState}} ({{.StatusEvidence.Category}})</li>{{else}}<li>No mapped repositories</li>{{end}}</ul>
      <a href="/projects/{{.Cockpit.Project.ID}}/activity">Open project activity</a>
    </details>
  </div>
  <aside aria-label="Next decision">
    <h2>Next decision</h2>
    {{if .Cockpit.NextDecision}}
      <p>{{.Cockpit.NextDecision.Title}}</p>
      <p>Accountable: {{.Cockpit.NextDecision.Accountable}}</p>
      <p>{{.Cockpit.NextDecision.Readiness}}</p>
    {{else}}
      <p>No governed decision is ready.</p>
    {{end}}
  </aside>
</div>
```

Visible copy distinguishes “Owned by Dana” from “Agent One is the delegate.”
Evidence shows exact event source/type/id/time, declared Ranking focus,
repository done state, and cost/unpriced tokens. The page must contain neither
`%` nor the phrases “project health” and “completion”.

- [ ] **Step 8: Preserve source-native task evidence links**

Add `URL string` to `webTimelineRow`. In `summarizeEntry`, set it from the
existing timeline object's `url` field for PR and CI entries. Render only
non-empty links:

```html
{{if .URL}}<a href="{{.URL}}" rel="noreferrer">Open source</a>{{end}}
```

Test a hostile-looking URL value through `html/template` and assert it is
escaped or rejected as an unsafe scheme; never use `template.URL` casts.

- [ ] **Step 9: Run focused assembly, rendering, and timeline tests**

```bash
go test ./internal/api -run 'Cockpit|ProjectPage|StateEvidence|Timeline|Shell' -count=1
```

Expected: PASS. API and HTML agree on project, mode, owner, delegate, blockers,
and evidence category; all empty JSON collections encode as `[]`.

- [ ] **Step 10: Re-run project cost and board contracts**

```bash
go test ./internal/store -run 'ProjectCost|ProjectWork' -count=1
go test ./internal/api -run Board -count=1
```

Expected: PASS; no duplicated cost arithmetic or board behavior change.

- [ ] **Step 11: Commit accountability and evidence disclosure**

```bash
git add internal/api/cockpit.go internal/api/cockpit_internal_test.go internal/api/cockpit_test.go internal/api/timeline.go internal/api/timeline_test.go internal/api/web.go internal/api/templates/project.html internal/api/templates/task.html internal/api/web_test.go
git commit -m "feat: disclose cockpit accountability and evidence"
```

### Task 5 — Prove the cockpit tracer through public surfaces

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - superpowers:verification-before-completion
blockedBy: [4]
```

**Files:** create `e2e/cockpit_test.go`; modify `e2e/smoke_test.go`,
`CLAUDE.md`, and `docs/follow-ups.md`.

**Interfaces:**

- Consumes: Tasks 1–4's authenticated cockpit API, signed GitHub webhook,
  project Overview, shell routes, and embedded assets.
- Produces: `TestProjectCockpitPublicSurface`, the durable public-surface
  acceptance test for Part 1, plus short documentation of the shipped shell
  and deliberately deferred browser/assistive-technology checks.

- [ ] **Step 1: Write the failing e2e scenario using only public writes**

Create `e2e/cockpit_test.go` with the existing `//go:build e2e` tag. Reuse the
suite's bootstrap, API, and signed-webhook helpers. Through the API:

1. create project `proj` with key `WL` and map `acme/app`;
2. create human actor `dana`, agent actor `agent-one`, and their bearer tokens;
3. create one task assigned to Dana, let Agent One claim it, then deliver a
   signed `pull_request` event that moves it to review;
4. create a blocker and dependent task and add their `blocks` edge.

Do not call `store.CreateTask`, `store.RecordEvent`, or another store writer
from this test.

- [ ] **Step 2: Assert the cockpit API contract**

Use Dana's bearer token to GET `/api/v1/projects/proj/cockpit` and assert:

```go
if got.Mode.Name != "operations" { t.Fatalf("mode = %q", got.Mode.Name) }
if got.CanonicalURL != "/projects/proj" { t.Fatalf("canonical = %q", got.CanonicalURL) }
if got.PinnedFocus != nil || got.NextDecision != nil { t.Fatalf("unexpected governed object") }
if item.Owner == nil || item.Owner.ID != "dana" { t.Fatalf("owner = %#v", item.Owner) }
if item.Delegate == nil || item.Delegate.ID != "agent-one" { t.Fatalf("delegate = %#v", item.Delegate) }
if item.StatusEvidence.Category != "observed" { t.Fatalf("evidence = %#v", item.StatusEvidence) }
```

Assert the dependent task appears in `work.blocked` and in the secondary
concerns, while every empty list decodes from JSON `[]`.

- [ ] **Step 3: Assert the browser-style Overview and canonical behavior**

GET `/projects/proj` with the configured web session fixture. Assert two
distinct navigation landmarks, one main landmark, one decision rail, Dana as
owner, Agent One as delegate, Observed status evidence, blocker copy, an
evidence `<details>`, and a source link.

GET `/projects/proj?variant=A`, `B`, and `C`; each must still render Operations
and the canonical link `/projects/proj`. Reject `%`, “project health”, fake
Crew/deliverable/approval records, and prototype controls.

- [ ] **Step 4: Assert honest destinations and embedded assets**

GET `/projects/proj/crew`, `/projects/proj/deliverables`, `/intake`, and
`/knowledge`. Each returns 200 with its owning-spec sentence and contains no
`<form` or `<button`. GET `/assets/app.css` and both font paths; each returns
200 without an authentication redirect.

- [ ] **Step 5: Run the e2e test and verify it fails before implementation is complete**

```bash
CI=1 go test -race -count=1 -tags e2e ./e2e/ -run TestProjectCockpitPublicSurface
```

Expected before Tasks 1–4: FAIL on the missing cockpit endpoint or shell.
Expected after Tasks 1–4: PASS.

- [ ] **Step 6: Align short repository documentation**

In `CLAUDE.md`, change the architecture description of `internal/api` from an
“unauthenticated read-only web UI” to “a session-gated, read-mostly project
cockpit; development mode remains open when no provider is configured.”

In `docs/follow-ups.md`, add these concrete deferred checks:

- full WCAG 2.2 AA assistive-technology walkthrough at the four-part series
  acceptance;
- browser-rendered 1280px/768px/360px regression automation if the CSS
  contract test proves insufficient;
- replace Part 1's unavailable pages as Parts 2–4 land;
- wire mode facts and project-lead-authorized pinned focus from spec 029.

- [ ] **Step 7: Run the focused unit and store suites against Postgres**

```bash
CI=1 go test ./internal/store ./internal/api -count=1
```

Expected: PASS with no skips.

- [ ] **Step 8: Run the full verification gate**

```bash
go test ./... -count=1
go test -race -count=1 -tags e2e ./e2e/
go vet ./...
go build ./...
./scripts/check-migrations.sh --no-fix
./scripts/secfmt.py -l
```

Expected: every command exits 0. `check-migrations` confirms Part 1 added no
migration; `secfmt` confirms no design-document anchor drift.

- [ ] **Step 9: Perform the final manual responsive and keyboard pass**

At 1280px, 768px, and 360px verify the canonical project journey: global nav,
project nav, outcome, decision rail, work, and evidence remain in reading
order; no horizontal compression hides the primary workflow; all interactive
elements work by keyboard; asynchronous behavior is absent in this read-only
slice, so there is no unannounced result to test.

- [ ] **Step 10: Commit the public acceptance proof**

```bash
git add e2e/cockpit_test.go e2e/smoke_test.go CLAUDE.md docs/follow-ups.md
git commit -m "test: prove the cockpit public-surface tracer"
```
