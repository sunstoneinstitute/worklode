---
status: accepted
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-3
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-1-milestones.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-3.1
    coverage: full
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-3.2
    coverage: full
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-3.3
    coverage: full
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-8.3
    coverage: full
blockedBy:
  - 2026-08-25-research-work-1-milestones.md
---

# Research work part 4 — deliverable state: labels, the prober, user-reported facts, and new ingest sources

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** finish spec 029 §3. Deliverables exist and the push half of §3.2
works (the signed data-catalog ingest files `artifact_evidence` against a
declared address), but three pieces `docs/follow-ups.md` names honestly are
still absent, and this plan closes them: identity **by label** (§3.1's
`worklode.deliverable=COW/datasets`, for artifacts whose address is minted at
build time), the **poll prober** (§3.2's separate process for addresses
nothing pushes about), and the **`user_reported` write path** (schema-legal
since migration 0040, written by nothing). It also adds §8.3's new ingest
sources — CMS publish transitions, CI publish reports, pipeline dataset
registrations — each a new `events.source` value with its own signed endpoint,
with the CMS one required to carry *who approved and published*. And it adds
the missing CLI verb: `lode deliverable list/add/report`.

**The load-bearing property** (029 §3.2): deliverable state is *reported,
never asserted by a human closing a task*, and every fact retains how it
became known. **Observed** (an emitter or the prober) and **user-reported**
(an authenticated actor typing what they know, where no integration exists)
stay visibly different — a user-reported fact is auditable but must not
masquerade as independent verification. Declared intent, reported state, and
an AI recommendation remain three different things. And "is the project
published" stays a query — every deliverable published means the project is
published — with no column storing it, in this plan or ever.

**Series:** part 4 (P6) of the 2026-08-25 research-work series planning spec
029. `2026-08-25-research-work-1-milestones` (P1) blocks this plan and owns
the rest of §3's aggregate: the `milestones` table, `deliverables.milestone_id`,
and the milestone-progress query — which is why §3 above is `partial` with
`fullCoverageWith` naming P1, while §3.1/§3.2/§3.3 are `full` here.

**Coverage and deferral, declared:** §8.3 is `full` for everything this
repository owns — the sources, the endpoints, the auth, the idempotency, the
CMS person requirement. The *emitting* side is other repos' work: the CMS
emitter is deferred to `sunstone-cms` (the `defers:` entry above), and the
pipeline emitter lives in the data-platform repo (no second `defers:` target
is declared, so this sentence is the record). Probing as *verification* of
reported state — reconciling a claim against production — is on spec 007's v2
line and out of scope here, per 029 §9.

**Tech stack:** Go 1.27, `net/http` mux, pgx against Postgres,
`templ`-rendered pages, cobra CLI, Prometheus client, kustomize manifests.
Store and `internal/api` tests need Postgres with pgvector.

**Read first:**
- `docs/specs/inlined/029-research-work-in-the-backbone.md` §3, §3.1–§3.3,
  §8.3 (the consolidated view, not the raw spec)
- `deploy/base/migrations/0040_catalog_evidence.up.sql` — the schema this
  plan extends: `artifact_declarations`, `artifact_evidence`, and the
  `events.source` CHECK
- `internal/store/artifactevidence.go` — `DeclareArtifact`,
  `OpenDeclarationsForArtifact`, `InsertArtifactEvidence`
- `internal/store/deliverables.go` — `CreateDeliverable`, `deliverableFrom`
  (the chained-LATERAL projection), `scanDeliverable`
- `internal/hooks/catalog.go` — the signed ingest this plan parameterizes;
  its file-top contract comment is the payload shape
- `internal/api/runtime.go` — `createRuntimeEvent`, the bearer-token +
  `dedupe_key` ingest pattern the prober's report endpoint follows
- `internal/watch/watcher.go` + `cmd/lode-watch/main.go` — the deployment
  pattern (own process, `-server`/`-token` flags, `HTTPReporter`) the prober
  follows; the pod informer itself is untouched
- `internal/cmd/project.go` (`newProjectCrewCmd`) and `internal/cli/render.go`
  — the CLI verb pattern and the decides/renders seam

## Global Constraints

- **Exact spellings, quoted once.** Evidence states:
  `'published'`, `'updated'`, `'deprecated'`, `'removed'`, `'failed'`.
  Provenance: `'observed'`, `'user_reported'`. The UI renders the provenance
  distinction as `User-reported` exactly — "human-reported" is not product
  language, and observed rows add no text (verification is the default the
  chip already claims). Declaration selectors: `'address'`, `'label'`. The
  label key is `worklode.deliverable`; the stored selector string is
  `worklode.deliverable=<KEY>/<slug>`, e.g. `worklode.deliverable=COW/datasets`.
  New `events.source` values: `'cms'`, `'ci'`, `'pipeline'`, `'prober'`.
  Webhook secrets: `LODE_CMS_WEBHOOK_SECRET`, `LODE_CI_WEBHOOK_SECRET`,
  `LODE_PIPELINE_WEBHOOK_SECRET`. Delivery headers: `X-CMS-Delivery`,
  `X-CI-Delivery`, `X-Pipeline-Delivery`. Routes:
  `POST /api/v1/deliverables/{id}/report`, `POST /deliverables/{id}/report`
  (web), `GET /api/v1/probe-targets`, `POST /api/v1/artifact-reports`,
  `POST /hooks/cms|ci|pipeline`. Event types: `deliverable.reported` (user
  report), `prober.<state>` (prober), `<source>.<event-or-state>` (signed
  ingests, as the catalog already does).
- **No state column, ever.** Deliverables store identity and description
  only; state is the newest `artifact_evidence` row, joined on read
  (`deliverableFrom` stays the only reader). "Is the project published" is a
  query over that join; no task in this plan may add a status column to
  `deliverables` or `projects`.
- **Migrations:** this plan owns numbers **0062 and 0063** in the series'
  allocation. Each is a new numbered `.up.sql`/`.down.sql` pair listed in
  `deploy/base/kustomization.yaml`, never an edit to a shipped migration.
  `./scripts/check-migrations.sh` renumbers on collision — eight sibling
  plans are landing in parallel, so the numbers are nominal.
