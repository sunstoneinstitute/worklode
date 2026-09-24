# Documents

Specs and plans are rows in the backbone, authored, reviewed, versioned and accepted through `lode doc`. Minutes and notes are working documents in the same store (06-design-queries-intents-decks.md §7). The git tree holds no design documents. A spec is a standing description that is revised or superseded and never finished. A plan is an executable document whose acceptance mints its tasks. Sections are addressable nodes with frozen `{#sec-N}` anchors, so a claim "this code satisfies section 4.2 of spec 6 as of version 3" stays correct after later edits. Edges between documents and sections (`covers`, `defers`, `implements`, `amends`, `replaces`, `requires`, `blocks`) are stored once and read in both directions. The lifecycle rides on the append-only event log, whose `doc-lifecycle` subscriber mints the review and planning tasks a document calls for.

## 1. Principle: rows are things someone made, groupings are queries

A row exists because an act created it. Everything cross-cutting is derived: a plan's task set, a spec's coverage, a milestone's membership, "all plans of spec N with unfinished work".

| Candidate row | Verdict |
|---|---|
| Plan root task grouping one plan's tasks | Never minted. Query over `tasks.plan_doc` |
| Spec-level container over its plans | Never minted. Query over `covers` plus task-set states |
| Spec umbrella task open while unimplemented | Never minted. Coverage query |
| Sprint or iteration container | Never minted. Time-boxing is ranking and deadlines |
| Milestone | Row (declared intent that deliverables ship together), membership derived |

## 2. Document kinds

| Kind | Class | Sections and anchors | Mutability after accept | Lifetime |
|---|---|---|---|---|
| `spec` | `wl:Spec` under `wl:DesignDoc` | yes, frozen | revised (section 9) or amended in place under the ladder (section 10) | stays true after implementation, superseded never consumed |
| `plan` | `wl:Plan`, sibling of `wl:DesignDoc` | none, nothing pins a claim to a plan | freely editable at any status | spent once executed |
| `minutes` | working document, one per meeting | none | one mutable working draft, finished into immutable numbered revisions. No acceptance, coverage, reviewers or minting (06 §7) | kept with the meeting |
| `note` | working document, one project, no meeting | none | same as `minutes` | project working knowledge |

The `adr` kind is retired: its content folds into the spec owning the surface, and a reversal is an `amends` or supersession on that section (06 §5). `--kind adr` and the `ADR` shorthand type remain only as ref spellings for documents that still exist.

`wl:DesignDoc`, `wl:Plan` and `wl:Section` are subclasses of `foaf:Document` and `prov:Entity`. `wl:Section` sits in the top-level disjointness axiom with `wl:Component`, `wl:DesignDoc`, `wl:Task`, `wl:Deliverable` and `wl:Project`.

A plan is a document rather than a task body so that review machinery (comment anchoring, revision tracking, the accept gate) is built once. Rationale worth keeping in a plan is promoted into the governing spec before the plan's tasks close.

**Documents are not closeable.** Tasks are undertakings and reach a terminal state. A document is revised, superseded or distrusted. The status scheme records how much a reader should trust the text. Progress is a property of tasks. "Is this spec implemented?" is always a coverage query (section 13).

**The cardinality test.** A document gathers many tasks over its life (authoring, each revision, each review). A task produces at most one document. A `decision` task is a third category: it produces a recorded answer on the task itself, no document and no diff, so it never faces the test. A decision that earns a durable rationale blocks the `design` task that writes it into the owning spec. They are two undertakings held in order.

**The authoring task closes on submission.** A `design` task's deliverable is a document submitted for review. It closes when the submission exists. Acceptance is a status transition on the document, performed by the owner, and never a state a task waits on.

## 3. The store

Documents are Postgres rows wrapped in the same event-logged transactions as tasks. The knowledge graph receives them by projection (07-knowledge-graph-and-search.md). The graph's copy is never an authoring surface.

| Table | Holds |
|---|---|
| `docs` | identity (project, kind, number, slug), title, body, status, owner, frontmatter as columns (`issued`, `requires`, `wasDerivedFrom`, ...), version counter |
| `doc_versions` | `(doc_id, version)` primary key, `body`, `title`, `issued`, `created_at`. Append-only snapshot of every version `docs` has superseded |
| `doc_sections` | anchor, heading, depth, `last_revised_in`. Specs only |
| `rules` | one design rule per anchored section of a spec or ADR: project, `number` (the `WL-RULE-<n>` ref, from `project_entity_seq` kind `RULE`), status, current version, `owner`, `tags` |
| `rule_versions` | `(rule_id, version)` primary key, `heading`, `body`. A draft version is rewritten in place; an accepted version's text never changes |
| `doc_rules` | the document's current arrangement. A spec or ADR arranges its own rules; a plan arranges the rules its `covers` entries reach, rewritten on every plan body write and again at accept (S16): `(doc_id, position)`, `rule_id`, `rule_version`, `depth`, `anchor`. Rewritten with `doc_sections` on every body write |
| `rule_edges` | `(from_rule, to_rule, type)` primary key, `source` (`manual` or `derived`): the typed edges between rules, `refines`, `constrains`, `conflictsWith` and `references` (12-spec-refactoring-design-tree.md S12, S26) |
| `task_governed_by` | `(task_id, rule_id)`, `rule_version`, `source` (`plan` or `manual`): the rules governing a task (03-tasks-and-execution.md §4), `pinned_version` when the link is pinned (12-spec-refactoring-design-tree.md S10) |
| `doc_edges` | `covers`, `defers`, `implements`, `amends`, `replaces`, `requires`, `blocks`, section-scoped where an end is a section. `declared_by` records which document typed the key. Unresolvable targets stored verbatim in `to_external` |
| `tasks.plan_doc` | nullable, the plan whose acceptance minted the task |
| `tasks.plan_task_key` | the declaration title as written at mint. Unique on `(plan_doc, plan_task_key)`, null together with `plan_doc` |
| `tasks.about_doc` | nullable, the document a review or design task is about |
| `decisions` | one row per question on a task (section 12) |

Every site that increments `docs.version` snapshots the pre-update row into `doc_versions` in the same transaction. Reading version N is `docs` when N is current and `doc_versions` otherwise. Diffing versions is out of scope.

