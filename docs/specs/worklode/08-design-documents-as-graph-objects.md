# Spec 08 — Design documents as graph objects

**Status:** draft · **Umbrella:** `00-umbrella-architecture.md` · **Depends on:** 03 (knowledge
graph — amends it), 04 (drift & overview — supplies a new deriver), 01 (execution backbone) ·
**Amends:** 03, 04, 05, 07, and rdf-registry ADR-0006.

## Purpose & scope

Spec 03 already decided that design documents are **graph-authored, never projected** — a Spec or
ADR is an intent-layer object, not a file. The implementation never caught up: superpowers writes
`docs/superpowers/{specs,plans}/`, spec 07 models a spec as `task_docs(repo, path)`, and this
repo keeps `docs/specs/worklode/`. This spec closes that gap and makes design documents durable
enough to link against.

The organising problem: **an in-repo file must be able to say "this code satisfies §4.2 of spec 03,
as validated against version 3" and still be correct a year later.** That single requirement drives
everything below — addressable sections, immutable accepted content, document versioning, and
derived (never declared) implementation coverage.

This spec covers, and only covers:

- The **`ls:` → `wl:`** prefix rename.
- **`wl:Section`** — sections as addressable nodes, and the immutability constraints on them.
- **Document versioning** — canonical plus versioned IRIs, and atomic publication.
- The **editorial lifecycle** and the revision model for an accepted document.
- **Implementation coverage** — `.worklode/implements.yaml`, its deriver, and why coverage is
  derived rather than declared.
- **Demoting Plans** out of the document model into the execution backbone.
- The **authoring, review and publication surfaces**.

Out of scope (reference, do not duplicate): the two-layer drift model and the other derivers (04);
task ranking (02); the lease lifecycle (01); reconciliation of *ingestion* gaps (07 — a different
diff over different entities); and **adoption** — importing an existing GitHub project's issues,
documents and repos into this model is candidate spec 09, not this spec (see *Adoption is out of
scope*).

---

## 1. Prefix rename: `ls:` → `wl:`

`ls:` predates the rename from *lodespar* to *Worklode*. No occurrence survives outside
documentation (187 occurrences across 11 Markdown files; zero in Go, SQL or YAML), and the `ls:`
ontology has not yet been opened as a PR against rdf-registry. The rename is therefore free now and
becomes a breaking change the moment that PR lands. **It must happen before spec 03 ships.**

| Role | Old | New | Namespace |
|---|---|---|---|
| Schema (classes/properties) | `ls:` | **`wl:`** | `https://worklode.io/ns/wl/ontology#` |
| Concepts (SKOS) | `lsc:` | **`wlc:`** | `https://worklode.io/ns/wl/concept/` |
| Instances | `lsid:` | **`wlid:`** | `https://worklode.io/ns/wl/id/` |

The **path segment changes with the prefix label** — rdf-registry sources move `rdf/ls/` → `rdf/wl/`
and the published base becomes `https://worklode.io/ns/wl/…`. A rename that stops at the prefix
label leaves the IRIs wrong, which is the only part that is actually load-bearing.

`wl:` also matches the existing `WL-<n>` task-id scheme, so the two identifier systems finally read
as one product.

---

## 2. What is, and is not, a design document

### Plans are demoted

Spec 03 places `ls:Plan` alongside `ls:ADR` and `ls:Spec` under `ls:DesignDoc`. That is wrong, and
spec 05 already contradicts it:

> Decomposition itself reuses existing **superpowers** skills (`writing-plans`, `brainstorming`,
> `subagent-driven-development`), **re-emitting the results as `lode` tasks** with `concern` +
> `priority`.

The distinguishing axis is **truth-condition and lifetime**, not what-versus-how:

- A **Spec or ADR** stays true after implementation. It is what drift is measured *against*, and
  what a reader consults to learn why the system has its present shape. It is superseded, never
  consumed.
- A **Plan** has a completion condition. Once executed it is *spent*. It never described the
  repository; it described a transition out of one state into another. Retained in the intent
  layer, a spent plan is permanent noise in every drift query.

