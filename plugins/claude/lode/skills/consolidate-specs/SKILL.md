---
name: consolidate-specs
description: Consolidate a project's overlapping specs (old design docs, rewrites, mirrors, "as of" snapshots) into one current-state spec per subsystem, with owner review, parallel target agents and a final check before retiring the sources
argument-hint: "[project key | doc refs | spec folder | subsystem]"
disable-model-invocation: true
---

# Consolidating a project's specs

Turn a sprawl of overlapping specs (old design docs, rewrites, mirrors, "as of" snapshots) into one coherent,
current-state set, one spec per subsystem. Most of the process applies to any spec store. The Worklode/`lode`
details are in the last section.

Scope for this run: $ARGUMENTS

The scope is free text. Empty means every spec in the current project. Examples:

```
/lode:consolidate-specs DP                            # one Worklode project
/lode:consolidate-specs DP-SPEC-12 DP-SPEC-25 DP-ADR-2  # specific documents
/lode:consolidate-specs docs/specs/ docs/specs2/      # git specs not yet in Worklode
/lode:consolidate-specs auth and deployment           # a subsystem, in words
/lode:consolidate-specs DP, skip the MCP specs        # a scope plus an exclusion
```

## Principles

- **The built system wins, with care.** Pin the baseline first: the branch and commit that count as "built".
  Every agent checks claims against that code and cites `file:line`. Sort each disagreement into one of three:
  - stale text: the spec follows the code;
  - a code bug: the requirement stays, and a task is filed to fix the code;
  - an owner-approved change: the spec follows the owner's decision.
  When it is unclear which, it is an owner question.
- **Flat current state.** Each spec says what the system does today. No amendment notes, no "as of",
  "previously" or "superseded by" text, no changelog. History lives in version control and the spec store's
  own version history. A stub the store requires (for example a frozen anchor kept as "Merged into §7.") is
  store metadata, not content, and is the one allowed exception.
- **One "Designed, not built" section per spec,** at the end. It lists decided-but-unbuilt capabilities, each
  with the task that builds it. Undecided ideas are not in it.
- **Agents do not decide.** When a point needs the owner, the agent leaves it out of the spec and reports it
  with a proposal. The coordinator batches those questions for the owner.
- **Code leftovers become tasks, not spec text.** Stale comments, dead config and bugs found along the way
  are filed as tasks. The spec describes the system, not its to-do list.
- **One owner per document at a time.** Only one agent edits a given spec. Cross-target findings go
  through the coordinator.
- **Specs stay current afterwards.** A change that alters behavior or a design decision updates the owning
  spec in the same change, as current state.

## Roles

- **Owner:** the human who decides. Reviews the map and the question batches.
- **Coordinator:** the main agent session. Writes the map, launches agents, routes findings between them,
  files tasks, keeps the question list, applies owner answers. It does not write the specs itself.
- **Target agents:** one per target spec. They merge, rewrite and verify against code, then report.
  With few targets, one person or agent can play the coordinator and target roles in turn.

## Phase 1: Map

Write a single working document, the **spec map**, before changing anything. It is the plan for the whole
consolidation and the place where owner decisions are recorded.

1. **Inventory.** Every place specs live (spec store, parallel rewrites, git mirrors, ADRs, loose docs,
   plans), with status, version, covering plans, and what is built.
2. **Per-area detail.** Fan out research agents, one per subsystem, to produce for each area:
   capabilities with code evidence (built / partly / not), contradictions between documents, overlaps, and
   open questions. Put this in an appendix.
3. **Target set.** One row per target spec: a code (T1, T2, ...), its seed document, what merges into it,
   and what is deleted afterwards.
4. **Disposition table.** One row per source section (or rule): its destination target and section, "kept
   as is" (for example an ADR that stays), or "dropped" with the reason the owner approved. A document can
   split across targets. This table is what the final check in phase 6 verifies.
5. **Cross-cutting decisions** (X1, X2, ...): where specs live, granularity, merge mechanics, which text
   wins, how to name things the code has not caught up with, what happens to stale tasks and code
   leftovers, and the execution order.
6. **Owner questions** (K1, K2, ...): the contradictions that no rule resolves.

Give every finding, decision and question a stable code (C-area-n, Q-area-n, X-n, K-n). Codes never change
during the process, so comments and answers can refer to them.

Make every document and task reference in the map a link, so the owner can click through.

## Phase 2: Owner review

Review the map with the owner in an inline-comment tool (for example `crit`), in rounds:

- The owner comments on rows. The coordinator records each answer next to the question as
  **Answer:**, and replies to the comment saying what changed.
- Questions are written one per paragraph so they can be commented on separately.
- When a comment asks for facts ("what endpoints are used for X?"), send a research agent and add the
  evidence to the map before asking again.
- When the review reveals that a document is wrong (for example, a row says "not built" for something that
  merged yesterday), fix the row, do not argue it.
- Act on direct instructions that come up in review (filing, editing or abandoning tasks) as they arrive.
- Commit the map when the review is approved.

## Phase 3: Execute in waves

Order targets by dependency: built core first, then targets that unblock work, then the
rest. Targets that share a source document go in different waves, or are told exactly which sections are
theirs.

For each wave:

1. Write a **shared brief** (once, reused by every agent; extend it between waves with lessons learned).
   It covers:
   - where the map is, and which of its sections and answers to read
   - the editing mechanics of the spec store
   - which text wins, the flat-current-state rule, and the "Designed, not built" section
   - how to retire a merged document
   - what not to touch: git, other targets' seeds
   - the report format