- **One model** (ADR 036): every shape crossing the HTTP boundary is declared
  in `internal/model` with wire-name fields; `internal/model/rule_test.go`
  enforces it. Read its failure message before working around it.
- **Every route is a `routeGuards` row** in `internal/api/router.go`;
  `NewServer` refuses to boot otherwise. Signed webhook routes are
  `open("authenticated by HMAC signature, not by an actor")` like the
  existing three; bearer API routes name a permission; role checks stay in
  the `grants` table, never in handlers.
- **`internal/cmd` decides, `internal/cli` renders:** every human-readable
  view is a `cli.*Table`/`cli.*Render` function in `internal/cli/render.go`
  taking an `io.Writer`; no tabwriter or timestamp formatting under
  `internal/cmd` (`renderrule_test.go` is the tripwire).
- **Metrics** (spec 022): nil-safe metrics structs in the owning package's
  `metrics.go`, `worklode_` prefix, bounded label values only — never a
  project id, deliverable id, artifact address, or actor. Metric names this
  plan owns: `worklode_artifact_evidence_total{source,state,entity_kind}`
  (hooks — see Decisions), `worklode_deliverable_reports_total{source,outcome}`
  (api), `worklode_probe_reports_total{state,result}` (api). Tests for every
  new or relabelled metric. The prober process itself exposes no `/metrics`
  listener (the `lode-watch` precedent); the server-side ingest metrics carry
  its meaningful outcomes.
- **Every mutation is one event.** Web and API writes wrap their store write
  in `RecordEvent` with the source naming the surface (`cli` for the JSON
  API, `web` for cockpit forms), as `recordDeliverable` already does; ingest
  writes ride their handler's `RecordEvent` transaction.
- **Store tests need Postgres with pgvector** (`store.OpenTestStore`); they
  skip silently without it unless `CI` is set — a green run without Postgres
  proved nothing.
- **`e2e/` drives public surfaces only** — HTTP API, signed webhooks, web
  pages; never a direct store write.
- **UI toolchain:** templ components compiled by `go generate ./...`, styles
  via the pinned Tailwind CLI; both generated artifacts are committed in any
  task touching a `.templ` or the stylesheet.
- **Every task leaves `go test ./...` green** and ends with a commit.

## Decisions this plan executes (made against the spec; do not reopen)

- **The prober is `lode-watch` in `-mode artifacts`, its own Deployment.**
  029 §3.2 asks for "a separate prober process (the `lode watch` pattern —
  own deployment, bearer token, dedupe keys)". A separate *process* does not
  require a seventh binary: the six-executable layout (053 §1, CLAUDE.md)
  stands, and `lode-watch` — the operator-side observation binary — grows a
  second mode that runs the probe loop instead of the pod informer. Own
  Kubernetes Deployment, own token, own credentials; `internal/watch` (the
  pod informer) is not extended — the probe loop is a new `internal/probe`
  package. The prober holds the read credentials, so the server's blast
  radius stays the database it owns.
- **Label values are minted, unique, and never hand-linked.** At creation
  with `label: true`, the server mints the value as
  `<project key>/<store.SlugifyTitle(name)>` and refuses a collision with an
  existing label declaration in the project. Humans never link artifacts to
  deliverables by hand — skills materialize the convention into deterministic
  checks (lint rules that `docker-bake.hcl` applies the tag, that
  `datasets.yaml` carries the label definitions); that skill work lives with
  sunstone-way/sunstone-py and is out of this repo's scope.
- **Evidence routed by label stores the selector string as its
  `artifact_uri`.** The routing key and the evidence key are the same string,
  so `deliverableFrom`'s correlation invariant ("evidence pairs only with the
  declaration that routed it") holds by construction, with no schema change
  to `artifact_evidence`. The concrete build-time address the emitter
  reports lands in the evidence `url`/`version`/`detail`, and the stored
  event payload keeps everything.
- **Routing is selector-scoped.** `OpenDeclarationsForArtifact` gains a
  selector argument: an address probe can never match a label declaration
  whose string happens to collide, and vice versa.
- **`/hooks/cms` requires both `published_by` and `approved_by`.** A publish
  fact without the person rebuilds the invisible-sign-off problem 029 exists
  to remove, so a CMS delivery missing either field is a 400, not a partial
  accept. Both land in the stored event payload and are merged into the
  evidence `detail`.
- **User-reported evidence for a deliverable with no declaration files with
  `artifact_uri = ''`.** A deliverable with no artifact (029 §3.3's
  `wl:Effect` — a state change) is exactly where user reporting matters
  most; the entity itself is the subject. The projection's evidence join
  correlates on `COALESCE(decl.artifact_uri, '')` so such a report surfaces.
- **`worklode_catalog_evidence_total` becomes
  `worklode_artifact_evidence_total{source,state,entity_kind}`.** Four
  ingest sources now write evidence; one counter with a bounded `source`
  label beats four names. No dashboard depends on the old name (spec 022's
  follow-up records that dashboards are still unbuilt), so this is a rename,
  not a break.
- **No SKOS scheme for evidence state or provenance.** The backbone owns
  those enums in the `artifact_evidence` CHECKs; mirroring them into
  `ns/concept.ttl` would be the duplication `docs/follow-ups.md` already
  flags as a liability for `wl:priority`/`wl:concern` (the `wlc:TaskState`
  precedent). §3.3's ns/ work is the `wl:artifact` term only — Task 11.
- **Prober v1 checks `http(s)` addresses only.** The checker is a per-scheme
  seam; `gs://`, `bigquery://`, `iceberg://` checkers arrive when a real
  project declares one, each bringing its own read credentials into the
  prober's deployment, never the server's.

## Tasks

### Task 1 — Migration 0062: label-form artifact declarations

```yaml
kind: feature
priority: high
skills:
  - golang-migrate:migration
  - golang-migrate:test-roundtrip
blockedBy: []
```

Create `deploy/base/migrations/0062_label_declarations.up.sql` / `.down.sql`
(numbers nominal; the pre-commit collision check renumbers). One change:

```sql
-- Spec 029 §3.1: a deliverable is identified by address OR by label, for
-- artifacts whose address is minted at build time (a docker tag, an Iceberg
-- snapshot). A label declaration stores the full selector string
-- ('worklode.deliverable=COW/datasets') in artifact_uri — the routing key and
-- the evidence key stay one string — and this column says which form the row
-- is, so an address lookup can never match a label declaration by accident.
ALTER TABLE artifact_declarations ADD COLUMN selector text NOT NULL DEFAULT 'address'
    CHECK (selector IN ('address', 'label'));
```

Down: `ALTER TABLE artifact_declarations DROP COLUMN selector;`.

- [ ] Write both files; add both lines under `worklode-migrations` in
      `deploy/base/kustomization.yaml`, matching the 0051 pattern.
- [ ] `./scripts/check-migrations.sh --no-fix` — expect exit 0 (or run
      without `--no-fix` and accept the renumber).
- [ ] Roundtrip against a scratch database (golang-migrate:test-roundtrip):
      up → down → up applies cleanly.
- [ ] `go test -trimpath ./internal/store -run TestMigrations -count=1` —
      expect `ok` (the harness applies the full chain on every
      `OpenTestStore`).
- [ ] Commit: `Label-form artifact declarations (029 §3.1)`.

### Task 2 — Store: mint, declare, route, and project the label form

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

`internal/store/artifactevidence.go`, `deliverables.go`,
`internal/model/deliverable.go`, and their tests. Four changes, one seam:

1. **Selector-scoped declare and route.** Widen the two tx helpers; update
   every existing caller to pass `"address"`:

```go
// DeclareArtifact records that (entityKind, entityID) is verified by the
// given selector: an artifact address, or a 'worklode.deliverable=…' label
// string (029 §3.1). Re-declaring is a no-op.
func DeclareArtifact(tx *sql.Tx, now time.Time,
	entityKind, entityID, selector, key string) error

// OpenDeclarationsForArtifact routes one reported key — an address, or one
// label pair rendered as 'k=v' — to every still-open entity that declared it
// under the same selector. Address and label namespaces never cross.
func OpenDeclarationsForArtifact(tx *sql.Tx, selector, key string) ([]DeclaredEntity, error)
```

   Each arm of `openDeclarationsSQL` gains `AND ad.selector = $2`.

2. **Minting.** `DeliverableInput` gains `Label bool`. In
   `CreateDeliverable`: `Label && Artifact != ""` is `ErrInvalidInput`
   ("declare an artifact address or a label, not both"). With `Label`, mint
   `value := key + "/" + SlugifyTitle(name)` (the existing helper in
   `tasks.go`), refuse `ErrInvalidInput` when a label declaration with
   selector string `"worklode.deliverable=" + value` already exists in the
   project (query `artifact_declarations` joined to `deliverables` on
   `project_id`), then `DeclareArtifact(tx, now, "deliverable", d.ID,
   "label", "worklode.deliverable="+value)`. The check runs before the
   ordinal allocation, so a refused input never burns an id.

3. **Projection.** The `decl` LATERAL in `deliverableFrom` selects
   `ad.selector` too; `deliverableSelect` carries it;
   `scanDeliverable` fills the model: selector `label` puts the full
   selector string in the new `model.Deliverable.Label` field and leaves
   `Artifact` empty; `address` keeps today's behaviour with `Label` empty.
   The evidence LATERAL's correlation `e.artifact_uri = decl.artifact_uri`
   is untouched — it already pairs a label declaration with label-routed
   evidence because both store the same string (see Decisions).

4. **Model** (`internal/model/deliverable.go`): `Deliverable` gains
   `Label string \`json:"label"\`` with a comment naming 029 §3.1;
   `CreateDeliverableInput` gains `Label bool \`json:"label"\``.

First test, verbatim shape (Postgres, `store.OpenTestStore(t)`):

```go
func TestCreateDeliverableByLabelMintsSelector(t *testing.T) {
	s := store.OpenTestStore(t)
	projectID := seedProject(t, s, "COW") // existing fixture helper
	tx := mustBegin(t, s)
	d, err := store.CreateDeliverable(tx, time.Now(), store.DeliverableInput{
		ProjectID: projectID, Name: "Datasets", Label: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Label != "worklode.deliverable=COW/datasets" {
		t.Errorf("Label = %q, want worklode.deliverable=COW/datasets", d.Label)
	}
	got, err := store.OpenDeclarationsForArtifact(tx, "label", d.Label)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != d.ID {
		t.Errorf("label routes to %v, want [%s]", got, d.ID)
	}
}
```

Also cover: label + artifact refused; a second same-name label deliverable
refused with no ordinal burned; an *address* lookup of the identical string
routes nothing (selector scoping); `GetDeliverable`/`ListDeliverables`
project `Label` and leave `Artifact` empty for a label row; existing
address-form tests still green after the signature change.

- [ ] `go test -trimpath ./internal/store -run 'TestCreateDeliverable|TestOpenDeclarations|TestDeliverable' -count=1`
      against Postgres — expect `ok`, not a skip.
- [ ] `go test -trimpath ./... -count=1` — green (callers of the widened
      signatures updated: `internal/hooks/catalog.go`, task/doc declare
      paths).
- [ ] Commit: `Deliverable identity by label: mint, declare, route (029 §3.1)`.

### Task 3 — API and web: declare a deliverable by label

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

The existing create mutation grows the label form on both of its surfaces at
once — the JSON API and the cockpit form share `validateDeliverable`, which
is the point of that function.

- `internal/api/deliverables.go`: `validateDeliverable` gains the `label
  bool` input and returns the message `"declare an artifact address or a
  label, not both"` when both are set (the store refuses too; validating
  here gives the 422 a name before an event is recorded).
  `createDeliverable` passes `req.Label`; `recordDeliverable`'s event
  payload gains `"label"`.
- `internal/api/webform.go` (`createDeliverableFromForm`) and the declare
  form template: a checkbox `name="label"` labelled
  "Identify by label (address minted at build time)", parsed as
  `r.PostFormValue("label") != ""`. The artifact input's help text says the
  two are alternatives.