The decisive test is §3's immutability constraint: locking an accepted spec's sections is correct,
and locking a plan's sections is actively harmful, because plans are rewritten mid-execution when
reality contradicts them. A class that must stay freely mutable cannot sit under a `wl:DesignDoc`
that carries the lock.

**Therefore:** `wl:Plan` is dropped. Plan-shaped work is an ordered task subtree in the execution
backbone — which is what a plan already is: a bundle of tasks with instructions attached. The
instructions live in task bodies and reach the agent through `lode task brief` (05).

**Durable rationale is promoted, not preserved.** Where a plan contains reasoning worth keeping
("we did it this way because X"), that paragraph is promoted into the governing Spec or a new ADR
before the plan's tasks close. Preserving whole spent plans to catch the occasional durable
sentence is the wrong trade; a plan carrying durable rationale is a signal that the spec was
incomplete.

This also dissolves the `docs/superpowers/plans/` drift completely: there is no plan file, so there
is nothing to drift.

### Resulting class hierarchy

```turtle
wl:DesignDoc  a owl:Class ; rdfs:subClassOf foaf:Document ;
    wl:layer wlc:intent .
wl:ADR   rdfs:subClassOf wl:DesignDoc .
wl:Spec  rdfs:subClassOf wl:DesignDoc .

wl:Section a owl:Class ;               # §3
    rdfs:subClassOf foaf:Document ;
    wl:layer wlc:intent ;
    rdfs:comment "An addressable, individually linkable part of a DesignDoc. Stable for the "
                 "life of the document; never deleted once the document is accepted." .

[] a owl:AllDisjointClasses ; owl:members ( wl:ADR wl:Spec ) .   # was ( ADR Spec Plan )
```

`wl:Section` joins the top-level disjointness axiom in 03 alongside `wl:Component`, `wl:DesignDoc`,
`wl:Task`, `wl:Deliverable` and `wl:Workstream`.

---

## 3. Sections as first-class nodes

Everything the in-repo claim needs — durable anchors, supersede-in-place, partial implementation —
is one requirement: **sections must be addressable nodes.**

Spec 03 explicitly declined this, and was right to for the requirement it had:

> Range is a literal section reference (`"§4.2"`, a heading string) — deliberately **not** a section
> IRI. […] recommended over minting section sub-IRIs (lighter; no addressable-section namespace to
> maintain).

That trade-off inverts here. A maintained addressable-section namespace is precisely what makes an
external link durable, so the cost 03 avoided is now the feature. This spec supersedes that choice
and, with it, retires `wl:supersededSection`.

### Anchors

Anchors are written in the source with **Pandoc/extended-Markdown attribute syntax**, and are
therefore explicit, author-visible, and portable to any Pandoc-compatible renderer:

```markdown
## 2.1 Installation and setup {#sec-2.1}

…as described in [Section 2.1](#sec-2.1).
```

The IRI follows 03's `id/<type>/<localid>` grammar without a new shape:

```
wlid:section/<doc-slug>/<anchor>      e.g.  wlid:section/spec-worklode-08/sec-3
```

The anchor is **assigned once, at first publication, and recorded in the document source**. The
heading text it labels may be freely reworded; the anchor may not change.

**The anchor carries the section number, with a `sec-` prefix.** The prefix is not decoration:
`{#2.1}` is a legal HTML5 id but an awkward CSS selector (`#\32 \.1` — a leading digit and a `.`
both need escaping) and an ambiguous URL fragment. `{#sec-2.1}` costs four characters and avoids
the escaping entirely. Unnumbered sections take a slug on the same pattern (`{#sec-purpose}`).

### Numbering is identity, so numbering is frozen

Deriving the anchor from the section number buys readable links and matches how people cite specs
in conversation. It costs one freedom, and the cost must be stated plainly:

> **Once a document is accepted, sections are never renumbered.** The number is an identifier that
> happens to look like a position.

This is not an extra rule; it is §7.1 and §7.3 restated in the vocabulary of numbers. Renumbering
re-points an anchor at different subject matter, which is precisely the silent corruption §7.3
forbids.