Each direction of a bidirectional edge (`amends`/`amendedBy`, `replaces`/`isReplacedBy`, `blocks`/`blockedBy`) is one stored row, always from origin to target. `blockedBy: [plan-2]` on plan 3 stores exactly the row `blocks: [plan-3]` on plan 2 would. Guards (both ends plans, reference resolvable, no self-block, no cycle) run on the row. Both plans declaring the same ordering is one row. Rewriting either plan clears only its own declarations, tracked by `declared_by`.

## 4. Sections and anchors

Anchors use Pandoc attribute syntax in the source and carry the section number with a `sec-` prefix:

```markdown
## 2.1 Installation and setup {#sec-2.1}
See [Section 2.1](#sec-2.1).
```

A section's IRI is its document's IRI plus the anchor as fragment, `/docs/WL-SPEC-25#sec-2.1` (07-knowledge-graph-and-search.md §10). Unnumbered sections take a slug (`{#sec-purpose}`). The anchor is assigned at first acceptance, recorded in the source, and never changes. Heading text may be reworded freely. The server renumbers section anchors on a doc write while the document is a draft.

**Numbering is identity, so accepted sections are never renumbered.** An insert between `2.1` and `2.2` is `2.1a`. An insert between `2.1a` and `2.1b` is `2.1aa`. Renumbering re-points anchors at different subject matter and is refused at the accept gate. Sub-numbering (`2.1.1`) collides with the depth limit and appending (`2.4`) breaks reading order, so neither is used.

**Anchor depth** is a server setting, default 3, surfaced through the admin configuration path. It governs addressability. Deeper headings render normally as content of their nearest anchored ancestor. Raising the limit is additive. Lowering it applies only to documents never accepted, and a publication that would orphan an accepted anchor is rejected naming the anchors. Unnumbered anchors count as depth 1.

What one class buys:

| Requirement | Expression |
|---|---|
| Partial implementation | `<component> wl:implements <section>`, coverage is a count |
| Section superseded, heading kept | `wl:status wlc:superseded ; dct:isReplacedBy <section>` |
| Section removed with explanation | `wl:status wlc:superseded` plus `dct:description` |
| Sections never deleted | set diff at accept time |
| Partial supersession of a document | `dct:replaces` between sections |

No `wl:fullyImplements` or `wl:partiallyImplements` exists. Declared coverage goes stale. Derived coverage cannot.

**Every anchored section is a design rule** (12-spec-refactoring-design-tree.md S8 to S11, S20). The store mints a rule the first time a section appears, numbered from the project's `RULE` counter as `WL-RULE-<n>` (`WL-CL-<n>`, the ref's earlier spelling, still resolves everywhere a rule ref is read, including refs in document text), and records the section's heading and body as version 1. On each later write, the store matches sections to the rules the document already arranged in three passes over the whole document, each rule claimed once: first anchor and heading both match, then heading alone, then anchor alone. Changed text rewrites the rule's newest version while that version is a draft, and becomes the next version, itself a draft, once the newest version is accepted; a section matching nothing becomes a new rule (12-spec-refactoring-design-tree.md S35). The anchor-only pass keeps a renamed section on its rule, and it also keeps a section that was deleted and replaced at the same anchor in one write on the old rule as a new version; withdrawing a rule explicitly is later work (12-spec-refactoring-design-tree.md S33). Accepting a document accepts every draft rule it arranges and writes nothing else. `lode show WL-RULE-<n>` and `GET /api/v1/rules/WL-RULE-<n>` read a rule with its current text and the documents arranging it. Tasks link to rules through `governedBy` (03-tasks-and-execution.md §4). A rule can also be edited directly: `PUT /api/v1/rules/WL-RULE-<n>` and `lode rule edit WL-RULE-<n> --file <body> [--heading <text>]` take the new heading and body, regenerate the arranging document's body with that one section changed, and write it through the document's own path, so the body stays an exact reassembly of its rules; the submitted body is normalised to what the parser would have produced for it, starting on the line after the heading and ending in a newline, with a blank line before the next heading when one follows. On a draft document the rule's draft version is rewritten in place. On an accepted document the write goes to the candidate revision, opened if none is open, and the rule's next version appears when the revision lands with `lode doc revise --accept`. A rule arranged in no document, or in more than one, refuses the edit in this stage. `GET /api/v1/rules/WL-RULE-<n>/versions`, `/versions/<v>`, `lode rule versions` and `lode show WL-RULE-<n> --version <v>` read the history. Shared rules across documents and the migration of the old specs are later work. A rule relates to other rules through typed edges written by an architect (`refines`, `constrains`, `conflictsWith`, `references`; 07-knowledge-graph-and-search.md §2) over `POST`/`DELETE /api/v1/rules/{id}/edges` and through `references` edges the store derives from the rule text on every version: each `WL-RULE-<n>` ref and each `<PROJECTKEY>-<TYPE>-<n>#sec-<a>` ref (`SPEC`, `ADR` or `PLAN`) that resolves to a rule becomes an edge, the derived set replaces the previous one, and a ref to the rule itself, to a whole document or to nothing contributes no edge. A rule carries an owner (an actor id) and tags, set over `PATCH /api/v1/rules/{id}`; dates stay on tasks and milestones (12-spec-refactoring-design-tree.md S15).

**Rule lineage.** Two more edge types record identity across a refactor, neither written over `POST /api/v1/rules/{id}/edges` (12-spec-refactoring-design-tree.md S22, S57). `wasDerivedFrom` is written by hand with `lode rule link <ref> --derived-from <ref>`; it is an ordinary manual edge with no other writer. `supersededBy` has one writer, `lode rule supersede`, and cannot be added or removed any other way. A split is document-first and mints no edge of its own: editing the document creates the new rule B narrower than A, and an architect records the lineage afterwards with `lode rule link WL-RULE-B --derived-from WL-RULE-A` (S56). A merge is an edit of A that absorbs B's text plus `lode rule supersede` naming `WL-RULE-B -> WL-RULE-A`, which withdraws B and writes `supersededBy` from B to A; A keeps its identity and its tasks, and B's tasks resolve to A through the edge (S22).