- `internal/ui/deliverables.templ` + `views.go`: `DeliverableRow` gains
  `Label string`; a label row renders
  `identified by label <span class="mono">worklode.deliverable=COW/datasets</span>`
  where an address row says `verified by …` today. `internal/api/render.go`'s
  view mapping carries it.

First test, in `internal/api/deliverables_test.go` (existing harness):

```go
func TestCreateDeliverableByLabel(t *testing.T) {
	st, h := newTestServer(t)
	projectID := seedProjectWithKey(t, st, "COW")

	body := `{"name":"Datasets","label":true}`
	rr := doReq(t, h, "POST", "/api/v1/projects/"+projectID+"/deliverables", token, body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body)
	}
	var d model.Deliverable
	mustDecode(t, rr, &d)
	if d.Label != "worklode.deliverable=COW/datasets" || d.Artifact != "" {
		t.Errorf("label %q artifact %q", d.Label, d.Artifact)
	}
}
```

Also cover: `{"label":true,"artifact":"gs://x"}` → 422 with the exact
message; the web form checkbox path creates a label deliverable with event
source `web`; the deliverables page renders `identified by label` for it.

- [ ] `go generate ./...` after editing the `.templ`; commit the
      regenerated `*_templ.go`.
- [ ] `go test -trimpath ./internal/api -run 'TestCreateDeliverable|TestDeliverables' -count=1`
      against Postgres — expect `ok`.
- [ ] Commit: `Declare a deliverable by label over API and web (029 §3.1)`.

### Task 4 — CLI: `lode deliverable list` and `add`

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

The missing verb `docs/follow-ups.md` names, mirroring
`POST|GET /api/v1/projects/{id}/deliverables`, split on the seam: the client
and the table live in `internal/cli`, the cobra command in `internal/cmd`.

- `internal/cli/client.go`:
  `ListDeliverables(ctx, project) (model.DeliverableListResponse, []byte, error)`
  and
  `CreateDeliverable(ctx, project string, in model.CreateDeliverableInput) (model.Deliverable, []byte, error)`,
  built on `doJSON` like their siblings.
- `internal/cli/render.go`: `DeliverableTable(w io.Writer, items
  []model.Deliverable)` — columns `ID`, `NAME`, `STATE` (`ReportedState`, or
  `declared` when empty), `IDENTITY` (`Artifact`, `Label`, or `-`),
  `REPORTED` (`cli.LocalTime(*ReportedAt)`, or `-`). Golden-style test
  beside the existing render tests.
- `internal/cmd/deliverable.go`: command group `lode deliverable` with
  `list <project>` and
  `add <project> <name> [--description] [--url] [--artifact <uri> | --label]`,
  following `newProjectCrewCmd`'s shape exactly: `RunE` fetches, honors
  `--json` via `printRaw`, and calls exactly one render function or prints a
  one-line confirmation (`declared COW-DEL-3 (Datasets)` — no timestamps, so
  no formatter needed). `--artifact` and `--label` together is a client-side
  error before any request.
- `docs/agent-surfaces.md`: walk its checklist for a new CLI verb; register
  the surface.

First test, in `internal/cmd` beside the existing command tests (httptest
server stub, the house pattern):

```go
func TestDeliverableAddByLabel(t *testing.T) {
	srv := stubAPI(t, "POST /api/v1/projects/COW/deliverables",
		http.StatusCreated, `{"id":"COW-DEL-1","name":"Datasets","label":"worklode.deliverable=COW/datasets"}`)
	out := runCmd(t, srv, "deliverable", "add", "COW", "Datasets", "--label")
	if !strings.Contains(out, "COW-DEL-1") {
		t.Errorf("output %q missing minted id", out)
	}
}
```

- [ ] `go test -trimpath ./internal/cmd ./internal/cli -count=1` — expect
      `ok`, including `renderrule_test.go` (no tabwriter or timestamp under
      `internal/cmd`).
- [ ] `docs/agent-surfaces.md` updated in the same commit.
- [ ] Commit: `lode deliverable list/add (029 §3.1, follow-up closed)`.

### Task 5 — User-reported state: one mutation, every surface

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 4]
```

The `user_reported` write path — schema-legal since 0040, written by nothing
— landed across store, API, CLI, web form, projection, UI, and metric in one
task, so the event provenance cannot end up half-wired.

**Store** (`internal/store/deliverables.go` + `artifactevidence.go`):

```go
// ReportDeliverableState files a user-reported evidence row against the
// deliverable's first declaration (the projection's rule: lowest declaration
// id), or against artifact_uri = '' when it declares none — an Effect's
// state change has no address, and the entity itself is the subject
// (029 §3.2, §3.3). state must be one of the artifact_evidence CHECK set.
func ReportDeliverableState(tx *sql.Tx, eventID int64, now time.Time,
	deliverableID, state, source, actorID, note string) error
```

Provenance is `'user_reported'` unconditionally; `detail` carries
`{"actor": …, "note": …}`. Unknown deliverable is `ErrNotFound`; an invalid
state is `ErrInvalidInput`.

**Projection**: `deliverableSelect` gains `COALESCE(ev.provenance, '')`;
the evidence LATERAL's correlation becomes
`e.artifact_uri = COALESCE(decl.artifact_uri, '')` so a no-declaration
deliverable's user reports surface; `model.Deliverable` gains
`ReportedProvenance string \`json:"reported_provenance"\``;
`scanDeliverable` fills it.

**API**: `POST /api/v1/deliverables/{id}/report`, body
`model.ReportDeliverableInput{State, Note string}` (new in
`internal/model`), guarded `permDeliverableWrite` in `routeGuards`. Handler
wraps the store write in `s.recordEvent(ctx, "cli",
"deliverable.reported", …)` exactly as `recordDeliverable` does; 422 on an
invalid state naming the five legal ones, 404 via `mapStoreErr`.

**Web**: `POST /deliverables/{id}/report` (deliverable ids are globally
unique, so no project segment is needed), session-gated `permWebWrite` +
`sameOriginForm` like every form in `webform.go`, event source `web`,
redirect back to the project's deliverables page. In
`internal/ui/deliverables.templ`, each row gains a compact form: a
`<select name="state">` of the five states and a `Report` submit button —
native controls, keyboard-operable with no JavaScript.

