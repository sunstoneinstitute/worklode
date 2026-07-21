# Worklode v1 — architecture & spec map (umbrella)

**Date:** 2026-07-21 · **Status:** spec · **Source:** graduated from the approved design
record `../2026-07-21-work-tracker-platform-graph-design.md` (D1–D15; full rationale there).

**Worklode** (product; CLI `lode`) is Sunstone's platform work +
architecture system — the successor scope of "work-tracker."

**Thesis: development work as ambition reconciliation.** Intent is *asserted*; reality is
*observed*; every gap between them — architectural drift, an unimplemented spec, a deliverable
not yet in prod — is a query over the diff.

Visual model: https://claude.ai/code/artifact/f66372e2-af75-4ea7-a8c1-73f6783b4d4c

---

## Architecture in one screen

**Two stores, authority split — no fact has two owners:**
- **Execution backbone** — Worklode · **Postgres**. Task state, leases (worktree-bound), provenance
  events, and the `blocks`/`child_of` edges that gate the pickup loop. `claim` is a single ACID
  transaction. *("**Backbone**" = the load-bearing central store the rest of the system hangs off —
  the anatomical metaphor; used throughout the specs for this store.)*
- **Knowledge graph** — data-platform `graph-server` · an RDF quad store with named graphs,
  versioning / time-travel, and branches (the storage backend is an implementation detail).
  Components, design docs, architectural relationships, drift.
- **IRI is the join.** Execution facts flow backbone → graph (projection); design facts flow
  graph → backbone as references. The work graph lives on one branch; project is a *property*.

**Three layers on the graph:** Intent (asserted) / Execution·VCS (observed) / Runtime·Deploy
(observed). **Deliverable** is the vertical reconciliation point where intent meets prod reality.

**Drift = the two-layer diff.** Asserted edges (authored with design docs, crit-reviewed) vs.
observed edges (derived from code/PRs/deploys). Overview, gaps, and drift are reads over the diff.

---

## v1 / v2 scope

**v1:** Component-grained graph; DesignDoc (ADR/Spec/Plan) with `dct:hasPart` decomposition;
Task/Issue/PR/Artifact/Deployment/Environment projected from what Worklode already ingests;
Deliverable as declared definition-of-done; `concern`/`focus`/atomic `claim --next`; drift +
ready-frontier queries; the worktree-bound plugin.

**v2:** Milestone grouping + observed deliverable confirmation; Flux notifications for a live
deploy view; finer VCS/runtime nodes; weighted critical path; an operational ontology.

---

## Spec map

Decomposed to the **~100k-token "smart zone"** (D15) so each sub-spec's *implementation* fits a
single context. Each is an independent spec → plan → implementation cycle.

| Spec | Scope | Depends on |
|---|---|---|
| **01 — Execution backbone** | Postgres schema baseline; task state machine; **worktree-bound leases**; events; blocks/child_of. The foundation. | — |
| **02 — Prioritization & pickup** | `concern` enum; `project.focus`; ranking; atomic `claim --next`; `--strict-focus`; `needs-decomposition` sizing. | 01 |
| **03 — Knowledge graph** | `ls:` vocabulary (rdf-registry PR); IRI scheme; entity model (incl. Deliverable/Milestone); backbone→graph projection. | 01 |
| **04 — Drift & overview** | Observed-layer derivers; the two-layer diff; standing queries (drift, doc gaps, unimplemented specs, ready frontier). | 03 |
| **05 — Worklode plugin** | Worktree lease lifecycle; compiled Go hooks + daisy-chain; slash commands; skills (working-under-worklode, authoring-design-as-graph, architectural-review). | 01, 02 |
| **06 — Data-platform KG requirements** | Must-haves the data-platform must ship for the KG side (prod deploy, query path, IRI scheme, write auth, writable branch). | — (cross-repo) |

---

## Shared conventions (binding on all sub-specs)

- **Naming:** product = Worklode; CLI = `lode` (D13).
- **Vocabulary (D4):** standards-first — `dct:requires`/`hasPart`/`replaces`, `foaf:Agent`,
  `prov:*`, `doap:Project`, SKOS for status/enums. **Mint `ls:` sparingly** — the full minted set
  is defined in **spec 03**. **No gtio.** The `ls:` ontology ships as a PR to **rdf-registry**.
- **IRI (D-KG):** rdf-registry ADR-0006 grammar — branch-free term IRIs, `/id/…` for instances.
  Canonical scheme defined in **spec 03**; consumed by **06**.
- **Review:** design docs reviewed via **crit**; `proposed → accepted` gated on crit resolution.
- **Determinism lens (D14):** push coordination into deterministic, token-free machinery
  (compiled hooks, CLI+`--json`, server-side selection); spend model tokens only on judgment.

## Decision index (record → owning spec)

D1–D3 authority split → 00/01/03 · D4 vocabulary/entity model → 03 · D5 two-layer/drift → 04 ·
D6 three layers → 00/03/04 · D7 Deliverable → 03 · D8 lease/heartbeat/claim-next → 01/02/05 ·
D9 ranking → 02 · D10 concern → 02 · D11 Task-as-bridge → 01/03 · D12 estimate-free → 02 ·
D13 naming → 00 · D14 plugin → 05 · D15 task sizing → 02/05.

---

## Open questions across specs (to resolve before implementation)

**Cross-cutting / blockers (decide first):**
- _None open._ The last cross-cutting blocker — [04] drift suppression — is resolved below.

**Resolved:**
- **[04] Drift suppression → resolved.** An intentional observed-but-unasserted edge is marked
  accepted via an asserted-layer `ls:AcceptedDeviation` node (spec 03 §Accepted deviations): it
  names the tolerated edge with RDF reification (un-asserted), is `ls:sanctionedBy` an ADR, and may
  expire (`dct:valid`). Spec 04's 4.1 violation query subtracts un-expired deviations
  (`observed − asserted − acknowledged`). Crit-reviewed and provenanced like any asserted edge —
  not a backbone allowlist.
- **[03] RDF namespace → `ls:` / `/rdf/ls/`** (Worklode), not `wt:`.
- **[03] RDF-1.2 publishing → resolved.** rdf-registry now publishes 1.2 alongside 1.1 (1.1 as
  `/rdf/ls/ontology.ttl`, 1.2 as `/rdf/ls/ontology.1-2.ttl`), so partial-supersession triple-term
  annotations ship natively — no interim workaround, no rdf-registry #14 dependency.
- **[05] `EnterWorktree` cross-editor → accepted for v1** (Claude Code gets auto-resume; other
  editors get renewal via git hooks only).
- **[02] Flag name → `--strict-focus`.**
- **[01] Reopen target → `done → ready`** (forces a fresh claim).
- **[04] PR→Task join → GitHub-native `Closes #N`** (via bidirectional Task↔Issue mirror); frontier
  `is_critical`/`fan_out` are computed as a query, **not** cached.

**Per-spec, lower stakes:**
- **[02]** persistent per-project strict mode; should `concern` become required; cross-project
  `fan_out` weighting.
- **[04]** projection-lag between authoritative (backbone) and overview (KG) frontier — acceptable?
- **[01]** isolation level (READ COMMITTED + `FOR UPDATE` vs SERIALIZABLE); sweeper correctness
  under multiple server replicas; Task↔GitHub-Issue mirror lifecycle (spec 01 Q5 / 05 Q05.4).
