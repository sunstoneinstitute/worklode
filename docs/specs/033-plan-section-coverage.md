---
status: draft
requires:
  - 025-documents-in-the-backbone.md
  - 026-design-doc-queries.md
  - 014-design-documents-as-graph-objects.md
amends:
  "#sec-1":
    - 025-documents-in-the-backbone.md#sec-4
  "#sec-2":
    - 025-documents-in-the-backbone.md#sec-7
    - 026-design-doc-queries.md#sec-2.1
  "#sec-4":
    - 006-knowledge-graph.md#sec-1.3
  "#sec-4.2":
    - 006-knowledge-graph.md#sec-1.3
    - 014-design-documents-as-graph-objects.md#sec-6
    - 014-design-documents-as-graph-objects.md#sec-12
---
# Spec 033 — Plan section coverage

## 0. Purpose & scope {#sec-0}

025 §7 answers "which specs need planning?" with *accepted sections not named by
any accepted plan's `implements`*. That predicate is binary, and when one spec
fans out to a plan series it is wrong in both directions: a part that touches a
section without finishing it reads as covered, and a part governed by a standing
rule it implements nothing from either reads as covering that rule or as having
forgotten it.

The gap is not theoretical. Four independent plans were written for spec 032
part 1 on 2026-08-09. All four carried the same seven-section `implements` list;
they disagreed about whether part 1 was a read-only projection or included the
lifecycle facts and their write path — a disagreement about roughly a third of
the work. The frontmatter could not express it, so nothing caught it.

This spec renames the plan-side key, qualifies the edge so the disagreement has
somewhere to live, and makes "is this spec fully planned?" decidable from
frontmatter alone. It changes what a plan *declares*. It does not change what a
plan *is* (025 §4), how acceptance mints tasks (025 §5), or how implemented code
claims a section (014 §6).

**Planning coverage is not implementation coverage.** 014 §6 records
`<component> wl:implements <section>` — observed, derived from
`.worklode/implements.yaml`, answering "does the running code satisfy this?".
This spec records `<plan> wl:covers <section>` — declared, authored, answering
"has anyone undertaken to build this?". Two layers, two questions, no shared
owner, and now no shared key.

## 1. A plan covers sections; it implements none {#sec-1}

> Amends 025 §4.

025 §4's ruling stands: a plan is a document, takes no anchors, and names spec
sections in its frontmatter. Two things change.

**The key is `covers`.** `wl:implements` is a component's claim that its code
meets a section — evidence, produced by work already done. A plan writes no
code, so it can claim nothing: it declares an undertaking, discharged by the
tasks its acceptance mints (025 §5), which produce the deliverables the
component then implements. Spelling both the promise and the evidence
`implements` left them indistinguishable, and this spec's own drafting tripped
over exactly that, aiming its first amendment at the wrong section. §4.2 finds
the same conflation one level further down. `covers` also lets the coverage
level read as a property of the relation rather than a qualifier bolted onto a
mismatched verb.

**Each entry is qualified**, carrying how completely this plan covers the
section:

| Level | Meaning |
|---|---|
| `full` | After this plan executes, the section is satisfied. Nothing further is owed. |
| `partial` | This plan covers part of the section. |
| `none` | The plan is bound by the section but covers nothing in it. |

`none` is not padding. 032 §11's rule that "end-to-end tests drive the HTTP UI
and API surfaces and do not write directly to the store" governs every part of a
series while being implemented by none of them. Without a way to say so, a
reader cannot distinguish a constraint a plan obeys from a section its author
overlooked — and an author cannot record having considered a section and
concluded it was someone else's.

A `partial` entry may name the plans that finish the section under
`fullCoverageWith`. That list is what makes closure decidable: without it,
`partial` is an open declaration that some of the section remains unplanned,
which is a legitimate and useful state to be in.

**This mints a sibling property, not a second spelling of one.** 014 §6 declined
to mint `wl:fullyImplements` on the grounds that coverage is a query over
sections, and that ruling is preserved: no property here says "how much" on its
own. The level hangs off a reified node that sits *beside* the `wl:covers` edge
rather than replacing it — PROV's arrangement, though not PROV's linkage
(§4.3). 014 §6.2 already gives the repo-side claim a structured form
(`section:`/`pinned:`/`by:`); this is the same move on the plan side.

## 2. Planning coverage is a three-valued query {#sec-2}

> Amends 025 §7.

025 §7's row **"Which specs need planning?" — accepted sections not named by any
accepted plan's `implements`** is replaced by this resolution. For an accepted
spec section `S`, over every accepted plan naming `S` in `covers`:

| Outcome | Rule |
|---|---|
| **fully planned** | some accepted plan claims `coverage: full`; **or** an accepted plan claims `partial` with a non-empty `fullCoverageWith: [P…]` where every `P` is accepted and contributes `full` or `partial` to `S` |
| **partially planned** | `S` is claimed only `partial`, and no `fullCoverageWith` closes it |
| **bound only** | `S` is claimed only `none` |
| **unplanned** | no accepted plan covers `S` |

`fullCoverageWith` is checked, never taken on trust: an empty list, a draft
target, a `none` target, or a target that does not itself cover `S` leaves the
section `partially planned` and is reported. A plan may otherwise assert closure
against a sibling that never undertook usable work on the section, which is the
same defect in a new place.

Only `full` and a closed `partial` set discharge the section. `none` contributes
nothing to coverage by construction — it records that the section was read.

The remaining rows of 025 §7 are untouched: ordering between plans stays a
document-level `blocks` edge, "all of spec N is done" is still not an event, and
no container is minted above a plan's tasks.

## 3. Frontmatter shape {#sec-3}

```yaml
covers:
  - spec: docs/specs/032-project-cockpit.md#sec-2
    coverage: full
  - spec: docs/specs/032-project-cockpit.md#sec-3
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-10-project-cockpit-2-intake-and-launch.md
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
```

| Key | Required | Value |
|---|---|---|
| `spec` | yes | A reference (014 §11.1) with a `#sec-N` fragment. Section-scoped: a plan claiming a whole document says nothing a coverage query can use. |
| `coverage` | yes | `full`, `partial`, or `none` |
| `fullCoverageWith` | no | A non-empty list of accepted plans contributing `full` or `partial` to the same section. Canonical repo-relative paths — plans carry no shorthand and no number. Invalid beside `full` or `none`. |

`covers` is plan-only, as `implements` was, and takes the position `implements`
held in the key order (014 §11): lifecycle, then `covers`, then dependency, then
amendment, then supersession.

**A bare string keeps working and means `full`.** `covers: docs/specs/011-delivery-lifecycle.md#sec-2`
is the qualified entry with `coverage: full`, so a single-plan spec never pays
for machinery it does not use. `NO-SPEC` (026 §4.2a) is likewise unqualified —
the absence of a governing spec has no sections to cover.

**`implements` on a plan stays parseable and means `covers`**, reported as
retired rather than rejected. The supporting documentation commit for this spec
normalised existing plan headers in this tree; the later implementation tasks
did not rewrite them. The tolerance is for plans outside this tree — unmerged
branches and abandoned worktrees — which would otherwise fail the commit hook
the moment they rebase. A plan carrying both keys is an error, because then
precedence would decide silently which one counted.

The object form is **required** where more than one accepted plan covers the
same section, because that is exactly the case the bare form cannot express.
Series parts therefore use it from the first part.

## 4. Ontology {#sec-4}

**Name a predicate by reading a triple aloud.** Substitute the class names of
subject and object and see whether the sentence survives. A predicate that needs
its domain and range recited before it means anything is too weak to carry the
edge, and the weakness shows up as prose that has to explain the term every time
it appears:

> The cockpit plan **covers** spec 032 §3. · Task WL-7 **produces** the
> graph-live deliverable. · The worklode component **implements** spec 006 §4.2.
> · The v1.2.0 release **was cut from** commit abc1234.

Every rename below was settled that way, including two abstract candidates —
`wl:satisfies` and `wl:realises` — that read acceptably in prose and failed in
triple position. The test also decides ranges, not just names: a range member
that makes the sentence false does not belong in it (§4.2).

`ns/` is the schema source (025 §9). The terms below are mirrored there, and
`riot --validate ns/*.ttl` passes:

```turtle
wl:covers a owl:ObjectProperty ;
    wl:layer wlc:execution ;
    rdfs:domain wl:Plan ; rdfs:range wl:Section ;
    rdfs:comment """A Plan's undertaking to see a Section built, discharged by the tasks its
        acceptance mints (025 §5). Deliberately not wl:implements: a plan writes no code, so it
        claims nothing — one word for the promise and for the evidence that redeems it leaves
        the two indistinguishable (033 §1).""" .

wl:Coverage a owl:Class ;
    wl:layer wlc:execution ;
    rdfs:comment """A qualified wl:covers: how completely one Plan covers one Section. Reified
        beside the direct edge, never in place of it, so 014 §6's coverage queries are untouched
        and no wl:fullyImplements is minted. The node names both ends itself rather than hanging
        off a Plan→Coverage predicate — see §4.3.""" .

wl:coveringPlan a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:domain wl:Coverage ; rdfs:range wl:Plan .

wl:coveredSection a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:domain wl:Coverage ; rdfs:range wl:Section .

wl:coverageLevel a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:domain wl:Coverage ; rdfs:range skos:Concept .

wl:completedWith a owl:ObjectProperty ;
    rdfs:domain wl:Coverage ; rdfs:range wl:Plan .
```

