---
status: accepted
covers:
  - spec: docs/specs/026-design-doc-queries.md#sec-2.5
    coverage: full
  - spec: docs/specs/026-design-doc-queries.md#sec-2.1
    coverage: partial
  - spec: docs/specs/026-design-doc-queries.md#sec-5
    coverage: none
---
# Plan — `lode doc todo`: one spec's remaining work

Builds 026 §2.5: a per-document recursive rollup of the work left before a spec
is fully implemented, joining the planning gap (§2.1), plan acceptance, and
execution state into one ordered list.

## What exists vs. what this builds

`internal/designdoc` already parses the corpus: `Parse`, `Frontmatter` with the
three-valued `CoverageList` (026 §5.1), `ResolveRef` with the `WL-SPEC-25`
shorthand, `FindCorpus`, `LoadSyncCorpus`, `PlanTasks`. None of it is joined,
and there is no `lode doc` command at all — `internal/cmd` has no `doc.go`.

What is missing: the §2.1 coverage predicate, the three-level walk, the
`requires` closure, and the CLI. Task closure is missing on the server side —
004 §1.3 makes it a per-repo predicate (`taskClosed`, `internal/store/tasks.go`)
that no client can evaluate, and nothing exposes it on the wire.

Not built here: `lode doc list --needs-planning` / `--needs-execution`. They ride
on the same predicate and become thin wrappers, but they are 026 §2.1/§2.2's
output contracts and belong to their own plan.

## Constraint that bites

`internal/cmd/show_test.go:839` asserts that **whenever a `doc` command exists it
must not own a `show` subcommand** — `lode doc show` was consolidated into `lode
show` (026 §3). That test is dormant today because no `doc` command exists.
Creating one activates it. Add no `doc show`.

## Tasks

### Task 1 — Expose server-computed task closure on the wire

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

026 §2.5 requires closure to be the server's answer. Add a derived `Closed`
field to the task entity, computed with the existing per-repo predicate.

- `internal/model/task.go`: add a `Closed bool` field to `Task`, json tag `closed`, with a
  comment naming 004 §1.3 and saying it is server-derived and read-only —
  a client cannot compute it, and it is ignored on any inbound body.
- `internal/store/tasks.go`: add `ClosedTaskIDs(ctx context.Context, ids []string)
  (map[string]bool, error)`, modelled on the existing `BlockedTaskIDs`. It runs
  one query over `tasks` aliased so that `taskClosed` applies, filtered by
  `id = ANY($1)`, and returns the ids that are closed. **`taskClosed`'s rendered
  subqueries bind `ch`, `tc`, `mc` and `pr` — the enclosing query must not reuse
  those aliases** (the doc comment on `taskClosed` says so).
  An empty `ids` returns an empty map without touching the database.
- Populate `Closed` in `ListTasks` and in `GetTask`'s returned `model.Task`, by
  calling `ClosedTaskIDs` with the ids just scanned. Two queries per list call is
  the deliberate cost: folding the predicate into `taskColumns` would collide
  with those aliases in every query that shares the column list.

Tests in `internal/store` (Postgres-backed, they skip without a DSN — run them
and confirm they actually ran, a silent skip proves nothing):

- an `abandoned` task is closed
- a task at or past its repo's `done_state` is closed; the same state is open
  where a `project_repos.done_state` gates higher
- a `ready` task is not closed
- `ClosedTaskIDs(ctx, nil)` returns an empty map and issues no query
- `ListTasks` sets `Closed` per row

- [ ] `model.Task.Closed`
- [ ] `Store.ClosedTaskIDs` + tests
- [ ] populate in `ListTasks` / `GetTask` + tests

### Task 2 — The §2.1 coverage predicate

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

New `internal/designdoc/coverage.go` + `coverage_test.go`. Pure, file-only, no
server and no CLI.

```go
type PlanningOutcome string // "full" | "partial" | "boundOnly" | "unplanned"

// PlanIndex is the corpus indexed for coverage queries.
type PlanIndex struct{ /* … */ }

func NewPlanIndex(docs []CorpusDoc) *PlanIndex
func (ix *PlanIndex) Section(specPath, anchor string) (PlanningOutcome, []string, []string)
```