**The refactor.** `lode rule supersede --map <file>` applies a map of old rules to their successors in one transaction (S24): every rule on the map's left side becomes `withdrawn` whether or not it names a successor, a `supersededBy` edge is written from it to each successor, and every accepted plan arranging a rule the map withdraws is marked `stale` (S23). Re-running an applied map changes nothing. `--dry-run` resolves and reports the counts without writing. A task governed by a withdrawn rule keeps its link and is never rewritten by the refactor (03-tasks-and-execution.md §4).

**A plan arranges the rules it covers** (12 S16, S42). Its `covers` entries are resolved to rules with the walk 03 §4 states and written as its arrangement; the plan's own prose stays prose and mints no rule. Which plans cover a rule is therefore a membership fact read from the arrangement (S25), and the rule detail lists them. The `coverage:` levels and `fullCoverageWith` keep working as they are in this stage; retiring them is later work (S44).

## 5. Versioning

Every design document has a version-free canonical IRI that denotes the current version and one immutable IRI per version:

```
/docs/WL-SPEC-25        canonical
/docs/WL-SPEC-25/3      snapshot
```

The grammar and the `wlid:` base are in 07-knowledge-graph-and-search.md §10.

The projection follows DCAT 3: `dcat:hasCurrentVersion`, `dcat:hasVersion`, `dcat:version`, `dcat:previousVersion`, `prov:wasRevisionOf`, `prov:wasAttributedTo`, `dct:issued`. A snapshot carries the document's own class. Ordering is by `dcat:version` as a number, because `/10` sorts before `/3` as a string. The canonical IRI is the only IRI anything links to by default. Versioned IRIs appear only in pinned claims (section 13). This is a named exception to rdf-registry ADR-0006's version-free rule.

Each version is its own immutable named graph. The canonical node lives in a small mutable graph holding the current-version pointer. Publishing a version is one transaction: insert the version graph, retarget the pointer. No reader observes a pointer disagreeing with its content.

**Section-level staleness without section versions.** `wl:lastRevisedIn` (functional, Section to versioned DesignDoc) records the version in which a section's content last changed. A claim pinned at v3 on section 4.2 is stale if and only if `lastRevisedIn` names a version above 3. Editing other sections invalidates nothing.

## 6. Constraints on accepted documents

Enforced by the server at accept time as a set diff between the accepted document and the candidate:

| Rule | Statement | Enforcement |
|---|---|---|
| 1 | Anchors are append-only. Every anchor in version N appears in N+1. Retirement is `superseded`, which keeps the anchor resolvable | refused |
| 2 | A superseded section carries an explanation: `isReplacedBy` a successor, or a `dct:description` saying why | detected, see below |
| 3 | Anchors are immutable. No renumbering, no reuse for a new topic. Inserts use letter suffixes | refused |
| 4 | Drafts are exempt from 1 to 3, except that anchors already published in an accepted version stay protected while a revision is in draft | refused |
| 5 | `lastRevisedIn` is set on exactly the sections whose content changed. Touching it on unchanged sections mass-invalidates claims | refused |
| 6 | Anchor depth respects the configured limit | refused |

Rule 2's subject is a section already superseded, a fact about the corpus around the document. Refusing an unrelated accept would block the wrong edit. So it is derived: a section is superseded when an effective `replaces` names it or its document, and explained when a `replaces` names it or its document. `lode doc list --bare-superseded` reports the difference. The reason text for a section dropped without successor is authored in the superseding document's frontmatter under `sectionDescriptions`, a map from anchor to explanation. The projector emits it as `dct:description`. No store column holds it.

Constraints bind from first acceptance. Never-accepted markdown is unconstrained.

## 7. Frontmatter and references

Frontmatter keys are ontology property local names. A key with no term signals that the ontology is missing one. An unknown key is an error.

| Key | Term | Value |
|---|---|---|
| `status` | `wl:status` | one `wlc:DesignDocStatus` concept |
| `issued` | `dct:issued` | ISO date of first publication |
| `covers` | `wl:covers` | spec sections a plan undertakes to realise. Plans only |
| `defers` | `wl:defers` | spec sections a plan hands to a named owner (`spec:`/`to:`). Plans only, after `covers` in key order |
| `requires` / `isRequiredBy` | `dct:requires` / `dct:isRequiredBy` | dependency |
| `blocks` / `blockedBy` | `wl:blocksPlan` / `wl:blockedByPlan` | document-level plan ordering. Plans only |
| `replaces` / `isReplacedBy` | `dct:replaces` / `dct:isReplacedBy` | supersession |
| `wasDerivedFrom` | `prov:wasDerivedFrom` | the design record this graduated from |
| `amends` / `amendedBy` | none | doc-level amendment, reduced to section edges by projection |
| `sectionDescriptions` | `dct:description` on the section | anchor to explanation for sections dropped without successor |
| `skills` | `wl:recommendsSkill` | skills the design recommends (09-cli-and-skills.md) |

`blocks`/`blockedBy` is the one key whose spelling differs from its term. `wl:blocksPlan` is a distinct Plan-to-Plan pair so the Task-to-Task `wl:blocks` closure stays type-homogeneous and `?t wl:dependsOn+ ?x` never walks document ordering. The same spelling inside a plan's `## Tasks` section is a different relation between the tasks it mints.

Each `covers` entry is a qualified reference with `coverage:` (`full`, `partial`, `none`) and optional `fullCoverageWith:`. A bare reference means `full`. `implements` in plan frontmatter parses as `covers`. A plan with no governing spec writes `covers: NO-SPEC`. Coverage semantics are in 06-design-queries-intents-decks.md.

**References carry the section.** A reference is a document ref plus an optional `#sec-<anchor>` fragment. Frontmatter references and in-document links resolve to the same section node and both survive heading rewording. A fragment naming an anchor absent from the target is a broken reference and fails `lode doc lint`. Amendment and supersession are section-scoped on both ends: the value is a map whose keys are the subject's own anchors, with `"."` for the whole document, each entry naming the other document's sections. A genuinely doc-wide amendment stays doc-level.

**Amendment references are bidirectional in reading.** `amends`/`amendedBy` and `replaces`/`isReplacedBy` are answered from either document so an agent asking "what still constrains this section" answers from the document it has open. The store holds one row per edge. Neither `amends` nor `amendedBy` has an ontology term. Doc-level amendment is indexing metadata that projection reduces to section-level `dct:isReplacedBy` edges.

