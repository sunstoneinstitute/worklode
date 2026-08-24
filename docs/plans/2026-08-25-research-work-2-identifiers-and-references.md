---
status: draft
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-1
    coverage: none
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-4
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-1-milestones.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-5
    coverage: full
blockedBy:
  - 2026-08-25-research-work-1-milestones.md
---

# Research work part 2 — per-kind identifiers and cross-project references

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** two halves. First, 029 §4's identifier cutover: every document kind
draws its number from the project's `(kind)` counter — `COW-SPEC-4` allocated,
never author-supplied — and a plan carries the two-part ordinal
`COW-PLAN-4-1` (parent spec's ordinal, then a per-spec count from 1;
`COW-PLAN-0-2` for a plan with no governing spec). Every existing document row
is backfilled so no id moves except the plans 025 §16.3 names, and
`lode show WL-PLAN-4-1` / `lode show --plan 4-1` stop erroring. Second,
029 §5's one typed edge table, `entity_edges`, carrying the references that
legitimately cross a project boundary — a milestone depending on a deliverable
produced under another project, and `seeded_by` from a project to its intake
task — surfaced on the milestone page part 1 builds so the half is
demonstrable, not just schema.

**The decided cutover** (do not re-litigate): 029 §4 stands as written,
decided by the CTO 2026-08-25, and now amends 025 §14.3 — that amendment is
already in both spec files. 025 §16.3 and §16.5 already describe this as a
cutover that changes *where* an id is minted, not the id, with one bounded
exception: 029 counts a plan-ordinal by mint order where 025 §16.3 counted by
corpus order, so a plan reordered or back-inserted before the cutover may be
renumbered. Task 2 rehearses the backfill against the real corpus and proves
which ids (if any) move.

**Series:** part 2 of the nine-part 2026-08-25 set planning spec 029.
`blockedBy` part 1 (`2026-08-25-research-work-1-milestones`): part 1's
migration 0052 creates `milestones` and widens the `project_entity_seq.kind`
CHECK to `('DEL','MILE')`, and its cockpit milestone page is where Task 9
surfaces deliverable references. Part 4
(`2026-08-25-research-work-3-intake-and-promotion`) consumes this part twice:
the promotion transaction writes the `seeded_by` edge through Task 7's store
path, and 029 §1's project metadata (labels, `horizon`) is entirely part 4's.
Two tasks here touch code part 1 also touches (the `unshowableReason` map in
`internal/cmd/show.go`, the `ON CONFLICT` target in the entity-seq mint);
whichever part lands second rebases — both collisions are named in the tasks.

**Coverage, declared:** §4 is `partial` because part 1 owns the milestone
(`MILE`) half of the identifier table — its counter widening and its
`lode show` support; this part owns SPEC/ADR/PLAN and the doc-number cutover.
Together they are `full` (`DEL` already works; bare task ids are explicitly
unchanged). §5 is `full`: the table, the store/API surface, and both crossing
reference kinds are built here — part 4 merely inserts `seeded_by` rows
through them. §1 is `none`: this plan is bound by §1's definition of
`seeded_by` as a reference (it shapes Task 6's rel vocabulary) but builds none
of §1 — labels, `horizon`, and the promotion that writes `seeded_by` are part
4's.

**Tech stack:** Go 1.26, `net/http` mux, pgx against Postgres,
`templ`-rendered pages, Prometheus client. Store and `internal/api` tests
need Postgres with pgvector.

**Read first:**
- `docs/specs/inlined/029-research-work-in-the-backbone.md` §4, §5
- `docs/specs/inlined/025-documents-in-the-backbone.md` §14.3, §16.3, §16.5
- `internal/store/docs.go` — `CreateDoc` (:98-160, the create path this plan
  rewires), `resolveDocRefBase` (:1978-2021, shorthand resolution), `DocIRI`
  (:2657, the graph id form that does *not* change)
- `internal/designdoc/corpus.go:274-329` — `loadPlans` / `planSpecOrdinal`,
  the corpus-order derivation the backfill must reproduce exactly
- `internal/cmd/show.go` — `typedID` (:20), `classify` (:83),
  `unshowableReason` (:61-73), `showOrdinalShape` (:119),
  `dispatchShowKind`/`runDocShowByOrdinal`
- `deploy/base/migrations/0015_deliverables.up.sql` (`project_entity_seq`),
  `0027_docs.up.sql` + `0034_soft_delete.up.sql` (the docs unique indexes),
  `0037_docs_number_matches_kind.up.sql` (the CHECK this plan replaces)
- `internal/store/deliverables.go:81-107` — the counter-upsert mint pattern
  every allocation in this plan copies

## Global Constraints

- **Exact spellings, quoted once.** Id grammar (029 §4): `COW-SPEC-4`,
  `COW-ADR-1`, `COW-PLAN-4-1`, `COW-PLAN-0-2`; bare task ids
  (`[A-Z][A-Z0-9]*-[0-9]+`) are untouched — only tasks appear in branch
  names, `Worklode-Task:` trailers, and merge correlation.
  `project_entity_seq.kind` CHECK after this plan:
  `('DEL','MILE','SPEC','ADR','PLAN')`; the `PLAN` counter is scoped by
  `subkey` = the parent spec ordinal as text (`'29'`, `'0'`); every other
  kind uses `subkey = ''`. Edge rels, the full vocabulary this plan admits:
  `'depends_on'` (milestone → deliverable), `'seeded_by'` (project → task).
  Routes: `GET/POST /api/v1/references`,
  `POST /projects/{id}/milestones/{mid}/references`. Permissions:
  `reference.read`, `reference.write`. Event type: `reference.created`.
  Metric: `worklode_reference_writes_total{rel,outcome}` — both labels
  bounded, never a project or entity id.
- **No id moves silently.** The backfill (Task 1) reproduces
  `loadPlans`'s corpus-order derivation — spec ordinal from the first
  `covers` entry, plan ordinal ascending by slug within the
  `(project, spec-ordinal)` group — so a backfilled id equals the id the
  disk corpus derives today. Task 2 proves it against the real corpus
  before anything else builds on the column.
- **Migrations:** each is a new numbered `.up.sql`/`.down.sql` pair listed in
  `deploy/base/kustomization.yaml`, never an edit to a shipped one. This
  plan's numbers are 0054 and 0055 (nominal; nine parts are claiming numbers
  in parallel and `./scripts/check-migrations.sh` renumbers on collision).
