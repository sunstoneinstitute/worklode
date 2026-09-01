---
status: draft
covers:
- docs/specs/037-vendored-design-skills.md#sec-2.2
- docs/specs/037-vendored-design-skills.md#sec-3.2
- spec: docs/specs/037-vendored-design-skills.md#sec-6
  coverage: partial
---
# The motherlode remixes — research and transformation prompts

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decide what each of spec 037 §3.2's five remixes actually says, and
produce the `UPSTREAM.md` transformation prompts (037 §2.2) that generate them.
This plan writes no skill bodies and no Go code. Its output is the accepted
research plus the prompt set that spec 037's implementation plans consume.

**Why it comes first:** 037 §13.3 makes acceptance of this research a
precondition for writing any vendored skill. Merging two upstream treatments is
a design act — skipping it produces five skills nobody agreed to, and a prompt
is a far cheaper thing to review than a merged skill body.

**Architecture:** One research document per family in `docs/research/`, named
`motherlode-<skill>.md`. Each states what each upstream source contributes,
what the merged skill does, what is dropped and why, and ends with the
transformation prompt in the form 037 §2.2 specifies. A final task extracts
what every family shares into the two plugin-level prompts.

**Sources** (read from the local plugin cache, both pinned by 037):

- `~/.claude/plugins/cache/claude-plugins-official/superpowers/6.2.0/skills/`
- `~/.claude/plugins/cache/claude-plugins-official/mattpocock-skills/1.2.1/skills/`

## Global constraints

- **Read both bodies in full before proposing a merge.** A merge argued from
  skill descriptions rather than bodies is the failure this plan exists to
  prevent. Quote the specific passages a decision turns on.
- **A merge is neither concatenation nor winner-takes-all** (037 §3.2). For
  every behaviour that differs, say which source it comes from and why — "both
  are good, keep both" is a decision only when the two compose.
- **Name what is dropped.** An upstream behaviour left out is a decision, and
  an unrecorded one gets silently reintroduced at the next re-vendor.
- **Two customisation axes apply to every family** (037 §3.2): Worklode-aware
  (documents are addressable, `lode show` renders a section, tasks come from
  plan acceptance) and Sunstone Way-aware (the seven stages; a data-science
  brainstorm reaches for a Topic Intake Brief and a Gate). Load
  `sunstone-core:sunstone-way` before asserting anything about the second.
- **Prompts are instructions to an agent holding the upstream sources**, not
  prose about the merge. The test is regeneration: someone with the pinned
  upstream and the prompt should land on the same skill.
- **Every document ends in a crit review** (`crit docs/research/<file>.md`) and
  is not done until Stig approves it. This is the §13.3 gate; a task that
  writes the document but skips the review has not finished.
- Do not edit `plugins/claude/lode/skills/` in this plan. The prompts live in the
  research documents until 037's implementation plan installs them.

## Non-goals

- **Writing or vendoring any skill body.** That is 037's implementation plan,
  which this one unblocks.
- **`handoff` and `domain-modeling`** — vendored unchanged (037 §3.1), so there
  is nothing to research. The `domain-modeling` overlap with the
  writing-for-agents remix (037 §12.1) is settled in Task 4, not here.
- **The eval harness** (037 §12.3). Each document may state how its remix would
  be judged; building the judge is separate work.
- **Retrieval-based skill routing** (037 §11).

## Tasks

### Task 1 — Research the `assay` remix

```yaml
kind: design
priority: high
skills:
  - mattpocock-skills:research
  - sunstone-core:sunstone-way
blockedBy: [ ]
```

The design remix, and the deepest: `superpowers:brainstorming` +
`mattpocock:grilling` + `mattpocock:to-spec`, invoked as `/lode:assay`
(037 §3.2). It goes first because it is the one skill 037 specifies end-to-end
(§6), so its research has the most external constraint to satisfy — and because
the pattern it sets is what Tasks 2–5 follow.

Write `docs/research/motherlode-assay.md`. It must resolve at least:

- How grilling's breadth-first design tree sits inside brainstorming's phased
  lifecycle. 037 §3.2 asserts "the tree inside the lifecycle" — confirm that
  against both bodies or say why it is wrong.
- What survives of brainstorming's own questioning technique once the tree
  replaces its dialogue.
- Where `to-spec`'s document-shaping belongs, given 037 §6.3 writes a corpus
  file with frontmatter and `{#sec-N}` anchors.
- How the destination contract (037 §6.2) and crit-rendered rounds (037 §6.4)
  attach — both are 037's inventions, not upstream behaviour.
- What the Sunstone Way axis changes when the destination is a research topic
  rather than a spec.

- [ ] Read all three upstream bodies in full, plus 037 §6 end to end
- [ ] Draft the document, including the transformation prompt
- [ ] `crit docs/research/motherlode-assay.md` and address the round
- [ ] Not done until Stig approves

### Task 2 — Research the `planning` remix

```yaml
kind: design
priority: high
skills:
  - mattpocock-skills:research
blockedBy: [1]
```

`superpowers:writing-plans` + `mattpocock:to-tickets`, producing plans
(037 §3.2). Blocked by Task 1 only for the document pattern; the subject matter
is independent.

Write `docs/research/motherlode-planning.md`, resolving at least:

- How the merged skill emits this repo's plan contract — `covers:`
  frontmatter, one `## Tasks` section, `### Task N — <title>` subsections with
  the `kind`/`priority`/`skills`/`blockedBy` block (`docs/authoring-design-docs.md`).
- What `to-tickets` contributes over `writing-plans`' own decomposition, and
  which wins where they disagree.
- Where `splitting-specs-into-plans` (already in this repo) takes over for a
  plan series, and how the skill knows to defer to it.
- Why Design and Planning stay separate skills (037 §3.2) in terms the skill
  body itself states, so the boundary survives contact with a user who wants
  one invocation.
- What the skill must *not* do: mint tasks, hold execution state, or write
  anything the backbone owns.

- [ ] Read both upstream bodies and `docs/authoring-design-docs.md`
- [ ] Draft the document, including the transformation prompt
- [ ] `crit docs/research/motherlode-planning.md` and address the round
- [ ] Not done until Stig approves

### Task 3 — Research the `tdd` and `debugging` remixes

```yaml
kind: design
priority: medium
skills:
  - mattpocock-skills:research
blockedBy: [1]
```

`superpowers:test-driven-development` + `mattpocock:tdd`, and
`superpowers:systematic-debugging` + `mattpocock:diagnosing-bugs`. One task for
both: they are the two families with no document output and the thinnest
Worklode surface, and the same judgment applies twice.

Write `docs/research/motherlode-tdd.md` and
`docs/research/motherlode-debugging.md`, each resolving:

- Where the two loops genuinely differ, as opposed to differing in wording.
  This is the pair most likely to be a near-duplicate, and "these are the same
  skill, take the better body" is an acceptable finding — record it as one.
- What Worklode-awareness even means here. Both may be legitimately thin; say
  so rather than inventing integration.
- Whether this repo's own conventions belong in the body — Postgres-backed
  store tests that skip silently without a database, the `e2e` build tag, the
  race detector — or whether that is `CLAUDE.md`'s job.

- [ ] Read all four upstream bodies
- [ ] Draft both documents, each with its transformation prompt
- [ ] `crit` each and address the rounds
- [ ] Not done until Stig approves both

### Task 4 — Research the `writing-for-agents` remix, and settle the `domain-modeling` overlap

```yaml
kind: design
priority: medium
skills:
  - mattpocock-skills:research
blockedBy: [1]
```

`superpowers:writing-skills` + `mattpocock:writing-for-agents`. This task also
closes 037 §12.1: `domain-modeling` is kept unchanged (§3.1), is
model-invocable, and overlaps this remix, so both can fire on one trigger.

Write `docs/research/motherlode-writing-for-agents.md`, resolving at least:

- What each source is for. `writing-for-agents` is a reference on context
  pointers and instruction design; `writing-skills` is a workflow for building
  and verifying a skill. Whether that is one merged skill or a reference plus a
  workflow is the first question, and "do not merge these" is a valid answer
  that this plan's §3.2 pairing does not foreclose.
- How the remix relates to `sunstone-dev:progressive-discovery`, which already
  covers instruction-file structure for this org.
- **The `domain-modeling` boundary.** Either the remix absorbs it, or
  `domain-modeling` earns a stated scope the remix does not enter. Whichever
  way it goes, 037 §12.1 gets an answer and 037 §5.2's suppression list gets
  its final shape.

- [ ] Read both upstream bodies plus `domain-modeling` and
      `sunstone-dev:progressive-discovery`
- [ ] Draft the document, including the transformation prompt
- [ ] Record the `domain-modeling` ruling explicitly, with its consequence for
      the suppression list
- [ ] `crit docs/research/motherlode-writing-for-agents.md` and address the round
- [ ] Not done until Stig approves

### Task 5 — Extract the plugin-level prompts and reconcile the set

```yaml
kind: design
priority: high
skills:
  - mattpocock-skills:writing-for-agents
blockedBy: [1, 2, 3, 4]
```

037 §2.2 splits transformation prompts in two layers: a plugin-level prompt per
upstream source holding what applies to every skill from it, and a skill-level
prompt per skill. Tasks 1–4 produce skill-level prompts; the shared layer can
only be written once all five families are known, or it is guesswork.

Write `docs/research/motherlode-prompts.md` holding the two plugin-level
prompts — one for `superpowers`, one for `mattpocock-skills` — and the
reconciled skill-level set.

- Extract what repeats across the five: path rewrites off `docs/superpowers/`,
  the standing rule that task state belongs to `lode`, house style, the
  Sunstone Way framing. Anything stated three times belongs in the plugin
  layer.
- Reconcile contradictions between families. Five documents written separately
  will disagree about at least one shared convention; this is where that is
  found rather than at vendoring time.
- Verify each prompt is regenerable: given the pinned upstream and the prompt,
  an agent should land on the intended skill. Spot-check by having a subagent
  follow one prompt cold and comparing its output to the research document's
  description.
- State which `handoff`/`domain-modeling` files need no prompt at all, so the
  drift check's "vendored but unprompted" case is defined.

- [ ] Extract the two plugin-level prompts
- [ ] Reconcile contradictions across the five skill-level prompts
- [ ] Cold-run one prompt through a subagent as a regeneration check
- [ ] `crit docs/research/motherlode-prompts.md` and address the round
- [ ] Not done until Stig approves

### Task 6 — Fold the findings back into spec 037

```yaml
kind: design
priority: high
skills: [ ]
blockedBy: [5]
```

The research will contradict 037 in places — that is what research is for, and
037 §3.2's pairings are its most likely casualty. Leaving the spec stale means
the implementation plan is written against a document the research already
overtook.

- [ ] Update 037 §3.2 where the accepted research changed a pairing, a name,
      or a produced artifact
- [ ] Close 037 §12.1 (`domain-modeling`) and §12.2 (what each remix says) with
      the accepted answers, or restate what remains open
- [ ] Update 037 §5.2's suppression list if Task 4 changed it
- [ ] Re-run `./scripts/secfmt.py -l` and `./scripts/secmeta.py`, and
      `./scripts/secindex.py` if any heading changed
- [ ] Note in 037 §3.2 that the accepted research is the prompt set's source

## Verification

This plan is done when:

1. Five research documents exist in `docs/research/`, each carrying a
   transformation prompt, each approved by Stig in crit.
2. `docs/research/motherlode-prompts.md` holds both plugin-level prompts and a
   reconciled skill-level set, with no unresolved contradiction between
   families.
3. One prompt has been cold-run by an agent that did not write it, and the
   result matches its research document.
4. Spec 037 reflects the accepted findings, with §12.1 and §12.2 closed or
   restated, and its checks pass.
5. `plugins/claude/lode/skills/` is untouched — the prompts are ready to install, and
   installing them is 037's implementation plan.