**CLI**: `lode deliverable report <id> <state> [--note]` on Task 4's group;
client method `ReportDeliverable(ctx, id string, in
model.ReportDeliverableInput)`; confirmation line
`reported <id> <state> (user-reported)`.

**UI provenance**: when `ReportedProvenance == "user_reported"`, the row
renders `User-reported` (exact string) as muted text beside the reported
time — the chip keeps its state colour, and the text is what keeps a claim
from masquerading as verification. Observed rows render nothing new. Rewrite
the page's stale footnote (it still says the prober is unbuilt) to state the
three provenance realities: emitters and the prober report observed facts,
people report user-reported ones, and a row with neither says `Declared`.

**Metric** (`internal/api/metrics.go`):
`worklode_deliverable_reports_total{source,outcome}` — `source` ∈
`{cli, web}`, `outcome` ∈ `{reported, invalid, not_found, error}`. Nil-safe;
test beside the existing metric tests.

First test:

```go
func TestUserReportedStateSurfacesWithProvenance(t *testing.T) {
	st, h := newTestServer(t)
	projectID := seedProjectWithKey(t, st, "COW")
	id := seedDeliverable(t, st, projectID, "Report PDF") // no artifact declared

	rr := doReq(t, h, "POST", "/api/v1/deliverables/"+id+"/report", token,
		`{"state":"published","note":"uploaded to the site"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body)
	}
	list := doReq(t, h, "GET", "/api/v1/projects/"+projectID+"/deliverables", token, "")
	var resp model.DeliverableListResponse
	mustDecode(t, list, &resp)
	if got := resp.Deliverables[0]; got.ReportedState != "published" ||
		got.ReportedProvenance != "user_reported" {
		t.Errorf("state %q provenance %q", got.ReportedState, got.ReportedProvenance)
	}
}
```

Also cover: invalid state → 422 and no event row; the web form path writes
source `web` and redirects; a catalog-observed row keeps
`ReportedProvenance == "observed"` and the page does *not* say
`User-reported` for it; the page *does* render `User-reported` for a
reported row; metric increments; CLI confirmation line.

- [ ] `go generate ./...`; commit regenerated artifacts.
- [ ] `go test -trimpath ./internal/api ./internal/store ./internal/cmd ./internal/cli -count=1`
      against Postgres — expect `ok`.
- [ ] Commit: `User-reported deliverable state across every surface (029 §3.2)`.

### Task 6 — Migration 0063, label routing, and the parameterized ingest: `/hooks/ci`, `/hooks/pipeline`

```yaml
kind: feature
priority: high
skills:
  - golang-migrate:migration
  - superpowers:test-driven-development
blockedBy: [2]
```

§8.3's 004-pattern sources, minus the CMS (Task 7 — its person requirement
deserves its own review). One migration, one refactor, two new routes.

**Migration** `deploy/base/migrations/0063_ingest_sources.up.sql` /
`.down.sql`, the same drop-and-recreate 0040 used:

```sql
-- Spec 029 §8.3: new ingest sources follow the 004 pattern — one
-- events.source value per system. cms/ci/pipeline are signed webhook
-- emitters; prober is the poll prober reporting over the bearer API
-- (029 §3.2). Down restores the 0040 list.
ALTER TABLE events DROP CONSTRAINT events_source_check;
ALTER TABLE events ADD CONSTRAINT events_source_check
    CHECK (source IN ('github','flux','watcher','cli','system','web',
                      'catalog','cms','ci','pipeline','prober'));
```

**Refactor** `internal/hooks/catalog.go`: the handler becomes an instance of
a parameterized ingest —

```go
// ingestConfig names one signed artifact-evidence source (029 §8.3). All
// instances share the payload contract at the top of this file, the HMAC
// scheme, the dedupe rule, and the routing; they differ only in identity
// and any per-source validation.
type ingestConfig struct {
	Source         string // events.source and evidence source: catalog|ci|pipeline|cms
	DeliveryHeader string // X-<Source>-Delivery
	Validate       func(ev *catalogEvent) string // extra check; "" = valid
}
```

`NewCatalogHandler` keeps its signature and becomes
`newIngestHandler(cfg, st, secret, log, m)` with source `catalog`; add
`NewCIHandler` (`ci`, `X-CI-Delivery`) and `NewPipelineHandler`
(`pipeline`, `X-Pipeline-Delivery`). Event types become
`cfg.Source + "." + …`. The stored-delivery replay path
(`applyStored`, and the reconcile dispatch that routes source `catalog` to
it) routes the new sources through the same applier.

**Label routing**: the payload gains
`Labels map[string]string \`json:"labels"\``; validation requires an
artifact *or* at least one label. `apply` routes the address (selector
`address`) and each pair rendered `k + "=" + v` (selector `label`) through
`OpenDeclarationsForArtifact`, filing evidence per routed target with
`ev.Artifact` = the routing key that matched (see Decisions) and the
reported concrete address in `URL`/`Version`/`Detail` as the emitter sent
them. Update the file-top contract comment.

**Metric**: rename `worklode_catalog_evidence_total` →
`worklode_artifact_evidence_total{source,state,entity_kind}` (Decisions);
`event(source, …)` already takes the source. Update the metric test.

**Wiring**: `Config` gains `CIWebhookSecret`/`PipelineWebhookSecret`
(env `LODE_CI_WEBHOOK_SECRET`/`LODE_PIPELINE_WEBHOOK_SECRET` in
`internal/serverapp/serverapp.go`); `server.go` registers
`POST /hooks/ci` and `POST /hooks/pipeline`; `routeGuards` rows
`open("authenticated by HMAC signature, not by an actor")`.

First test, in `internal/hooks/catalog_test.go`'s harness:

```go
func TestPipelineDeliveryRoutesByLabel(t *testing.T) {
	e := newCatalogEnv(t) // existing harness, extended to mount all sources
	del := e.seedLabelDeliverable(t, "COW", "Datasets") // Task 2's mint path

	body := `{"event":"dataset.registered","state":"published",
	  "artifact":"iceberg://prod/cow/casualties@snap-991",
	  "labels":{"worklode.deliverable":"COW/datasets"}}`
	e.deliverSigned(t, "/hooks/pipeline", "X-Pipeline-Delivery", "p-1", body)

	if got := e.rawQueryString(t,
		`SELECT provenance FROM artifact_evidence WHERE entity_id = $1`, del); got != "observed" {
		t.Errorf("provenance = %q, want observed", got)
	}
	if got := e.rawQueryString(t,
		`SELECT artifact_uri FROM artifact_evidence WHERE entity_id = $1`, del); got != "worklode.deliverable=COW/datasets" {
		t.Errorf("evidence key = %q, want the selector string", got)
	}
}
```

Also cover: the `ci` route files with `source = 'ci'` and event type
`ci.<state>`; a payload with neither artifact nor labels → 400; a delivery
matching both an address and a label files one row per routing key;
redelivery dedupes; every existing catalog test still green; the renamed
metric carries `source`.

- [ ] `./scripts/check-migrations.sh --no-fix`; roundtrip 0063;
      kustomization lines added.
- [ ] `go test -trimpath ./internal/hooks ./internal/store -count=1` against
      Postgres — expect `ok`.
- [ ] `go test -trimpath ./... -count=1` — green (router boot checks prove
      the new routes are guarded).
- [ ] Commit: `CI and pipeline ingest sources; evidence routes by label (029 §8.3, §3.1)`.

### Task 7 — `/hooks/cms`: publish transitions carry who approved and published

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [6]
```

The CMS source, as its own ingest instance with the one rule §8.3 adds: the
publish fact without the person would rebuild the invisible-sign-off problem
the whole spec exists to remove, so the person fields are required, not
polite.

- Payload additions (cms only): `published_by` (who hit publish) and
  `approved_by` (who approved), both non-blank strings after trimming. The
  `Validate` hook returns the exact message
  `"published_by and approved_by are required"` when either is missing —
  a 400, before any event is recorded.
- `NewCMSHandler` = `newIngestHandler` with source `cms`, header
  `X-CMS-Delivery`, secret `LODE_CMS_WEBHOOK_SECRET` (`Config` field +
  serverapp env + `server.go` registration + `routeGuards` open row).
- Both fields are merged into the evidence `detail` jsonb (alongside any
  emitter `detail`), and the stored event payload keeps the whole body — the
  event is the provenance record either way.
- The emitting side — the CMS posting on a post's `_status` transition — is
  `sunstone-cms`'s work (this plan's `defers:` entry). This task ships the
  receiving contract it will be written against; keep the file-top contract
  comment authoritative.

First test:

```go
func TestCMSDeliveryWithoutPersonIsRefused(t *testing.T) {
	e := newCatalogEnv(t)
	e.seedAddressDeliverable(t, "COW", "Story", "https://sunstone.example/cow")

	body := `{"event":"post.published","state":"published",
	  "artifact":"https://sunstone.example/cow"}`
	rr := e.deliverSignedRaw(t, "/hooks/cms", "X-CMS-Delivery", "c-1", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if n := e.rawQueryInt(t, `SELECT count(*) FROM events WHERE source = 'cms'`); n != 0 {
		t.Errorf("refused delivery recorded %d events, want 0", n)
	}
}
```

Also cover: a complete delivery files evidence whose `detail` carries both
names and whose event payload retains them; duplicate delivery acks
`duplicate`; unrouted delivery acks `unrouted` and stays a replay candidate.

- [ ] `go test -trimpath ./internal/hooks -count=1` against Postgres —
      expect `ok`.
- [ ] Commit: `CMS ingest: publish facts carry who approved and published (029 §8.3)`.

### Task 8 — Prober server side: probe targets and the reports ingest

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [6]
```

The two bearer-token endpoints the prober speaks — following
`POST /api/v1/runtime-events`' shape (`internal/api/runtime.go`), which is
already the `lode-watch` pattern's server half.

**Store** (`internal/store/artifactevidence.go`):
`ProbeTargets(ctx) ([]string, error)` — the distinct `artifact_uri` of
`selector = 'address'` declarations whose entity is still open, reusing
`openDeclarationsSQL`'s three openness arms so probing and routing cannot
drift on what open means. Label declarations are never probe targets: their
addresses are minted at build time and reach worklode by push.

**Model**: `ProbeTargetsResponse{Artifacts []string}` and
`ArtifactReportInput{Artifact, State, Version, URL, DedupeKey, OccurredAt
string}` in `internal/model`.

**API** (`internal/api/probe.go`):

- `GET /api/v1/probe-targets` → `ProbeTargetsResponse`.
- `POST /api/v1/artifact-reports`: validate state against the five-state
  set and `dedupe_key` non-blank (422 otherwise), then
  `RecordEvent(ctx, "prober", in.DedupeKey, "prober."+in.State, payload,
  apply)` where the apply routes via
  `OpenDeclarationsForArtifact(tx, "address", in.Artifact)` and files
  evidence with `Source: "prober"`, `Provenance: "observed"` — the prober is
  an emitter, not a person. Ack `{"status":"ok"|"duplicate"|"unrouted"}`
  like the signed ingests; an unrouted report stays a replay candidate the
  same way (the declaration may arrive later).
- Permission: `permArtifactProbe Permission = "artifact.probe"`, grants
  `{RoleUser, RoleAdmin}`, `routeGuards` rows for both routes.
- Metric: `worklode_probe_reports_total{state,result}` — `state` from the
  five-state set or `invalid`; `result` ∈ `{ok, duplicate, unrouted,
  invalid, error}`. Nil-safe, tested.

First test, `internal/api/probe_test.go` on `runtime_test.go`'s pattern:

```go
func TestArtifactReportFilesObservedEvidence(t *testing.T) {
	st, h := newTestServer(t)
	projectID := seedProjectWithKey(t, st, "COW")
	id := seedAddressDeliverable(t, st, projectID, "Datapackage", "https://data.example/cow.zip")

	body := `{"artifact":"https://data.example/cow.zip","state":"published",
	  "version":"etag-abc","dedupe_key":"probe:https://data.example/cow.zip:published:etag-abc"}`
	rr := doReq(t, h, "POST", "/api/v1/artifact-reports", token, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body)
	}
	got := queryString(t, st,
		`SELECT source || '/' || provenance FROM artifact_evidence WHERE entity_id = $1`, id)
	if got != "prober/observed" {
		t.Errorf("evidence = %q, want prober/observed", got)
	}
}
```

Also cover: `GET /api/v1/probe-targets` lists the address and not a label
selector; a redelivered `dedupe_key` acks `duplicate` and writes nothing; an
address nobody declares acks `unrouted`; missing token → 401; metric.

- [ ] `go test -trimpath ./internal/api ./internal/store -count=1` against
      Postgres — expect `ok`; router boot checks green.
- [ ] Commit: `Probe targets and artifact-reports ingest (029 §3.2)`.

### Task 9 — The prober process: `lode-watch -mode artifacts`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [8]
```

The poll half of §3.2: a separate process on the `lode watch` pattern — own
deployment, bearer token, dedupe keys — that checks declared addresses and
reports what it finds. New package `internal/probe`; `internal/watch` (the
pod informer) is untouched.

```go
// Package probe polls declared artifact addresses and reports observed
// state to the worklode server (spec 029 §3.2). It runs as lode-watch
// -mode artifacts: its own deployment, holding whatever read credentials
// the addresses need, so the server's blast radius stays the database it
// owns. It verifies existence and change, never a human claim — probing as
// verification of reported state is 007 v2 and deliberately absent.
package probe

type Options struct {
	Server   string        // worklode base URL
	Token    string        // bearer token (LODE_TOKEN)
	Interval time.Duration // full-sweep period, default 15m
}

// Run loops until ctx ends: GET /api/v1/probe-targets, check each address
// with the checker registered for its scheme, POST one artifact-report per
// finding. Unsupported schemes are logged once per sweep and skipped — the
// checker map is where gs:// and bigquery:// arrive later.
func Run(ctx context.Context, opts Options) error
```

- **Checker seam:** `type checker func(ctx context.Context, address string)
  (state, fingerprint string, ok bool)`, keyed by URL scheme. v1 registers
  `http`/`https` only: `HEAD` (falling back to `GET` on 405/501); 2xx →
  `published` with fingerprint = `ETag`, else `Last-Modified`, else the
  status code; 404/410 → `removed`; anything else (5xx, timeout) → `ok =
  false`, logged, *not reported* — a flaky origin is not evidence of state.
- **Dedupe key:** `"probe:" + address + ":" + state + ":" + fingerprint` —
  steady state re-reports dedupe to no-ops server-side, and a state or
  content change mints a new key, so at-least-once polling files each fact
  once. This is the `dedupe_keys` half of the `lode watch` pattern
  (`internal/watch`'s reporter posts the same way).
- **`cmd/lode-watch/main.go`:** new `-mode` flag (`pods` default,
  `artifacts`), plus `-interval` (default `15m`). `artifacts` mode requires
  `-server` and `-token` and ignores `-kubeconfig`/`-cluster`; it calls
  `probe.Run`. `pods` mode is byte-for-byte today's behaviour, and the
  `lode watch` shim passes flags through unchanged because it execs.
- No `/metrics` listener, matching `lode-watch` today; the server-side
  `worklode_probe_reports_total` carries the outcomes (Global Constraints).

First test, `internal/probe/probe_test.go`, no Postgres — two `httptest`
servers, one playing the origin and one the worklode API:

```go
func TestSweepReportsPublishedWithETagFingerprint(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v7"`)
	}))
	defer origin.Close()

	var got model.ArtifactReportInput
	api := fakeWorklode(t, []string{origin.URL + "/cow.zip"}, &got)
	defer api.Close()

	if err := probe.SweepOnce(context.Background(), probe.Options{Server: api.URL, Token: "wl_test"}); err != nil {
		t.Fatal(err)
	}
	if got.State != "published" || !strings.Contains(got.DedupeKey, `"v7"`) {
		t.Errorf("reported %+v", got)
	}
}
```

(Export `SweepOnce` for exactly this; `Run` is the ticker around it.) Also
cover: 404 reports `removed`; 500 reports nothing; an unsupported
`bigquery://` target is skipped without error; `-mode artifacts` without
`-token` exits 2 with a message (a `main_test.go`-style run test).