**Inserts therefore use a letter suffix at the same level** — a new section between `2.1` and `2.2`
is `2.1a`, not `2.1.1` and not `2.4`:

| Strategy | Reading order | Depth | Verdict |
|---|---|---|---|
| Renumber `2.2` → `2.3` | preserved | unchanged | **forbidden** — corrupts inbound claims |
| Sub-number as `2.1.1` | preserved | **+1 per insert** | collides with the §7 depth limit |
| Append as `2.4` | **broken** | unchanged | legal but unreadable |
| **Suffix as `2.1a`** | **preserved** | **unchanged** | **adopted** |

The suffix convention is long-established in legislative drafting for exactly this reason, and it
sorts correctly both for a reader and for a naive lexical sort. A reader encountering `2.1a` learns
something true and useful: that section was added after the document was first accepted.

### What one mint buys

With `wl:Section` in place, nearly every remaining requirement is expressible in terms that already
exist:

| Requirement | Expression |
|---|---|
| Partial implementation | `<task> wl:implements <section>` — coverage is a **count query** |
| Section superseded, heading retained | `<section> wl:status wlc:superseded ; dct:isReplacedBy <section>` |
| Section removed with an explanation | `wl:status wlc:superseded` + `dct:description` |
| Sections may never be deleted | SHACL/CI over the section-IRI set (§4) |
| Partial supersession of a document | `dct:replaces` between sections; `wl:supersededSection` retired |

Note that **no `wl:fullyImplements` / `wl:partiallyImplements` is minted.** A declared coverage
predicate is a second source of truth that goes stale the moment either side changes, and it forces
an unanswerable question ("is three of five sections partial or full?"). Derived coverage cannot go
stale. See §6.

---

## 4. Versioning

### Canonical plus versioned IRIs

Every design document has a **version-free canonical IRI** that always denotes the current version,
and one **immutable versioned IRI** per published version:

```
wlid:doc/spec-worklode-08         # canonical — always the current version
wlid:doc/spec-worklode-08/v3      # immutable snapshot
```

Reuse rather than mint — DCAT 3 (W3C Recommendation) standardises exactly this pattern:

```turtle
wlid:doc/spec-worklode-08
    a wl:Spec ;
    dcat:hasCurrentVersion wlid:doc/spec-worklode-08/v3 ;
    dcat:hasVersion        wlid:doc/spec-worklode-08/v1 ,
                           wlid:doc/spec-worklode-08/v2 ,
                           wlid:doc/spec-worklode-08/v3 .

wlid:doc/spec-worklode-08/v3
    dcat:version        "3" ;
    dcat:previousVersion wlid:doc/spec-worklode-08/v2 ;
    prov:wasRevisionOf   wlid:doc/spec-worklode-08/v2 ;
    prov:wasAttributedTo wlid:agent/stig ;
    dct:issued "2026-07-26"^^xsd:date .
```

> **To verify when authoring the Turtle:** the exact sub-property axioms DCAT 3 declares for
> `dcat:previousVersion` (relative to `dct:replaces`) should be read off the specification rather
> than assumed. The property *names* are settled; their axiomatisation is not restated here.

### Relationship to ADR-0006

