# Spec 04 — Drift & overview

**Status:** spec · **Umbrella:** `00-umbrella-architecture.md` · **Source decisions:** D5, D6,
D12 (design record `../2026-07-21-work-tracker-platform-graph-design.md`).

> **Dependency note:** the entity model, `ls:` vocabulary, and IRI scheme this spec queries are
> owned by **spec 03 — knowledge graph**, which is not yet written. Where a predicate or IRI is
> load-bearing here it is taken from the design record (D4) and marked *(03 confirms)*. If 03
> lands a different name, the query sketches below adopt it verbatim.

## Purpose & scope

This is the payoff of the whole design (D5): **development work as ambition reconciliation.**
Intent is *asserted*; reality is *observed*; every gap between them is a query over the diff.

This spec covers, and only covers:
- **The two-layer model** — how asserted vs. observed edges are represented so that *drift = the diff*.
- **Observed-layer derivers** — the concrete jobs that materialize the reality layer.
- **Standing queries** — drift, doc gaps, drifted specs, unimplemented specs, the ready frontier.
- **Critical path v1** — estimate-free criticality (D12).
- **The overview surface** — how all of the above is exposed (`lode` subcommands + read-only web).

Out of scope (reference, do not duplicate): the vocabulary/entity model/projection (03); the
`claim --next` ranking function itself (02 — we supply the *ready-frontier ordering* it consumes);
the execution backbone (01); the plugin (05); the data-platform deploy of the query path (06).

---

## The two-layer model

Two edge layers over the **same** node set (Components, DesignDocs, Tasks, Deliverables from 03):

- **Asserted layer (intent).** Authored *with* a design doc, crit-reviewed, `proposed → accepted`
  gated on crit resolution. These are the edges a human/agent claims are true: "component A
  *should* depend on B", "this Spec *governs* component C". Written by the design-authoring skill (05).
- **Observed layer (reality).** Derived mechanically by the derivers below from code, PRs, and
  deploys. No human authors these; they are recomputed and overwritten on every run.

**Drift = the set difference between the two layers, per predicate.** Every struggle-list item
(D5) is a read over that diff — never a bespoke report, always a standing query.

### Representation: named graphs per source

The KG is a named-graph quad store (06). We partition by source so each layer is independently
recomputable and a re-run is an **atomic named-graph replace** (`PUT`, per 06):

| Named graph | Layer | Written by |
|---|---|---|
| `…/graph/asserted/<designdoc-id>` | asserted | design-authoring skill (05), one graph per design doc |
| `…/graph/observed/go-imports` | observed | Go/package-import deriver |
| `…/graph/observed/repo-layout` | observed | component-boundary deriver |
| `…/graph/observed/pr-affects` | observed | PR-path deriver |
| `…/graph/observed/deploy` | observed | deploy/runtime projection from `internal/hooks/` |

Concrete IRI grammar for the graph names is owned by 03/06 (branch-free term IRIs, ADR-0006);
the `asserted` vs `observed/*` partition is this spec's requirement. A deriver **must** confine
its writes to its own `observed/*` graph, so a bad run can never corrupt asserted intent.

*(Alternative considered and rejected for v1: RDF-1.2 triple-term annotation tagging each edge
with a `ls:layer` — heavier to query and to replace atomically than one graph per source.)*

---

## Observed-layer derivers

All derivers share a contract:
- **Idempotent & full-replace.** A run computes the complete edge set for its source and `PUT`s
  its whole `observed/*` graph. No incremental deltas; no stale edges survive a run.
