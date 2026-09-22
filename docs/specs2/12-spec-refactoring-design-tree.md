# Spec refactoring: design tree

Working document for the grilling session. Each round settles a set of
decisions and opens the next. Answer inline; the recommended answer is marked
with an arrow. Settled decisions move to the top as the tree grows.

## The tree so far

```
Spec refactoring as a Worklode process
├── A. Unit of governance                           settled: the design clause (Q12-Q21)
│   ├── clause = lowest heading unit, ceiling from the embedding config (S7)
│   ├── immutable versions, links resolve to newest, version recorded (S9)
│   ├── status on the clause, documents derive theirs (S10)
│   ├── typed edges refines / constrains / conflictsWith / references (S11, S26)
│   ├── documents = frozen arrangements of a query (S12), depth on the entry (S21)
│   ├── document-first authoring, clause edits after (S13), ref WL-CL-<n> (S20)
│   └── one model for all project sizes (S16)
├── B. Link storage                                  Q2, Q3 settled
├── C. Tasks without a plan                          Q4 settled, Q22 settled: SubscriberLock (S17)
├── D. Clause identity across a refactor             settled (S22)
├── E. Old specs and open plans after a refactor     settled (S14, S23, S28)
├── F. Plan lifecycle                                Q9 settled, Q23 settled: soft 32k hard 64k (S18)
│   └── plan = frozen arrangement + task list (S16), coverage = membership (S25)
└── G. The refactor as a primitive                   settled (S24, S27, S29, S30)
```

## Settled in round 1

- S1 (Q1 partial): the unit is the section, and a link to a whole spec is a shortcut for all its sections. What a section is becomes the round 2 question.
- S2 (Q2): a task carries direct `governedBy` edges, materialised at mint time from the plan's coverage. The plan is never the source of truth for the link.
- S3 (Q3): after minting, only the architect and the refactor process change a task's governing links. Every change is an event on the task.
- S4 (Q4): planless tasks get their governing link at the design authority gate, filled in asynchronously by a dedicated spec reconciliation loop that needs a lock so two pods do not do the same work.
- S5 (Q9): `stale` is an intermediate plan state. `withdrawn` (obsolete) and `spent` (all tasks closed) are terminal. Plans in a terminal state are closed and hidden by default. A closed plan's tasks must each carry at least one `governedBy` link.
- S6 (Q10): the plan size cap lives in the plan-writing skill, fed by a server-side plan token budget with a per-project override. The number is open (Q23).

## Settled in round 2

- S7 (Q12, ceiling): the clause size ceiling is the embedding model's context window, read from server config. `LODE_EMBEDDING_CONTEXT_TOKENS` is required whenever `LODE_EMBEDDING_MODEL` is set, and the chunker's `ChunkRunes` is derived from it instead of the hardcoded 3600 sized to EmbeddingGemma. The 800 word-based figure from the clause-size study is dropped as a limit. What remains of Q12 is the unit (lowest heading unit or one rule) and whether a split below the ceiling is advisory.
- S8 (Q12, unit): the lowest heading unit, split below the ceiling one rule per clause as advice. No objection came back on this in round 2; say so if that is wrong.
- S9 (Q13): the term is `clause`, introduced in prose as "design clause".
- S10 (Q14): immutable versions. A link targets the clause identity and resolves to the newest version, records the version current when the link was made, and can pin a version in the rare case that needs it. Pinned links use the same versioned IRI form as the graph (Q24).
- S11 (Q15): status is stored on the clause. Accepting a document accepts every draft clause it arranges and writes nothing else; document state is derived.
- S12 (Q16): a small typed edge set defined in `ns/`: `refines`, `constrains`, `conflictsWith`, plus named clusters.
- S13 (Q17): a document is a query frozen into an ordered list, and the frozen list is what review accepts. The arrangement must carry enough to reassemble the source document with whitespace-only differences (Q25).
- S14 (Q18): document-first for bulk authoring, clause edits afterwards. A mapping file from the old specs to clause nodes is wanted (A1), keyed by the ref scheme decided in Q24.
- S15 (Q19): a clause carries status, perhaps owner, and tags or labels. Dates live on milestones and reach a clause only through tasks.
- S16 (Q20): a plan is a frozen arrangement of clauses plus a task list. Plans and specs share one mechanism.
- S17 (Q21): one model for every project size.
- S18 (Q22): the reconciler is one more eventbus subscriber behind `SubscriberLock`.
- S19 (Q23): plan token budget soft 32k, hard 64k, absolute token counts with a per-project override. Past the soft budget the planner can be a larger model; the hard ceiling is refused at post time.

Actions taken from round 2:

- A1: the mapping file exists as `docs/specs2/ttl/supersession.ttl` (524 `wl:supersedesSection` edges, 23 residue rows listed in `docs/specs2/ttl/README.md`). It is keyed by the interim `<clauses/NN-h2-h3>` IRIs and gets re-keyed once Q24 settles.

## Settled in round 3