**The shorthand.** `<PROJECTKEY>-<TYPE>-<n>[#sec-<anchor>]`, matching `^([A-Z][A-Z0-9]{1,9})-(SPEC|ADR|PLAN)-(\d+)(#sec-.+)?$`. Examples: `WL-SPEC-1`, `WL-SPEC-25#sec-9`, `WL-ADR-7`, `WL-PLAN-98`. `<n>` is an integer with no zero padding. Numbers draw from a per-`(project, kind)` counter, so `WL-SPEC-1` and `WL-PLAN-1` both exist and `<TYPE>` selects. `lode doc add` allocates the next free number. An explicit `--number` is legal for reserving or importing and is collision-checked against the same counter. `<TYPE>` is verified against the target's kind. `SPEC`, `ADR` and `PLAN` are reserved project keys. `ADR` resolves only documents that already exist (section 2). A document reference must never parse as a task id. A ref also resolves by slug or bare corpus number. Bare task ids matching `[A-Z][A-Z0-9]{1,9}-\d+` in bodies autolink to the cockpit.

## 8. Edges

| Edge | From | To | Meaning |
|---|---|---|---|
| `covers` | plan | spec or section | the plan undertakes to realise it, at a coverage level |
| `defers` | plan | section, with owner | named planning gap until some plan covers it |
| `implements` | task (from a plan declaration) or component (from a repo manifest) | section | the work or code discharges the section |
| `amends` / `amendedBy` | document or section | document or section | text folded in by `lode show --inline` |
| `replaces` / `isReplacedBy` | document or section | document or section | supersession |
| `requires` / `isRequiredBy` | document | document | dependency |
| `blocks` / `blockedBy` | plan | plan | ordering. A task is blocked while any task of a blocking plan is open |

A plan `covers` a section (an undertaking). Work `implements` it. The task-level `implements` edges let section closure walk from a section to its tasks directly.

## 9. Editorial lifecycle

```
draft --(lode doc accept, owner only)--> accepted --> superseded
```

| Status | Applies to | Meaning |
|---|---|---|
| `draft` | document | editable, unconstrained beyond protected published anchors |
| `in_review` | document | a human began review work in the review UI |
| `accepted` | document | passed review at a point in time. No promise it will be built |
| `superseded` | document, section | replaced. Stays readable and linkable |
| `stale` | plan | unexecuted plan covering a section amended in place. Stored, set by the edit path, cleared by re-acceptance |
| `withdrawn` | document | closed without a successor |
| `spent` | plan | every minted task has closed. Set by the store, terminal, hidden from the default listing (12 S5) |
| `patched` | section | approved text modified in place since approval |

`proposed` and `implemented` do not exist. "Under review" is a draft with an open `review` task. Submission is an event with no status column.

**Owner and reviewers.** A document has one owner (defaults to the creator) and a set of reviewers (`lode doc set reviewers`). The owner accepts and lands revisions. The document is not accepted until every assigned reviewer approves. Approval is never acceptance. Ownership transfers through `POST /api/v1/docs/{id}/owner` (`lode doc transfer`, one document or every document one actor owns with `--from`), by the current owner or an admin, emitting `doc.owner_changed`. A transfer to the current owner is a legal no-op and still emits.

**Acceptance is a deliberate human act.** No transition or subscriber performs it. Decomposing an accepted spec into plans is also chosen explicitly. Skills offer the step, never take it.

**Submit.** `lode doc submit <id>` emits `wl:DocumentSubmitted` and changes no column. The subscriber mints a `review` task (section 15). A second submit while that task is open mints nothing. A plan edited since its last submission mints a fresh review task once the previous one closed.

**Revising an accepted spec.** Nothing links to a new IRI and the status never flips back to draft. A revision is a candidate version `v(n+1)` in status `draft` against the stable document identity, one candidate slot per document:

1. `lode doc revise <ref>` opens the candidate. Needs `doc.write`.
2. The accepted version stays current and authoritative. Readers and drift queries are unaffected.
3. Review with crit, comments anchored to sections.
4. `lode doc revise <ref> --accept` lands it: the section 6 constraint check and the publication transaction succeed or fail together. Owner only.
5. `lode doc revise <ref> --discard` (`DELETE /api/v1/docs/{id}/revision`) withdraws the candidate and frees the slot. Owner or the revision's author. The `doc.revision_discarded` event carries the withdrawn body.

Within a revision sections may be added, reworded or marked superseded. An anchor the accepted version published may not be removed.

**Edit.** `lode doc edit` changes an accepted document in place. For a plan this is the normal path at any status and bumps the version. For a spec it is allowed only under section 10's rules. A body edit is a compare-and-swap against the version the caller read; a concurrent edit makes it fail rather than overwrite. A plan that has minted tasks refuses an edit that leaves its `## Tasks` section unreadable (section 11).

**Grooming.** An accepted document nothing acts on pollutes every reader's context. The lease sweeper supplies the clock: when an accepted document crosses the staleness threshold (30 days by default, configurable per instance and per project) with no execution against it, it emits `doc.stale` once. The subscriber mints a `design` grooming task: re-evaluate, adjust, or close. Any revision re-arms the clock. A stale accepted document is excluded from `lode task brief` assembly, flagged in `lode doc show`, and flagged where another document `requires` it. The target is 100% resolution: every accepted document shipped or explicitly closed. The number to watch is `lode doc list --unresolved --older-than 30d`.

**Plan lifecycle.** A plan becomes `spent` when its last minted task closes (S5); `withdrawn` and `spent` plans are hidden from `lode doc list` and the cockpit list unless `--status all` or a status is named (S45). A plan whose arranged rule is withdrawn becomes `stale` (S23, S46). Re-planning a stale plan is claimed with `lode work next --replan` (S28, S47).

## 10. In-place amendment and the escalation ladder

An executor that hits something its plan did not anticipate does not improvise and does not stop for a human unless human judgment is required. It spawns a fixer subagent at the tier the fix needs (plan defects to the planning tier, spec defects above it) and waits. A subagent has bounded latency. A minted task has unbounded queue latency. The rule generalises: a synchronous subagent when the work is bounded and tier is the only thing missing, a minted task when the work needs human judgment or another operator.

| Fixer outcome | Executor | Document |
|---|---|---|
| Resolved, non-substantive | continues | amended in place, note attached |
| Resolved, substantive | stops | amended in place, sections marked `patched`, review task minted for the original approvers |
| Not resolved, or needs human judgment | stops | unchanged. `lode task escalate` mints a `design` task |