- [ ] `go test -trimpath ./internal/probe ./cmd/lode-watch -count=1` —
      expect `ok` (no Postgres needed).
- [ ] `make build-all` — all six executables still build; no new
      executable exists.
- [ ] Commit: `The poll prober: lode-watch -mode artifacts (029 §3.2)`.

### Task 10 — Prober deployment manifests

```yaml
kind: feature
priority: medium
skills:
  - sunstone-devops:kubernetes
blockedBy: [9]
```

The "own deployment" half of the pattern, as manifests in this repo the way
`deploy/base/` carries the server's. New directory `deploy/prober/`:

- `kustomization.yaml` + `deployment.yaml`: namespace `worklode`, one
  replica, the Dockerfile's existing `watcher` image (it already contains
  `/lode-watch`; no Dockerfile change), args
  `["-mode", "artifacts", "-server", "http://worklode.worklode.svc:8080", "-interval", "15m"]`,
  `LODE_TOKEN` from a `worklode-prober-token` Secret (created by the
  operator from a minted `wl_…` token; the manifest references, never
  contains, it). Copy `deploy/base/deployment.yaml`'s securityContext
  (nonroot, read-only rootfs, drop-all) verbatim.
- Deliberately **no ServiceAccount, no RBAC, no kubeconfig**: unlike the pod
  informer, the prober talks HTTP outward only. Read credentials for future
  non-HTTP checkers mount here, in this Deployment, when those checkers
  exist — never into the server's.
- A short `deploy/prober/README.md` is not needed; a header comment in the
  Deployment stating the above is. Flux wiring into `hzdev`/`hzprod` lives
  in the provisioning repo, the same boundary the watcher's pending
  manifests follow-up (`docs/follow-ups.md` `[P3]`) already records.