- S20 (Q24): the textual ref is `WL-CL-<n>`, a project counter like tasks, minted at split time and stable across versions and arrangements. The canonical URL and IRI are not the ref: `/projects/<proj>/<kind>/<n>` and `/projects/<proj>/<kind>/<n>/<ver>` for a version, with `kind` unique across document kinds (spec, adr, plan, clause) and task kinds (feature, bug, chore, design, review, spike), which holds today. The root routes `/tasks/WL-456` and `/docs/WL-SPEC-4` become redirects, and an uppercase `<proj>` is a `<KEY>` typed by habit and redirects too. This amends 07 §10.2 (branch-free, version-free `/docs/<KEY>-<TYPE>-<n>` IRIs) and is noted there as a follow-up, outside this design's scope.
- S21 (Q25): depth lives on the arrangement entry: (clause, position, depth, optional heading override). Acceptance test: split and reassemble every document in `docs/specs2/`, `diff -w` empty.
- S22 (Q26, revised by your Q6 reply): links stay on the clause they were minted against. A split is a new version of A plus a new clause B (`wasDerivedFrom` A); a merge is a new version of A that absorbs B, with B `supersededBy` A. Tasks on B resolve to A through the edge, tasks on A never move. `supersededBy` is many-to-many for the migration from specs, where every old clause is withdrawn with successors.
- S23 (Q27): a plan whose arranged clause is withdrawn goes `stale`; plans were read-only already. Re-planning a stale plan is work `lode next` can hand out (Q32).
- S24 (Q28): the primitive is transactional bookkeeping over a clause-to-clause map; an embedding-derived candidate map is the next step; agent orchestration stays a skill. The migration runs in two steps (A2).
- S25 (Q29): coverage is arrangement membership; `coverage:` levels and `fullCoverageWith` go.
- S26 (Q30): `references` is a stored edge, minted from the clause text at post time and re-derived on every version.

Actions from round 3:

- A2: the migration path is old specs to clauses first, then a clause-to-clause map. Today's `docs/specs2/ttl/` split only the eleven new documents into clauses; `supersession.ttl` points at old sections, not old clauses. Next generation step: run the same split rule over the 47 inline exports, mint old clauses as `withdrawn` (the migration from specs is the one case where every old clause is superseded, since none of them continues as a live version), and rewrite the 524 edges as clause-to-clause `supersededBy`. The 23 residue rows stay a hand decision.
- A3: 07 §10.2 gets a follow-up note for the `/projects/<proj>/<kind>/<n>` canonical URL scheme (S20).

## Settled in round 4

- S27 (Q31): old, withdrawn clauses take `WL-CL-<n>` numbers from the same project counter as live clauses.
- S28 (Q32): re-planning a stale plan is asked for explicitly, never minted automatically: planning needs a top-tier model and not every harness is set up for it. `lode next --replan` lists stale plans and hands one out; the `design` task that tracks the work is minted at that moment, so nothing sits in the queue for a harness that cannot do it.
- S29 (Q33, scope): the gate is opt-in per project. `.worklode/config.toml` names the guarded paths (`paths` globstar or `regex`) and the required trailer line; `lode doctor` warns when a gated project does not require pull requests. Written into 11 §3. The trailer's target form (clause ref or section ref) is still Q33.
- S30 (Q33, target): the trailer names a clause, `Spec: WL-CL-456` or `Spec: WL-CL-456 amended`. Spec and section refs are accepted during a transition, switched by a server-side per-project setting (project-bound, so never `.worklode/config.toml`). Written into 11 §4.

## Recorded from increment 1

Behaviours the first implementation fixed that the rounds above did not name. Each is a decision now, kept here so a reader of the code finds its reason.

- S31 (S2, S3 applied to a re-accept): a plan's `covers` edges govern only the tasks that accept mints. Re-accepting a plan whose `covers` widened governs the tasks the re-accept mints and leaves every earlier task as it was. Under S3 the architect adds the new clauses to existing tasks by hand, and each addition is an event on the task.
- S32 (S20 applied to `governedBy`): a clause ref carries its project key and resolves across projects, so a task may be governed by a clause of another project. This is allowed. Platform clauses govern work in the repositories that build on them, and one project key per ref is what makes the link unambiguous.
- S33 (matcher ceiling): the third matching pass keeps a clause when only its anchor matches. A section deleted and replaced by an unrelated section at the same anchor in one write therefore continues the old clause as a new version, and tasks governed by the old text now resolve to the new text. A rename of a section that stays in place reads the same way and is the case the pass exists for. Withdrawing a clause explicitly, with S22's lineage edges, is the remedy and belongs to the clause-first editing increment.
- S35 (S10 refined): a version is minted only from an accepted version. While a clause's newest version is a draft, every write rewrites that version in place; an autosaving editor may save hundreds of times while the author works, and those states have no reader. Accepting the arranging document locks the version. A later change to a locked clause becomes its next version, a draft until its document is accepted. Since a clause has at most one draft version and it is always the newest, the clause's own status is the newest version's status and every version below it is accepted.
- S34 (S3 applied to the timeline): `task.governed` and `task.ungoverned` are rows in the event log and nothing else. `lode task timeline` and the cockpit timeline read task state transitions, so a governance change appears in `lode event tail` and on the task's detail, and in neither timeline.
- S36 (S20 and A3 applied in increment 1b): the canonical URL ships for clauses first. `/projects/<proj>/clause/<n>` and `/<ver>` are served; the document kinds redirect to the existing `/docs/<KEY>-<TYPE>-<n>` page; task kinds and the root-route redirects are the remaining half, recorded in 07 §10.2. A clause edit writes through the arranging document (S13, S14): a draft is rewritten in place, an accepted document's edit lands with its revision (S35), and a clause arranged in zero or several documents refuses the edit until plans are arrangements.