**`lode task escalate --to plan|spec --reason "..."`** runs one transaction: release the executor's lease and move the task to `blocked`, mint a `design` task against the referenced document carrying the reason and failing context, add a `blocks` edge from it to the blocked task, assign it to the plan's author, and deduplicate (a second escalation on the same section joins the open task).

**In-place amendment is allowed when nothing refers to the section.** Referrers are accepted documents claiming the section through `requires`, `covers`, `amends` or `replaces`, and tasks already claimed against a plan that covers it. The plan whose execution triggered the fix does not count, and unexecuted plans do not count (they become `stale` and are regenerated). `lode doc referrers <ref>#sec-N` is the query.

**Substantive is decided in two parts.** The server decides the mechanical part at `lode doc edit` time and refuses a silent patch that trips a rule, naming it. The fixer judges the rest, and uncertain counts as substantive.

| Class | Rules |
|---|---|
| Mechanically substantive (server) | section has a referrer; edit adds a dependency; edit touches anything mirrored in `ns/`; edit changes a schema, migration, API, CLI flag, event name, IRI or enum; edit changes acceptance criteria or a definition of done |
| Judged substantive (fixer) | removes, reverses or narrows a normative statement; changes a default or threshold; adds a requirement implying work the plan lacks; contradicts another section |
| Non-substantive (only these) | wording that changes no assertion; an added example, typo, repaired reference or anchor; filling a gap the document was silent on, following a principle stated elsewhere in it |

A `patched` section leaves the document `accepted`. `lode doc show` and `lode task brief` render it with its notes inline. "How much of this document is still what a human approved" is a query.

**Notes.** `lode doc note <doc>#sec-N --body "..."` attaches a note to a frozen anchor, linked to the task and session that raised it. It never blocks execution. Two callers: an executor recording a defect it is not fixing, and a fixer recording what it changed and why. Notes surface in `lode doc list --has-notes` and on the review view. Note volume is the design-quality signal. Agents never autonomously draft amendment documents, because a draft's `amends` changes what the corpus reads as pending.

**Stale plans.** An unexecuted plan whose `covers` names a section amended in place becomes `stale` and a re-planning task is minted. Its minted-but-unclaimed tasks are re-derived with the plan or flagged at claim time.

**Tier routing.** `lode work next --kind <list>` filters the claim by kind. Mechanical loops run `feature,bug,chore`. High-tier loops run `design,spike,review`. Without it an escalated task is claimed by the loop that could not resolve it.

## 11. Plans

**Token budget.** A plan body is measured against a token budget (S19): the server's soft and hard values come from `LODE_PLAN_TOKENS_SOFT` and `LODE_PLAN_TOKENS_HARD`, a project may override either under `plan_tokens_soft` and `plan_tokens_hard` in its settings, a write over the soft budget carries a warning and a write over the hard ceiling is refused (S48, S49).

### 11.1 The `## Tasks` section

A plan declares its tasks in exactly one `## Tasks` section containing nothing but `### Task <N> — <title>` subsections. Prose directly under the heading fails the accept. `N` runs from 1 without gaps within each plan. A plan series shares no sequence. Each subsection opens with a fenced YAML block, then prose (what to do, which files, the test that proves it), then an optional `- [ ]` step list.

````markdown
## Tasks

### Task 1 — Short imperative title

```yaml
kind: feature            # feature | bug | chore | design
priority: medium         # critical | high | medium | low
skills: [superpowers:test-driven-development]
blockedBy: [ ]           # task numbers within this plan
implements: [ WL-SPEC-29#sec-7 ]
```

Prose, then steps.
````

| Key | Required | Default | Destination |
|---|---|---|---|
| `kind` | yes | none | `feature`, `bug`, `chore`, `design` to `tasks.kind`. `review` and `spike` are authored outside plans |
| `priority` | no | `medium` | `tasks.priority`, ranking input, not projected |
| `skills` | no | none | the task skill pin, projected as `wl:requiresSkill`. Exact match on the written `plugin:skill` identifier first, then the segment after the colon. Unknown skill is a warning |
| `blockedBy` | no | none | `blocks` edges between minted tasks. Unknown number, self-reference or cycle fails the accept |
| `implements` | no | none | `implements` edges from the minted task to spec sections, possibly from several specs, or none under `covers: NO-SPEC` |

The title after `Task <N> — ` (em dash required, non-empty) becomes `tasks.title`. Everything from the metadata block to the next task heading becomes `tasks.body` verbatim. Step boxes are executor guidance. Nothing reads them, and task state is the only execution state.

After minting, the plan keeps the heading, the metadata block and a `Minted as WL-412` line, and folds the prose into a collapsed `<details><summary>Original detail</summary>` element. The task body is the working instruction. Keeping the declaration is what lets a later re-accept mint tasks added afterwards.

### 11.2 Acceptance mints the tasks

`lode doc accept` on a plan runs one transaction: create each declared task in `draft`, set `plan_doc` and `plan_task_key`, wire `blockedBy` as `blocks` edges. Nothing is minted above them. The invariant is `status = accepted` if and only if its tasks exist, for plans accepted through the verb. Three cases sit outside it:

- **Historical import** records the status the plan reached and mints nothing.
- **Re-acceptance after an edit** mints only declarations with no row yet. Existing rows are never mutated or deleted. A minted task outlives its declaration. Withdrawing work is a task transition. `blockedBy` edges are wired only into tasks this acceptance minted. A soft-deleted task keeps its declaration's identity.
- **The coverage-only plan** declares at least one `covers` or `defers` entry and no tasks at all (no `## Tasks` heading and no heading opening with `Task` or `Tasks` plus a number). It accepts and mints nothing, which puts its coverage in force. An empty `## Tasks` section or a `## Task 1` heading missing the em dash fails the accept. `lode doc lint` applies the same test.

A declaration's identity is its title, recorded at mint in `plan_task_key`. Titles are unique within a plan (a duplicate refuses the accept). Retitling a declaration withdraws it and declares another. Once a plan has minted anything, a body edit that leaves `## Tasks` unreadable (missing, unparseable, duplicate titles) is refused at the edit. A plan that minted nothing is exempt.

