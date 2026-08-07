---
status: superseded
issued: 2026-07-21
amendedBy:
  ".":
    - 014-design-documents-as-graph-objects.md#sec-1
  "#sec-1":
    - 014-design-documents-as-graph-objects.md
    - 015-runtime-layer.md#sec-7
  "#sec-5":
    - 008-worklode-plugin.md
    - 011-delivery-lifecycle.md
    - 030-branch-and-worktree-naming.md
  "#sec-6":
    - 014-design-documents-as-graph-objects.md#sec-2
isReplacedBy:
  ".":
    - 004-execution-backbone.md
    - 005-prioritization-and-pickup.md
    - 006-knowledge-graph.md
    - 007-drift-and-overview.md
    - 008-worklode-plugin.md
    - 009-data-platform-kg-requirements.md
---
# Spec 003 — worklode → platform knowledge graph design record

> **Status corrected.** D1–D15 are no longer unimplemented: the backbone, ranking, per-project task keys, delivery lifecycle and agent sessions all shipped (migrations 0001–0005).

> **Prefix renamed by 014 §1.** Read every `ls:` / `lsc:` / `lsid:` below as `wl:` / `wlc:` / `wlid:` under `https://worklode.io/ns/`.

This design record holds decisions D1–D15. Specs `004`–`009` graduated out of
it; D-ids are not renumbered.

Thesis: **development work as ambition reconciliation.** Intent is *declared*; reality
is *observed*; every gap between them — architectural drift, an unimplemented spec, a
deliverable not yet in prod — is a query over the diff.

Visual: https://claude.ai/code/artifact/f66372e2-af75-4ea7-a8c1-73f6783b4d4c

---

## 1. Resolved decisions {#sec-1}

**D1 — Two stores, not one monolith.** An *execution* store and a *knowledge* store,
joined by IRI.

**D2/D3 — Authority split (no fact has two owners).**
- **Execution backbone** = Worklode · **Postgres**. Task state, leases, provenance events, and
  the `blocks`/`child_of` edges that gate the pickup loop. Keeps `claim` a single ACID
  transaction — Postgres lets N claims proceed concurrently, serializing only on contended rows.
- **Knowledge graph** = data-platform `graph-server` · an RDF quad store with named graphs,
  versioning/time-travel, and branches (storage backend an implementation detail).
  Components, design docs, architectural relationships, drift.
- **IRI is the join.** Execution facts flow backbone→graph (projection); design facts
  flow graph→backbone as references. The work graph lives on **one branch**; project is a
  *property*, not a branch (graph-server branches are sibling-invisible → project-as-branch
  would hide cross-project edges, the exact thing we need).

**D4 — Component-grained entity model, standards-first vocabulary.**

> **Amended by 014.** Prefix is `wl:`; `Plan` is dropped from DesignDoc; partial supersession uses addressable section IRIs, not the annotated edge; `implemented` leaves the status enum.

- Atomic unit = **Component** (repo/project are coarser groupings). No Capability/Requirement (YAGNI).
- **`ls:DesignDoc`** with real subclasses `ls:ADR` / `ls:Spec` / `ls:Plan`; `ls:status`
  domain `DesignDoc` (SKOS scheme: draft→proposed→accepted→superseded→implemented).
- **Supersession can be partial** — an ADR may be superseded only in specific subsections,
  not wholesale. Model options: addressable DesignDoc *sections* (sub-resources with their
  own IRIs), or a scoped `dcterms:replaces` edge annotated (RDF-1.2 triple term) with the
  affected section. Recommend the annotated-edge form (lighter); confirm.
- Reuse W3C/community terms: `dcterms:requires` (dependency), `dcterms:hasPart`
  (decomposition), `dcterms:replaces` (supersession), `foaf:Agent`/`prov:*` (owners),
  `doap:Project` (repo layer). **No gtio** (research-scoped, experimental).
- Mint only: `ls:Component`, `ls:DesignDoc`(+3), `ls:Task`, `ls:governs`, `ls:implements`, `ls:affects`.
- DesignDocs reviewed via **crit**; the `ls:` vocabulary lands as a PR to **rdf-registry**.

**D5 — Two-layer graph; drift = the diff.**
- **Declared layer** (intent): authored with the design doc, crit-reviewed.
- **Observed layer** (reality): derived by ingestors (Go imports, repo structure, PR paths, deploy/runtime hooks).
- Struggle-list becomes standing queries: drift, missing ADRs, drifted specs,
  unimplemented specs, doc gaps — all reads over the diff.
- New authoring burden accepted: a per-repo manifest declaring component boundaries
  (needed where one repo holds many components, e.g. research-stack).

**D6 — Three layers, v1/v2 scoped.**

> **Amended by 015 §7.** Commit is v1 (delivery resolution needs it) and WorkflowRun is gone — `wl:Build` subsumes it.