## Facts (looked up, not for you to answer)

How refs are parsed today (for Q24): the `<KEY>-<TYPE>-<n>` grammar is written out in five places with no shared constant: `internal/mdrender/autolink.go` (`SPEC|ADR|PLAN` shorthand, `spec N §M` keyword form, bare task ids filtered by live project keys), `internal/designdoc/resolve.go` (the resolution grammar `lode show` and the API use, fragment split on `#`), `internal/cmd/show.go` (accepts any `<TYPE>` and an optional `#sec-` suffix), `internal/store/changes.go` (`Worklode-Task:` trailer), `internal/api/admin.go` (key shape). Document versions are addressed by `--version N`, `/versions/{id}/{n}` and `?v=N`, and 07 §10.2 gives them the IRI `/docs/<KEY>-<TYPE>-<n>/<v>`. No prefix for a sub-document unit other than `#sec-N` exists anywhere. In `ns/clause.ttl`, `wl:position` sits on the clause and nothing carries heading depth (for Q25).

How the links work in the code today:

- Task to plan: `tasks.plan_doc` (the plan whose acceptance minted the task) and `tasks.plan_task_key` (which declaration in the plan). `tasks.about_doc` plus `tasks.about_anchor` is a different link: the document and section a review, design or escalation task is about. `docs.generated_by_task` is the reverse: the task that authored a document.
- Plan to spec section: `doc_edges` rows of type `covers`, from the plan to the spec with `to_anchor` naming the section. Coverage is three-valued (full, partial, none).
- Task to spec section: none, except `about_anchor`. A task reaches its governing section only through its plan's `covers` edges.
- Supersession today: `doc_edges` carries `amends` and `replaces` edges, each either document-scoped or section-scoped (`from_anchor`, `to_anchor`). Only a document-scoped `replaces` changes stored status (target flips to `superseded` on accept). A section-scoped `replaces` is a fact the `--inline` renderer folds in at read time; `doc_sections` has no status column, so a section cannot be marked superseded as stored state. Corrected after your comment: the metadata is per section, the stored status is per document.
- Amending a section marks accepted covering plans with no claimed work `stale`. A stale plan is re-accepted only after an edit.
- Document statuses: `draft`, `accepted`, `stale`, `superseded`, `withdrawn`. Checked per layer after your comment: `draft`, `accepted` and `withdrawn` are complete in store, API and CLI. `superseded` is complete except the UI shows only a chip and the CLI `doc show` banner has no superseded case, so an old version reads as current. `stale` is partial everywhere: only the amendment path writes it, the clock sweep emits `doc.stale` but never changes the row, no command sets it, and the UI has no chip or banner for `stale` or `withdrawn` (both render as the draft chip). The docs page has no status filter control.

Still running: the section-level map from the 47 old specs to the new documents. Questions that depend on it wait for round 2.

## Round 1 (answered, kept for the record)

❓ **Q1** - **Unit of governance**: A task is governed by what, exactly? (a) a spec document, (b) one or more spec sections, (c) a section at a pinned version, (d) a sentence or paragraph.

➡️ (b), sections, with (a) as the permitted fallback when a task spans a whole spec. Sections are the unit the amend and supersede machinery already works on and the unit a plan covers. Pinning a version (c) belongs on the link as metadata, never as the identity, or every amendment orphans the task. (d) is too fine to survive an edit.

---

❓ **Q2** - **Direct or derived**: Should a task carry its own `governedBy` link to spec sections, or should the link be derived at query time from task -> plan -> covered sections?

➡️ Direct. Materialise `governedBy` edges (task to spec section, a new edge kind next to `planned_in`, `about`, `generated_by`) when the plan mints the task, copying the plan's `covers` targets. From then on the plan can be abandoned, marked stale or deleted without the task losing its spec. Derivation through the plan stays available as a check ("does the task's link still agree with the plan it came from") but is never the source of truth.

---

❓ **Q3** - **Who may change the link**: After minting, who can add or remove a task's governing sections? (a) nobody, it is a fact of birth, (b) the architect, (c) any holder of the lease, (d) the refactor process only.

➡️ (b) and (d). The lease holder discovers scope drift but should record it as a note or block, not rewrite the link. The refactor process rewrites links mechanically through supersession (Q5). The architect corrects by hand in the rare case where a task was minted under the wrong section. Every change is an event on the task.

---

❓ **Q4** - **Tasks without a plan**: Bugs, chores and small fixes have no plan. When does such a task acquire its governing sections? (a) at creation, mandatory, (b) at creation, optional, then mandatory at the design authority gate (11-design-authority-gate.md) when the PR touches a guarded path, (c) never, they are a separate class of work, (d) inferred by the corpus search from the task text.