ADR-0006 requires version-free instance IRIs, and spec 03 restates this ("`<localid>` opaque &
stable, **never** carrying a git branch or version"). Versioned IRIs are compatible with that
requirement but the reconciliation must be explicit, because it is not self-evident:

> **The canonical IRI remains version-free and is the only IRI anything links to by default.
> Versioned IRIs are additional siblings, and appear exclusively in pinned claims (§6).**

This needs a small amendment to rdf-registry ADR-0006 permitting the versioned sibling under a
named exception, rather than a silent local deviation.

### Publication is one transaction

Each version is its own **immutable named graph**; the canonical document node lives in a small
mutable graph holding little more than the current-version pointer:

| Named graph | Mutability | Holds |
|---|---|---|
| `…/graph/asserted/<doc>` | mutable, tiny | canonical node, `dcat:hasCurrentVersion`, version list |
| `…/graph/asserted/<doc>/v3` | **immutable once written** | the full section set and content of v3 |

Publishing v4 is therefore a single SPARQL Update — `INSERT` the new version graph, retarget one
`dcat:hasCurrentVersion` triple — which Oxigraph applies atomically. No reader ever observes a
document whose current-version pointer disagrees with its content.

Immutable version graphs also make the §3 immutability constraint nearly free: it is a set diff
between two graphs at publish time (§7), not a bespoke checker over Markdown.

### Section-level staleness without section-level versions

Versioning at section granularity would multiply the namespace badly. Instead, each section records
the document version in which it last changed:

```turtle
wl:lastRevisedIn a owl:ObjectProperty, owl:FunctionalProperty ;   # MINT
    rdfs:domain wl:Section ;
    rdfs:comment "The document version in which this section's content last changed. Lets a claim "
                 "pinned at version N be tested for staleness without per-section version IRIs." .
```

A claim pinned at v3 against §4.2 is stale **iff** `§4.2 wl:lastRevisedIn > v3`. Editing §9
therefore does not invalidate anyone's claim on §4.2 — document-level versioning, section-level
precision, one property.

---

## 5. Editorial lifecycle and revisions

### `implemented` leaves the status enum

Spec 03's `lsc:DesignDocStatus` is ordered `draft → proposed → accepted → superseded →
implemented`, which asserts that *superseded* precedes *implemented* — incoherent on its face. The
deeper problem is that implementation is per-section and derived (§6), so a document-level
`implemented` status is a hand-maintained, lossy summary of something computable, and it will
drift.

The scheme becomes purely **editorial**:

```turtle
wlc:DesignDocStatusOrder a skos:OrderedCollection ;
    skos:memberList ( wlc:draft wlc:proposed wlc:accepted wlc:superseded ) .
```

`wlc:implemented` is removed. "Is this spec implemented?" is a coverage query (§6), never a stored
status. `wl:status` applies to both `wl:DesignDoc` and `wl:Section`; its domain widens accordingly.

### Revising an accepted document

Because the canonical IRI is version-free and `wl:status` is functional, "drafting v2" can be
neither a new document IRI (inbound `wlid:section/…` links would silently continue to denote the
old content) nor a status flip back to `draft` on the accepted document (the §3 constraints would
lapse precisely while they are most needed).

It is therefore a **proposed revision against a stable document IRI** — structurally a pull request
for a spec:

1. A revision is opened against the current accepted version, producing a candidate version graph
   `…/v(n+1)` with status `wlc:proposed`.
2. The accepted version remains current and authoritative throughout. Readers and drift queries are
   unaffected.
3. The revision is reviewed with **crit** (as 03 already requires for `proposed → accepted`).
4. On resolution, publication runs the §4 single transaction and the §7 constraint check together.
   Either both succeed or the revision does not land.

Within a revision, sections may be freely added, reworded, or marked superseded. What the revision
may **not** do is remove an anchor that the accepted version published — that is the one invariant
that survives into draft, because inbound links do not care that a document is mid-revision.

---

## 6. Implementation coverage

### Three objects, previously conflated

The intuition that an unimplemented spec is future work rather than repository content resolves
once three distinct objects are separated:

| Object | Home | Why |
|---|---|---|
| **Intent** — the Spec/ADR content | Graph | Status-gated, crit-reviewed, section-addressable. Not a git file. |
| **Work to produce it** | Backbone `wl:Task` | Future work belongs with future work. |
| **"This repo at this commit satisfies §X of doc Y"** | **Git** | It *is* a property of the commit. |

Only the third belongs in a repository, and it belongs there for a real reason: it is the one claim
whose truth is a function of the working tree. The commit log cannot substitute, because a later
commit can silently invalidate an earlier one's claim, and reconstructing the current picture by
replaying history is both expensive and unreliable.

### `.worklode/implements.yaml`

Spec 04 already establishes `.worklode/components.yaml` (path globs → Component IRIs). Its natural
sibling declares which intent the repository satisfies:

```yaml
# .worklode/implements.yaml
implements:
  - section: wlid:section/spec-worklode-01/sec-4
    pinned:  wlid:doc/spec-worklode-01/v2     # version validated against
    by:      [internal/store/lease.go, internal/store/sweeper.go]
  - section: wlid:section/spec-worklode-07/sec-3.1
    pinned:  wlid:doc/spec-worklode-07/v1
    by:      [internal/hooks/apply.go]
```

Machine-readable, not prose. Maintained by coding agents as part of the work that satisfies a
section, and reviewable in the same diff as the code that justifies it.

### The component is derived, never declared

A claim is made **by a Component**, not by a repository: a repository is a packaging accident,
whereas a component is the unit the architecture is actually described in.

Note what the manifest above does *not* contain: a `component:` field. The claiming component is
**derived from the `by:` paths** through `components.yaml`'s existing first-match-wins mapping (04).
This follows the principle running through the whole spec — derive, never declare — and removes an
entire class of drift, since a declared component can disagree with the files listed beside it while
a derived one cannot.

Consequences:

- **A claim whose paths span several components splits into one claim per component**, each pinning
  the same version independently. This is not a degenerate case but the correct reading: components
  advance against a spec at their own pace, and two components implementing the same section is
  exactly what the graph should record.
- **A path matching no component is a publication error**, naming the offending path. Spec 04
  already reports unmatched paths as a gap; here it is fatal, because a claim that cannot be
  attributed is a claim that cannot be checked.

### Single-component repositories

Most repositories hold one component, and they should never have to say so. The clean model already
exists in 03, which defines the Component IRI as `id/component/<slug>` where the slug is *"manifest
slug; default = repo coords"*, and in 04, which grants a single-component repo *"a trivial
whole-repo manifest (or a default)"*.

Made explicit: **every mapped repository has at least one component.** Where `components.yaml` is
absent or declares none, Worklode synthesises an **implicit component** whose IRI is the repo
coordinates —

```
wlid:component/github.com/sunstoneinstitute/worklode
```

— matching a whole-repo path glob. No new machinery, no new IRI shape, and no configuration for the
common case: a simple repository writes `implements.yaml` and never mentions components at all,
while a multi-component repository gets correct per-component attribution from the manifest it
already maintains. The implicit component is promoted to an explicit one the moment
`components.yaml` declares it, and the IRI is unchanged by that promotion — so adopting
`components.yaml` later never invalidates existing claims.

### Deriver: `observed/repo-implements`

The manifest is a hand-maintained *claim about code*, so it enters the **observed** layer under
spec 04's existing deriver contract — idempotent, full-replace, confined to its own named graph:

- **Input:** every mapped repo's `.worklode/implements.yaml` at the default branch head.
- **Output:** `<component> wl:implements <section>` into `…/graph/observed/repo-implements`, plus
  the pinned version for staleness testing.
- **Trigger:** push to the default branch; on a schedule as a backstop.

Coverage and staleness then fall out as standing queries over the two-layer diff, with no new
machinery:

| Query | Reads |
|---|---|
| Unimplemented intent | accepted sections with no `wl:implements` edge |
| Coverage of a document | implemented sections ÷ non-superseded sections |
| **Stale claim** | claim pinned at vN where `section wl:lastRevisedIn > vN` |
| Orphaned claim | claim naming an anchor absent from the current version |

### This replaces spec 07's engine 3

Spec 07 engine 3 detects spec drift by comparing a document file's last commit date against a
task's closure time — a git-mtime heuristic that fires on typo fixes and misses semantic changes to
a section nobody claimed. The stale-claim query above is exact and section-scoped. **Engine 3 is
superseded by this spec** and should be removed from 07 rather than built; 07's `task_docs` table
likewise gives way to the manifest. Engines 1 and 2 of spec 07 are untouched — they diagnose missed
*ingestion*, an unrelated problem.

---

## 7. Constraints on accepted documents

Enforced at publication (§4) and in CI, as a set diff between the current and candidate version
graphs:

1. **Anchors are append-only.** Every `wl:Section` IRI in version N appears in version N+1. Removal
   is never permitted; retirement is `wl:status wlc:superseded`, which keeps the anchor resolvable.
2. **A superseded section carries an explanation** — `dct:isReplacedBy` to its successor, or a
   `dct:description` saying why it went away. A bare superseded section is a broken promise to
   whoever linked to it.
3. **Anchors are immutable, so accepted sections are never renumbered.** An anchor is never
   re-pointed at different subject matter. Rewording a heading is fine; renumbering, or reusing an
   anchor for a new topic, silently corrupts every inbound claim. Inserts use the `2.1a` suffix
   convention (§3).
4. **Draft documents are exempt from 1–3** — with the single exception in §5: anchors already
   published in an accepted version are protected even while a revision is in draft.
5. `wl:lastRevisedIn` is set on exactly those sections whose content actually changed. A publication
   that touches it on unchanged sections mass-invalidates valid claims and is rejected.
6. **Anchor depth respects the configured limit** (§7.1 below), evaluated at publication.

### Anchor depth

Markdown permits six heading levels. Worklode addresses fewer, because every addressable level is a
level someone can pin a claim to, and claims at excessive granularity age badly.

**The limit is server-configurable, defaulting to 3.** It governs *addressability*, not authoring:
headings deeper than the limit are perfectly legal and render normally — they are simply content
within their nearest anchored ancestor rather than nodes in their own right. Authors are never
blocked from structuring a document as they wish; they are only prevented from making arbitrarily
fine-grained promises.

The `2.1a` insert convention (§3) exists partly to keep this limit workable: inserts consume
suffixes rather than depth, so a document cannot be pushed past the limit by ordinary revision.

**Raising the limit is safe; lowering it is not.** Raising it makes previously-unaddressable
headings addressable — purely additive. Lowering it would remove anchors that accepted documents
have already published, which constraint 1 forbids outright. Therefore:

- lowering the limit applies only to documents that have never been accepted;
- a publication that would orphan an accepted anchor because of a lowered limit is **rejected**,
  naming the anchors at fault.

This makes the setting one-way-safe by construction, rather than relying on operator discipline.

These extend `rdf/shapes/wl-shapes.ttl` (03) under the existing SHACL gate (ADR-0003).

---

## 8. Task kinds

Two enumerations exist today and disagree:

- `deploy/base/migrations/0001_baseline.up.sql:53` → `('feature','bug','chore','spec')`
- Spec 03 `lsc:TaskKind` → `feature / bug / chore / review / spike`

`spec` is absent from the SKOS scheme; `review` and `spike` are absent from the database. This is a
live inconsistency independent of everything else in this spec.

**Resolution: reconcile to the union** — `feature, bug, chore, spec, review, spike` — in both the
`CHECK` constraint and `wlc:TaskKind`. A migration widens the constraint; no rows change.

No kind is added for plans, planning, speccing, or reconciliation:

- **Task kinds describe the nature of work, not the class of artifact produced.** Which document
  came out is carried by the document, which has its own class.
- A *speccing* task versus an *implementing* task is distinguished by the predicate, not the kind
  (§9).
- `reconcile` is an activity, not a kind of work; such tasks are `chore`.

---

## 9. Authorship

`wl:implements` covers Task → DesignDoc ("realises this intent"). Nothing expresses "this task
*wrote* that document," which is what a `spec`-kind task actually does. Reuse rather than mint:

```turtle
wl:Task rdfs:subClassOf prov:Activity .
wlid:doc/spec-worklode-08 prov:wasGeneratedBy wlid:task/01H8XZ7K… .
```

Zero mints, and it cleanly separates authoring from executing — the same task graph can now answer
"which task produced this spec?" and "which tasks implement it?" without conflating them.

---

## 10. Surfaces

| Surface | Purpose |
|---|---|
| `lode doc list \| show <slug> [--version vN]` | Read documents and sections; `--json` |
| `lode doc coverage <slug>` | Per-section implemented / unimplemented / stale |
| `lode doc revise <slug>` | Open a proposed revision (§5) |
| `lode doc publish <slug>` | Run §7 constraints, then the §4 transaction |
| `lode doc anchors <slug>` | List anchors with depth and addressability; the lint an author runs before publishing |
| `lode drift --docs` | Stale and orphaned claims (§6), alongside 04's other drift |
| Read-only web view | Rendered document, per-section coverage badges, version history |

Anchor depth (§7.1) is a **server setting**, surfaced through the existing admin configuration
path rather than a per-repo file — it governs what claims are expressible across the whole
installation, so it cannot be a per-repository decision.

Review is **crit**, as 03 already specifies for `proposed → accepted`; sections give crit comments a
natural anchor, so a review comment and an implementation claim address the same node. The web view
extends spec 04's read-only overview surface rather than introducing a new application.

The on-disk path of a document ceases to be its identity. Until documents move into the graph,
tracked paths stay per-project configuration — which is spec 07's open question 2, now answered:
configuration, not convention, and temporary either way.

---

## Amendments to existing specs

| Spec | Change |
|---|---|
| 03 | `ls:`→`wl:` throughout; drop `wl:Plan`; add `wl:Section`, `wl:lastRevisedIn`; retire `wl:supersededSection`; remove `wlc:implemented`; widen `wl:status` domain; add `spec` to `wlc:TaskKind`; update both disjointness axioms and acceptance criteria 2 and 5 |
| 04 | Add the `observed/repo-implements` deriver and the `.worklode/implements.yaml` manifest; add the coverage and stale-claim standing queries |
| 05 | `lode task brief` supplies a governing **Spec section**, not a "Spec/Plan excerpt" — bounded by construction, but now dependent on §3; `/lode-spec` outputs become {ADR, Spec, task subtree} |
| 07 | Remove engine 3 and `task_docs`; close open question 2 |
| Migration | Widen the `tasks.kind` CHECK constraint (§8) |
| ADR-0006 | Permit the versioned sibling IRI under a named exception (§4) |

## Dependencies

- **Spec 03** — the vocabulary this amends; the SHACL gate and `owlrl` closure tests extend to the
  new terms.
- **Spec 04** — the deriver contract, named-graph partitioning, and the overview surface.
- **Spec 01** — task subtrees, which now carry what plans used to.
- **rdf-registry** — ADR-0006 amendment; ADR-0003 SHACL gate; the `wl:` rename lands in the same PR
  as the 03 ontology, never after it.
- **crit** — review of proposed revisions.

## Adoption is out of scope

The constraints in §7 bind **from a document's first publication onward**. Markdown that has never
been published is unconstrained, so this spec can ship without touching a single existing file, and
`docs/specs/worklode/00`–`07` keep working exactly as they do today until someone deliberately
publishes them.

Two pieces of work follow from this spec without belonging to it:

- **Dogfooding Worklode within Worklode** — a task, not a spec. It is how this design earns
  confidence, and it should not be smuggled in as a migration section.
- **Onboarding existing projects** — a candidate **spec 09**. Importing an existing GitHub project
  wholesale (issues → Tasks, `docs/specs/**` and `docs/adr/**` → published documents at v1, repos →
  Components, GitHub projects → Workstreams) is a substantial design in its own right, with real
  questions this spec should not prejudge: what anchors get assigned to a corpus that never had
  them, how imported issues reconcile with the already-shipped `lode inbox` promotion path (07), and
  whether a first publication of legacy prose should be `accepted` or `draft`. Spec 08 defines the
  target state; spec 09 would define how an existing project reaches it.

## Open questions

1. ~~Anchor assignment ergonomics~~ — **RESOLVED:** Pandoc attribute syntax, `{#sec-2.1}`, carrying
   the section number with a `sec-` prefix (§3).
2. ~~Granularity of a section~~ — **RESOLVED:** server-configurable, default 3, governing
   addressability rather than authoring; raising is safe, lowering is constrained (§7).
3. ~~Migrating the existing corpus~~ — **RESOLVED:** out of scope; constraints bind from first
   publication. Dogfooding is a task; onboarding is candidate spec 09 (above).
4. ~~Does a Component pin, or a repository~~ — **RESOLVED:** the Component pins, derived from the
   claim's paths rather than declared, with an implicit repo-coords Component for single-component
   repositories (§6).
5. **Suffix exhaustion.** `2.1a` handles an insert between `2.1` and `2.2`. An insert between `2.1a`
   and `2.1b` extends the suffix — `2.1aa` sorts lexically between them and needs no new rule — but
   this should be stated in the authoring skill before someone invents `2.1a1`.
6. **Pin repetition.** A Component implementing twelve sections of one document repeats the same
   `pinned:` value twelve times. A document-level default pin with per-claim override would be
   kinder to write and to review; it also introduces a second place for the pin to be wrong. Decide
   on ergonomics once real manifests exist.
7. **Depth of unnumbered sections.** `{#sec-purpose}` and similar front-matter anchors carry no
   number, so their depth is nominal. Treating them as depth 1 is the obvious reading; confirm it
   against a document with a deeply structured appendix.

## Acceptance criteria

1. No `ls:`, `lsc:` or `lsid:` occurrence remains in `docs/`; the rdf-registry sources sit at
   `rdf/wl/` and publish under `https://worklode.io/ns/wl/`.
2. `wl:Plan` is absent from the ontology; a plan-shaped body of work is representable as a task
   subtree, and no acceptance criterion anywhere still refers to `Spec ⊃ Plan ⊃ Task`.
3. A section declared `## 2.1 Title {#sec-2.1}` is addressable as
   `wlid:section/<doc-slug>/sec-2.1` and linkable in-document as `[Section 2.1](#sec-2.1)`;
   rewording the heading leaves every inbound claim resolving to the same content.
3a. A revision inserting a section between `2.1` and `2.2` names it `2.1a`, renumbers nothing, and
    leaves every claim against `sec-2.2` resolving to the content it always did. A revision that
    renumbers `2.2` → `2.3` is **rejected** by the §7 gate.
4. Publishing v4 of a document is a single transaction: no reader observes a current-version pointer
   disagreeing with its content, and the v3 graph is byte-identical afterwards.
5. A publication attempting to delete a section anchor accepted in an earlier version is **rejected**
   by the SHACL gate; the same publication marking that section `wlc:superseded` with a
   `dct:isReplacedBy` target **succeeds**.
6. `.worklode/implements.yaml` in a repository produces `wl:implements` edges in
   `observed/repo-implements`; a second run with unchanged input is a no-op; a removed entry
   disappears from the graph (full-replace).
6a. A repository with no `components.yaml` produces claims attributed to an implicit component whose
    IRI is its repo coordinates; adding a `components.yaml` that names that same whole-repo
    component leaves every existing claim's subject IRI **unchanged**.
6b. A claim whose `by:` paths span two declared components yields **two** edges, one per component,
    each carrying the same pinned version; a claim whose path matches no component is rejected,
    naming the path.
6c. With the depth limit at 3, a `#####` heading renders but is **not** addressable, and a claim
    naming it is rejected. Raising the limit to 4 makes it addressable with no other change;
    lowering the limit to 2 is rejected for any document already accepted with depth-3 anchors,
    naming those anchors.
7. Coverage of a document is computed, never stored: `lode doc coverage` reports implemented,
   unimplemented and superseded sections, and no `wl:fullyImplements` predicate exists.
8. A claim pinned at v3 against a section subsequently revised in v4 reports **stale**; a claim
   pinned at v3 against a section untouched by v4 does **not**, even though the document version
   advanced.
9. `wlc:implemented` is absent from `wlc:DesignDocStatus`, and the remaining order is
   `draft → proposed → accepted → superseded`.
10. A revision of an accepted document leaves the accepted version current and drift queries
    unaffected until crit resolves; the accepted version's anchors are protected throughout.
11. `tasks.kind` accepts all of `feature, bug, chore, spec, review, spike`, `wlc:TaskKind` contains
    exactly the same six, and the migration round-trips up and down.
12. A `spec`-kind task that produced a document is reachable by `prov:wasGeneratedBy`, and is
    distinguishable from the tasks that `wl:implements` that document's sections.
