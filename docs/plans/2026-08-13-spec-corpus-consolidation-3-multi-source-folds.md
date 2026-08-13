---
status: draft
covers: NO-SPEC
---
# Spec corpus consolidation — part 3: multi-source folds

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Fold the four multi-source documents —
`001-identity-and-authentication.md` ← {001, 002, 023, 031},
`004-execution-backbone.md` ← {004, 010, 011, 018},
`008-worklode-plugin.md` ← {008, 024, 030}, and `012-agent-sessions.md` (a
near-1:1 fold sitting in this part for its cross-target 024 amendment, not its
size) — from `docs/specs/` into `docs/specs2/`. The contract is the part-1
plan, `docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`: its
**Rewrite rules** 1–11 and **The per-document task template** bind every task
below verbatim; nothing here amends them.

**`fold.yaml` is already authored for these four documents and is not to be
edited by any task.** The placement decisions — the cross-source merges, the
renumbering, the drops with their loss-naming reasons, the `allow_dropped_ids`
seeding — are planning-tier work done on this branch, and
`docs/specs2/mapping.yaml` is already regenerated from the merged part-3
entries, before any rewrite task runs. That sequencing is load-bearing: the
per-document grep can only repoint a hit naming 011 or 024 against a mapping
that already knows where those documents fold. A rewrite task that cannot
follow a rule stops and reports a `fold.yaml` defect rather than editing
`fold.yaml`.

Model per `MODEL_SELECTION.md`, deviating from the part-1 Series row ("sonnet
impl") for implementation: tasks 1–3 (001, 004, 008) are implemented at
`opus`, because each applies substantive merge rulings — voided amendment
notes, an authority ordering across sources that reverse each other, the
029 §9 discharge, two authorised verbatim carries — rather than a mechanical
transcription, so the Sonnet row's "fully specified, no open design
decisions" bar is not met. Task 4 (012) is `sonnet`. Per-document review and
the whole-branch review before merge stay `opus`, as part 1 already assigns
for the multi-source folds.

## Rulings from the planning merge

The part-2 hand-off and the four authoring passes raised escalations; the
planning merge settled them. These bind every task below, and part 4 inherits
rulings 1 and 2. Three further rulings are per-document and are folded into
the context of the task they bind: the 004 §1.2 carry (task 2),
`internal/tokencrypt` into 001's new §9.5 (task 1), and the ratification of
001's three under-scoped-supersession resolutions (task 1).

1. **Citation convention** (part 2 sent this to the planning tier; this is
   the answer). An absorbed amendment cites the amending spec **only when
   that spec folds into a different target document**. When the amender folds
   into this same document, the amendment dissolves at cutover and the
   citation is dropped. When the amender's material survives as another
   document, the citation stays — spelled as an ordinary old-corpus
   cross-reference (`§` prose form or filename) that `refmap.py` repoints at
   cutover; never a bare number, and never hand-repointed to new-corpus
   numbering (ruling 5). In this part: absorptions from 002/023/031 (task 1),
   from 010/011/018 (task 2) and from 030 (task 3) dissolve uncited; the
   014, 012, 030, 025 and 024 citations in tasks 2–4 stay for cutover.
2. **A source document belongs to exactly one target.** `mapping.yaml`
   derives one `documents:` row per (source, target) pair, so a source under
   two targets would make every whole-document and `WL-SPEC-N` reference to
   it ambiguous. Hence 024 and 030 fold whole into 008 even though they amend
   012 and 011: their amendment *content* is absorbed by 012's and 004's
   rewriters from the inline notes (see **Cross-target obligations**), while
   every 024 and 030 anchor lives in folded 008.
3. **Frontmatter stays `status` and `requires`.** That is all the scaffold
   emits, and no task adds `replaces:` or `wasDerivedFrom:` — every document
   this fold retires stops existing at cutover, so a supersession edge naming
   one would be repointed into nonsense. This settles the open question of
   restoring `replaces: 003-platform-graph-design.md` on folded 004: it is
   not restored; `mapping.yaml` is the record.
4. **Rule 4 has two axes** (part 2's finding, ratified). Resolve
   document-status qualifiers — "014 is draft", "lands when 014 does" —
   because drafts count as current in this fold. Leave implementation-status
   qualifiers alone — "until 013 ships", "today it half does" — because
   whether code has shipped is untouched by consolidating the corpus. Where
   one phrase glues both, drop only the document-status half.
5. **Hand-repointing is only ever done for references `refmap.py` cannot
   see**, which is exactly what the per-document bare-number grep selects
   for. A reference carrying a `§`, a filename or a `WL-SPEC-N` is
   `refmap.py`'s at cutover — repointing it now double-maps it (part 2's
   reverted 029 fix is the precedent). Letter-suffix references
   (`019 §4.3a`-shaped) are invisible to both and are reported, not fixed.

## Cross-target obligations

Three seams where an amendment's content and its anchors land in different
documents; ruling 2 is why they exist.

- **024 §5 → task 4 (012).** 024 §5 amends 012 §1: the `agent_sessions.agent`
  CHECK gains `copilot`. Folded 012 states the post-amendment CHECK directly.
  The anchor `024-multi-harness-integration.md#sec-5` itself lands in folded
  008's merged §19 (Dependencies), whose rewriter states the dependency as
  the post-amendment fact — "the CHECK includes `copilot`" — not as an act of
  amending a document that no longer exists separately.
- **030's amendments to 011 §2/§4/§6 → task 2 (004).** 011 folds into 004, so
  004's rewriter absorbs the three amendments — branch names come from
  `LODE_BRANCH_TEMPLATE`; the server remains the sole authority; the
  fixed-prefix rule is retired — wherever those sections landed (§5.2, §5.4,
  §2.5). Every 030 anchor lives in folded 008.
- **004 and 008 must agree on the branch rule.** Both documents state it —
  004 via absorbed 011/030 material, 008 via 030's own sections (§3–§8). 008
  is authoritative on the template grammar; 004 states the rule
  (`LODE_BRANCH_TEMPLATE`, default `{{ .id }}-{{ .slug }}`,
  server-authoritative, bare-template CLI fallback) and cross-references
  rather than restating the grammar. Merge review reads the two side by side.

## Residuals for part 5

Known now and parked deliberately; distinct from the ones part 2's hand-off
already records.

- **031 §2.3 states a stale infrastructure fact, kept verbatim.** It
  justifies the in-memory one-time code store with "the server is
  single-instance (one PVC + litestream)"; the backbone is Postgres today.
  Rule 6 keeps the sentence; fixing it is a future spec amendment, not a
  rewrite or a cutover edit.
- **The `ls:`→`wl:` respell diverges between folded 008 and folded 016.**
  Folded 008 enacts 014 §1's prefix rename (two `allow_dropped_ids` entries
  authorise it); folded 016 shipped keeping `ls:Skill`/`ls:recommendsSkill`
  because its planning pass never ruled. Deliberate and cheap to complete:
  part 5 owes 016 the matching entries and respell.