➡️ (b). Mandatory at creation slows down filing a bug from a stack trace. Never (c) leaves the drift you described. The gate is already the point where the change has to name its sentence, so the `Spec:` trailer becomes the `governedBy` edge. (d) as a suggestion in the cockpit and in `lode task add`, never as the recorded fact.

---

❓ **Q5** - **Section identity across a refactor**: Old spec A §5 becomes new document B §3, possibly merged with C §2. How does a task governed by A §5 keep a live link? (a) rewrite the task's edges to B §3 during the refactor, (b) add a section-level `supersededBy` edge A §5 -> B §3 (new: today supersession is document-level only) and resolve through it at query time, (c) both: record the supersession edge and also rewrite.

➡️ Your reply on this thread settles it: clauses supersede clauses, that is all. A task's `governedBy` edge stays on the clause identity it was minted against (S10); an edit is a new version and the link resolves to it; a clause absorbed into another carries one `supersededBy` edge, which queries follow. No section-level machinery is needed. Recorded in S22 and Q26.

---

❓ **Q6** - **One-to-many and many-to-one**: A refactor splits one old section into several new ones, and merges several old sections into one. Which is the edge cardinality, and how does a task governed by a split section resolve? (a) `supersededBy` is many-to-many and a task resolves to all live targets, (b) the refactor must name exactly one primary target per old section, others are `seeAlso`, (c) the refactor writes per-task overrides where the split matters.

➡️ Your reply on this thread reshapes it: a refactor with clauses still splits and merges, and identity survives more often than my first answer allowed. Splitting A is a new version of A with a narrower scope plus a new clause B; A keeps its identity and its tasks, B starts clean, and B records `wasDerivedFrom` A so the provenance is queryable. Merging A and B keeps A active as a new version that absorbs B's text, and marks B `supersededBy` A; tasks on B resolve to A through that edge, tasks on A never move. Withdrawal without a successor is the rare case. `supersededBy` stays many-to-many for the migration from specs, where every old clause has successors and none stays live. Recorded in S22.

---

❓ **Q7** - **Old specs after a refactor**: What happens to the 47 old specs? (a) status `superseded`, read-only forever, still shown when following a task's link, (b) deleted after the map is verified, (c) kept as `accepted` alongside the new ones.

➡️ (a), and your reply on this thread names the gap: this holds while specs and clauses coexist, and the migration between them has to be designed. That is A2 (old specs split into clauses first, then a clause-to-clause map) plus S30's transition (section refs accepted at the gate behind a server-side per-project setting). While a project is mid-migration, `lode show <old ref>` renders the old arrangement and names each clause's live successor; once every old clause has a successor the project flips the gate setting and the old refs are redirects.

---

❓ **Q8** - **Open plans at refactor time**: Plans covering superseded sections, some with minted tasks, some not yet accepted. What happens? (a) all marked `stale`, tasks continue, (b) unaccepted plans withdrawn, accepted plans with minted tasks marked `stale` and left read-only, (c) plans re-pointed at the new sections.