- Layer 1 Intent (declared): Component, DesignDoc, [Milestone v2], Deliverable.
- Layer 2 Execution/VCS (observed): Task, Issue, PullRequest; [Branch, Commit, WorkflowRun, Event v2].
- Layer 3 Runtime/Deploy (observed): Artifact, Deployment, Environment; [Cluster, Namespace, Flux* v2].
- Most of layers 2–3 already ingested relationally → projection, not new build.

**D7 — Deliverable in v1 = declared definition-of-done** (the intent-layer target, e.g.
"image `foo:tag` pushed", "service live in prod"). Auto-confirming it by probing
artifacts/deployments is v2.

**D8 — Scenario 1 pickup (from parallel-session handoff):**
- Lease renewal = **commit-cadence heartbeat** (renew before each commit-batch; no timer).
- **Collisions are the throughput cap.** Today `claim` needs an id (list→pick→claim→race→retry),
  so work is hand-kicked to avoid collisions → throughput capped. Prize = safe unattended
  parallel pickup (24/7 agents on a well-spec'd project).
- Fix = **`lode task claim --next`**: rank + lease in one serialized transaction. No
  list→pick→claim window = no collision.

---

## 2. Decisions from round 1 (crit) {#sec-2}

**D9 — `claim --next` ranking.** Default sort key `(is_critical, concern_rank, priority, blocking fan-out)`.
- **Critical bypasses focus by default** — a `critical` task is picked regardless of
  `project.focus` (critical always first).
- **`project.focus` is a soft filter** — below critical it directs the choice; agents never
  idle while ready work exists, they drift to off-focus work rather than stall.
- **`--strict-focus`** (renamed from `--override-focus`, which read against its meaning) —
  suppresses the critical-first jump so `project.focus` governs *even critical tasks*. Sort
  becomes `(concern_rank, priority, fan-out)`: critical work is still ordered by priority
  within its concern, it just no longer preempts focus. *Confirm the name.*

**D10 — Task `concern` property.** Name = **`concern`** (chosen over `dimension`/`aspect` —
right amount of specificity). A **fixed enum**: `completeness | performance | usability |
security | …` (closed set like `priority`; grows only by explicit schema change).
`project.focus` = an ordered list of concerns.

**D11 — Task-as-bridge confirmed.** Task is execution-owned (Postgres backbone) and *projected*
into the knowledge graph. Right cardinality.

**D12 — Estimate-free.** No effort estimates in v1 (effort estimation judged unlikely to add
meaningful signal). Weighted critical path stays optional and low-priority; criticality
proxied by unit-weight chain length + blocking fan-out.

---

## 3. Minimum data-platform support required (OPEN — to nail in round 2) {#sec-3}

Because leases/execution stay on the Postgres backbone, `graph-server` only has to host the
*knowledge* graph. Minimum for wl v1:

1. **Prod deployment of graph-server** (dev-only today).
2. **A read/query path** for overview + drift — *decision needed*: deploy Oxigraph + the
   outbox materializer (SPARQL), **or** commit to GSP `GET`-per-graph and query inside wl.
3. **Stable IRI scheme** for wl entities, aligned with rdf-registry ADR-0006 (branch-free
   term IRIs; `/id/…` for instances).

Explicitly **not** required for v1: lease/job primitive (backbone owns it), If-Match CAS
(a single projector + branch-lock atomicity suffices), branch merge/diff, per-namespace
ACL, markdown-as-asset (design content stays as files in the designs repo; only RDF
descriptors live in graph-server).

---

## 4. Naming — DECIDED: **Worklode** {#sec-4}

**D13 — Product name = Worklode**  Guides "what to work on next" — the north star this
whole design serves. **CLI = `lode`.** Repo rename is an optional follow-up, not decided here.

Shortlist considered:

- **Worklode** — guides "what to work on next" (your north star); reconciliation → navigation.
- **Cairn** — a stone waypoint marking the path and progress; Sunstone-stone resonance; humble, apt.
- **Keystone** — the stone that holds the arch (the whole platform) together.
- **Gnomon** — the sundial's shadow-caster; tells position by *observing* light — a literal
  nod to the observed layer, and on the sun theme.
- **Meridian** — the reference line where intent and reality align.
- **Reckon / Reckoner** — dead reckoning: fixing position from observed facts (= ambition reconciliation).

---

## 5. D14 — Claude Code integration: the Worklode plugin {#sec-5}

> **Amended by 008, 011 and 030.** The branch name is a server-rendered template (`LODE_BRANCH_TEMPLATE`, default `{{ .id }}-{{ .slug }}`); the worktree lives under a configurable base directory (`worktree_dir`, default `.worktrees`). The earlier `wt/` directory and `lode/`/`wl/` branch prefixes are gone.

**Design lens: push coordination into deterministic, token-free machinery; spend model tokens
only on judgment.** CLI over MCP, hooks over prompts, server-side selection over agent reasoning.

**The worktree is the unit of Worklode work; the lease binds to the git worktree, not the
session.** This is what keeps Worklode strictly *opt-in* — a plain Claude Code session is
untouched. You enter Worklode mode only by claiming, which spins up a worktree.
- `/lode-next [--project]` → atomic `claim --next` → creates a **deterministically named
  worktree** `wl/<id>-<slug>` (named from the task *after* the lease is held) → binds the lease
  to that worktree → injects the brief.
- The lease lives and dies with the worktree: **`ExitWorktree` / worktree removal → auto-release.**
  Sessions come and go; the lease persists while the worktree exists.
- `/lode-resume` → re-acquire an **expired** lease for a task whose worktree still exists
  (deterministic name makes the lookup trivial) — the "came back after the sweeper reclaimed it" case.

**Hooks (deterministic, ~0 tokens) — every one is a NOP outside a Worklode worktree:**
- `EnterWorktree` → **auto-resume** when the worktree has no already-running session, has a bound
  lease, and that lease has **expired**. (Safe: re-acquiring *your own* stalled worktree — distinct
  from auto-*claim*, which stays opt-in.)
- `SessionStart` → if inside a Worklode worktree, *resume*: inject `lode task brief`. Never a
  silent claim. **(Q14.3 resolved: resume-only.)** Outside a worktree, cheaply scan for
  **abandoned worktrees** (bound lease, expired, no running session) and *offer* to resume one —
  must stay fast: compiled `lode` scan, and any prompt is Haiku/script-level, never an expensive call.
- `PreToolUse` on `git commit` → `lode task renew` (commit-cadence heartbeat, D8) — **NOP when
  the session isn't on a Worklode task.**
- `ExitWorktree` / `SessionEnd` → release the worktree's lease if idle.
- Uniform guard: no bound worktree ⇒ the hook does nothing. Worklode is invisible to ordinary sessions.
- **Implementation:** hook executables are **compiled Go binaries** (fast startup, no interpreter),
  and **daisy-chain** rather than terminate — a `--next <cmd> [argv…]` option `execve`s the next
  hook instead of `exit(0)`, so Worklode composes with hooks a repo already has.

**CLI, not MCP (Q14.1 resolved: no MCP in v1).** Agents drive `lode --json`: no per-tool schema
tokens in context, deterministic greppable output.

**git hooks too, but must coexist (Q14.2 resolved: both).** `lode install` wires the
heartbeat for editor-agnostic use and must **chain with existing hooks, never clobber them** (via
the `--next`/`execve` daisy-chain above). **Sensible default:** if a `.pre-commit-config.yaml`
exists in the repo root, always chain with pre-commit.

**`lode task brief <id> --json` — deterministic context assembly (biggest token win).** One
bounded payload: task + concern/priority + governing Spec/Plan excerpt + affected components +
definition-of-done + branch. No file spelunking.

**Slash commands:** `/lode-next`, `/lode-resume`, `/lode-done`, `/lode-block <id>`, `/lode-status`, `/lode-spec`.

**Skills (judgment only):**
- *Working under Worklode* — done/block/release judgment (renewal is hook-enforced).
- *Authoring design as graph* — graduated to task complexity (D15): write only the design
  artifacts a task actually needs, get crit review, write declared-layer edges.
- *Architectural review* — uses the knowledge graph (existing ADRs/specs/components) to review a
  new spec/design for alignment with the overall architecture: pushes back to keep things aligned,
  or surfaces when the architecture itself needs to change. A first real payoff of ADRs/specs
  being first-class graph objects. Model it on the **`grill-with-docs`** skill pattern (challenge a
  plan against the existing domain model + ADRs, updating docs as decisions crystallise), but fed
  from the graph instead of loose files.
- Decomposition reuses existing superpowers, re-emitted as `lode` tasks with concern + priority.

**Subagent (optional):** `lode-worker` for headless 24/7 loops.

## 6. D15 — Task sizing & graduated decomposition {#sec-6}

> **Amended by 014 §2.** There is no Plan document: plan-shaped work is an ordered task subtree, so graduation now runs {nothing → task subtree → Spec/ADR}.

- **Not every task needs the same artifacts.** Most need a **Plan**; some need a **Spec/ADR**;
  many need neither. `/lode-spec` and the authoring skill are graduated — produce only what the
  task's complexity warrants; don't ceremony-tax small tasks.
- **`needs-decomposition`** — a task label meaning scope is too big to fit the context window's
  **"smart zone"** (the effective-reasoning region, well below the hard limit). Such a task is
  **not claimable by `claim --next`** until split; it routes to decomposition (Spec/Plan → child
  tasks) first. **Decomposing a big task is itself a Worklode task.**
- **The "too big" call is agentic, made during review** (crit), not a static filter — but backed
  by a concrete, **server-side-configurable token budget** (default ~**100k**). The reviewer
  (human or agent) sets `needs-decomposition` when the task's projected context would blow the budget.
- (**Q15.1 resolved:** assessed at review time, agentic, with a configurable token ceiling — no
  separate heuristic detector needed for v1.)