2. Launch **one agent per target, in parallel,** each with a short per-target prompt:
   - the seed
   - what merges in, by section, if the source is shared
   - what gets deleted or retired
   - the owner answers that apply
   - where in the code to verify
   - the current version of each document it edits. The agent records it, and before accepting, checks
     for changes made since and re-verifies the claims they touch.
3. As reports arrive, the coordinator:
   - **routes cross-target findings** to the agent that owns the other doc, by message, while it is still
     running if possible ("T6, absorb DP-SPEC-12 §10.1 and report the anchor; T2 will cut it to a pointer");
   - **files code leftovers** as tasks right away, grouping small ones into one chore and widening an
     existing chore instead of creating near-duplicates;
   - **adds owner questions** to the running list;
   - **broadcasts tool quirks** an agent discovered to the agents still running.

Target agent report format:

- Seed ref, old and new version.
- Rules or sections moved (from -> to) and documents now fully retired.
- Source files fully or partly merged (and what is left).
- Contradictions resolved by checking code, one line each with `file:line`.
- Code leftovers, `file:line` and a one-line fix.
- Owner questions, each with a proposal.
- Content that belongs to another target.

## Phase 4: Batch the owner questions

Collect every open question from all agents into one document with a recommendation per question, grouped
(housekeeping, design points, per-spec). Review it with the owner in the same inline tool. A question with
no comment takes the recommendation. Then apply the answers:

- spec edits go to agents (next phase);
- task operations (file, close, abandon, re-scope, delete) the coordinator does directly;
- decisions on design points are written into the owning spec.

## Phase 5: Fix-up pass

One more round of agents, each owning one or two documents:

- **Rule granularity:** every requirement must give useful guidance on its own. Merge rules that were
  split too small. Mark pure context or rationale as informative so it does not count as a planning gap.
- **Owner answers** for that document.
- **Cross-links** between the new specs (for example "the deployment spec" becomes a real reference).
- **ADRs** rewritten in place as current decisions.

Order within each document: finish all edits, accept the final revision, then apply supersession of moved
rules, then verify that rule and task links point at the successors. Retiring the sources waits for phase 6.

## Phase 6: Final check

Before retiring anything, the coordinator checks, and the owner approves the result:

- every row of the disposition table is satisfied: the content is at its destination, or the drop is
  approved;
- references between the new specs resolve, and the interfaces they describe agree;
- rule and task links point at the new specs;
- no owner question is open;
- nothing outside the specs (docs, READMEs, code comments, other projects) still links to a document about
  to be retired, or each such link has a task.

## Phase 7: Cleanup

- Retire every merged document in the spec store, with a justification naming where its content went.
- Delete or abandon stale planning tasks (decompose and review tasks for docs that no longer exist).
  Reconcile "Designed, not built" items with existing tasks and plans first. File one "decompose the
  Designed, not built section" task only for a spec whose section has items with no task.
- Close tasks the agents found already built, at the right delivery state.
- One git PR that deletes the old spec files and mirrors, turns the architecture doc into an index of the
  new specs, and fixes links in READMEs and guides. Leave code comments to a separate task.
- Rebase open PRs that touched deleted files. Compare each commit that only edited deleted spec files
  against the final specs. Move anything missing into the specs, then drop the commit.

## Lessons

- **Parallel agents on separate documents work well.** Overlap is only a problem where two targets share a
  source; split those by section in the prompts.
- **Expect every agent to find code-vs-spec contradictions** and stale comments. Budget coordinator time
  for filing them.
- **Newly sectioned specs mint many rules,** including context. Plan the granularity pass from the start.
- **Ask for facts before asking for decisions.** Owners decide faster with a table of what the code does.
- **Keep the question list in one place** and give each a recommendation, so the owner can answer
  "defaults" plus exceptions.
- **Spec store versions move during the run.** Give later agents the current versions, not the map's
  snapshot, and have them check for changes before accepting.
- **A build task running in parallel must not revise a spec that a consolidation agent owns.** Have it leave
  a note on the section instead. The owning agent, or the fix-up pass, writes it in.

## Worklode / `lode` specifics

- Accepted docs change through `lode doc revise <ref>`, then `--file`, then `--accept`. Drafts change through
  `lode doc edit --file`. Only one candidate revision can be open per doc: an agent that finds one open must
  stop, not discard it.
- `lode doc revise --file` refuses a body with frontmatter. Set title and issue date with `lode doc edit`, and
  dependencies with `lode doc link --requires`.
- Section numbers and anchors are derived from the arrangement (WL-REQ-165), so adding or unarranging a
  section renumbers the ones after it. Cite rules by ref, not by section.
- Move a rule's content with `lode rule supersede --map`, which carries task links over. **Supersede after
  `--accept`, not before:** accepting a revision re-activates rules superseded earlier.
- A doc whose rules are all superseded still shows `accepted`. Retire it with `lode doc withdraw
  --justification`. Drafts cannot be withdrawn. Delete them with `lode doc delete`, which can be undone.
- Docs with no anchored sections have no rules, so supersession does not apply. Withdraw them directly.
- `lode rule set --kind informative` marks context. A rule that an accepted plan `covers` cannot become
  informative; leave it a requirement.
- Stored doc notes cannot be deleted (as of October 2026). Leave stale ones.
- Accepting a new spec can be blocked by an open approval that only the web UI can decide. Ask the owner.
- Write every task and doc reference shown to the owner as a link to the Worklode web UI.
