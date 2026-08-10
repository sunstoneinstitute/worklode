---
status: draft
issued: 2026-08-09
requires:
  - 014-design-documents-as-graph-objects.md
  - 019-project-scoping.md
  - 025-documents-in-the-backbone.md
  - 026-design-doc-queries.md
  - 029-research-work-in-the-backbone.md
amends:
  "#sec-7":
    - 025-documents-in-the-backbone.md#sec-2
---
# Spec 034 — Design-doc sync: the git→backbone on-ramp

## 0. Why {#sec-0}

Spec 025 (accepted) moves specs, ADRs, and plans into the backbone: authored
there, reviewed there, projected to the graph, with the git `docs/specs/` and
`docs/plans/` trees a transitional mirror — deleted, or kept as an opt-in git
mirror, once the corpus is imported (025 §2). That end state needs the authoring
surface 025 §10 reserves — `lode doc new`/`submit`/`accept` — and the document
store behind it. None of
it is built.

Until it is, design documents live only in git. The backbone cannot answer
`lode doc list --needs-planning`, an agent without the checkout cannot read a
spec, and "is this spec planned?" stays a script over a working tree rather
than a query. The corpus is real work with no presence in the system of record.

This spec is the on-ramp. It adds a one-way git→backbone sync that populates a
minimal document store from the git corpus now, so the backbone becomes the
queryable record for specs and plans before authoring moves server-side.
Authoring stays in git; reads move to `lode show`/`lode doc list`. When 025's
authoring lands, the sync retires.

## 1. On-ramp, not a second end state {#sec-1}

This does not change 025's destination — backbone-authored documents, with the
git trees then deleted or kept as an opt-in mirror (025 §2). It is the
incremental form of the one-time corpus import 025 §12 defers to
its final phase: the corpus flows continuously while git remains the authoring
surface, instead of arriving in one cutover.

025 §1's rule is "no fact with two owners." The sync relaxes it for the
transition, but bounds the relaxation so there is still a single authority:

- **One-way.** git → backbone only. The backbone's document rows are a
  projection; nothing edits them but the sync.
- **Default-branch gate (§3).** The projection is populated only from the
  reviewed, merged corpus. The authoritative copy is git on the default branch;
  the store is its read-through image.

So git-on-the-default-branch owns the fact; the backbone holds a derived copy.
No document has two authors.

## 2. What syncs — corpora and config {#sec-2}

`.worklode/config.toml` (git-tracked, repo-local) declares which directories
sync and as which kind. The config reader is a flat `key = "value"` parser with
no TOML-table support, so the declaration is two optional scalar keys:

```toml
current_project = "worklode"
project_key     = "WL"

spec_corpus = "docs/specs"   # synced as SPEC/ADR documents
plan_corpus = "docs/plans"   # synced as PLAN documents
```

A key's presence enables sync of that corpus; its value is the repo-relative
directory. The values shown are the conventional defaults — a repo whose docs
live elsewhere sets the path, and a repo that syncs specs but not plans (or
neither) omits the other key. Within `spec_corpus`, a file is an ADR when its
frontmatter carries `kind: adr` and a spec otherwise (026 §4.2); every file in
`plan_corpus` is a plan.

These keys are repo-scoped and are not merged from the user-level config, like
`worktree_dir` (019) — a corpus path means nothing outside its repository. They
generalize `designdoc.FindCorpus`, which today hardcodes `docs/specs`.

## 3. The sync command {#sec-3}

```
lode doc sync [--force] [-n|--dry-run] [--json]
```

`lode doc sync` reads each configured corpus, parses every document —
frontmatter, body, and for specs/ADRs the sections and their anchors — through
`internal/designdoc`, and upserts the results to the backbone (§4). It is a
push: git → backbone, never the reverse.