A plan's body edit is its next version. Acceptance is keyed on document and version, so re-accepting an already accepted version mints nothing and answers with the document unchanged.

A plan's task set is `tasks WHERE plan_doc = <doc>`. `child_of` survives only for decomposing an oversized task into subtasks, with its guards applying to any task that has children. The depth cap of 2 is spent on task to subtask. Checkboxes are never execution state.

| Entity | Owns | Created by |
|---|---|---|
| Authoring task (`design`) | the work of writing the plan | whoever picks up planning |
| Plan document | content, editorial status, identity of the set | the authoring work |
| Tasks with `plan_doc` | execution state | the accept transaction |

### 11.3 Spec fan-out is a query

| Need | Owner |
|---|---|
| How far along is spec N | `lode doc coverage`: accepted sections times plans times task-set states |
| Which specs need planning | `lode doc list --needs-planning`: accepted sections no accepted plan's `covers` discharges (`full`, or a closing `fullCoverageWith` set) |
| Which plans need execution | `lode doc list --needs-execution`: accepted plans whose task set is unminted or unfinished |
| One spec's remaining work | `lode doc todo <ref> [--deps]` (06-design-queries-intents-decks.md) |
| Plan B after plan A | `blocks` edge between the plan documents |
| These land together | Milestone over deliverables |
| Task waits on part of spec N | blocks on the covering plan, or on individual tasks |

"All of spec N is done" is never a completion event, because coverage grows under amendment.

## 12. Task kinds and decisions

The state machine, ranking and leases are in 03-tasks-and-execution.md. The kind set is fixed here because plans mint into it:

```sql
CHECK (kind IN ('feature','bug','chore','design','review','spike','decision'))
```

| Kind | Meaning |
|---|---|
| `feature`, `bug`, `chore` | claimable, worktree-bound work that lands a diff. `reconcile` work is `chore` |
| `design` | author or revise a spec or plan. Closes when the document is submitted. Never an umbrella held open against coverage |
| `review` | minted by the lifecycle. Plans cannot declare it |
| `spike` | time-boxed throwaway experiment. Its outcome feeds planning |
| `decision` | answer the questions posed on the task and record the answers. One accountable assignee via `lode task assign`. It has no worktree, no branch and no PR, and is excluded from `readyCandidates`. Closes when the last question is answered, in the same transaction |

No kind is structural. Which document a task produced is carried by the document (`prov:wasGeneratedBy`). `validKinds` and `wlc:TaskKind` are generated from `ns/` and tested to agree.

**A decision's data** lives in `decisions`: `id`, `task_id`, `key` (unique per task, addressed as `WL-643/x-distribution`), `position`, `group`, `question`, `context`, `response_type` in `single_select`, `multi_select`, `single_select_notes`, `pick_or_freetext`, `yes_no`, `freetext`; `options` as `[{label, description}]` (null for `yes_no` and `freetext`), `min_picks`/`max_picks` for `multi_select`, `answer` as `{picked, notes, freetext}` or `{"value": "yes"|"no"|"unsure"}`, `decided_by`, `decided_at`. Any task may carry rows. Only a `decision` task closes by answering. `response_type` is fixed when the question is posed. Writing the answer and stamping the decider is one transaction. An unanswered row may be reworded, re-ordered or moved to another task. An answered row is immutable. Ephemeral mid-session questions are a different object, unspecified.

## 13. Implementation coverage

Three objects:

| Object | Home |
|---|---|
| Design content (spec sections) | backbone, projected to the graph |
| Work to produce it | `wl:Task` |
| "This repo at this commit satisfies section X of doc Y" | git, in `.worklode/implements.yaml` |

```yaml
implements:
  - section: /docs/WL-SPEC-4#sec-3
    pinned:  /docs/WL-SPEC-4/2
    by:      [internal/store/lease.go, internal/store/sweeper.go]
```

The claiming component is derived from `by:` paths through `components.yaml`'s first-match mapping. The manifest has no `component:` field. Paths spanning several components split into one claim per component. A path matching no component is a fatal publication error. Every mapped repo has at least one component: with no `components.yaml` an implicit component whose IRI is the repo coordinates (`/components/github.com/sunstoneinstitute/worklode`) matches the whole repo, and promoting it to an explicit one leaves the IRI unchanged.

The repo-local deriver `observed/repo-implements` (`lode graph derive`, on push to the default branch plus a scheduled backstop) writes `<component> wl:implements <section>` into its own named graph, full-replace, with the pin as an RDF-1.2 annotation `<< c wl:implements s >> wl:pinnedVersion </docs/<KEY>-<TYPE>-<n>/<v>>`.

| Query | Reads |
|---|---|
| Unimplemented intent | accepted sections with no `implements` edge |
| Coverage of a document | implemented sections over non-superseded sections |
| Stale claim | pinned at vN where `lastRevisedIn` exceeds vN |
| Orphaned claim | anchor absent from the current version |
| Delivered coverage | implemented sections whose Deliverable is deployed to an Environment, via `wl:deliveredBy` |

`lode doc coverage <ref>` reports per section implemented, unimplemented, stale and superseded. `lode graph drift --docs` reports stale and orphaned claims. No timestamp heuristic determines whether intent is satisfied. `wl:implements` names only Component-to-Section (and task-to-section from plan declarations); a Task `wl:produces` a Deliverable and `wl:affects` a Component.

**Authorship.** `wl:Task` is a `prov:Activity`. A document names the task that wrote it with `prov:wasGeneratedBy`. A task's author is `prov:wasAssociatedWith`, because `prov:Activity` and `prov:Entity` are disjoint.

**Project and Milestone.** `wl:Project` is the backbone's unbounded project umbrella over `project_repos`. Every task carries exactly one `wl:inProject`. `wl:Milestone` groups Deliverables. Task membership derives through `wl:produces`. No task-to-milestone edge is stored. Sprints are unrepresentable.

## 14. `ns/` is the schema source