➡️ Updated after round 2. A plan is a frozen arrangement plus a task list (S16), so a refactor that withdraws a clause it arranges marks the plan `stale` (S5's intermediate state) and read-only. Its open tasks keep working from their own `governedBy` links (S2), and the plan closes as `spent` or `withdrawn` when they finish. An unaccepted plan is withdrawn. Nothing re-points a plan; the fix for a stale plan is a new one frozen against the successor clauses. Carried as Q27.

---

❓ **Q9** - **Plan terminal state**: A plan is read-only once a task is minted from it. Does it get a terminal status when all its tasks close? (a) yes, `spent`, set automatically, (b) no, `accepted` with all tasks closed is enough, derive it, (c) `stale` doubles as the terminal state.

➡️ (b). Derive it. The Progress page already derives plan state from task state, and a stored status that a query can compute is a second owner of one fact. Add `spent` only if a filter on the doc list needs it and the derivation proves too slow.

---

❓ **Q10** - **Plan size cap**: You want a plan to fit in about 50k tokens with room to reason. Enforce how? (a) `lode doc lint` and `lode doc add` refuse a plan over the cap, (b) warn only, (c) cap in the plan-writing skill's instructions only.

➡️ (a), with the cap as a project setting defaulting to 50k tokens measured by the same tokenizer the corpus index uses. A cap the tool does not enforce is a suggestion, and the skill (c) cannot see what a subagent actually posted.

---

❓ **Q11** - **The refactor as a primitive**: Which part of "spec refactoring" does Worklode own? (a) only the bookkeeping: a `lode doc supersede --map <tsv>` that takes a section map, sets statuses, mints the `supersededBy` edges and marks plans stale, all in one transaction, (b) also the agent orchestration: `lode doc refactor` that fans out the rewrite to agents and posts the result, (c) also the mapping: derive the section map from the old and new texts by embedding similarity, with the architect confirming.

➡️ Your reply on this thread: the problem dissolves once refactoring is rearranging clauses, and the primitive shrinks to S24's transactional bookkeeping. You also expect a successor problem at the clause level, shape unknown: topic sprawl across clauses, dissolved ownership, or agents reading so many clauses that the token gain erodes. Recorded as risks R1 to R3 below, each paired with the sprawl metric that would show it first, so the graph is measured from day one rather than after the symptom.


## Round 2

Your Q1 comment reframes the root: the spec layer as a graph of design elements
("speclets") that documents merely arrange, clustered by topic, component or
surface, living in a vector space, with plans and tasks attached to the element.
Round 2 works that model from first principles. Q5 to Q8 and Q11 wait behind
it.

❓ **Q12** - **What one element is**: What is the smallest thing that gets an identity in the graph? (a) one normative statement: a single rule, invariant, data shape, command, or endpoint, typically a paragraph plus a table, (b) a coherent cluster of statements about one concern, roughly today's H3, (c) today's H2 section, (d) size-bounded by tokens rather than by kind, say 100 to 1500 tokens.

➡️ Ceiling settled as S7: one clause must embed as one chunk, so its upper bound is the configured `LODE_EMBEDDING_CONTEXT_TOKENS` minus the context header, counted with the model's tokenizer (or a 3 characters per token proxy at lint time). For the unit: (b), the lowest heading unit (the H3 where one exists, else the H2). Splitting a leaf below the ceiling into one clause per rule stays advisory, argued by the amendment data in `docs/research/q12-clause-size-study.md`, and a leaf under 100 tokens folds into its neighbour unless it is a standalone rule. What drives it:

- The 45 effective amendments in the old specs are one rule each: p50 61 tokens against target sections of p50 744, so an amendment rewrites 8% of its target.
- They hit H2 and H3 targets 60/40, so heading level does not predict where change lands.
- The H2 was unstable under the refactor: 68% of kept old H2s merged into a shared new section and 43% landed under a new H3.
- Leaf headings already fit 100 to 800 tokens in about 80% of cases in both corpora, so the rule mostly ratifies what authors do and only splits the 6% of H2s over 1500 tokens, which by hand hold four to eight concerns each.
- Plan `covers` edges split 57/40 between H2 and H3, so plans will not care either way.

---

❓ **Q13** - **The name**: "speclet" is your placeholder. Options: speclet, clause, tenet, design element, norm, statement.

➡️ `clause`. It is the unit lawyers cite, amend and assemble into documents, which is exactly the lifecycle here. It is one syllable, has an obvious plural, works as an entity in `lode clause show`, and does not collide with anything in the current glossary. `speclet` reads as a diminutive spec and invites people to write small documents instead of elements.

---

❓ **Q14** - **Identity and change**: How does a clause change? (a) mutable in place with a version history, links point at the identity and resolve to the current text, (b) immutable versions, an edit creates a new version node with a `supersedes` edge from the old, the identity is the chain, (c) immutable and content-addressed, an edit is a new clause and the old one is withdrawn.

➡️ (b). Links (governedBy, covers, implements) target the clause identity and resolve to the current version by default, with the version at link time recorded on the link as metadata. Invalidation (what an edit invalidated, today 05-documents.md) becomes "which tasks and plans link to a clause whose current version is newer than the one they were minted against". This is the mechanism that removes amendment ceremony: there is no "amend section 5 of spec 8", there is a new version of one clause, and everything downstream sees it. (c) loses the identity that makes a chain of versions one thing; (a) loses the record.

---

❓ **Q15** - **Status moves to the clause**: Does the document status go away? (a) yes, clauses carry `draft`, `accepted`, `superseded`, `withdrawn`, and a document has no status of its own, (b) both, a document can still be accepted as a whole, which accepts every clause in it, (c) documents keep status, clauses inherit.

➡️ (a) for the fact, (b) for the ceremony. The stored status lives on the clause. "Accepting a spec" stays as an act that accepts every draft clause the document arranges, because that is how review happens in practice, but it writes clause statuses and nothing else. A document then has a derived state (all accepted, some draft, some superseded) and no stored one.

---

❓ **Q16** - **Typed edges between clauses**: Besides version supersession, which relations are first class? (a) none, clustering is by embedding only, (b) a small typed set: `refines` (narrows a parent), `constrains` (must hold when the other does), `conflictsWith` (recorded tension), plus membership in named clusters, (c) an open vocabulary in `ns/`.

➡️ (b), kept to three or four kinds, defined in `ns/` so the ontology stays the one place edge vocabulary lives. Embeddings give ad hoc neighbourhoods; typed edges give the few relations a query must be able to rely on: what a task must also satisfy (`constrains`), what a clause is a detail of (`refines`), and the tensions an architect has chosen to leave in place (`conflictsWith`). An open vocabulary (c) recreates spec churn as edge churn.

---

❓ **Q17** - **Documents as arrangements**: A spec document becomes an ordered arrangement of clauses. Is the arrangement (a) a stored ordered list with a title, edited by hand, (b) a stored query (tags, cluster, search terms, scalars) evaluated at read time, (c) a query that can be frozen into a list, the frozen list being what a review accepts.

➡️ (c). Ad hoc documents from a query are the feature you described: "everything about the cockpit that is still draft, by priority". But a review has to accept a fixed text, and a plan has to cover a fixed set, so the thing that gets accepted or covered is the frozen list. Freezing records the query it came from so it can be re-run. Documents own no text; the clause does.

---

❓ **Q18** - **Where clauses live and how they are authored**: Do clauses exist independently of documents from day one? (a) yes, `lode clause add` is the authoring primitive and documents are optional arrangements, (b) no, authoring stays document-first: an agent posts a markdown document and the backbone splits it into clauses on heading boundaries, (c) both, document-first for bulk authoring, clause-first for edits.

➡️ (c). Agents write good prose in one pass and poor prose one paragraph at a time, so bulk authoring stays a document post that the backbone splits. Every later change is a clause edit, which is what kills the amendment churn. The split rule is the H3-or-token-bound rule from Q12, and the migration path is the section map already in `docs/specs2/section-map.tsv`: every row becomes a clause, the eleven new documents become the first arrangements, and the old specs become arrangements of superseded clause versions.

---

❓ **Q19** - **Scalars on a clause**: You want scalars like due dates and priority to shape a document. Which live on the clause itself? Candidates: status, priority, component, surface tags, owner, horizon or due date, confidence.

➡️ Status, priority, owner and tags (component, surface). Not dates: a clause says what is true when built, and when it must be built belongs to the milestone and the tasks that implement it. A document query can still sort by "earliest due date among the tasks implementing this clause", which is derived. Confidence is tempting but no one will maintain it.

---

❓ **Q20** - **Plans in the clause model**: A plan covers clauses instead of sections. Does anything else about plans change? (a) no, `covers` retargets to clauses and the rest of 05-documents.md stands, (b) a plan itself becomes an arrangement of clauses plus a task list, so plans and specs share one mechanism, (c) plans are retired; a task list is minted directly from a frozen arrangement.

➡️ (b). A plan is the frozen arrangement of the clauses one body of work implements, plus the tasks that implement them, sized to the token budget from Q23. That is your "evaluate a body of work holistically in one context window" purpose stated as data, and it makes the plan's coverage exact by construction. (c) throws away the holistic review step you said plans exist for.

---

❓ **Q21** - **The superpowers band**: You observe that document-and-section specs fit a middle band of project size and break above it. Does the clause model need to serve the small band too, or is it explicitly for projects past the threshold? (a) one model for all sizes, small projects just have few clauses, (b) two modes, document-only below a size and clauses above, (c) clauses always, but the small-project tooling never shows them.

➡️ (a). One model, and the document-first authoring in Q18 is what makes it cheap for a small project: they post a spec and never look at clauses. Two modes (b) means a migration at the threshold, which is the exact moment a project is least able to afford one.

---

❓ **Q22** - **The reconciler's lock**: The async spec reconciliation loop from Q4 needs to run on one pod. (a) a Postgres advisory lock held for the loop's lifetime, (b) `SELECT ... FOR UPDATE SKIP LOCKED` per work item, so several pods share the queue, (c) whatever the existing background loops use.

➡️ (c). The doc-lifecycle subscriber already holds a session-scoped Postgres advisory lock per subscriber name (`SubscriberLock`), stands by when another pod holds it, and rewinds unacked work on takeover. The reconciler is one more eventbus subscriber behind the same lock. Per-item `SKIP LOCKED` (b) is only worth it if one pod cannot keep up, which a reconciler that runs on PR events will not hit.

---

❓ **Q23** - **Plan token budget**: A server-side setting with a per-project override. What is the default and how is it derived? (a) a fixed number, 40k tokens, (b) a fraction of the smallest model context the project plans with, say 25% of 128k = 32k, (c) two numbers: a soft budget the skill aims for and a hard ceiling the backbone rejects.

➡️ (c), soft 32k, hard 64k, both project-overridable, measured with the corpus tokenizer at post time. 32k leaves a 128k-context planner three quarters of its window for the code and its own reasoning and keeps even Fable under the 200k you want to stay below once the codebase context is added. The hard ceiling exists so a runaway agent cannot post a 300k plan that every later reader has to load.

## Round 3

Round 2 settled the clause model, so the questions deferred behind Q1 come back in clause terms (Q26 to Q28), and three follow-ups come from your round 2 comments (Q24, Q25, Q30).

❓ **Q24** - **The clause ref and IRI scheme**: Your Q18 comment: refs like `WL-456` and `WL-SPEC-4#sec-4.2` are brief, regex-friendly and familiar; how are clauses numbered once they are detached from documents? Facts: the IRI scheme in 07 §10.2 is `/<plural type>/<natural key>`, version-free, with a document version as a path segment (`/docs/WL-SPEC-25/3`) and a section as a fragment. No textual ref embeds a version today; a version is `--version N` on the CLI or `?v=N` in the cockpit. The `<KEY>-<TYPE>-<n>` grammar is copied in five places (see Facts). Options: (a) a new type in the existing grammar, `WL-CL-456`, numbered per project like tasks, IRI `/clauses/WL-CL-456`, version `/clauses/WL-CL-456/3`; a section ref `WL-SPEC-4#sec-4.2` keeps resolving, to the clause at that position in the arrangement, (b) clauses are addressed only through an arrangement, `WL-SPEC-4#sec-4.2`, and a detached clause has no ref, (c) an opaque id (content hash or UUID) with the ref grammar left alone.

➡️ (a). `CL` rides the grammar every regex already accepts (`internal/cmd/show.go` takes any `<TYPE>`; the autolinker's `SPEC|ADR|PLAN` alternation grows one word), auto-links for free, and reads as what it is. A pin is the versioned IRI on the link record, `/clauses/WL-CL-456/3`, matching how document versions are already written; prose and `covers` stay version-free, and `lode show WL-CL-456/3` accepts the textual form for humans. (b) ties identity to the document, which is the thing the model removes. (c) is unreadable in a commit trailer and in a `Spec:` line at the gate. Numbering is a project counter, so `WL-CL-456` is minted at split time and never changes across versions or arrangements.

---

❓ **Q25** - **Heading level in an arrangement**: Your Q17 comment: reassembling a document from its clauses and arrangement should give only whitespace differences, and the vocabulary has no heading level today. Where does depth live? (a) on the arrangement entry: each position carries a depth, so the same clause can sit at H2 in one document and H3 in another, (b) on the clause itself, as the level it was authored at, (c) derived from `refines`: a clause that refines another renders one level below it.

➡️ (a). Depth is layout, and layout belongs to the arrangement. (b) breaks the moment a clause is reused in a second document, and (c) mixes a semantic edge with presentation: two clauses at the same depth in a document often have no `refines` relation, and the H2 preamble clause has to render as a heading with body text, which `refines` cannot express. The arrangement entry becomes (clause, position, depth, heading text override optional). Acceptance test: split every document in `docs/specs2/` into clauses and reassemble; `diff -w` must be empty. That test runs against the current `clauses.py` output before any store work.

---

❓ **Q26** - **Clause identity when a refactor merges or splits**: Versions cover an edit to one clause (S10). A refactor also merges several clauses into one and splits one into several, which is where identity changes. Tasks linked to the old clauses: (a) links stay on the withdrawn clause, which carries `supersededBy` edges to every successor, and queries follow the chain to the live clauses, (b) the refactor rewrites the task links to the successor set in the same transaction, recording the old target on the event, (c) both: rewrite the link and keep the old target as link metadata.

➡️ (a) for the link, with the split and merge shapes from your Q6 reply: a split keeps A live as a narrower version and adds B, a merge keeps A live and marks B `supersededBy` A. Only a clause with no live continuation is withdrawn. The withdrawn clause is the text the task was written against and the `supersededBy` edges are the auditable fact; a merge resolves to one successor, a split leaves A's tasks on A and starts B without tasks. The cost is one join per hop; refactors are rare enough that chains stay short, and the cockpit and `lode show` resolve "current governing clauses" through the chain by default.

---

❓ **Q27** - **Open plans when their clauses are superseded**: A plan is a frozen arrangement (S16). A refactor withdraws some clauses it arranges. (a) the plan goes `stale` (S5's intermediate state) and stays there until someone re-freezes it against the successor clauses, at which point it is current again, (b) the plan goes `stale` and is read-only from then on; open tasks keep working from their own `governedBy` links and the plan is closed as `spent` or `withdrawn` when they finish, (c) the refactor re-freezes the plan automatically by following the `supersededBy` chain.

➡️ (b). It gives `stale` the meaning you asked for in Q8, a fixable state, without making plans mutable: the fix is a new plan, and a task's link to its clause is what survives (S2). (a) reopens a plan after it was accepted, which is back-patching. (c) invents a plan body no one reviewed.

---

❓ **Q28** - **The refactor as a primitive, in clause terms**: Which part does Worklode own? (a) the bookkeeping: `lode clause supersede --map <file>` takes a mapping of old clauses to new ones, withdraws the old, mints `supersededBy` edges, marks the affected plans `stale`, all in one transaction, (b) also the mapping proposal: derive a candidate map by embedding similarity between old and new clauses, the architect confirms, (c) also the agent fan-out that rewrites the text.

➡️ (a) now, (b) as the next step, never (c). The bookkeeping must be transactional or a half-applied refactor leaves tasks pointing at nothing. The similarity map is cheap once every clause has an embedding, and the section map the agents produced by hand this week is exactly what it would have generated as a first draft. Orchestrating the agents is a skill, and skills already run outside the server.

---

❓ **Q29** - **Coverage becomes membership**: A plan covers exactly the clauses it arranges (S16). Today a plan declares `covers` with `coverage: full | partial | none` and `fullCoverageWith` for the plans that complete a partial one (05-documents.md). (a) drop the levels: a clause is covered when an accepted plan arranges it, partial coverage is arranging fewer clauses, (b) keep `partial` on the arrangement entry for a clause a plan only touches, (c) keep the whole machinery unchanged on top of membership.

➡️ (a). The levels exist because a section was too coarse to cover exactly, and the clause is the fix for that. If a clause is too big to cover in one plan it is two clauses. `fullCoverageWith` becomes a query: which accepted plans arrange this clause.

---

❓ **Q30** - **Is `references` an edge?**: The generated graph carries 230 `references` edges (a clause citing another clause or document in prose) next to the three typed edges from S12. (a) first class, stored like the others, minted from the text at post time, (b) derived at read time from the clause text and never stored, (c) dropped; embeddings cover "related".

➡️ (a). It is the edge that makes the M3 one-hop neighbourhood useful (the report in `docs/research/sprawl-metrics-report.md` shows most references today target a whole document, which is what leaves 76.7% of clauses with no inbound edge). Minting at post time means it is re-derived on every new version and never hand-maintained, so it does not add edge churn.

## Round 4

Three follow-ups from round 3. Nothing else in the tree is open, so if these settle the frontier is empty.

❓ **Q31** - **Numbering the old clauses**: A2 splits the 47 old specs into clauses that are `withdrawn` from birth. (a) they take `WL-CL-<n>` numbers from the same project counter as live clauses, (b) they get a separate historical range or prefix, (c) they keep only their old section ref `WL-SPEC-4#sec-4.2` as identity and never get a clause number.

➡️ (a). Identity is identity; a task's `governedBy` edge and a `supersededBy` edge need one target type, and a separate range (b) is a second scheme to explain forever. (c) makes the migration a special case in every query. The counter is a bigint and the old corpus is about 750 leaves, so the cost is a one-time block of numbers.

---

❓ **Q32** - **Re-planning a stale plan**: Your Q27 comment: `lode next` could offer re-planning of stale plans. (a) when a plan goes `stale` the doc-lifecycle subscriber mints one `design` task "re-plan <plan> against its successor clauses" with `about_doc` on the stale plan, and `lode next` hands it out like any task, (b) a `lode next --replan` mode that lists stale plans without minting a task, (c) nothing automatic; the architect files the task.

➡️ (a). It is the pattern doc-lifecycle already runs for submission and acceptance (025 §15.4): the prompt is minted, the act is not, and the same suppression rule (no second task while one is open) applies. (b) is a second work queue outside the task table, which the model has avoided so far. The task closes when a new plan is accepted that arranges the successor clauses, or when the stale plan is withdrawn.

---

❓ **Q33** - **The gate trailer names a clause**: The gate is the design authority gate in `11-design-authority-gate.md`: a CI job that runs on every PR touching a guarded path (store, migrations, API routes, CLI commands, ontology) and requires a `Spec:` trailer in the commit or PR body. The trailer answers the citation test, "name the spec sentence this change makes true", so the spec stays authoritative and code cannot quietly change what the system does. Its forms today are `Spec: WL-SPEC-4 sec-5` (the change implements that section), `Spec: WL-SPEC-4 sec-5 amended` (the PR also ships the spec change), and `Spec: none fix|refactor|perf|copy|tests|build|config` (the change alters how, not what). S4 made this trailer the source of a planless task's `governedBy` edge, filled in by the reconciler. Your round 4 reply settled the scope (S29: opt-in per project, paths and trailer in `.worklode/config.toml`, `lode doctor` checks the PR requirement). What is left is the trailer's target once clauses exist: (a) `Spec: WL-CL-456` and `Spec: WL-CL-456 amended` (the PR ships a new version of that clause), `Spec: none` unchanged, (b) keep the section form and let the gate resolve it to the clause at that position in the arrangement, (c) accept both forms indefinitely.

➡️ (a), with (b) accepted for one release as a redirect so open branches do not fail the gate on the day the trailer changes. The trailer is where the `governedBy` edge for a planless task comes from (S4), so it should name the thing the edge targets. The gate's citation test reads the same: name the clause this change makes true.

## Risks (from your Q11 reply)

The clause model solves spec sprawl and will meet its own sprawl. Each risk is paired with the metric from `docs/research/sprawl-metrics-report.md` that would show it first.

- R1 topic sprawl across clauses: the clauses on one topic spread across arrangements and stop referencing each other. Watched by M2 on the clause graph (arrangements per topic, connected components per topic, largest component share; today 48.6 components and 9.1%).
- R2 dissolved ownership: no clause has an owner, or one owner holds most of them, so no one answers for a topic. Watched by the owner tag from S15 (share of clauses with an owner, HHI of owners per topic), which the report does not compute yet.
- R3 agents read too many clauses: one-hop neighbourhoods grow until the load approaches whole-document reading and the token gain is gone. Watched by M3 (one-hop over clause tokens, 1.2x today against 3.8x whole-arrangement) and M4 (share of clauses with no inbound edge, 76.7% today; falling is good until the one-hop ratio starts climbing).

## Frontier

Empty after round 6. Every branch of the tree is settled (S1 to S30, with S31 to S36 recorded from the first increment), with three actions recorded (A1 to A3) and one flagged assumption: S8, the clause unit is the lowest heading unit, which drew no objection. Nothing is acted on until you confirm this is a shared understanding; a Finish Review with no comments is that confirmation.