**Default-branch gate.** Without `--force`, sync refuses unless the checkout is
on the repository's default branch *and* the working tree is clean. The store
is a projection of the reviewed corpus (§1); syncing a feature branch or a dirty
tree would publish unreviewed text as the queryable record. The default branch
is read from the remote's `HEAD`; cleanliness from `git status --porcelain`.

**`--force`** bypasses the gate — it pushes from a non-default branch and from
uncommitted working-tree files, for local iteration and previews. A forced sync
records its provenance on the sync event (source branch, dirty flag), so a
later default-branch sync overwrites it and a consumer can tell a forced
projection from a reviewed one.

**`--dry-run`** reports what would change (added, updated, unchanged) and writes
nothing. **`--json`** emits the same report as objects. A document whose
frontmatter fails to parse is a sync error, not a silently skipped row.

## 4. The backbone document store {#sec-4}

A minimal slice of 025 §2's store — enough to receive the sync and serve reads,
and no more:

- `docs` — identity (`project`, `kind ∈ {spec, adr, plan}`, ordinal, §5),
  `status`, `title`, `body`, frontmatter, a version counter, and sync
  provenance (source branch, dirty flag, `synced_at`).
- `doc_sections` — anchor, heading, depth; specs and ADRs only. Plans take none
  (025 §4).
- `doc_edges` — `implements`, `amends`/`amendedBy`, `replaces`/`isReplacedBy`,
  section-scoped where an end is a section; plans' document-level `blocks`.

Upsert is idempotent on `(project, kind, ordinal)`: re-syncing an unchanged
corpus is a no-op, a changed document updates in place, and each sync appends to
the event log (004). The store carries `status` from frontmatter as data — it
does **not** run 025 §3's editorial transitions or §5's accept-mints-tasks; the
on-ramp populates, it does not author.

The API is an authenticated `/api/v1/docs` surface: a bulk upsert (the sync
target), get, and list (backing `lode doc list`). Metrics per §10.

## 5. Identity {#sec-5}

Identity is derived from the corpus, per 026's model, so ids are stable and
resolvable the moment a document exists:

- **Spec / ADR** — `<KEY>-SPEC-<n>` / `<KEY>-ADR-<n>`, where `n` is the file's
  leading number (`014-…​.md` → 14) and `<KEY>` is `project_key`.
- **Plan** — `<KEY>-PLAN-<spec-ordinal>-<plan-ordinal>`. The spec-ordinal is the
  number of the spec the plan `implements`; a plan with `implements: NO-SPEC` (or
  absent) uses `0`. The plan-ordinal counts the plans implementing that spec in a
  deterministic corpus order — ascending by filename (date prefix, then slug).

029 §4 moves id assignment to server-side per-`(project, kind)` counters. This
spec keeps derivation on the file side and writes the derived id into the store;
the grammar matches 029's, so the cutover changes where an id is minted, not the
id — with one bounded exception: 029 counts a plan-ordinal by mint order, this
spec by corpus order, so a plan whose corpus order differs from its eventual
mint order may be renumbered at the 029 cutover. Appending a new plan never
renumbers an existing one, so the exposure is limited to plans reordered or
back-inserted before 029 lands.

## 6. Reads {#sec-6}