Classes, properties, SKOS enums and SHACL shapes live in `ns/*.ttl` (`wl:` ontology and `wlc:` concepts under `https://worklode.io/ns/`; `wlid:` is the instance's own base URL, 07-knowledge-graph-and-search.md §10). A codegen step (`scripts/nsgen.py`) emits Go constants, SQL `CHECK` fragments and event validation tables. Generated artifacts are checked in and CI fails on diff. A schema change is one commit touching Turtle, generated code and migration. The mirror follows the spec: amend the spec first, then `ns/`. Validated with `riot --validate`.

## 15. The event log and the `doc-lifecycle` subscriber

The append-only `events` table is written in the transaction that makes the change it records. It is also a log with ordered, offset-tracked, at-least-once subscribers. Operational reading (`lode event tail`, the Progress page stream) is in 10-cockpit.md and 01-system-and-deployment.md.

**Commit horizon.** `events.id` is assigned at insert and visible at commit, so a `last_seen_id` cursor skips a slow transaction's lower id forever. Readers take only rows with `txid < pg_snapshot_xmin(pg_current_snapshot())` (`events.txid xid8 NOT NULL DEFAULT pg_current_xact_id()`, indexed on `(txid, id)`). The visible log grows only at its tail. Aborted transactions leave holes, writers are not serialized, and any long transaction anywhere stalls every subscriber, which the lag metric exposes.

**Subscribers.** `event_subscribers(name PK, last_read_offset, last_acked_offset, updated_at)` with `acked <= read`. Read takes the batch below the horizon after `last_read_offset`. Ack advances `last_acked_offset` forward only. Restart resumes at `last_acked_offset`, redelivering the unacked window. One active consumer per subscriber via `pg_try_advisory_lock(hashtext('wl:subscriber:' || name))` on a dedicated connection. Release unlocks explicitly and destroys the session. The loop polls (default 1s), no `LISTEN`/`NOTIFY`. A new subscriber row starts at offset 0 (`ON CONFLICT DO NOTHING`) and replays the whole log. Events are never deleted or compacted.

**Emitting.** Domain events carry a curie type and JSON-LD payload with `@id` `wlid:events/<id>` (id reserved by `nextval` and inserted with `OVERRIDING SYSTEM VALUE`), `prov:atTime`, `prov:wasAssociatedWith`, `wl:subject`, and per-type properties. `wl:Event rdfs:subClassOf prov:Activity`, one subclass per type. `external_id` is deterministic, `<type>:<subject>:<version>`, so `(source, external_id)` makes retries idempotent. Typed emit helpers are generated from `ns/`. Vendor webhook events keep their dotted types and payloads in the same table. No `CHECK` on `events.type`. Every payload names its subject (`task`, `doc`). A minted task id is completed on the row inside the recording transaction.

| Event | Payload |
|---|---|
| `doc.owner_changed` | `doc`, `actor`, `request`. Previous owner in `state_log` |
| `doc.revision_discarded` | the withdrawn body |
| `doc.patched` | section, substantive classification, the rule that decided it |
| `doc.stale` | the grooming clock fired, once per crossing |
| `task.gap_found`, `fix.started`, `fix.finished` (`outcome` in `resolved`, `substantive`, `escalated`) | the escalation ladder funnel |
| `wl:DocumentSubmitted`, `wl:DocumentAccepted` | subject, and for accepted `wl:fromStatus`/`wl:toStatus` |

Task and lease event payloads are listed in 03-tasks-and-execution.md.

**`doc-lifecycle`**, a pure `Evaluate(event) -> []Action` in `internal/watcher`, executed by `internal/api/docwatch.go` when `NewServer` has a `BackgroundCtx`:

| Event | Action | Guard |
|---|---|---|
| `wl:DocumentSubmitted` | mint `review`, state `ready`, `about_doc` set, in the document's project | no open review task about it |
| `wl:DocumentAccepted` on a spec | mint `design`, state `ready`: decide how to decompose this spec into plans and write them | no open design task about it |
| `doc.stale` | mint `design` grooming task | none stated |

Minted tasks carry `prov:wasInformedBy` to the event. Idempotency has two layers: an action is recorded as an event with `external_id = <subscriber>:<rule>:<event-id>`, so redelivery is a no-op; a legitimate repeat (re-accepting a revised spec) is absorbed and noted on the open task, and mints a fresh task once it has closed. No watcher action emits an event its own subscriber consumes. No watcher accepts a document. `wl:DocumentAccepted` on a plan mints nothing, because the accept transaction already did.

**Planning cost lands on the planning task.** A planning session claims its `design` task into a worktree before writing, so `lode task cost` answers for planning. Tokens spent before the claim stay unattributed.

**Metrics** (01-system-and-deployment.md conventions): `worklode_event_subscriber_lag{subscriber}` gauge, `worklode_events_processed_total{subscriber,type,outcome}` counter, `worklode_event_batch_duration_seconds{subscriber}` histogram in `internal/eventbus`; `worklode_event_streams_active`, `worklode_event_stream_events_sent_total` in `internal/api`; `worklode_watcher_actions_total{rule,outcome}` in `internal/watcher`. Unknown types count as `other`.

## 16. The backbone is the only home

`lode doc import` brings a markdown corpus into the backbone. It is a client-side two-pass walker over `internal/designdoc` writing through the public API only (pass 2 re-resolves frontmatter once the whole corpus exists). It is idempotent by slug, walks the top level only, states status rather than deriving it (a plan with no status imports `accepted`, needs the admin-only `doc.import` permission, and mints no tasks), and lands every document at version 1 with `last_revised_in` 1. Revision history is not reconstructed.

The importer knows one identity convention, the slug. Duplicate slugs are reconciled by hand before import. Import writes documents only, so a mis-import is reversed by deleting the document.

Numbering is the server's at write time, anchor permanence is enforced in the store, consolidation is `lode show --inline`, frontmatter is validated on `lode doc add`/`edit`.

**Dangling references are reported.** `rebuildEdges` stores an unresolvable target verbatim in `to_external` and `repointExternalEdges` fixes rows up when the target arrives, so forward references are accepted on write. `lode doc lint` (no argument) reports every unresolved edge and coverage-closure entry and every resolved edge whose anchor names no section of its target, exiting non-zero. `lode doc lint <file>` lints a draft's anchors before posting. Prose citations that name no document are also reported.

A document created in the cockpit is reachable by exactly the same commands as an imported one.

## 17. What an edit invalidated

A minted task body is a snapshot. Editing a plan after acceptance leaves minted tasks carrying the old prose, and the task is what `lode task brief` shows. `lode doc impact <ref>[#sec-N] [--json] [--paths]` (`GET /api/v1/docs/{id}/impact?anchor=sec-N`, `doc.read`) is a pure read over stored state, adds no state and changes no rule. Exit 0 whether or not anything is reported. A ref naming no document exits non-zero.

**Declaration drift** (subject or downstream document is a plan). Parse the current body with `designdoc.PlanTasks` and join declaration title to `plan_task_key`:

| State | Meaning |
|---|---|
| `matches` | task exists and still carries the declaration |
| `drifted` | task exists, declaration changed since mint |
| `not minted` | declaration has no task, re-accept the plan |
| `minted, declaration removed` | task's key matches no declaration. Deliberate withdrawal or accidental retitle |

Drift compares only what mint wrote: body (trimmed whole-string compare), `kind`, `priority`, `skills`. `blockedBy` is excluded. The report names the differing fields and prints no diff.

**Downstream documents.** Plans whose `covers` names the document or section at any level, plans that `defers` to it, documents that `amends` or `replaces` it, documents naming it in `requires`, plans it `blocks` and plans declaring `blockedBy` on it. One hop only. Drafts included and marked. A bare ref reports the union over the document and all its sections. Each row: ref, status, edge type.

**Open work.** For every downstream plan and the subject when it is a plan, the unclosed minted tasks (not `merged`, `deployed_dev`, `deployed_prod`, `released`, `abandoned`, or soft-deleted) with id, state, title, plan and drift state. Closed tasks are counted only.

**`--paths`** resolves backtick-quoted repo paths (contains `/` or a known extension, no scheme) in the subject body and the reported task bodies against the current checkout and reports missing ones with their source body. Errors outside a repo. Bare migration numbers are not checked. Doc-to-doc references stay with `lode doc lint`.

**The skill** fires on "I just edited a spec or a plan" and carries the state-to-action table: run impact first; a drifted closed task is history, leave it; a drifted `ready` or `draft` task must be re-bodied before claim; a drifted `in_progress` or `in_review` task is a conversation with its worker; `not minted` means re-accept; `minted, declaration removed` is either close the task or an accidental retitle.

`lode doc referrers` (section 10) and `lode doc impact` answer different questions and neither changes the other. Forward-looking "what is left" is `lode doc todo`.

## 18. Surfaces and permissions

| Command | Purpose |
|---|---|
| `lode doc add --kind spec\|plan\|minutes\|note [--body\|--body-file] [--number]` | author a draft. Minutes and notes skip submit and accept (06 §7) |
| `lode doc edit <ref>` | in-place edit, compare-and-swap, server-enforced substantive rules |
| `lode doc submit <ref>` | emit `wl:DocumentSubmitted`, mint the review task |
| `lode doc set reviewers <ref> ...` | assign reviewers |
| `lode doc accept <ref>` | owner's manual commit. On a plan, mints declarations with no row |
| `lode doc revise <ref> [--file\|--accept\|--discard]` | open, update, land or discard a candidate revision |
| `lode doc transfer <ref> [--from <actor>]` | hand ownership |
| `lode doc note <ref>#sec-N --body` | attach a note to an anchor |
| `lode doc list [--kind --status --needs-planning --needs-execution --bare-superseded --has-notes --unresolved --older-than]` | the map |
| `lode show <ref> [--inline] [--section sec-N] [--version vN]` | read. `--inline` folds in-force amendments and supersessions |
| `lode doc show <ref> --json` | structured sections and `edges`/`edges_in` |
| `lode doc lint [<file>]` | anchors and depth of a draft, or the corpus's dangling references |
| `lode doc coverage <ref>` | per-section implemented, unimplemented, stale |
| `lode doc todo <ref> [--deps]` | one spec's remaining work |
| `lode doc referrers <ref>#sec-N` | the in-place amendment gate's set |
| `lode doc impact <ref>[#sec-N] [--paths]` | what an edit invalidated |
| `lode doc progress` | spec progress (10-cockpit.md) |
| `lode doc import [--dry-run]` | corpus import, admin |
| `lode graph drift --docs` | stale and orphaned claims |
| `lode task escalate --to plan\|spec --reason` | the ladder's human step |
| `lode event tail [--type --since --follow]`, `lode event subscribers`, `lode event seek <name> --to <offset>` | the log. `--follow` and `seek` are admin |

Reads are `GET /api/v1/docs`, `/docs/{id}` (with `?version=`), `/docs/{id}/impact`. Writes include `POST /docs`, `/docs/{id}/submit`, `/docs/{id}/accept`, `/docs/{id}/revision` with `DELETE` to discard, `/docs/{id}/owner`, all through `routeGuards`.

| Act | Who |
|---|---|
| Read documents | `doc.read` |
| Add, edit a draft, open a revision, note | `doc.write` |
| Accept, land a revision | owner |
| Discard a revision | owner or the revision's author |
| Transfer ownership | owner or admin |
| Import with stated status | `doc.import`, admin |
| `event seek`, `event tail --follow` | admin |

Roles and how they map to these permissions are in 02-identity-actors-and-secrets.md. Review is crit, with comments anchored to sections. The lode plugin ships skills for authoring, review, accepting, offering decomposition, plan review and post-edit impact; every state change is one of the verbs above (09-cli-and-skills.md). The cockpit's document views are in 10-cockpit.md.

## Sources

WL-SPEC-25 (documents in the backbone, with 029 §4, 061 §2, 064, 068 amendments folded in), WL-SPEC-55 (documents leave the tree), WL-SPEC-67 (what an edit invalidated).

## Open questions

- Compare-and-swap on `lode doc edit` shipped as WL-848 but no source spec specifies its precondition shape (version number or ETag) or the conflict response.
- Whether a git mirror of documents (025 §5 `[doc_sync]`, `doc pull`/`doc push`) is still wanted. Neither verb is on 061's allowlist and 09's `lode doc fetch` cache covers the read side, so it is dropped here.
- The shorthand type for `minutes` and `note` refs, and their `wl:` classes, are not stated in SPEC-64.
- Pin repetition in `implements.yaml`: a document-level default pin with per-claim override is undecided.
- Whether the fixer's judged-substantive verdicts should be spot-audited at a higher tier.
- Whether a note may carry proposed replacement text without becoming a draft amendment.
- The `doc.stale` grooming rule's guard against a duplicate open grooming task is not stated.