- **024 §3.1's harness event map has no worktree-exit event**, while 008's
  release step, hook table, External row and acceptance criterion 2 all rely
  on `ExitWorktree`. Not an outright conflict — 024's map simply never covers
  exit — so the fold preserves both as stated and folded 008 visibly carries
  the mismatch. Reconciling it is a spec amendment; the rewriter must not
  invent an event-map row.
- **Folded 008's computed `requires:` lists both 004 and 011.** Cutover's
  `refmap.py` repoints 011→004, leaving 004 listed twice; part 5 dedups
  `requires:` after the rewrite pass.
- **Folded 001 carries one un-superseded contradiction of the login end
  state** — the one place it is not stated exactly once. 023 (2026-07-31)
  postdates 031 (2026-07-20) and its §3 removes GitHub web login and
  `/auth/choose`, yet 031 §8/§8.1/§8.2 — folded 001's §8/§8.1/§8.2 — still
  describe the chooser "when both providers are on", `s.gh == nil`,
  `githubCallback` and `providers: ["github"]`. No supersession edge covers
  it, so it is transcribed verbatim: de-GitHub'ing those sections would drop
  normative claims and identifiers the fold has no authority to remove —
  the contradiction is the corpus's, not the fold's. Resolving it needs a
  real amendment to the auth design by a human, and part 5 must not let it
  slip past the `git rm -r docs/specs`.

## Global Constraints

Inherited from part 1, restated so a task reading only this plan misses none:

- `mapping.yaml` is generated from `fold.yaml` alone. Never hand-edit it, and
  never derive it by reading `docs/specs2/*.md`. It is already regenerated
  for part 3; no task runs `fold.py --mapping`.
- `fold.py --scaffold` refuses to overwrite an existing `docs/specs2/*.md`.
  Regenerating a document after the rewrite has started is a data-loss bug.
- Every anchor in the twelve sources' `--with-drafts` view (plus 018's
  preamble) appears exactly once across `from:` and `dropped:` — already
  satisfied by `fold.yaml`; `--check --partial` must stay clean after every
  task.
- Do not renumber by hand. `new:` numbers come from `fold.yaml`; `secfmt.py`
  owns the `{#sec-N}` anchors in the written files.
- Introduce no new Python dependency beyond PyYAML.
- Part 3 creates the four `docs/specs2/` documents and nothing else. It does
  not modify `docs/specs/`, `scripts/fold.py` or `scripts/refmap.py`, and it
  repoints no references outside `docs/specs2/` — the corpus-wide reference
  rewrite is part 5.
- `docs/plans/` gets its spec references repointed at cutover (part 5) and
  nothing else.

## Tasks

Each task is the part-1 plan's frozen per-document task template with the
document substituted, preceded by a context recording what `fold.yaml`
decided and the rulings the rewrite applies. Two template notes hold for all
four: the pre-commit hooks cover `docs/specs2/` for `secfmt.py`/`secmeta.py`
but nothing runs `secindex.py` for you, so run the steps and see them clean
before reporting; and `--check` reporting `undeclared` on a heading you added
is rewrite rule 10 working — delete the heading or escalate, never extend
`fold.yaml`. The tasks are independent.

### Task 1 — Fold `001-identity-and-authentication.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/001-identity-and-authentication.md`.

**Context:** 44 live anchors across four sources into 25 sections (14
top-level, 11 subsections); everything renumbers, so every internal pointer
goes through `mapping.yaml`. The same login design is told three times at
three stages — 001 (Keycloak SSO), 002 (GitHub as a second provider), 023
(Keycloak sole login, GitHub demoted to link-only) — plus 031
(provider-neutral CLI login); the folded document states the end state once.
Merges: 001 §0 + 023 §0 → §0; 002 §1 + 023 §1 → §1; 001 §3.1 + 031 §6 → §7
(the dormant direct token exchange); 031 §§0–2 → §8 with 031's subsections as
§8.1–8.6; the two empty "Design" containers (002 §3, 023 §3) → the §9
container heading; 002 §3.1 + 002 §3.6 + 023 §3.6 → §9.1; 002 §3.5 +
023 §3.5 → §9.5; four-way Testing → §11; three-way Non-goals → §12; two-way
Open questions → §13. Seven drops: four superseded anchors (001 §4,
002 §3.2/3.3/3.4) and three live-but-spent narratives (002 §0, 002 §2,
023 §2) — each `dropped:` reason names what is lost. No source carries a
Dependencies or Acceptance criteria section, so the folded document has
neither — a `fold.yaml` decision, not a loss to report. Rule 3 has one
ordinary absorption: 002 §3.5's "Amended by spec 023" note, whose amending
text (023 §3.5) sits in the same new §9.5 — no citation kept (ruling 1:
every amender here folds into this same document). 002's three amendment
notes on 001 — §0's front-door note, §1's Authorization coda, §3.2's
`/auth/choose` note — are **void**: 023 §3.1 reversed 002's login model
without retracting them, so the post-amendment rule is 001's original text,
stated as-is. `allow_dropped_ids` pre-seeds `http://localhost:8000` and
`http://localhost:18000` — deliberate losses (dead fixed-port CLI
redirect-URI client config under 031's ephemeral-port loopback), not span
artifacts. The sources' one span-artifact site (023 §3.2's nested
struct-tag markup) yields real identifiers — `json:"github_username"`,
`expected_github_login` — that must survive; nothing is seeded for them, and
rule 9 applies doubly: keep the struct tag whole on one line. `fold.yaml`
was amended once during execution, by the planning tier, not by a task:
folding 001 needed two more `allow_dropped_ids` entries —
`docs/plans/2026-07-20-provider-neutral-cli-login-design.md` and
`docs/authoring-design-docs.md`, both cited only in 031 §0's promotion
blockquote, which the fold deletes (the folded document has four sources
and was promoted from nothing). The rewriter reported it as a `fold.yaml`
defect and did not edit the file — exactly the behaviour the template
demands, and the plan keeps demanding it. Provenance
block: 001 is doc-wide `amendedBy` 031, per-section `amendedBy` from 002,
`isReplacedBy` §4 → 031; 002 `amends` 001 doc-wide and per-section, is
doc-wide `amendedBy` 031, `isReplacedBy` §3.2–3.4 → 023 §3.1; 023 `replaces`
002 §3.2–3.4 and `amends` 002 §3.5; 031 `amends` 001 and 002, `replaces`
001 §4, 002 §3.3 and a plan. Every edge is internal to the fold, so folded
frontmatter is `status: draft` with no `requires:`. The authority order the
rulings encode: 031 over 001/002 for anything CLI-login; 023 over 002 for
GitHub's role; 001's original text over 002's voided amendments. No 029 §9
obligation lands here, and no source mentions `epic`.