`wlc:CoverageLevel` is a SKOS concept scheme in `ns/concept.ttl` with exactly
`wlc:full`, `wlc:partial`, and `wlc:none`. SHACL enforces that a `wl:Coverage`
whose level is `wlc:full` or `wlc:none` carries no `wl:completedWith`, and that
its `wl:coveringPlan` also asserts a direct `wl:covers` to its
`wl:coveredSection`.

The range is `skos:Concept`, not `wlc:CoverageLevel`: the latter is a
`skos:ConceptScheme`, not an RDFS class. The exact three members and their
membership in that scheme are closed-world constraints, so SHACL owns them.

`wl:status` is also corrected here for the Plan that 025 §4 restored:

```turtle
wl:status a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:domain [ a owl:Class ;
        owl:unionOf ( wl:DesignDoc wl:Plan wl:Section ) ] ;
    rdfs:range skos:Concept .
```

Omitting `wl:Plan` would infer every status-bearing Plan into the
`DesignDoc ∪ Section` union, which is disjoint from Plan. `wl:PlanShape`
therefore requires a Plan to carry one status from `wlc:DesignDocStatus`,
matching the existing DesignDoc shape.

### 4.1 Two terms this displaces {#sec-4.1}

**`wl:covers` was taken.** 015 §6 used it for the delivery frontier —
`wl:Artifact` → `wl:Commit`, "carries every commit up to and including that
commit". Two `rdfs:domain` assertions on one property intersect, and `wl:Artifact
⊓ wl:Plan` is empty under 006's disjointness axiom, so this was a contradiction
rather than a clash of taste.

**015's edge is renamed `wl:cutFrom`** — the commit a release was cut from. That
is the fact `release_frontiers` actually stores: `target_commitish` where it
resolves, else the default branch head at publish time. "Covers everything up to
it" was never a separate assertion but a consequence of the object being a commit
on the default branch, so the old name described the query rather than the edge,
and needed three words to do it. The frontier reading moves into the term's
comment, where a consequence belongs. 015 is draft and no code reads the term —
`release_frontiers` is a table name and is untouched — so this costs nothing.
006 §1.4's `ls:covers` mention is left as written: it predates 014 §1's
`ls:`→`wl:` rename and is historical text in an accepted document.

**`wl:Plan` had to come back.** 014 §2 dropped it and `ns/` still listed it under
"deliberately absent"; 025 §4 restored it as a sibling of `wl:DesignDoc` and
`ns/` never caught up. `wl:covers` needs it as a domain, so the class is minted
here per 025 §4's Turtle and joins the top-level disjointness axiom.

### 4.2 `wl:implements` splits, and states its domain {#sec-4.2}

The plan-side conflation had a twin. `wl:implements` carried two unrelated
relations at once — `Component → Section`, the manifest claim 014 §6 added, and
`Task | Issue | PullRequest → Deliverable | Component`, 006 §1.3's original — and
its comment recorded the consequence plainly: *"Domain is deliberately
unstated."* It was unstated because the two halves have no common subject, so no
domain could be written that was not a lie. The union range was doing the work of
telling them apart, which means a reader distinguishes a promise from a delivery
by inspecting the object's type.

The halves separate. `wl:implements` keeps the Component→Section claim, because
that is the paradigmatic sense of the word in software and the manifest is
already named for it; the Task half takes a new term:

| Edge | Property | Says |
|---|---|---|
| Plan → Section | `wl:covers` | intent — someone undertook this |
| Task → Deliverable | `wl:produces` | work — this is what makes it exist |
| Component → Section | `wl:implements` | evidence — the code meets the section |

Both now state a domain, so SHACL can constrain them and 014 §6's coverage
queries read `wl:implements` directly instead of filtering by the type of the
object. `.worklode/implements.yaml` and `observed/repo-implements` are unchanged:
the term they were named for is the one they kept.

The sentence test also decides the domain and range, and it removes three
members 006 §1.3 admitted:

- **`Component` leaves the range.** "PR #39 produces the worklode-api component"
  is false — the component already exists — and `wl:affects` already says a task,
  issue or pull request touches one. Two edges for one fact is the duplication
  this ontology refuses elsewhere.
- **`Issue` and `PullRequest` leave the domain.** "Issue #204 produces the
  Keycloak-SSO deliverable" is false: an issue is a request. The backbone agrees
  — `issues` carries `triage_state: new → promoted | dismissed` and a `task_id`,
  nothing else outbound, and `pull_requests` likewise binds only a task. Neither
  has a column a deliverable edge could project from, and 006's own projection
  table sources this row from "VCS ingest", which has no such field to read. Both
  reach a deliverable through the task they are bound to: `wl:mirrors` for the
  issue, the PR's task binding for the pull request.

The domain is therefore `wl:Task` alone. No triple is lost: nothing in the
repository emits one. `internal/graphserver` is an HTTP client against a remote
graph server and constructs no statements, so every edge in this section is a
contract for a projector that does not yet exist.

Two claims in the old comment were false and do not survive: `wl:implements` was
never "realisation of intent" in general — that spanned all three edges above —
and "SHACL enforces presence" named no shape, `ns/shapes.ttl` having never
mentioned the property. Writing those shapes is left to the implementation (§5).

006 §1.3's own text keeps `ls:implements` as written: it predates 014 §1's
`ls:`→`wl:` rename, and 014 §12 already records the re-pointing against it.

### 4.3 The qualified node names both its ends {#sec-4.3}

There is deliberately no `Plan → Coverage` predicate. PROV needs
`prov:qualifiedAssociation` because an Association carries attributes of its own
and has no other route back to the Activity it qualifies. A `wl:Coverage` has no
such independence: the (plan, section) pair *is* its identity, so the node can
name both ends and be found from either.

Dropping it also removes the one predicate in this spec that failed §4's test —
"the cockpit plan qualified-covers a coverage" is not a sentence, and no rename
repairs it, because the defect is that the object is a reification node rather
than the thing being covered. Where a predicate cannot be read aloud, the fix is
usually the shape rather than the word.

Nothing emits any of this yet: `internal/graphserver` is an HTTP client that
constructs no statements, so `wl:Coverage` is a contract for the document
projector 025 §2 anticipates. 015 §6 declined to mint a per-environment frontier
node on the grounds that neither shape was "worth minting before a query wants
it", and the same restraint would justify deferring this whole subsection. It is
specified now because 014 §11 requires a term behind every frontmatter key, and
§3's `coverage` and `fullCoverageWith` are frontmatter that `secmeta.py` already
enforces — a validated key with no term is exactly the private extension that
rule exists to prevent.

## 5. Checks {#sec-5}

`scripts/secmeta.py` gates this at commit. It accepts `covers` in both forms and
reports:

- an entry missing `spec` or `coverage`, or carrying an unknown key;
- a `coverage` value outside the three;
- `fullCoverageWith` beside `full` or `none`;
- a `fullCoverageWith` target that resolves to no file, lacks `accepted`
  status, or does not contribute `full` or `partial` to the same section;
- an empty `fullCoverageWith`, or a non-canonical plan path;
- a `spec` reference without a `#sec-N` fragment;
- the bare form on a section another accepted plan also covers;
- `implements` on a plan — deprecated, write `covers`;
- both keys on one plan — an error.