- **Every mutation is one event.** Reference creation wraps its store write
  in the same `RecordEvent` + apply-callback transaction `recordDeliverable`
  (`internal/api/deliverables.go:98`) uses. Document creation keeps its
  existing `doc.created` event; allocation changes the number's origin, not
  the event.
- **One model (ADR 036).** `model.EntityEdge` and the `SpecOrdinal` field on
  `model.Doc` are declared in `internal/model` with wire-name fields;
  `internal/model/rule_test.go` and `deps_test.go` stay green.
- **Every route is named in `routeGuards`** (`internal/api/router.go`);
  `NewServer` refuses to boot otherwise. Role checks stay in the `grants`
  table, never in handlers.
- **`internal/cmd` decides, `internal/cli` renders:** the two-part plan
  ordinal is formatted by a shared cell formatter in `internal/cli/render.go`,
  never inline in a cobra `RunE`; `renderrule_test.go` is the tripwire.
- **Metrics** (spec 022): nil-safe metrics struct in the owning package's
  `metrics.go`, `prometheus.Registerer` threaded from `serve.go`,
  `worklode_` prefix, bounded labels, tests.
- **Store tests need Postgres with pgvector**; they skip silently without it
  unless `CI` is set — a green run without Postgres proved nothing.
- **`e2e/` drives public surfaces only** — HTTP API, signed webhooks, web
  pages; never a direct store write.
- **Every task leaves `go test ./...` green** and ends with a commit.

## Decisions this plan executes (settled here; do not reopen)