- [ ] `kubectl kustomize deploy/prober` renders without error and contains
      exactly one Deployment with the four args above.
- [ ] `grep -c 'serviceAccountName' deploy/prober/deployment.yaml` → 0.
- [ ] Commit: `Prober deployment manifests (029 §3.2)`.

### Task 11 — ns/: the `wl:artifact` term (§3.3)

```yaml
kind: chore
priority: medium
skills:
  - worklode-docs-authoring
blockedBy: []
```

§3.3's lineage is mostly already true in `ns/`: `wl:Deliverable` is a class
(029 §3.3's "006's declared definition-of-done made concrete") and
`wl:Effect` is its subclass for the no-artifact case — verify both, change
neither. What §3.3's "the ns/ mirrors follow at acceptance" still owes from
*this* plan's sections is the term behind the `artifact` frontmatter key,
which `docs/authoring-design-docs.md`'s frontmatter table currently records
as "029 §3.3 defers the term to acceptance". (`wl:Milestone` and the
participants/approvals vocabulary stay owed by parts 1, 3, and 6 of their
series — `docs/follow-ups.md:214-218` — not here.)

- `ns/ontology.ttl`: mint `wl:artifact a owl:DatatypeProperty` — the
  declared catalog address or label selector an entity is verified by
  (029 §3.1); no `rdfs:domain`, because deliverables, tasks, and docs all
  declare one; comment notes it declares additively (removing the key
  undeclares nothing) and cites 029 §3.1/§3.3.
- `docs/authoring-design-docs.md`: the `artifact` row's Term cell becomes
  `wl:artifact`.
- `ns/ontology.ttl`'s "deliberately absent" block gains one line: no SKOS
  scheme for evidence state or provenance — the backbone owns those enums in
  the `artifact_evidence` CHECKs (the `wlc:TaskState` precedent; see
  Decisions).
- `ns/concept.ttl` is untouched, so `scripts/nsgen.py` needs no run — state
  that in the commit message rather than regenerating for show.

- [ ] `riot --validate ns/ontology.ttl ns/shapes.ttl ns/concept.ttl` — no
      output, exit 0.
- [ ] `go test -trimpath ./internal/store -run TestTaskStateShapeMatchesStateMachine -count=1`
      and `python3 scripts/secmeta_test.py` — green (the ns round-trip
      checks).
- [ ] Commit: `Mint wl:artifact; deliverable lineage terms verified (029 §3.3)`.

### Task 12 — e2e journey and docs alignment

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3, 5, 7, 8]
```

`e2e/deliverable_state_test.go` (build tag `e2e`), on `smoke_test.go`'s
harness, public surfaces only — the prober binary itself is unit-tested in
Task 9, so e2e exercises its wire contract directly:

1. Bootstrap token → project via `/api/v1`; declare deliverable A by label
   (`{"label":true}`) and deliverable B by address; declare C with neither.
2. Signed `/hooks/pipeline` delivery carrying
   `labels: {"worklode.deliverable": "<A's value>"}` → the project's
   deliverables page shows A `Published` with no `User-reported` text.
3. `POST /api/v1/artifact-reports` with B's address and a dedupe key → B
   shows `Published`; replay the same key → ack `duplicate`.
4. `POST /api/v1/deliverables/{C}/report` `{"state":"published"}` → the page
   shows C `Published` **and** `User-reported` — the provenance split,
   visible on the wire.
5. Signed `/hooks/cms` delivery *without* `published_by` → 400; with both
   person fields → evidence lands and the ack is `ok`.

Docs alignment, same task: rewrite `docs/follow-ups.md`'s
"Deliverables landed without the rest of spec 029" entry down to the one
piece this plan does not close — milestone attachment, owned by
`2026-08-25-research-work-1-milestones` — and drop the "no CLI verb" and
`[P3]` prober-adjacent phrasing that is no longer true. Check the entry
against what actually merged rather than against this plan's intent.

- [ ] `go test -race -count=1 -tags e2e ./e2e/ -run TestDeliverableState`
      against Postgres — expect `ok`.
- [ ] Full suite: `go test -race -count=1 -tags e2e ./e2e/` — green.
- [ ] Commit: `e2e: deliverable state observed, probed, and user-reported over public surfaces`.

## Verification

- `go test -trimpath -race -count=1 ./...` green with Postgres reachable (a
  silent skip proved nothing); `go test -race -count=1 -tags e2e ./e2e/`
  green.
- `make build-all` — six executables, no seventh.
- `curl -s localhost:9090/metrics | grep -E 'worklode_(artifact_evidence|deliverable_reports|probe_reports)_total'`
  shows all three families after exercising the flows, and
  `worklode_catalog_evidence_total` is gone.
- `kubectl kustomize deploy/prober` renders.
- `riot --validate ns/*.ttl` exits 0.
- `lode doc anchors docs/plans/2026-08-25-research-work-4-deliverable-state.md`
  reports no errors.

## Deferred — stated so each gap is a decision

- **Milestone attachment** (`deliverables.milestone_id`, milestone-progress
  queries): `2026-08-25-research-work-1-milestones`, which is what makes §3
  `partial` here.
- **Probing as verification of reported state** — reconciling a claim
  against production is 007's v2 line (029 §9). The prober built here
  observes; it never adjudicates a user report.
- **Non-HTTP checkers** (`gs://`, `bigquery://`, `iceberg://`): the
  per-scheme seam in `internal/probe` is where they land, each bringing its
  read credentials into the prober's Deployment.
- **The emitting sides**: the CMS emitter is deferred to `sunstone-cms`
  (frontmatter `defers:`); the pipeline emitter lives in the data-platform
  repo; the CI publish-report step lives in each publishing repo's workflow.
  This plan ships the contracts they are written against.
- **Skill-side label enforcement** (lint that `docker-bake.hcl` applies the
  tag, that `datasets.yaml` carries the label definitions): sunstone-way /
  sunstone-py, per 029 §3.1.
- **`lode show COW-DEL-3`**: the identifier dispatch is §4, owned by
  `2026-08-25-research-work-2-identifiers-and-references`.