`covers` replaces `implements` in `PLAN_ONLY`, and `NO-SPEC`'s "only on a plan's
`implements`" constraint moves with it. `internal/designdoc/frontmatter.go`
gains the field alongside the deprecated one. As today the script reports and
never rewrites, so a failure is the author's to decide.

### 5.1 Documents the rename reaches {#sec-5.1}

The rename is not confined to this spec; its supporting change updates every
surface that owns the old spelling:

| Document | What changes |
|---|---|
| 026 §2.1, §4.2a, §6, and the `--needs-planning` query | Use accepted plans and 033's qualified `covers` resolution. Its `.worklode/implements.yaml` references remain the component claim. |
| `docs/authoring-design-docs.md` §Frontmatter | Names `covers` and the documented key order. |
| `CLAUDE.md` §Conventions | Requires plan `covers`, including `covers: NO-SPEC`. |
| `ns/ontology.ttl`, `ns/concept.ttl`, `ns/shapes.ttl` | Carry §4's terms and constraints. |

014 §11's key table row is corrected in place (014 is draft): it listed
`implements` as a DesignDoc key, but the key is plan-only, so the row was
already stale before this spec.

## 6. Out of scope {#sec-6}

- **`lode doc coverage`** — 025 §7 already owns the command; this spec fixes only
  the predicate it evaluates. The store-backed implementation follows the
  document store.
- **Forcing a migration outside this tree** — the supporting spec commit
  normalised existing in-tree headers, but plans on unmerged branches and other
  worktrees remain parseable under retired `implements`. The post-plan
  implementation tasks themselves did not rewrite plans.
- **Implementation coverage** — 014 §6's component claim is untouched, and keeps
  `wl:implements`.
- **Graph projection** — the projector reads whatever `ns/` declares; no new
  contract (006 §6).