- **Plan counters live in `project_entity_seq`, scoped by a new `subkey`
  column.** 029 §4 says plans count "per parent spec", which the table's
  `(project_id, kind)` key cannot hold; the alternative — minting plan
  ordinals from `max(number)+1` over sibling doc rows — would give documents
  a second mint mechanism beside the counter every other kind uses. One
  mechanism wins: `subkey text NOT NULL DEFAULT ''`, primary key widened to
  `(project_id, kind, subkey)`. Every existing `ON CONFLICT (project_id,
  kind)` mint (deliverables, part 1's milestones) is updated to the new
  conflict target in the same task as the migration, because the old target
  stops matching a unique constraint the moment the PK changes.
- **The backfill orders plans by corpus order, not `created_at`.** 025 §16.3
  *permits* renumbering a plan whose corpus order differs from mint order; it
  does not require it. Corpus order reproduces every id the disk corpus
  derives today, so the cutover moves nothing — the §16.3 exception then
  governs only post-cutover minting, where the counter (mint order) takes
  over. Task 2's rehearsal is the proof.
- **Explicit numbers stay accepted on the API for import.** "Sequence-
  allocated, not author-supplied" governs authoring: `lode doc new` without
  `--number` gets the counter's next value. The corpus importer still states
  each spec/ADR's number (the filename carries identity), and an explicit
  insert advances the counter past it. Plans take no explicit ordinal
  anywhere: the importer walks files in corpus order, so server-side
  allocation reproduces corpus order by construction.
- **`DocIRI` does not change.** Plans keep `wlid:doc/plan-<project>-<slug>`:
  the slug-keyed IRI is already stored as `wl:subject` on every doc event,
  and rewriting identity in the append-only event log is exactly what this
  repo never does. The number is an id for humans and references; the IRI
  is for provenance. Update the comment at `internal/store/docs.go:2657`
  (it cites "plans carry no number") in Task 1, nothing else.
- **`task_edges` stays where it is.** Its four rels are task-only, carry real
  foreign keys into `tasks`, and feed the hierarchy and blocking queries on
  the hottest path; folding them into a polymorphic `(kind, id)` table would
  trade FK integrity for uniformity nobody asked for. `entity_edges` carries
  only what cannot live there: references whose ends are different kinds.
- **The web add-reference form takes a typed deliverable id** (`COW-DEL-3`),
  validated server-side, rather than a cross-project dropdown — a picker over
  every deliverable in every project is part 4-era UI at the earliest.

## Tasks

### Task 1 — Migration 0054 and the allocation cutover in the store

```yaml
kind: feature
priority: high
skills:
  - worklode-migrations
  - golang-migrate:test-roundtrip
blockedBy: []
```

The schema and the code that satisfies it land together: 0054 makes
`docs.number` NOT NULL, so a `CreateDoc` that still inserts NULL for plans
would break every plan-creating test between two tasks. One task, one commit.

Create `deploy/base/migrations/0054_doc_identifier_cutover.up.sql` /
`.down.sql`, listed in `deploy/base/kustomization.yaml`:

```sql
-- Identifier cutover (029 §4, amending 025 §14.3): document numbers are
-- allocated from project_entity_seq, and a plan carries the two-part
-- ordinal <spec>-<n>.

-- 1. Counters. 0052 widened the kind CHECK to ('DEL','MILE'); this adds the
--    document kinds. subkey narrows one kind's counter: '' everywhere except
--    PLAN, whose counter is per parent spec ordinal ('29', '0').
ALTER TABLE project_entity_seq ADD COLUMN subkey text NOT NULL DEFAULT '';
ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL','MILE','SPEC','ADR','PLAN'));
ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_pkey;
ALTER TABLE project_entity_seq ADD PRIMARY KEY (project_id, kind, subkey);

-- 2. A plan's first ordinal: its parent spec's number, 0 for NO-SPEC.
ALTER TABLE docs ADD COLUMN spec_ordinal integer;

-- 3. Backfill every plan row (tombstoned included), reproducing
--    internal/designdoc/corpus.go loadPlans exactly: spec ordinal from the
--    FIRST covers edge (resolved target's number; else the leading number of
--    the external ref's base filename; else the shorthand's number; else 0),
--    plan ordinal by slug ascending within (project, spec ordinal).
WITH first_covers AS (
    SELECT DISTINCT ON (e.from_doc) e.from_doc, e.to_doc, e.to_external
      FROM doc_edges e WHERE e.type = 'covers'
     ORDER BY e.from_doc, e.id
),
derived AS (
    SELECT d.id, d.project_id, d.slug,
           COALESCE(
               t.number,
               NULLIF(substring(regexp_replace(
                   split_part(fc.to_external, '#', 1), '^.*/', '')
                   FROM '^0*([0-9]+)-'), '')::int,
               NULLIF(substring(split_part(fc.to_external, '#', 1)
                   FROM '^[A-Z][A-Z0-9]{1,9}-SPEC-([0-9]+)$'), '')::int,
               0) AS spec_ord
      FROM docs d
      LEFT JOIN first_covers fc ON fc.from_doc = d.id
      LEFT JOIN docs t ON t.id = fc.to_doc
     WHERE d.kind = 'plan'
),
numbered AS (
    SELECT id, spec_ord, row_number() OVER (
               PARTITION BY project_id, spec_ord ORDER BY slug) AS plan_ord
      FROM derived
)
UPDATE docs SET spec_ordinal = n.spec_ord, number = n.plan_ord
  FROM numbered n WHERE docs.id = n.id;

-- 4. The invariant, replacing 0037's biconditional: every document carries a
--    number; exactly plans carry a spec ordinal.
ALTER TABLE docs ALTER COLUMN number SET NOT NULL;
ALTER TABLE docs DROP CONSTRAINT docs_number_matches_kind;
ALTER TABLE docs ADD CONSTRAINT docs_number_matches_kind
    CHECK ((kind = 'plan') = (spec_ordinal IS NOT NULL));

-- 5. Identity index, widened by the plan's first ordinal. Same name and the
--    same liveness predicate as 0034, so CreateDoc's ErrDocExists mapping
--    still fires.
DROP INDEX docs_project_kind_number;
CREATE UNIQUE INDEX docs_project_kind_number
    ON docs (project_id, kind, coalesce(spec_ordinal, -1), number)
    WHERE deleted_at IS NULL;

-- 6. Seed the counters past everything already minted, tombstones included
--    so a purged number is never reissued. No ON CONFLICT: the old CHECK
--    made SPEC/ADR/PLAN rows impossible, so none exist.
INSERT INTO project_entity_seq (project_id, kind, subkey, next)
SELECT project_id, upper(kind), '', max(number) + 1
  FROM docs WHERE kind IN ('spec','adr') GROUP BY project_id, kind;
INSERT INTO project_entity_seq (project_id, kind, subkey, next)
SELECT project_id, 'PLAN', spec_ordinal::text, max(number) + 1
  FROM docs WHERE kind = 'plan' GROUP BY project_id, spec_ordinal;
```

Down (golang-migrate:gen-down, in reverse order): delete the
SPEC/ADR/PLAN counter rows; restore the `(project_id, kind)` PK, drop
`subkey`, restore the `('DEL','MILE')` CHECK; restore 0034's index; set
plans' `number` back to NULL, drop the NOT NULL, restore 0037's
`CHECK ((kind = 'plan') = (number IS NULL))`; drop `spec_ordinal`.

Store changes, same commit:

- `internal/store/docs.go` `CreateDoc`: replace the number/kind switch. A
  spec or ADR with `Number == 0` allocates from the `(project, SPEC|ADR, '')`
  counter with the `deliverables.go:99` upsert pattern; an explicit
  `Number > 0` inserts it and advances the counter with
  `next = GREATEST(project_entity_seq.next, $n + 1)`. A plan refuses an
  explicit number as today, derives its spec ordinal (below), and allocates
  its second ordinal from `(project, 'PLAN', specOrdinal)`.
- New helper in `docs.go`: `planSpecOrdinal(tx, project, fm) (int, error)` —
  the server-side twin of `designdoc.planSpecOrdinal`: first `covers` entry;
  none or `NO-SPEC` → 0; else resolve the base through `resolveDocRefBase`
  and read the target's number; unresolved → `designdoc.ParseShorthand`'s
  number or the exported leading-number parse (`designdoc.LeadingNumber`,
  the renamed export of `corpus.go`'s `leadingNumber`); nothing parseable →
  `ErrInvalidInput` naming the ref.
- `model.Doc` gains `SpecOrdinal int` (`json:"spec_ordinal,omitempty"`,
  0 on specs and ADRs); `docColumns` appends `spec_ordinal` **last** so
  positional scans keep working, `scanDoc` reads it (nullable → 0).
- Update the `DocIRI` comment (Decisions above); behaviour unchanged.
- `internal/api/docs.go:127`: drop the "a %s needs a corpus number" refusal
  for `Number == 0` — zero now means allocate.
- Update every `ON CONFLICT (project_id, kind)` on `project_entity_seq` to
  `(project_id, kind, subkey)` inserting `''`: `CreateDeliverable`
  (`internal/store/deliverables.go:99`) and part 1's milestone mint. **Part 1
  collision:** if part 1 has not landed its mint yet, its plan inherits the
  three-column target from this commit; whichever lands second rebases.

First test of the new behaviour, in `internal/store/docs_test.go`:

```go
func TestCreateDocAllocatesNumbers(t *testing.T) {
	st := OpenTestStore(t)
	// ... project fixture with key "WL" ...
	spec := mustCreateDoc(t, st, DocInput{Kind: "spec", Slug: "001-a", Body: specBody})
	if spec.Number != 1 {
		t.Fatalf("first spec number = %d; want 1 (allocated)", spec.Number)
	}
	// Explicit number advances the counter past itself.
	mustCreateDoc(t, st, DocInput{Kind: "spec", Slug: "007-b", Number: 7, Body: specBody})
	next := mustCreateDoc(t, st, DocInput{Kind: "spec", Slug: "008-c", Body: specBody})
	if next.Number != 8 {
		t.Fatalf("allocated after explicit 7 = %d; want 8", next.Number)
	}
	// A plan covering spec 7 is PLAN-7-1; a NO-SPEC plan is PLAN-0-1.
	p := mustCreateDoc(t, st, DocInput{Kind: "plan", Slug: "2026-08-25-x", Body: planCovering7})
	if p.SpecOrdinal != 7 || p.Number != 1 {
		t.Fatalf("plan ordinal = %d-%d; want 7-1", p.SpecOrdinal, p.Number)
	}
}
```

- [ ] Write both migration files; add all four lines to
      `deploy/base/kustomization.yaml`.
- [ ] `./scripts/check-migrations.sh --no-fix` — exit 0 (or accept the
      renumber and update this plan's references to 0054).
- [ ] Roundtrip: up → down → up applies cleanly against a scratch database.
- [ ] Store changes above; extend `docs_test.go` with the allocation test and
      a case per branch (explicit advance, plan NO-SPEC, plan with
      unresolvable-but-parseable covers path, second plan under the same spec
      getting `-2`).
- [ ] `go test -trimpath ./internal/store ./internal/api ./internal/model` —
      ok, against the compose Postgres.
- [ ] `go test -trimpath ./...` green; commit:
      `Allocate doc numbers from per-kind counters (029 §4)`.

### Task 2 — Backfill rehearsal: prove no id moves

```yaml
kind: chore
priority: high
skills: []
blockedBy: [1]
```

The backfill rewrites identity across the whole corpus; rehearse it against
the real one before anything builds on the column. The trick is that the
import must run with **pre-cutover** code against the **pre-0054** schema, so
build the merge-base binaries into a scratch worktree:

```bash
BASE=$(git merge-base HEAD origin/main)
git worktree add /tmp/wl-rehearsal "$BASE"
make -C /tmp/wl-rehearsal build-user bin/lode-server bin/lode-migrate
```

Then, against a scratch database (compose Postgres, fresh DB `rehearsal`):

- [ ] Migrate to the pre-cutover schema with the merge-base migrations
      (which end at 0053 or earlier):
      `/tmp/wl-rehearsal/bin/lode-migrate up` (DSN pointed at `rehearsal`).
- [ ] Start `/tmp/wl-rehearsal/bin/lode-server` against it, bootstrap a
      project keyed `WL`, and import this repo's corpus:
      `lode doc import` (admin token; expect one line per file, plans
      showing `-` for number).
- [ ] Stop the server; apply this branch's 0054 alone:
      `migrate -path deploy/base/migrations -database "$DSN" up` (or
      `bin/lode-migrate up` from this worktree).
- [ ] Run the invariant queries; expected output as stated:

```bash
psql "$DSN" -Atc "SELECT count(*) FROM docs WHERE kind='plan'
                   AND (number IS NULL OR spec_ordinal IS NULL)"
# 0
psql "$DSN" -Atc "SELECT count(*) FROM docs WHERE kind='plan'"
# equals: ls docs/plans/*.md | wc -l   (93 at time of writing)
# Dense ordinals: every (project, spec_ordinal) group counts 1..n with no gap.
psql "$DSN" -Atc "SELECT count(*) FROM (
    SELECT project_id, spec_ordinal, count(*) c, max(number) m
      FROM docs WHERE kind='plan' GROUP BY 1,2) g WHERE c <> m"
# 0
# Counters sit one past the max for every kind.
psql "$DSN" -Atc "SELECT count(*) FROM project_entity_seq s
    JOIN (SELECT project_id, upper(kind) k, coalesce(spec_ordinal::text,'') sk,
                 max(number) m FROM docs GROUP BY 1,2,3) d
      ON (d.project_id, d.k, d.sk) = (s.project_id, s.kind, s.subkey)
   WHERE s.next <> d.m + 1"
# 0
```

- [ ] Spot-check the spec-29 group against the corpus order the loader
      derives (`loadPlans`: first `covers` entry, then slug ascending):

```bash
psql "$DSN" -c "SELECT p.key || '-PLAN-' || d.spec_ordinal || '-' || d.number,
                       d.slug
                  FROM docs d JOIN projects p ON p.id = d.project_id
                 WHERE d.kind = 'plan' AND d.spec_ordinal = 29
                 ORDER BY d.number"
# WL-PLAN-29-1 | 2026-08-14-approvals-1-table-and-web-act
# WL-PLAN-29-2 | 2026-08-14-project-crew-participants
# ... then this 2026-08-25 series in filename order
```

- [ ] Record the rehearsal's outcome in the task's close note: either "no id
      differs from the disk derivation" or the exact list of renumbered plans
      (the 025 §16.3 exception), so the operator applying 0054 in production
      knows what to expect. If any query's expectation fails, fix Task 1's
      backfill SQL — the loader is the oracle, not the SQL.
- [ ] `git worktree remove /tmp/wl-rehearsal`; drop the scratch DB. Commit
      anything Task 1 needed fixing; otherwise this task ships no code.

### Task 3 — The PLAN shorthand parses and resolves

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

`internal/designdoc/resolve.go`: widen `shorthandPattern` to

```go
shorthandPattern = regexp.MustCompile(
	`^([A-Z][A-Z0-9]{1,9})-(SPEC|ADR|PLAN)-(\d+)(?:-(\d+))?$`)
```

`Shorthand` gains `PlanOrdinal int` (the second number; the first stays in
`Number`, which for a plan is the spec ordinal). `ParseShorthand` refuses the
mismatched shapes: `WL-PLAN-4` (a plan needs both ordinals) and
`WL-SPEC-4-1` (only plans carry two) both report false — the caller falls
through to the other ref forms, exactly as an unknown shape does today.
`Kind()` maps `PLAN` → `"plan"`.

`internal/store/docs.go` `resolveDocRefBase`: the shorthand arm branches on
kind — spec/ADR query unchanged; `plan` queries
`kind = 'plan' AND spec_ordinal = $2 AND number = $3` with the same
liveness ordering.

Table-test `ParseShorthand` (no database) with at least: `WL-PLAN-29-2` ok,
`WL-PLAN-0-1` ok, `WL-PLAN-4` false, `WL-SPEC-4-1` false, `WL-ADR-7`
unchanged. Store-test resolution: import two plans under one spec, resolve
`WL-PLAN-<n>-2` to the second, and confirm `WL-PLAN-<n>-9` resolves to
nothing rather than erroring.

- [ ] `designdoc` changes + table tests.
- [ ] `resolveDocRefBase` plan arm + store test.
- [ ] Check the fold points the grammar feeds: `secfmt.py` and
      `docs/authoring-design-docs.md` still say "plans have no shorthand" —
      leave the *docs* to Task 10, but confirm nothing in `scripts/` or
      `internal/designdoc` hard-rejects the new form.
- [ ] `go test -trimpath ./...` green; commit:
      `Parse and resolve the WL-PLAN-4-1 shorthand (029 §4)`.

### Task 4 — `lode show` stops refusing plans

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

`internal/cmd/show.go`:

- `classify`: move `"PLAN"` from the `targetUnshowable` arm to `targetDoc` —
  `resolveDocRef` now owns the grammar end to end. `MILE` stays unshowable
  here; **part 1 collision:** part 1's plan removes the `"milestone"` entry
  from the same `unshowableKindWords`/`unshowableReason` maps in its own
  task; whichever lands second rebases the two-line diff.
- Delete the `"plan"` entries from `unshowableKindWords` and
  `unshowableReason`, and the `PLAN` case's flag help
  (`--plan ... not showable yet`) becomes `show a plan by ordinal (e.g.
  --plan 4-1)`.
- `dispatchShowKind`: route `"plan"` to `runDocShowByOrdinal`, which gains
  the plan shape: with a known project key, build `<KEY>-PLAN-<value>` (the
  value already carries both ordinals; `showOrdinalShape` at :119 admits
  `4-1` today). With no key configured there is no bare-number fallback for
  a two-part ordinal — a lone `4-1` would misparse as number-form 4 — so
  refuse with `--plan needs a configured project key; pass the full id
  (WL-PLAN-4-1) positionally`. `--section` stays spec/ADR-only (plans have
  no sections); `expectedKind` plumbs `PLAN` through `runDocShow`'s kind
  check so `--plan` on a spec's ordinal still mismatches loudly.

Tests, in `internal/cmd/show_test.go`: the pinned refusal strings at
:640-656 and :952-956 change sides — replace the plan refusal tests with:

```go
func TestShowPlanResolves(t *testing.T) {
	setupDocServer(t, "WL", map[string]string{"014-fixture.md": fixtureSpec},
		withPlan("2026-08-25-p", planCovering14)) // extend the helper as needed
	out, err := runLode(t, "show", "WL-PLAN-14-1")
	if err != nil {
		t.Fatalf("lode show WL-PLAN-14-1: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "2026-08-25-p") {
		t.Fatalf("output does not render the plan: %s", out)
	}
}
```

plus `--plan 14-1` equivalent, the keyless refusal, and keep
`TestShowUnknownTypeErrors` (its "known types: SPEC, ADR, PLAN, MILE, DEL"
string is still true) and `TestShowPlanFlagOrdinalShape` (:983) as-is.

- [ ] `classify`/maps/dispatch/flag-help changes.
- [ ] Test updates; `go test -trimpath ./internal/cmd` ok.
- [ ] `lode show WL-PLAN-29-1` against the local stack renders
      the approvals-1 plan body; `lode show --plan 29-1` matches.
- [ ] `go test -trimpath ./...` green; commit:
      `lode show resolves plans by two-part ordinal (029 §4)`.

### Task 5 — CLI rendering and authoring surface for allocated numbers

```yaml
kind: feature
priority: medium
skills: []
blockedBy: [1]
```

The rendering half of the cutover, kept on the `internal/cli` side of the
seam:

- `internal/cli/render.go`: new shared cell formatter
  `DocOrdinal(kind string, specOrdinal, number int) string` — `"29-3"` for a
  plan, the bare number for a spec/ADR, `"-"` for 0 (a walked file the
  server has not numbered). `DocTable` (:287) uses it; `DocNumber` stays for
  callers formatting a bare number.
- `internal/cmd/doc.go:131`: `--number` help becomes `corpus number (spec/adr
  only; omit to allocate the next number)`; no validation change — the server
  decides (Task 1).
- `internal/cmd/docimport.go`: the dry run keeps printing `-` for plans (the
  ordinal is the server's to mint); the non-dry import output re-reads the
  created doc, so allocated numbers appear without change — verify, don't
  rebuild.

Test: a golden-ish table test on `DocOrdinal` (pure, no DB), and one
`DocTable` case with a plan row asserting the `29-3` cell.

- [ ] Formatter + tests; `renderrule_test.go` stays green (no tabwriter or
      timestamps entered `internal/cmd`).
- [ ] `lode doc new --kind spec --slug test-alloc --file -` against the local
      stack: response shows the allocated next number.
- [ ] `go test -trimpath ./...` green; commit:
      `Render two-part plan ordinals; doc new allocates by default (029 §4)`.

### Task 6 — Migration 0055: the `entity_edges` table

```yaml
kind: feature
priority: high
skills:
  - worklode-migrations
  - golang-migrate:test-roundtrip
blockedBy: []
```

Create `deploy/base/migrations/0055_entity_edges.up.sql` / `.down.sql`,
listed in `deploy/base/kustomization.yaml`:

```sql
-- References across projects (029 §5). Containment never crosses a project
-- boundary; these references do, which is why the ends are (kind, id) pairs
-- with no project column and no FK — each side's per-kind table carries
-- identity, and there is deliberately no unified entities table.
-- task_edges is NOT folded in: its rels are task-only, with real FKs into
-- tasks and the hierarchy/blocking queries tuned to them.
CREATE TABLE entity_edges (
    from_kind  text NOT NULL CHECK (from_kind IN ('project','milestone')),
    from_id    text NOT NULL,
    to_kind    text NOT NULL CHECK (to_kind IN ('deliverable','task')),
    to_id      text NOT NULL,
    rel        text NOT NULL CHECK (rel IN ('depends_on','seeded_by')),
    created_at timestamptz NOT NULL,
    created_by text REFERENCES actors (id) ON DELETE RESTRICT,
    PRIMARY KEY (from_kind, from_id, to_kind, to_id, rel)
);

-- The read is always "references touching this entity", from either end.
CREATE INDEX entity_edges_to_idx ON entity_edges (to_kind, to_id);
```

The PK doubles as the from-side index. The kind CHECKs carry only the kinds
the two admitted rels use, widened when a rel arrives that needs more —
the same convention as `project_entity_seq.kind`. Down: `DROP TABLE
entity_edges;`.

- [ ] Both files + kustomization lines;
      `./scripts/check-migrations.sh --no-fix` exit 0.
- [ ] Roundtrip up → down → up clean.
- [ ] `go test -trimpath ./internal/store -run TestMigrations` ok.
- [ ] Commit: `Add the entity_edges reference table (029 §5)`.

### Task 7 — Store and model: create and read references

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [6]
```

`internal/model/reference.go`:

```go
// EntityEdge is one typed reference between entities of different kinds
// (029 §5), the only edges that may cross a project boundary.
type EntityEdge struct {
	FromKind  string    `json:"from_kind"`
	From      string    `json:"from"`
	ToKind    string    `json:"to_kind"`
	To        string    `json:"to"`
	Rel       string    `json:"rel"` // depends_on | seeded_by
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}
```

`internal/store/references.go`:

- `referenceShapes` — the rel vocabulary as data, one row per rel naming its
  end kinds: `depends_on: milestone → deliverable`,
  `seeded_by: project → task`. A rel outside the table, or ends of the wrong
  kind, is `ErrInvalidInput` naming what was expected.
- `CreateEntityEdge(tx, now, in) (*model.EntityEdge, error)` — validates the
  shape, checks both ends exist in their kind's table (`milestones`,
  `deliverables`, `projects`, `tasks`; a missing end is `ErrNotFound` naming
  kind and id), inserts, and maps the PK violation to a named
  `ErrReferenceExists`. Cross-project ends are the point — no project check.
  Transaction-scoped like `CreateDeliverable`, for part 4's promotion
  transaction to compose.
- `(s *Store) ReferencesFor(ctx, kind, id) ([]model.EntityEdge, error)` —
  edges where either end matches, from-side first, stable order.
- `(s *Store) MilestoneDeliverableRefs(ctx, milestoneID)
  ([]model.Deliverable, error)` — the one bulk reader Task 9's page needs:
  join `entity_edges` (`rel = 'depends_on'`) to `deliverables` through the
  existing `deliverableFrom` projection, so the referenced deliverable's
  reported state comes along and "blocked on an unpublished table" is
  visible, not just a bare id.

First test:

```go
func TestCreateEntityEdgeShapes(t *testing.T) {
	st := OpenTestStore(t)
	// fixtures: project A with milestone M, project B with deliverable D, task T
	edge := mustRef(t, st, "milestone", mID, "deliverable", dID, "depends_on")
	if edge.CreatedAt.IsZero() {
		t.Fatal("created_at not stamped")
	}
	// wrong shape, unknown rel, dangling end, duplicate
	refuse(t, st, "milestone", mID, "task", tID, "depends_on")   // ErrInvalidInput
	refuse(t, st, "milestone", mID, "deliverable", dID, "eats")  // ErrInvalidInput
	refuse(t, st, "milestone", mID, "deliverable", "B-DEL-99", "depends_on") // ErrNotFound
	refuseDup(t, st, "milestone", mID, "deliverable", dID, "depends_on")     // ErrReferenceExists
}
```

plus a `seeded_by` case (project → task, across projects) — part 4 writes
that row in production; the shape is proven here.

- [ ] Model type (`rule_test.go` green), store file, tests including
      `ReferencesFor` from both ends and `MilestoneDeliverableRefs` carrying
      reported state.
- [ ] `go test -trimpath ./...` green; commit:
      `Store references in entity_edges with a typed rel vocabulary (029 §5)`.

### Task 8 — API: reference routes, event, metric

```yaml
kind: feature
priority: high
skills: []
blockedBy: [7]
```

`internal/api/references.go`, following `deliverables.go`'s shape:

- `POST /api/v1/references` — body is `model.EntityEdge` (only the five
  identity fields read; `created_by` is the authenticated actor). Wraps
  `CreateEntityEdge` in `RecordEvent(..., "reference.created", ...)` exactly
  as `recordDeliverable` does, with the same source derivation. 201 with the
  created edge; store sentinels map through `mapStoreErr` (`ErrInvalidInput`
  422, `ErrNotFound` 404, `ErrReferenceExists` 409).
- `GET /api/v1/references?kind=<k>&id=<id>` — `ReferencesFor`; both params
  required, 422 otherwise.
- `routeGuards` rows: `guarded(permReferenceRead)` /
  `guarded(permReferenceWrite)`; both permissions granted to
  `{RoleUser, RoleAdmin}` in `grants` — any crew member may declare a
  dependency, matching deliverable creation.
- `internal/api/metrics.go` (or the existing api metrics struct):
  `worklode_reference_writes_total{rel,outcome}`, `rel` bounded by the
  vocabulary, `outcome` `ok|error`. Test with a registry as the existing
  metric tests do.

Handler tests: create via `POST` and read back via `GET` from each end;
assert the `reference.created` event row exists with the actor; assert the
guard table boots (`NewServer` in tests already fails on an unguarded
route).

- [ ] Handlers, guards, grants, metric, tests.
- [ ] `go test -trimpath ./internal/api` ok.
- [ ] `go test -trimpath ./...` green; commit:
      `Reference API: POST/GET /api/v1/references (029 §5)`.

### Task 9 — Deliverable references on the milestone page

```yaml
kind: feature
priority: medium
skills:
  - worklode-cockpit-ui
blockedBy: [8]
```

Part 1's plan builds the cockpit milestone surface under
`/projects/{id}/milestones...`; this task extends its detail view (rebase to
part 1's exact route and templ component names as landed):

- A **References** section on the milestone detail: one row per
  `MilestoneDeliverableRefs` result — deliverable id (its project key makes
  the cross-project origin visible), name, and reported state, with the id
  linking to that project's Deliverables page. Empty state: `No deliverable
  references.`
- An add form: one text input named `deliverable` taking a typed id
  (`COW-DEL-3`), posting to
  `POST /projects/{id}/milestones/{mid}/references` — a `webform.go`
  handler that parses the id, resolves it to a deliverable, and writes
  through the same `RecordEvent` + `CreateEntityEdge` path as the JSON
  route (`reference.created`, source `web`). Unknown id → the form re-renders
  with the store's error text. Route guard: `guarded(permWebWrite)`.
- View types stay in `internal/ui/views.go`; `internal/ui` keeps its
  stdlib + `internal/model` + templ-runtime dependency set. Regenerate
  `*_templ.go` and the stylesheet if touched (`go generate ./...`).

Handler test: milestone page renders a seeded cross-project reference with
its reported state; the POST creates the edge and redirects back; a bad id
re-renders with the error.

- [ ] Store reader is Task 7's — reuse, don't re-derive.
- [ ] Templ + handler + guard + tests; `go generate ./...` diff committed.
- [ ] Screenshot or curl the page against the local stack: the reference row
      shows id, name, and state.
- [ ] `go test -trimpath ./...` green; commit:
      `Show and add deliverable references on the milestone page (029 §5)`.

### Task 10 — e2e journey and docs alignment

```yaml
kind: chore
priority: medium
skills:
  - worklode-lode-plugin
blockedBy: [4, 5, 9]
```

One e2e test in `e2e/` driving public surfaces only:

- Create a project; `POST /api/v1/docs` a spec with no number → number 1
  allocated; a plan covering it → `spec_ordinal` 1, number 1 in the
  response; `lode show <KEY>-PLAN-1-1` renders it.
- Create a milestone (part 1's API) in project A and a deliverable in
  project B; `POST /api/v1/references` the `depends_on` edge; `GET` it back
  from the deliverable end; load the milestone page and assert the
  reference row.

Docs alignment, in the same commit — the surfaces register
(`docs/agent-surfaces.md`) names the checklist; walk it for the changed CLI:

- `docs/authoring-design-docs.md`: the "Plans have no shorthand" paragraph
  and the `--number` flag prose are now false — rewrite both to the 029 §4
  state (plans have `<KEY>-PLAN-<spec>-<n>`; `--number` optional,
  server-allocated). Keep the edits short; this file is a checklist, not a
  spec.
- `docs/agent-surfaces.md`: check the skills and plugin commands that
  hardcode `lode doc new`/`lode show` invocations; update any that state
  "plans are not showable" or require `--number`.
- `docs/follow-ups.md`: check whether an existing entry about plan ids or
  `lode show` refusals is now discharged; strike it if so.

- [ ] `make test-e2e` (TEST_POSTGRES_DSN reachable) — the new journey
      passes.
- [ ] Docs edits above; `./scripts/secfmt.py -l` clean.
- [ ] `go test -trimpath ./...` green; commit:
      `e2e: allocated ids and cross-project references; align docs (029 §4, §5)`.

## What this part does not do

- **029 §1's project metadata**: `labels`, `horizon`, and the promotion
  transaction that stamps them and writes the `seeded_by` edge — part 4
  (`2026-08-25-research-work-3-intake-and-promotion`). This part only makes
  the edge row possible and proves its shape.
- **Milestones themselves** — table, API, page, `MILE` counter and
  `lode show` support: part 1.
- **Anything about approval flows** (029 §7) — parts 3 and 5.
- **A CLI verb for references**: the API, the web form, and part 4's
  transaction are the three writers 029 names; a `lode` verb waits for a
  caller that needs one.