- **Deterministic.** Same inputs → same triples (stable IRI minting via 03's scheme).
- **Cheap to re-run.** Triggered on a schedule (CI/cron) and/or by the relevant hook; a content
  hash of the inputs short-circuits a no-op `PUT`.
- **Confined** to its own named graph (above).

### 1. Dependency edges — Go imports / `go.mod` / package manifests

- **Input:** for each repo, the resolved import graph. Go: `go list -deps -json ./...` +
  `go.mod` module paths. Other stacks: `package.json`, `pyproject.toml`/import scan, etc.
- **Map** each source package/module → the owning **Component** via the component-boundary
  manifest (deriver 2). Import edges *within* a component are dropped; edges *between* components
  become `<componentA> dct:requires <componentB>` *(03 confirms `dct:requires`)*.
- **Output graph:** `observed/go-imports`.
- This layer is the ground truth that architectural-drift queries compare asserted intent against.

### 2. `component lives-in repo` — filesystem + component-boundary manifest

- **Input:** repo filesystem + a **per-repo component-boundary manifest** (new authoring burden
  accepted in D5), e.g. `.worklode/components.yaml`, mapping path globs → Component IRIs. Needed
  because one repo can hold many components (e.g. **research-stack**); a repo with a single
  component gets a trivial whole-repo manifest (or a default).
- **Output:** `<repo> dct:hasPart <component>` linking the `doap:Project` (repo layer, D4) to
  each Component *(03 confirms the repo↔component predicate; `dct:hasPart`/`isPartOf` proposed)*.
- **Output graph:** `observed/repo-layout`.
- The manifest is also the **path→component index** consumed by derivers 1 and 3; it is the single
  place component boundaries are declared.

**Manifest sketch:**
```yaml
# .worklode/components.yaml
repo: <doap:Project IRI or short name>
components:
  - iri: <component IRI>          # minted per 03 scheme
    name: ingest
    paths: ["cmd/ingest/**", "internal/ingest/**"]
  - iri: <component IRI>
    name: graph-server
    paths: ["cmd/graph-server/**", "internal/graph/**"]
# first-match-wins; unmatched paths belong to no component (reported as a gap)
```

### 3. `task affects component` — PR paths

- **Input:** merged/open PR changed-file lists (already ingested by `internal/hooks/github.go`).
- **Join PR → Task:** via the deterministic worktree/branch name `wt/<id>-<slug>` (D14) or an
  explicit task ref in the PR body. *(Open question below — confirm the join is always present.)*
- **Map** each changed path → Component via the manifest, then emit `<task> ls:affects <component>`
  *(03 mints `ls:affects`)*.
- **Output graph:** `observed/pr-affects`.
- Feeds unimplemented/drifted-spec queries (a Task that touched a component the Spec governs).

### 4. deploy / runtime — existing Worklode hooks

- **Input:** the already-ingested relational tables behind `internal/hooks/flux.go`
  (Deployments, Environments) and `internal/hooks/github.go` (releases → Artifacts). D6: most of
  layers 2–3 are already ingested → this is **projection, not new build**.
- **Output:** observed `Artifact` / `Deployment` / `Environment` nodes and their edges to the
  Deliverable reconciliation point (Deliverable model owned by 03/D7).
- **Output graph:** `observed/deploy`.
- v1 stops at projecting what the hooks record; auto-*confirming* a Deliverable by probing prod is
  v2 (D7).

---

## Standing queries

Each is a SPARQL-shaped read over the graph. Sketches use `ls:` for vocabulary, `g:` for the graph
names above, and elide the IRI prefix boilerplate 03 defines. Layer comparison is expressed with
`GRAPH` clauses; `UNION` across sibling `observed/*` graphs is implied where written as one graph.

### 4.1 Architectural drift

Two directions, both surfaced:

**Violation — observed dependency absent from asserted** (undocumented coupling; code did
something the architecture never sanctioned), **minus sanctioned accepted deviations** (03):
```sparql
SELECT ?from ?to WHERE {
  GRAPH g:observed { ?from dct:requires ?to . }
  FILTER NOT EXISTS { GRAPH g:asserted { ?from dct:requires ?to . } }
  # suppress un-expired accepted deviations (ls:AcceptedDeviation, spec 03):
  FILTER NOT EXISTS {
    ?dev a ls:AcceptedDeviation ;
         rdf:subject ?from ; rdf:predicate dct:requires ; rdf:object ?to .
    # a deviation suppresses UNLESS it carries an expiry already in the past:
    FILTER NOT EXISTS { ?dev dct:valid ?exp . FILTER (?exp < xsd:date(NOW())) }
  }
}
```
Drift is thus **`observed − asserted − acknowledged`**, where *acknowledged* = un-expired
`ls:AcceptedDeviation`s. The deviation names the tolerated edge via RDF reification and never
asserts it (03), so suppression does **not** leak into the asserted layer and the *stale-intent*
query below is unaffected. `xsd:date(NOW())` casts the query clock to match `dct:valid`
(`xsd:date`); a runtime that lacks `NOW()` binds today's date from `lode` instead. Expired
deviations re-surface here automatically; all deviations (active + expired) are listable via
`lode drift --acknowledged`.

**Stale intent — asserted dependency absent from observed** (documented but no longer real; the
edge the architecture claims but code has abandoned):
```sparql
SELECT ?from ?to WHERE {
  GRAPH g:asserted { ?from dct:requires ?to . }
  FILTER NOT EXISTS { GRAPH g:observed { ?from dct:requires ?to . } }
}
```

### 4.2 Missing ADRs / doc gaps — Component with no governing DesignDoc

```sparql
SELECT ?c WHERE {
  ?c a ls:Component .
  FILTER NOT EXISTS { ?d a ls:DesignDoc ; ls:governs ?c . }
}
```
Also reports repo paths matched to no component (from deriver 2) as coverage gaps.

### 4.3 Drifted specs — DesignDoc modified after its last implementing Task

The design intent moved after the code that implemented it — the spec and the code have diverged:
```sparql
SELECT ?doc ?docMod (MAX(?done) AS ?lastImpl) WHERE {
  ?doc a ls:DesignDoc ; dct:modified ?docMod .
  ?t  ls:implements ?doc ; prov:endedAtTime ?done .
}
GROUP BY ?doc ?docMod
HAVING (?docMod > MAX(?done))
```

### 4.4 Unimplemented specs — accepted DesignDoc with no implementing Task/PR

```sparql
SELECT ?doc WHERE {
  ?doc a ls:DesignDoc ; ls:status ls:status/accepted .
  FILTER NOT EXISTS { ?t ls:implements ?doc . }
}
```
(`ls:status` SKOS scheme `draft→proposed→accepted→superseded→implemented`, D4.)

### 4.5 The ready frontier — ready + unblocked tasks

The set that feeds `claim --next` (02). "Frontier" = the **leading edge of actionable work** — the
tasks right at the boundary between done/blocked and not-yet-startable. A Task is on the frontier
when it is `ready` and no dependency/blocker is unresolved:
```sparql
SELECT ?t WHERE {
  ?t a ls:Task ; ls:status ls:status/ready .
  FILTER NOT EXISTS {
    ?t dct:requires ?dep . ?dep ls:status ?s .
    FILTER (?s != ls:status/done)
  }
  FILTER NOT EXISTS { ?b ls:blocks ?t . ?b ls:status ?bs . FILTER (?bs != ls:status/done) }
}
```

> **Authority caveat (important).** The `blocks`/`child_of` edges that gate pickup live on the
> **execution backbone** (Postgres, D2), and the *atomic* `claim --next` transaction must read them
> there — it cannot depend on the eventually-consistent KG projection. So the **authoritative**
> ready frontier is computed on the backbone (01/02); the query above is the **read-only overview**
> version for humans and dashboards. Both must agree; the KG copy is derived from the backbone
> projection. This spec does **not** supply the atomic ordering — 02 computes that on the backbone;
> below is only the read-only overview mirror.

**Overview frontier (mirror of 02's ordering).** The overview frontier is presented pre-sorted by
the same D9 key `(is_critical, concern_rank, priority, blocking_fan_out)` that **02 computes
authoritatively on the backbone**. For *human overview only*, 04 additionally offers an enriched
cross-store **critical path** (below) over `blocks` + `dct:requires`; this enriched metric is
**not** used by the atomic `claim --next`.

---

## Critical path v1 (D12)

**Estimate-free.** No effort weights in v1 (D12 — effort estimation judged unlikely to add
signal). Criticality is proxied by two unit-weight graph measures over the combined dependency
DAG whose edges are `dct:requires` (KG) **+** `blocks` (backbone):

- **Chain depth** `depth(t)` = length of the longest unit-weight predecessor chain ending at `t`.
  Tasks on a longest chain form the **critical path**; `is_critical(t)` = `t` lies on such a chain.
- **Blocking fan-out** `fanout(t)` = count of tasks **transitively** blocked by `t` (how much work
  `t` unblocks when done). Drives the `blocking-fan-out` term of the D9 sort key.

**Where it runs.** The DAG spans both stores (`blocks` on the backbone, `dct:requires` in the
KG), so Worklode computes it by joining: pull both edge sets, topologically sort, then a single
longest-path + transitive-closure pass. Not expressed as pure SPARQL (longest-path counting is
awkward in SPARQL); the query path only supplies the `dct:requires` edges. It is a **query,
not a stored property** — recomputed on each overview read (and thus never stale), never cached as
a materialized `is_critical`/`fanout` attribute.

**Cycle handling.** The union must be a DAG. A cycle (a `requires`/`blocks` loop) is a data error:
detect it, exclude the cycle from depth/fan-out, and surface it as its own overview finding rather
than looping or silently dropping edges.

Weighted critical path stays **v2** (optional, low priority — D12).

---

## Overview / CLI surface

Everything is a read; nothing here mutates the graph. All commands honor D14 determinism:
`--json` gives greppable, stable output; the compiled `lode` binary is the only dependency.

**CLI (`lode`):**

| Command | Returns |
|---|---|
| `lode overview` / `lode status` | one-screen roll-up: counts per drift class + critical-path head |
| `lode drift [--component <c>] [--acknowledged]` | 4.1 violations **and** stale-intent edges; `--acknowledged` lists accepted deviations (active + expired) |
| `lode gaps` | 4.2 doc gaps + unmatched-path coverage gaps |
| `lode specs --drifted` | 4.3 |
| `lode specs --unimplemented` | 4.4 |
| `lode frontier` / `lode ready [--project <p>]` | 4.5, pre-sorted by the ordering contract |
| `lode critical-path [--task <t>]` | critical path, `depth`, `fanout`; flags cycles |

**Read-only web views.** Worklode serves a small read-only dashboard backed by the SPARQL endpoint
(Oxigraph, per 06): a **drift board** (violations + stale intent), a **doc-gap** list, a **spec
status** view (drifted/unimplemented/accepted), a **critical-path** view, and the **ready
frontier**. Read-only by construction — the only ways to change the graph are authoring design
(asserted, via 05) and running derivers (observed); there is no mutation affordance in these views.

---

## Dependencies

- **03 — knowledge graph** *(not yet written)*: entity model, `ls:` vocabulary
  (`Component`/`DesignDoc`/`Task`/`governs`/`implements`/`affects`), Deliverable, `ls:status` SKOS
  scheme, and the IRI/graph-name grammar every query and deriver above binds to. **Hard blocker.**
- **06 — data-platform KG requirements**: the SPARQL read path (Oxigraph + outbox materializer),
  prod `graph-server`, the fixed writable branch, and external-service write auth the derivers
  `PUT` through. **Hard blocker for the observed-layer writes and every query.**
- **01 — execution backbone**: `blocks`/`child_of` edges and task status the ready frontier and
  critical path read; the backbone→KG projection that mirrors them.
- **02 — prioritization & pickup**: consumes the ready-frontier ordering and `is_critical` /
  `blocking-fan-out` this spec produces.
- **`internal/hooks/`** (`flux.go`, `github.go`): existing ingestion the deploy and PR-affects
  derivers project from.

## Open questions

1. ~~PR → Task join reliability~~ — **RESOLVED.** Tasks mirror **bidirectionally** to GitHub
   Issues (`ls:mirrors`, spec 03); a PR joins its Task the GitHub-native way — `Closes #N` in the
   PR body closes the mirrored Issue, and the `pr-affects` deriver resolves PR → Issue → Task
   through it. No bespoke branch-name parse or enforced PR-body task ref needed; we piggyback on
   how GitHub already models it. (Task↔Issue mirroring is a new requirement on 01/05.)
2. **Layer partition mechanism — confirm named graphs.** This spec commits to one named graph per
   source over RDF-1.2 edge annotation. 03/06 should ratify (or override) before implementation.
   (03 confirms these asserted/observed source graphs are **orthogonal** to its per-Workstream
   projection graphs.)
3. ~~**Drift acknowledgement / suppression.**~~ — **RESOLVED (spec 03 §Accepted deviations).**
   An intentional observed-but-unasserted edge is marked accepted with an asserted-layer
   `ls:AcceptedDeviation` node that names the edge via RDF reification (un-asserted), is
   `ls:sanctionedBy` an ADR, and optionally expires (`dct:valid`). Lives in the sanctioning
   ADR's asserted graph → crit-reviewed and provenanced, not a backbone allowlist. The 4.1 query above
   subtracts un-expired deviations (`observed − asserted − acknowledged`).
4. ~~Ready-frontier duality~~ — **RESOLVED (yes).** Authoritative frontier on the backbone (atomic
   `claim --next`); KG frontier is read-only overview only. Brief inconsistency under projection
   lag is accepted.
5. ~~Critical-path staleness~~ — **RESOLVED.** Critical path is a **query, not a property**: `depth`
   / `fanout` / `is_critical` are computed on read over backbone+KG, never cached or materialized as a
   stored attribute. (The atomic `claim --next` sorts by the columns the backbone owns; the enriched
   cross-store critical path is overview-only — see below.)

## Acceptance criteria

- **Two-layer wiring:** asserted and `observed/*` named graphs exist and are populated; a deriver
  re-run fully replaces its own graph and touches no other. A planted asserted edge and a planted
  observed edge round-trip.
- **Derivers:** all four run idempotently and confine writes to their graph. The component-boundary
  manifest format is defined and a **multi-component repo (research-stack)** resolves each path to
  the right Component; unmatched paths are reported.
- **Standing queries:** 4.1–4.5 each return correct results on a seeded graph. Architectural drift
  correctly reports **both** a planted violation (observed-not-asserted) and a planted stale-intent
  edge (asserted-not-observed).
- **Drift suppression:** a planted `ls:AcceptedDeviation` (sanctioned by an ADR) removes its edge
  from 4.1 violations while leaving the stale-intent query unchanged; the edge appears under
  `lode drift --acknowledged`; a deviation whose `dct:valid` is in the past re-surfaces as a
  violation.
- **Critical path:** `depth` and `fanout` are correct on a known DAG; a planted cycle is detected,
  excluded, and surfaced (not silently dropped, no infinite loop).
- **Ordering contract:** `lode frontier --json` returns the ready set pre-sorted by
  `(is_critical, concern_rank-placeholder, priority, fan-out)` and matches the backbone's frontier.
- **Surface:** every `lode` command above emits deterministic `--json`; the read-only web dashboard
  renders each view from the SPARQL endpoint with no mutation affordance.