Rulings the rewrite applies (fold decisions, not rewriter calls; the
planning merge ratified the three substantive under-scoped-supersession
resolutions — the device-flow deletion, the permission narrowing to
023 §3.6's ceiling, and the fixed-port redirect-URI drop):

- **§0/§2/§6:** state the post-023/031 rule once. Keycloak is again the sole
  front door minting `wl_` tokens; §2's Decisions-table CLI-login row
  restates 031's server-mediated flow and its Authorization row (Keycloak
  client roles) is current; §6 states the single-provider session flow —
  the `/auth/choose` chooser is gone.
- **§4:** the fixed CLI-callback redirect URIs go — 031 §1 makes the
  loopback URI pre-registration-free. The web callback URIs in the same
  list stay.
- **§7:** 031 §6's later statement governs the framing: `POST
  /auth/oidc/token` is a kept, dormant direct contract, not the CLI flow;
  001 §3.1's mint mechanics remain normative. 031 §6's Add/Remove ledger
  lines duplicate §8.1/§8.4/§8.5 and reduce to their §7-relevant residue
  (the `/auth/oidc/*` endpoints stay, dormant, and why).
- **§8:** 031 §0's promotion blockquote ("Promoted from `docs/plans/…`") is
  old-corpus document mechanics with nothing to say in the folded corpus; it
  goes, like the scaffold's provenance markers.
- **§9.1:** 023 governs throughout — the permission ceiling is 023 §3.6's
  read-mostly set (never `contents: write`); Organization → Members: read is
  not needed (023 §3.1); 002 §3.1's device-flow line goes (no device flow
  exists and GitHub is not a CLI provider); the merged env table drops
  `LODE_GITHUB_ORG` / `LODE_GITHUB_ADMIN_TEAM` per 023 §3.6's config-after
  list. The App-per-environment split and the hzdev callback/webhook URLs
  remain current.
- **§9.5:** 023 governs — the token row *is* the link. 002 §3.5's
  `github:<id>` actor-key paragraph is retired by 023 §3.1's actor merge;
  what survives of 002 §3.5 is the AES-GCM/`LODE_TOKEN_ENC_KEY` sealing, the
  dedicated-table rationale, and lazy refresh, which 023 §3.5 restates more
  precisely. **Merge ruling: name `internal/tokencrypt` (the sealing
  package) in this section** — 023 §2's `spent` drop would otherwise take
  the corpus's only mention of a live Go package with it. This is an
  authorised planning-tier placement like the 004 §1.2 carry, not a
  precedent a rewriter may set on its own; `internal/githubauth.Roles` and
  002 §2's code-map pointers stay accepted losses per the `dropped:`
  reasons.
- **§11/§12:** where a 001/002 bullet tests a retired mechanism, the later
  source's statement governs — 031 §7's CLI tests over 001 §5's
  stubbed-Keycloak bullet; 023 §6 over 002 §6's device-flow and
  GitHub-login-handler bullets; 002 §6's `internal/githubauth` unit bullet
  survives for authorize-URL construction and code exchange, minus
  membership → role mapping. 001 §6's "logout" item survives only as
  web-session logout (`lode logout` exists, §8.5); 002 §4's
  actor-migration non-goal is spent (023 §3.1's merge performed the
  reconciliation); its outbound-features item merges with 023 §4's first
  item as a restatement.
- **Internal pointers invisible to `refmap.py` and to the grep, repointed by
  hand:** 023 §1 "(§3.1)" → §3 and "(§F)" → §9.1; 023 §3.1 "(§C)" → §9.3 and
  "unchanged from spec 001" → an internal pointer (001 is this document);
  023 §3.3 "(§E)" twice → §9.5; 023 §4 "as in spec 002" → internal (§9.1);
  031 §3 "§1" → §8 and "(§6)" → §7; 002 §7 "(Section A)" → §9.1. The
  bare-number grep will also hit "spec 011" (023 §1) and "spec 008"
  (023 §1/§3.6): the 011 hit repoints to the 004 fold per `mapping.yaml`;
  008 keeps its number, so those hits stand. No surviving section points
  into `dropped:` material, so rule 5's escalation shape does not occur
  here. 031 §2.3's stale "one PVC + litestream" parenthetical stays verbatim
  (rule 6; recorded under **Residuals for part 5**).

- [ ] `./scripts/fold.py --scaffold --only 001-identity-and-authentication.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/001-identity-and-authentication.md` — repoint each hit by
      hand against `mapping.yaml` (rewrite rule 5's manual half). `refmap.py`
      cannot see a bare number, so these are the references nothing else will
      catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 2 — Fold `004-execution-backbone.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/004-execution-backbone.md`.

**Context:** 46 live anchors plus `018-task-hierarchy.md#preamble` land in 40
sections; one retired anchor (004 §1.2, the pre-delivery state machine 011
replaced) is `dropped:`. Renumbering is pervasive — every 010/011/018
section and 004's §1.3–1.5 move — so the bare-number grep matters more than
in part 2: hits naming 010, 011 or 018 are now *internal* references and
repoint to sections of this document via `mapping.yaml`; hits naming
survivors (005, 006, 007, 008, 012) stay for cutover. Two cross-source
merges: 010 §5 + 011 §6 become §2.5 — both govern the
task-key/branch-pattern regex family, and 011 §6's inline 030 notes state
the current branch rule, so rule 3 settles 010 §5's stale `wl/<id>` claims;
state the post-030 rule once, and keep one clause naming the retired
spellings `LODE_BRANCH_PREFIX`, `lode/` and `wl/` so rule 7 and
`--check --ids` stay satisfied document-wide — and 018 §3 (a bodyless
container heading) merges into §6.3 with 018 §3.1. 018's preamble is placed
(opening §6), not dropped: it is the corpus's only statement of the
post-025 hierarchy rule and the rewriter absorbs it under rule 3. Inline
amendment notes to absorb under rule 3: 004 §1.1 (two — 010 task identity,
014 §8 kind enum), 004 §2 (012: closing a lease stamps `ended_at` on open
`agent_sessions` rows), 004 §4 (011: `projects.deploy_gated` dropped),
004 §5 (011: server-derived `branch`; state the post-030 fallback, bare
`<id>-<slug>`), 010 §2 (014 §11.3: `SPEC`/`ADR` reserved as project keys),
011 §1 (the "Corrected against spec 004 §2" self-correction — absorb it;
every claim in it survives, including "delivery transitions never close the
lease" and the undecided `lode task done` sentence), 011 §2 and §4 (030:
`LODE_BRANCH_TEMPLATE`, `wl/` no longer recognized, bare-template
fallback — the cross-target absorption recorded above), 011 §6 (014 §11.3
and 030), 018 preamble (025 doc-wide). Citations per ruling 1: notes from
010/011/018 dissolve uncited (same target); the 014, 012, 030 and 025
citations stay in old-corpus, refmap-visible form for cutover to repoint.
`allow_dropped_ids` seeds 17 entries: the 14 source-side span artifacts
(8 from 004, 4 from 011, 2 from 018; 010 has none) plus three real,
accepted losses — the pre-025/029 kind-enum span
`feature, bug, chore, spec, epic, review, spike` and the pre-011 reopen
spellings `done|abandoned → ready` (004 §5) and `done→ready` (004 §8).
Provenance block: 010 and 018 amend 004 doc-wide; 018 amends 011 (folded
§6.4 states that `ResolveDelivery` leaves a task with children alone; §5
does not repeat it) and 005 (absorbed in part 1, not ours); 025 §6 amends
018 doc-wide; 011 replaces 004 §1.2; 004 replaces/wasDerivedFrom 003. All
are internalized by the rewrite or out of scope, and frontmatter stays
`status` + `requires` (ruling 3: `replaces: 003` is not restored).

Rulings the rewrite applies (fold decisions, not rewriter calls):

- **004 §1.2's over-scoped supersession is repaired in the fold, not
  deferred (merge ruling).** 011 §1 replaced the section wholesale but only
  extends the machine past `merged`; two claims nothing else states — the
  base table's trigger annotations (publish, submit for review, rework,
  release/expiry) and the full `Transition(tx, now, taskID, from, to,
  eventID)` contract sentence (atomic from-state check inside the caller's
  tx, `updated_at` bump, `state_log` row attributed to the event, unknown
  task `ErrNotFound`, wrong from-state `ErrBadTransition`) — are carried
  into the new §1.1 **verbatim**, notwithstanding rewrite rule 6's "never
  add a normative claim". This is a planning-tier placement decision, made
  here because part 5's `git rm -r docs/specs` is the last moment the
  material could be recovered; it is an authorised exception, not a
  precedent a rewriter may take on its own. The `dropped:` reason's
  "resolve before part 5" clause is thereby discharged.
- **§1.1's tasks DDL states the current schema:** the `state` CHECK becomes
  `('draft','ready','in_progress','in_review','merged','deployed_dev',
  'deployed_prod','released','abandoned')` (011 §1/§5); the `kind` CHECK
  becomes `('feature','bug','chore','design','review','spike')` (025 §6,
  029 §2); the `id` column comment becomes `<KEY>-<n>`. State the retired
  identity as retired — ids were `WT-<n>` from the global `task_seq`
  counter, which is dropped; per-project keys are §2 — so `WT-<n>`, `WT-`
  and `task_seq` survive as text.
- **The 029 §9 discharge.** The obligation recorded in the part-2 plan's
  "Obligations from 029 sec-9" section lands here, and this context is its
  only carrier — 029 appears in no source's frontmatter or body, so neither
  the scaffold's provenance block nor rule 3 will surface it. §6's intro
  (018 preamble + §0) states the post-025/029 shape: no `kind = 'epic'`;
  every mechanism — ready-set exclusion, restricted state machine, roll-up,
  depth cap, single parent, brief — applies to *a task that has children*;
  `decompose` creates parent-hood and children in one transaction and no
  longer converts the parent's kind; the container role 018 built the epic
  for is carried by project and milestone (029 §2). §6.1's Decisions table
  is recast: container identity follows from having children (the
  preamble's "exactly as sharp as a column" sentence is the rationale); the
  parent rule is 029's — `checkHierarchy` accepts an ordinary task as
  parent; the claimable/delivery-states/direct-claim rows are restated for
  a task with children; retired identifiers (`kind = 'epic'`, `lode task
  add --kind epic`) survive inside explicit retirement clauses. §6.3 states
  the ready-set exclusion as a has-children predicate, with the retired
  `AND t.kind <> 'epic'` SQL named as the pre-029 form. §6.10 drops "set
  `kind = 'epic'`" from the transaction description; everything else stands
  as 018 §8 left it. The depth cap states 029 §2's sentence — the cap of 2
  edges now spans task → subtask only and stops binding in practice.
- **025's "the only way a task acquires children" vs the surviving adoption
  surfaces:** keep §6.7/§6.8's surfaces (`POST /api/v1/tasks` with
  `parent`, `lode task parent/unparent`, `--parent` on create) with the
  epic-parent validation replaced by the ordinary-task parent rule, reading
  "only way" as how parent-hood *begins* — it retires `--kind epic`
  creation and in-place conversion, not child management. The whole-branch
  review confirms or overturns; overturning is a `fold.yaml`-level change
  (drop or reshape §6.7/§6.8), not a rewrite tweak.
- **§1.3 (`task_edges`)** rewords "makes B an epic over A" to parent-task
  terms (a `child_of` edge makes B the parent of A; §6 governs semantics).
- **Resolved-question records stay verbatim as historical dispositions**
  (004 §7 Q1's `done → ready`, 018's Q018.1/Q018.2 in §12); live rules live
  in §5.1 and §6. Only the live statements — 004 §5's reopen path and
  004 §8's criterion 6 — are restated post-011 (`merged`, reopen from
  delivery states).
- **§9 Testing** consolidates three disjoint lists (keys, delivery,
  hierarchy) by subsystem, merging nothing that is not a restatement;
  018 §9's epic-worded tests are restated for a task with children. §13's
  criteria come from 004 alone; criteria touching shipped history
  (`session_id` removal etc.) stay per ruling 4's implementation axis.
- **Known deliberate duplication:** the server-derived `branch` claim
  appears in both §5.4 (from 011 §4) and §8 (from 004 §5's absorbed note);
  rule 2 cannot de-duplicate across sections and both state the same
  post-030 rule — reviewers should not flag it. Per **Cross-target
  obligations**, the branch rule must agree with folded 008's statement,
  with 008 authoritative on the template grammar and this document
  cross-referencing rather than restating it.

- [ ] `./scripts/fold.py --scaffold --only 004-execution-backbone.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/004-execution-backbone.md` — repoint each hit by hand
      against `mapping.yaml` (rewrite rule 5's manual half). `refmap.py`
      cannot see a bare number, so these are the references nothing else will
      catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 3 — Fold `008-worklode-plugin.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/008-worklode-plugin.md`.

**Context:** 47 live anchors (008: 14, 024: 20, 030: 13) into 41 sections —
the largest part-3 fold. Merges: the three Purpose & scope sections (008 §0,
024 §1, 030 §0) become §0, where rule 2 settles the harness axis; the
sources' "out of scope" lists there name material this document now covers
(030's lease model and worktree→lease binding; 024's lease lifecycle, `lode
task brief`, the guard invariant) — those are self-scoping sentences to be
rewritten to scope the folded document, not claims to preserve. 008 §1 + §2
merge into §1 (design lens & CLI naming); the scaffolding trio folds
008+024 into §19–§21; 030 has no trio and its Testing section survives as
§8, renamed to say it covers naming and the guard. Everything else is 1:1
with renumbering: 030's naming cluster becomes §3–§8, deliberately placed
between the lease lifecycle (§2) and Hooks (§9) so the path guard is
defined before Hooks invokes it; 008 §4–§10 become §9–§15; 024 §2/§3/§4/§8
become §16/§17/§18/§22. One drop: 008's `#preamble` — 014 §1's
whole-document `ls:`→`wl:` amendment as a standing rewrite instruction —
absorbed and *enacted*: write `wl:governs`/`wl:affects` directly wherever
008 wrote the `ls:` forms; the `ls:governs`/`ls:affects`
`allow_dropped_ids` entries authorise the span drops; keep no reading
instruction and no "formerly `ls:`". Ten inline amendment notes to absorb
under rule 3, all in 008: §3 (030), §4 (030 §3.2), §6 (014 §3 + 030), §7
(030 at top **and** 014 mid-section after the table), §8 (014 §2), §12
(030), §13 (030 at top **and** 014 mid-section after criterion 5) — the two
mid-section notes are easy to miss. Citations per ruling 1: the 030 notes
dissolve uncited (030 folds into this document); the four 014 citations
stay in old-corpus, refmap-visible form (014's material survives in part-4
targets). Absorbing 030 also means writing the post-030 path
(`<worktree_dir>/<branch>`, default `.worktrees/<id>-<slug>`) everywhere
`wt/<id>-<slug>` appeared — **including 024's Q024.5 and acceptance
criterion 3, which predate 030 and carry no note**. 030's `amends` list
omits 024 (source rot); the merge ratified the structural settlement: both
anchors land in merged sections whose 008 half carries an absorbed 030
note, rule 2 governs the seam, and the `wt/<id>-<slug>` entry authorises
the expected span drop — a reviewer should not flag the rewrite as
unlicensed, and a surviving historical mention in the cutover section
(§7) is fine. The fourth seeded entry is `", which "` — 030 §3.2's
source-side artifact from the wrapped span `git config --worktree --get
worklode.task-id`; keep that span on one line in the rewrite (rule 9). The
bare-number grep will hit many self-references ("spec 030", "(024)", "008
Q008.2"): cross-references among 008/024/030 are now intra-document, so
hits *without* a `§` are hand-repointed into internal section references
per `mapping.yaml`, while hits carrying a `§` (e.g. "spec 030 §3.2") are
`refmap.py`'s at cutover and are left alone (ruling 5). Provenance block:
008 is doc-wide `amendedBy` 014 §1 (enacted by the respell above) and
`amends`/`replaces`/`wasDerivedFrom` 003 (003 is retired by the fold;
ruling 3 — the claims vanish and `mapping.yaml` is the record); 024
`amends` 012 §1 and 030 `amends` three 011 sections (both cross-target —
see **Cross-target obligations**; every 024 and 030 anchor still lives
here); 030's six 008 amendments are internal and dissolve by absorption.
Computed `requires:` is [004, 005, 012, 016, 022, 011] — the 011→004 dup at
cutover is a recorded residual — and folded 008 stays `status: draft` at
cutover because 030 is draft (part-1 rule). 024 §3.1's missing
worktree-exit event vs `ExitWorktree` is preserved as stated (recorded
under **Residuals for part 5**); the rewriter must not invent an event-map
row.

- [ ] `./scripts/fold.py --scaffold --only 008-worklode-plugin.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/008-worklode-plugin.md` — repoint each hit by hand against
      `mapping.yaml` (rewrite rule 5's manual half). `refmap.py` cannot see
      a bare number, so these are the references nothing else will catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 4 — Fold `012-agent-sessions.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/012-agent-sessions.md`.

**Context:** 8 live anchors, 1:1, numbers and headings unchanged, no drops,
no preamble, and no scaffolding trio to consolidate — 012 never had
Dependencies, Open questions or Acceptance criteria sections, and the
keep-the-trio rule is keep-if-present. One rule-3 absorption: §1's "Amended
by spec 024 §5" blockquote is deleted and the section states the
post-amendment rule directly in both spellings — in the `CREATE TABLE
agent_sessions` block the `agent` CHECK becomes
`('claude-code','codex','copilot','cursor','aider','opencode','pi','amp','other')`,
`copilot` inserted between `codex` and `cursor` and nothing else changed;
and the body prose already saying the CHECK "mirrors the existing style of
`events.source` and `actors.kind`; adding a tool is a one-line migration"
stays verbatim, because the amendment exercised exactly the reservation it
describes. Per ruling 1 the absorption keeps one citation: 024 folds into a
*different* target (008), so the amended text retains a refmap-visible
pointer in old-corpus form — the "spec 024 §5" spelling the note already
uses is fine — for cutover to repoint into folded 008; never a bare retired
number, and never a hand-repoint to new-corpus numbering (ruling 5). Two
look-alikes in the same section are not amendment notes: the bold
"Superseded by migration `0008_session_cost`" paragraph is the spec's own
statement of where cost detail now lives — substance, kept under rule 6,
not a corpus supersession — and `0008_session_cost`/migration-number tokens
are migration identifiers, not spec references. Rule 4's qualifiers here
are all implementation-status and stay verbatim ("it is always empty
today", "Nothing computes them in this cut", §7's struck-through "Done"
item and its session-marker-pid bug note). `allow_dropped_ids` pre-seeds
two entries for §3's source-side artifact (the wrapped
`(source, external_id)` span); they cover the source side only — keep every
span on one line in the rewrite (rule 9). Provenance block: 012 `amends`
004 §2 as a whole document — informational here; absorbing that claim
belongs to the 004 fold (task 2), whose 004 §2 carries the inline note —
and `amendedBy` §1 ← 024 §5, handled above. Expect the post-rewrite
bare-number grep clean (the note was its only hit); §4's
`.claude/worktrees/` is a path, not a reference. One `--check --ids`
lesson from this fold, which part 4 inherits: the check matches exact
substrings, so a source that wraps a `CHECK (... IN (...))` tuple across a
line reads as a dropped identifier even when every token survives inside
the fence. The fix is reflowing the tuple onto one unbroken line, not an
`allow_dropped_ids` entry — rewrite rule 9's failure mode arriving from
the source side rather than the rewriter's.

- [ ] `./scripts/fold.py --scaffold --only 012-agent-sessions.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/012-agent-sessions.md` — repoint each hit by hand against
      `mapping.yaml` (rewrite rule 5's manual half). `refmap.py` cannot see
      a bare number, so these are the references nothing else will catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.
