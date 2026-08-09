---
status: draft
issued: 2026-08-04
requires:
  - 004-execution-backbone.md
  - 014-design-documents-as-graph-objects.md
  - 025-documents-in-the-backbone.md
  - 026-design-doc-queries.md
  - 027-event-watchers.md
---
# Spec 028 — Escalation and the post-acceptance document lifecycle

## 0. Why {#sec-0}

`MODEL_SELECTION.md` splits the work by tier: a high-tier model writes plans precise enough
that a cheaper model can execute them mechanically. The split has a failure mode it does not
name. When an executor hits something the plan did not anticipate, resolving it means changing
the plan, and changing the plan is not an act the executing tier is allowed to perform. Today
the executor either improvises — which the tier split forbids — or stops with an explanation in
a session transcript that dies with the session.

Nothing carries the discovery upward, so the corpus never learns that a plan was
under-specified, and the human who wrote it never finds out.

This spec defines the upward path: how an executor escalates, who fixes what, when an accepted
document may be changed in place, and what makes that change visible afterwards. It also
defines the downward consequence — an accepted document that nothing ever acts on is not
neutral, it pollutes the context of every agent and human who reads it, so documents that stall
are groomed or closed.

Both halves are the same argument: `status: accepted` records that a document passed review at
a point in time. It does not promise the design was good enough, and it does not promise anyone
still intends to build it.

---

## 1. The ladder {#sec-1}

An executor that cannot proceed does not stop and wait for a human. It spawns a **fixer
subagent** at the tier the fix requires — plan defects go to the planning tier, spec defects
above it — and waits for that subagent to finish.

Stopping for another agent is acceptable; stopping for a human is not, unless human judgment is
genuinely required. A subagent has bounded latency. A minted task has unbounded queue latency:
nothing guarantees an agent is free to claim it, so the executor could idle for hours over a
defect a higher tier resolves in minutes.

The fixer ends in exactly one of three outcomes:

| Outcome | Executor | Document |
|---|---|---|
| Resolved, non-substantive | Continues immediately | Amended in place, note attached (§4) |
| Resolved, substantive | Stops | Amended in place, affected sections marked `patched`, review requested (§3, §6) |
| Not resolved, or needs human judgment | Stops | Unchanged; `lode task escalate` mints a `design` task (§2) |

The rule generalises past this flow: **a synchronous subagent when the work is bounded and tier
is the only thing missing; a minted task when the work needs human judgment or another
operator.**

## 2. Escalation to a human {#sec-2}

`lode task escalate --to plan|spec --reason "..."` runs one transaction:

1. releases the executor's lease and moves the task to `blocked`;
2. mints a `design` task against the referenced document, carrying the reason and the failing
   context;
3. adds a `blocks` edge from the minted task to the blocked one, so execution resumes when the
   amendment lands;
4. assigns the minted task to the human who authored the plan;
5. deduplicates — a second task escalating the same document section joins the open `design`
   task rather than minting a rival.

Everything but the assignment reuses machinery 004 and 025 already define. `blocked` now covers
waiting on a decision as well as waiting on work; the blocking task's `kind` distinguishes them.

## 3. In-place amendment {#sec-3}

An accepted document may be edited in place — no amendment edge, no supersession — when nothing
refers to the section being edited. The amendment machinery of 014 §11 exists to protect inbound
claims, so where there are no inbound claims it is ceremony.

This narrows 025 §3's accepted-version guarantee rather than breaking it. Read that guarantee
as **accepted *and used***: what an inbound claim pins is the text it pinned, and a section
nobody pins has pinned nothing. The revision flow of 014 §5 — candidate draft against a stable
identity, accepted version authoritative throughout — remains the only path for every section
that has a referrer, which is every section the guarantee was written to protect. §3.1's
mechanically-substantive list makes "has a referrer" the first rule the server checks, so the
narrowing is enforced rather than asserted.

**Referrers, for this purpose,** are accepted documents claiming the section through
`requires`, `covers`, `amends` or `replaces`, and tasks already claimed against a plan that
covers it. Two things deliberately do not count:

- the plan whose execution triggered this fix — it is being amended in the same breath;
- plans that have not been executed, which §5 marks stale and regenerates instead.

Referrers are a query over the corpus, in line with 025 §1: rows are things someone made,
groupings are derived.

### 3.1 What counts as substantive {#sec-3.1}

The fixer subagent has an incentive to rule its own change non-substantive, because
"substantive" stops the executor it was spawned to unblock. The classification therefore has a
mechanical part the tool decides and a judged part the fixer decides, and the judged part
defaults against the fixer's interest.

**Mechanically substantive** — the server decides, the fixer has no say:

- the section is referenced by another accepted document (§3's referrer query);
- the edit adds a new dependency, in the document's frontmatter or in its prose;
- the edit touches anything mirrored in `ns/` — ontology terms, `wlc:` enums, SHACL shapes;
- the edit changes a schema, migration, API surface, CLI flag, event name, IRI or enum value;
- the edit changes acceptance criteria or a definition of done.

**Judged substantive** by the fixer:

- removes, reverses or narrows an existing normative statement;
- changes a default or a threshold;
- adds a requirement implying work the plan does not contain;
- contradicts another section of the same document.

**Non-substantive**, and only these:

- wording that changes no assertion;
- an added example, a typo, a repaired reference or anchor;
- filling a gap the document was *silent* on, where the fill follows a principle stated
  elsewhere in the same document.

**Uncertain counts as substantive.** The skill states this in those words: a model judging
ambiguity while under pressure to continue needs the thumb on the scale made explicit.

### 3.2 Enforcement is server-side {#sec-3.2}

The mechanical rules are enforced at `lode doc edit` time. A silent patch that trips one of them
is refused with the rule that fired, and the caller must take the review path. Advisory
enforcement in a skill would leave the gate to the same agent the gate exists to check.

## 4. Notes {#sec-4}

`lode doc note <doc>#sec-N --body "..."` attaches a note to a frozen section anchor, linked to
the task and session that raised it. It never blocks execution.

Two callers, one verb:

- an executor recording a defect it is not fixing — the design is wrong or incomplete, but not
  so wrong that continuing produces garbage;
- a fixer recording what it changed in place and why, alongside a non-substantive amendment.

Notes accumulate and surface in `lode doc list --has-notes` and on the document's review view.
Note volume is the corpus's design-quality signal: a section collecting notes from several
independent tasks was accepted too early.

Agents do not autonomously draft amendment *documents*. 026 §3.1 treats a draft's `amends` as a
live proposal that changes what the corpus reads as pending, so autonomous drafting would make
the corpus assert things nobody decided.

## 5. Stale plans {#sec-5}

A plan that has not been executed and `covers`-refers to a section amended in place is
regenerated, not patched: its status becomes `stale` and a re-planning task is minted against
it.

**`stale` is stored, not derived.** Deriving it from amendment timestamps would recompute a
verdict that an agent session already reached at real cost, and would recompute it differently
as the corpus moves underneath it — a plan judged stale in August would silently un-stale when
the amendment that caused it was itself superseded. A stored status also gives the re-planning
task something to close against. It is set by the §3 edit path and cleared by re-acceptance,
and every transition lands in the event log (§9), so the derivation remains reconstructible
without being the source of truth.

The same treatment applies to the exemption in §3: the triggering plan's own tasks that were
minted but not yet claimed were derived from pre-amendment text. They are re-derived with the
plan, or flagged at claim time. Without this the exemption ships tasks built against text that
no longer exists.

## 6. Reviewers and the accept gate {#sec-6}

A document carries an assigned reviewer set and is not accepted until every assigned reviewer
approves. Who gets assigned stays a social choice, as it is on a pull request; the gate itself
is mechanical.

`in_review` means a human has begun the review work — entering the document in the review UI
sets it.

A section amended in place under §3 takes section status `patched`: approved text, modified
since. The document stays `accepted`, because putting the whole document back to `in_review`
would tell every reader that nothing in it was approved. A `review` task is minted for the
original approvers, and `lode doc show` and `lode task brief` render a patched section with its
notes inline, so the next reader sees which paragraph has not passed the gate.

"How much of this document is still what a human approved" is then a query.

## 7. Grooming {#sec-7}

An accepted document that nothing acts on is worse than absent: it reads as settled design and
is actually an unbuilt intention, and every agent that loads it pays for the confusion.

The event log of 027 cannot see this, because nothing happens — there is no event for silence.
The lease sweeper of 004 supplies the clock:

- when an accepted document crosses the staleness threshold with no execution against it, the
  sweeper emits `doc.stale` into `events`, once, marked so it does not re-fire each sweep;
- 027's subscriber machinery mints a `design` grooming task — re-evaluate, adjust, or close;
- any revision re-arms the clock, so a document groomed but still not built returns.

**Threshold:** 30 days by default, configurable per instance and per project.

Staleness also changes how the document is served, with no human in the loop: a stale accepted
document is excluded from `lode task brief` assembly, flagged in `lode doc show`, and flagged
where another document `requires` it. This is the half that addresses the actual harm, and it is
a rendering rule rather than a workflow.

`withdrawn` joins the status scheme so a document can be closed without pointing at a successor.

The target this serves is **100% resolution**: every accepted document is shipped or explicitly
closed. Conversion — every accepted document eventually shipped — is the wrong target, because
it puts pressure on the margin to build designs that reality has already overtaken. The number
worth watching is the age of the unresolved set, not its size:
`lode doc list --unresolved --older-than 30d`.

## 8. Tier routing on claim {#sec-8}

`claim --next --kind <list>` filters the ready set by task kind. Mechanical loops run
`--kind feature,bug,chore`; high-tier loops run `--kind design,spike,review`.

Without it the ladder is a circle: an escalated `design` task is claimed by the same cheap loop
that could not resolve it. The `lode:next` skill defaults the flag from the session's model
tier.

This needs no schema change — 025 §6 already fixes the kind set.

## 9. Events {#sec-9}

The whole ladder is instrumented, per 027's log:

| Event | Emitted when |
|---|---|
| `task.gap_found` | executor finds the plan or spec does not cover the case |
| `fix.started` | fixer subagent spawned, with the tier and the target document |
| `fix.finished` | fixer done — payload carries `outcome`: `resolved`, `substantive`, `escalated` |
| `doc.patched` | a section amended in place, with the substantive classification and the rule that decided it |
| `doc.stale` | §7's clock fires |

`fix.finished.outcome` turns the ladder into a funnel: how often executors hit gaps, how often
the fixer resolves them, how often a human is pulled in. That ratio measures whether the
planning tier is producing mechanically executable plans, which is the premise the tier split
rests on. Escalations per plan is the same measurement per document.

## 10. Open questions {#sec-10}

- Whether the fixer's judged-substantive classification should be spot-audited — sampling
  `doc.patched` events with a non-substantive verdict and re-judging at a higher tier.
- Whether a note should be able to carry a proposed replacement text without becoming a draft
  amendment document (§4 forbids the latter).