`Section` returns the outcome for one spec section plus the accepted plans
contributing to it and the draft plans covering it, both as repo-relative paths
sorted ascending. Implement 026 §2.1's table exactly:

| Outcome | Rule |
|---|---|
| `full` | some **accepted** plan claims `coverage: full`; **or** an accepted plan claims `partial` with a non-empty `fullCoverageWith` in which every named plan is accepted **and** itself contributes `full` or `partial` to this same section |
| `partial` | claimed only `partial`, and no `fullCoverageWith` closes it |
| `boundOnly` | claimed only `none` |
| `unplanned` | no accepted plan covers it |

Rules the tests must pin, all stated by §2.1:

- Only `status: accepted` plans count. A draft plan never discharges a section —
  but it is returned in the draft list, because §2.5 needs it to emit `plan-draft`.
- `fullCoverageWith` is **verified, never trusted**: an empty list, a draft
  target, a target contributing `none`, or a target that does not cover this
  section at all leaves the section `partial`.
- A whole-document `covers` (no `#sec-N` fragment) contributes to nothing. The
  corpus has four such plans today; they must not read as coverage.
- `covers: NO-SPEC` contributes to nothing and is never a gap.
- The retired `implements` spelling reads as `covers` (use `CoverageEntries`).
- Overlap is legal and unremarked: two plans on one section is not an error.

Read the corpus with `LoadSyncCorpus`. Note that plan files contain YAML task
blocks with their own `status:` lines — parse frontmatter, never grep.

- [ ] `PlanningOutcome`, `PlanIndex`, `NewPlanIndex`
- [ ] `Section` implementing the table
- [ ] table tests for every row, including all four `fullCoverageWith` refusals

### Task 3 — The rollup walk, ordering, and `--deps`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

New `internal/designdoc/todo.go` + `todo_test.go`, on top of Task 2's index.
Still pure and file-only: this task resolves everything except task closure,
which arrives as an injected lookup.

```go
type TodoItem struct {
    Type    string // unplanned | partial | plan-draft | unexecuted | blocked
    Doc     string // repo-relative spec path the item belongs to
    Anchor  string // "sec-9.2"; empty when the item is about the document itself
    Heading string
    Plan    string // repo-relative plan path; empty for unplanned
    Task    string // "WL-42"; empty when the plan names none
    Detail  string // one line naming why
}

type Diagnostics struct {
    Unfollowed []string // requires edges not walked (no --deps)
    Cycles     []string // requires cycles met during the walk
    Notes      []string // degradations, e.g. no closure lookup
}

type TodoOptions struct {
    Deps   bool
    Closed func(taskID string) (closed bool, known bool) // nil = no closure lookup
}

func Todo(docs []CorpusDoc, specPath string, opts TodoOptions) ([]TodoItem, Diagnostics, error)
```

Behaviour, all from 026 §2.5:

- Walk the spec's **current** sections. A section an effective `replaces` names
  is dropped, as is every section of a `superseded` document; `amends` keeps the
  section. Reuse the supersession reading `scripts/currentspec.py` implements —
  a `replaces` claim only takes effect once the claiming document is `accepted`.
- Emit one item per section by outcome: `unplanned` → `unplanned`, `partial` →
  `partial`, `boundOnly` → nothing, `full` → descend to its accepted plans.
- A section covered by a draft plan emits `plan-draft` against that plan,
  whatever the accepted-plan outcome is — it is a human acceptance decision and
  must never be folded into `unexecuted`.
- For each accepted covering plan: `unexecuted` when its `task` is absent, or
  when `Closed` says the task is open or unknown. Nothing when closed.
- `blocked` when an accepted plan `requires` another plan that is not itself
  discharged.
- **Spec `status: draft`** → return exactly one `plan-draft` item against the
  document itself, with `Anchor` empty. Never return an empty list here.