Reads are 026's surface — `lode show <ref>` and `lode doc list` (with
`--kind`/`--status`/`--needs-planning`/`--needs-execution`). On a checkout they
read the disk corpus (026's `LoadCorpus`); against the backbone — no checkout, or
a cross-project reference — they read the synced store through the store-backed
loader 026 §10 anticipates. This spec supplies the store that loader reads; it
adds no read command of its own. 026 owns the read surface, 034 owns the
populate-and-store half.

## 7. Relationship to specs 025, 026, 029 {#sec-7}

**Amends 025 §2.** 025 §2 has the git trees "stay in git … until this spec is
implemented and the corpus is imported. From that point the backbone is the
store of record," the files then deleted or kept as an opt-in mirror. This spec
makes that import continuous: the corpus is synced to the backbone while git
remains the authoring surface, and the store-of-record cutover waits for
backbone authoring (025 §3/§5) to land. Whether the files are then deleted or
kept as an opt-in mirror is 025 §2's to settle, unchanged by this spec; 034 only
adds the ongoing on-ramp sync ahead of it.

**Extends 026.** 026 is a read-only surface by design (026 §9) and deliberately
adds no config key, putting the corpus directory on a `--docs` flag (026 §1).
This spec adds the populate half and the persistent per-corpus config keys (§2),
because a sync needs a durable, git-tracked declaration of what to sync, not a
per-invocation flag.

**Anticipates 029.** Id derivation is file-side here and counter-side there
(§5); the store's per-kind identity matches 029's grammar so the cutover
renumbers nothing but the plan-ordinal exception §5 names.

## 8. Ontology: `wl:Plan` {#sec-8}

`ns/` still carries 014 §2's decision that `wl:Plan` is dropped — "plan-shaped
work is a task subtree, not a document." 025 §4 (accepted) reintroduced plans as
documents, "a sibling of DesignDoc, not a subclass," and that reintroduction was
never mirrored into `ns/`. This spec closes the gap: `ns/ontology.ttl` gains
`wl:Plan` as a sibling of `wl:DesignDoc` that takes no sections or anchors, with
whatever SKOS or shape terms the synced kind needs, validated with `riot
--validate ns/*.ttl`. The governing spec (025 §4) already exists; this is the
mirror catching up, per the repo's amend-spec-first rule.

## 9. CLI ergonomics — `--body-file` {#sec-9}

`lode task add` gains a `--body-file <file>` flag beside `--body <string>`,
matching `gh`: `--body` takes an inline string, `--body-file` reads the body
from a file, and exactly one may be given. The document write commands 025 adds
(`lode doc new`) adopt the same pair. It is a small, standalone change, grouped
here because the plugin that consumes this on-ramp authors bodies from files.

## 10. Metrics {#sec-10}

Per 022, the new server surface carries `worklode_*` metrics with tests: docs
synced by kind and outcome, sync duration, a forced-sync counter, and store
upsert outcomes. The metrics struct is nil-safe and lives in the owning
package, with the `prometheus.Registerer` threaded from `serve.go` and label
values bounded.

## 11. Out of scope {#sec-11}

- Backbone authoring — `lode doc new`/`submit`/`accept`, editorial transitions,
  and the accept-mints-tasks transaction (025 §3, §5).
- Server-assigned per-`(project, kind)` counters, milestones, and deliverables
  (029).
- `api → git` pull; any bidirectional sync.
- `lode doc coverage`, which also needs the implementation side
  (`.worklode/implements.yaml`, 014 §6) — 026 §9.
- Deleting the git corpus (or standing up 025 §2's opt-in mirror), which waits
  for backbone authoring.

## 12. Acceptance criteria {#sec-12}

1. `.worklode/config.toml` accepts `spec_corpus` and `plan_corpus`; the loader
   exposes them repo-scoped, and an unknown key still errors.
2. `lode doc sync` on the default branch with a clean tree upserts every
   configured corpus document to the backbone; a second run reports no changes.
3. Off the default branch, or with a dirty tree, `lode doc sync` refuses; `--force`
   proceeds and records source branch and dirty flag on the sync event.
4. `--dry-run` reports adds/updates/unchanged and writes nothing; a document with
   unparseable frontmatter fails the sync.
5. Synced specs, ADRs, and plans resolve to `<KEY>-SPEC-<n>`, `<KEY>-ADR-<n>`, and
   `<KEY>-PLAN-<spec>-<plan>` per §5, and `lode doc list` returns them from the
   store.
6. `lode task add` accepts `--body-file`; `--body` and `--body-file` are mutually
   exclusive.
7. `ns/*.ttl` defines `wl:Plan` and passes `riot --validate`.
8. The sync endpoint and store operations export the §10 metrics, with tests.