- **`--deps`** follows `requires` transitively across specs, marking visited
  documents. A cycle is recorded in `Diagnostics.Cycles` and the walk continues —
  025 and 026 require each other, and failing there would make the flag useless
  on the pair that motivates it. Without `Deps`, every outgoing `requires` edge
  is listed in `Diagnostics.Unfollowed`.
- **Ordering**: topological over plan `requires`, ties broken by the spec's own
  section document order, then by plan path. Two runs over an unchanged corpus
  must produce byte-identical output — assert that in a test.
- `NO-SPEC` as the ref is an error, not an empty run.

Tests build a small synthetic corpus in `t.TempDir()`; do not assert against the
live `docs/` tree, which changes under the test.

- [ ] `TodoItem`, `Diagnostics`, `TodoOptions`
- [ ] per-section item emission for all five types
- [ ] draft-spec single-item case
- [ ] `--deps` transitive walk with a deliberate cycle
- [ ] deterministic topological ordering + a repeat-run equality test

### Task 4 — `lode doc todo` on the CLI

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 3]
```

New `internal/cmd/doctodo.go` + `doctodo_test.go`, hung off the existing `doc`
parent command.

- `newDocTodoCmd()` — `lode doc todo <ref>`, flag `--deps`, added to the
  existing `newDocCmd()` subcommand list. `--json` comes from the root
  persistent flag; read it with `jsonOut(cmd)`.
- Resolve `<ref>` with `resolveDocRef` against the documents `GET /docs` serves,
  so a filename, a repo-relative path and `WL-SPEC-25` all work.
- Build the corpus from the backbone: `GET /docs`, then one `GET /docs/{id}` per
  document for its body, fanned out, each turned into a `CorpusDoc` by
  `designdoc.CorpusDocFromBody` at the path `designdoc.CorpusPath` gives it.
- Closure: one `client.ListTasks` call with the project filter, indexed by id
  into the `TodoOptions.Closed` lookup. A referenced task absent from the
  response is `known == false` and renders as `unexecuted (task not found)`. An
  unreachable server is an error: the corpus lives there too, so there is no
  narrower answer to fall back to.
- Table output: one line per item, columns type / anchor / plan / detail, then a
  footer rendering the diagnostics. `--json` emits `{"items": [...],
  "diagnostics": {...}}` — one document, both halves.
- **Exit status is 0 whether or not work remains.** A non-zero exit would make
  "work is left" indistinguishable from "the query failed".

Per ADR 036 the `--json` shape is an `internal/cmd` stdout contract, not an HTTP
body, so a **named** struct declared here is correct; an anonymous one is not
(`internal/model/rule_test.go`).

Tests drive the cobra command against a stub backbone serving a synthetic
corpus and a task list, asserting rendered output and the JSON shape, in the
style of the existing `internal/cmd` tests.

- [ ] `todo` subcommand, ref resolution, `--deps`
- [ ] corpus in one list + one body per document; closure in one request, plus
      the not-found and unreachable paths
- [ ] table and `--json` rendering, exit 0 with work outstanding

### Task 5 — Dogfood it and record what it finds

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [4]
```

Verification against the real corpus, which is the point of the command.

- `make build`, then run `./bin/lode doc todo WL-SPEC-25` and
  `./bin/lode doc todo WL-SPEC-25 --deps`. Both must exit 0 and produce a
  non-empty, ordered list. 025 is `status: draft`, so the no-`--deps` run is
  expected to report the acceptance decision — confirm it does rather than
  printing nothing.
- Run `./bin/lode doc todo WL-SPEC-26` and confirm the walk answers about a
  document other than the one 025 leads with.
- `make test`, `make vet`, `./scripts/secfmt.py -l`, `./scripts/secmeta.py`,
  `./scripts/inlinespec.py --check`.
- Append one entry to `docs/follow-ups.md`: `lode doc list --needs-planning` and
  `--needs-execution` (026 §2.1/§2.2) are still unbuilt and now reduce to thin
  wrappers over `designdoc.PlanIndex`. Check the file first — do not re-file
  something already there.

- [ ] both `WL-SPEC-25` runs, output pasted into the task
- [ ] `WL-SPEC-26` run
- [ ] full check suite green
- [ ] follow-up filed
