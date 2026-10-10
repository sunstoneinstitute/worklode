# Specs, rules and plans: the document model

Deep reference for the `worklode` skill. Documents live *in the backbone*
(WL-SPEC-77). Design work starts from rules (WL-REQ-1300): find them
with `lode search` or `lode rule list`, change them with `lode rule`, and touch
a spec only to change its arrangement. Use `lode doc` for plans and a new
spec's first draft. Scratch files are editor buffers.

## Kinds and lifecycle

A **rule** is a design rule with its own identity, kind, heading, body,
status and version history. A **spec** arranges rules into a readable
document. Each placement records the rule and version, position, depth and
section anchor. A rule ref names the rule; a ref such as `WL-SPEC-77#sec-11`
names a place in that spec's arrangement.

A rule's kind is one of five, and sets the infix its ref prints with:

- **requirement** (`WL-REQ-12`): a plan builds it once. A planning gap until
  an accepted plan covers it. New rules are requirements.
- **catalogue** (`WL-REQ-12`): a set of entries with one shape, such as a
  command tree or error table. A plan builds its entries. Same planning
  status as a requirement.
- **invariant** (`WL-RULE-12`): holds in every state and binds every task in
  its project. Never covered, never a gap.
- **definition** (`WL-RULE-12`): one term, one meaning, in one scope. Never
  covered, never a gap.
- **principle** (`WL-RULE-12`): a stance other rules `refine`. Never covered,
  never a gap.

The number alone identifies a rule: `WL-REQ-12`, `WL-RULE-12` and the old
`WL-CL-12` resolve to the same rule wherever a ref is read. Change a kind
with `lode rule set <ref> --kind <kind>`. A rule
that both builds something and binds later work holds two obligations:
split it into a requirement and an invariant.

- **Spec** — a standing description, revised as the design changes. Each
  anchored section becomes a rule; content under deeper, unanchored headings
  belongs to the nearest anchored rule. Acceptance accepts its draft rules.
- **Plan** — an executable document governed by the rules its `covers`
  entries reach. Its own prose creates no rules. Accepting its `## Tasks`
  declarations mints tasks governed by those same rules. No
  container task is minted. Re-acceptance mints only declarations not already
  represented by a task; keep declaration titles stable.
- **Existing ADRs** — still readable and arrange rules like specs. Put new
  durable rationale in the spec owning the subject.

Specs use `draft`, `accepted` and `superseded`; plans can also be `stale`.
A submitted spec stays `draft` while review runs. Rules have their own status, including
`withdrawn`. A rule's text can change while it is draft; changing accepted
text creates a new draft version, preserving the accepted version.

## Reading and changing rules

```bash
lode search <query>                          # rank docs, tasks and skills; a spec hit names a section
lode rule list --doc <spec-ref>              # spec arrangement; --doc <plan-ref> lists governing rules
lode show <rule-ref> --json                  # text, arrangements, governed tasks and edges
lode rule versions <rule-ref>
lode show <rule-ref> --version <n>
lode rule add --heading <text> --file <body> [--kind <kind>] [--tag <t>]  # new draft rule in no spec
lode rule edit <rule-ref> --file <body-file> # text under the heading; optional --heading
lode rule arrange <spec> <rule-ref>          # --after or --under <ref|sec-N>, --anchor sec-N
lode rule unarrange <spec> <rule-ref>        # out of the spec, not withdrawn
```

A rule exists without a spec: plans cover it, tasks are governed by it, and
any number of specs may arrange it. Arranging writes the spec in place when
it is a draft, and into its candidate revision when it is accepted.

A spec write through `lode doc` also writes rules. `lode doc show <ref>
--editable` prints each anchored heading with its rule ref (`{#sec-3
rule=WL-REQ-12}`); written back, that heading arranges the named rule and
changed text under it is a rule edit. A heading with no `rule=` matches the
spec's own rules by anchor and heading, then heading alone, then anchor alone,
and an unmatched one creates a rule. A rule the write leaves out is
unarranged, not withdrawn.

A rule edit writes only the rule (WL-REQ-1298): it rewrites the draft
version, or adds the next version as a draft when the newest is accepted. It
works for a rule arranged in no spec or in several. A spec shows each rule at
its newest accepted version and marks a pending draft. `lode rule accept
<rule-ref>` (owner only; `--substantive` for a judged change) accepts the
draft and bumps every accepted spec arranging it.

## Rules govern plans and their tasks

A `covers` entry is a plain reference: a requirement ref (`WL-REQ-<n>`), a
document/section reference such as `WL-SPEC-77#sec-11`, or a whole-document
reference. A requirement ref resolves to that rule; a section reference
reaches the requirements at that anchor and under it; a whole-document
reference reaches all its requirements. Invariants, definitions and principles in
scope are skipped, and a direct ref to one is refused. Unresolved refs
reach none. Each entry is resolved
when the plan is written and stored as a `covers` edge from the plan to the
rule; the governing rules are re-resolved on each plan write and again at
acceptance. A `covers` edge always means the plan builds the whole rule:
there are no coverage levels. `coverage:` and `fullCoverageWith:` are refused
on write.

A rule the plan must obey but builds nothing in is an invariant, and governs
every task in its project without a `covers` entry once accepted; a rule with nothing to build
(a definition or principle) carries no `covers` entry either. A
plan that builds only part of a rule is a sign the rule should split
(`lode rule link WL-REQ-B --derived-from WL-REQ-A`) so each plan covers
whole rules.

Tasks minted from a plan receive `governedBy` links to its governing rules.
For work filed outside a plan, name the governing rules explicitly:

```bash
lode task add --title "..." --kind feature --governed-by <rule-ref>
lode task govern <task-id> --by <rule-ref>
lode task govern <task-id> --by <rule-ref> --pin
lode task ungovern <task-id> --by <rule-ref>
lode show <task-id> --json                   # inspect governed_by
```

Repeat `--governed-by` for several rules. Links follow the newest rule text by
default. `--pin` keeps the version current when the link is made; governing
again without `--pin` restores following. The link-time version remains
visible so readers can see that the text has moved on. A task's `plan_doc`
says where it came from; `governed_by` says which requirements it follows.
Re-accepting a changed plan leaves existing tasks and their governance alone;
inspect and update affected open tasks explicitly.

## Rule relationships and refactoring

Use `lode rule link <ref>` with one of `--refines`, `--needs`, `--references`,
`--constrains`, `--conflicts-with`, `--amends` or `--derived-from <other-ref>`.
`refines` and `needs` put the target in the rule's context closure, which
`lode show <ref> --closure` reads; `references` does not. `lode rule terms`
lists a project's definitions and `lode rule lint` reports undefined terms,
oversized closures, conflicts and positional references.
`lode rule unlink` removes a manually written link. References in rule text
also produce derived `references` edges; edit the text to change those.

A split starts with `lode rule add` creating the narrower rule, then
`lode rule link <new-ref> --derived-from <old-ref>` records its origin.
A merge edits the surviving rule to absorb the other text. Record successors
with a map, one line per old rule: `<old-ref> -> <successor-ref> ...`.
An empty right side withdraws the rule without a successor.

```bash
lode rule supersede --map <file> --dry-run
lode rule supersede --map <file>
```

The refactor withdraws the old rules, writes a `supersedes` edge from each
successor to the rule it replaces, and marks accepted plans governed by
withdrawn rules stale. It preserves task links;
`governed_by[].resolves_to` reports live successors. Moving text or changing
document order alone performs none of this and completes no work. Inspect
arrangements, stale plans and governed tasks after a refactor.

## Authoring flow

```bash
lode doc lint <file>                                       # local lint before creating/editing
lode doc add --kind spec --slug <slug> --file <file>       # creates a draft spec
lode doc edit <ref> --file <file>                    # replace a draft's body; on a spec, writes its rules
lode doc revise <ref> --file <file>                  # open a candidate revision on an accepted doc; --accept lands it
lode doc revise <ref> --discard                      # withdraw it without landing: owner or its author
lode doc submit <ref>                                # records a review event; mints a review task
lode doc accept <ref>                                # owner-gated; on a plan, mints the declarations that have no task yet
lode doc transfer <ref> --to <actor>                 # owner-gated; reassigns the document to another actor
```

Draft the markdown — frontmatter included — in a scratch file first; it's an
editor buffer, nothing reads it again after `lode doc add`. Read a document
back with `lode show <ref>` (see shorthand below) or `lode doc show
<ref> --json` for the body plus parsed sections and edges.

`lode doc sections` lists sections across the corpus rather than for one
document: bare, it is every section in the project (`--project=` widens it to
every project); with a number or anchor, `lode doc sections 8.2` answers
"which document defines §8.2".

## Frontmatter

Mandatory, no exceptions. Keys are ontology property names (`ns/ontology.ttl`,
`ns/concept.ttl`), ordered lifecycle → `covers` → `defers` → dependency →
amendment → supersession:

| Key | Shape | On |
|---|---|---|
| `status` | `draft` \| `accepted` \| `superseded` | all |
| `issued` | `YYYY-MM-DD` | specs |
| `covers` | requirement ref(s) (`WL-REQ-<n>`), spec section reference(s), or whole-document reference(s) — plain references only, no coverage level — or `NO-SPEC` | **plans**, mandatory |
| `defers` | list of `{spec, to}`: a section this plan hands off (`spec`, with `#sec-N`) and the document that owns it (`to`, no fragment) — reported `deferred` with its owner by `--needs-planning` until some plan covers it (026 §5.3) | **plans** |
| `requires` | reference list (`isRequiredBy` is refused) | all |
| `blockedBy` | plan references — the named plans run first; both ends must be plans in the same project (`blocks` is refused) | **plans** |
| `wasDerivedFrom` | scalar reference | specs |
| `amends`, `amendedBy`, `replaces`, `isReplacedBy` | refused: amendment and supersession are rule edges (`lode rule link --amends`, `lode rule supersede`) | none |

`covers: NO-SPEC` is the reserved sentinel for a plan answering to no
governing spec (a mechanical refactor, a build fix) — write it explicitly;
an absent `covers` reads as a forgotten one, not a deliberate choice.

### The `<KEY>-SPEC-<n>` shorthand

Cross-project reference, since a doc reference cannot cross a repository:

```
<PROJECTKEY>-SPEC|PLAN-<n>[#sec-<anchor>]
```

`WL-SPEC-73` · `WL-SPEC-77#sec-11` · `WL-PLAN-7` · `CMS-SPEC-4`.
`<n>` is the document's own corpus number, unpadded. A retired
`<KEY>-ADR-<n>` resolves to the spec that replaced the ADR. The `SPEC`/`PLAN`
token disambiguates it from a task id (`WL-4` the task vs `WL-SPEC-75` the
document) and is checked against the document's actual kind. Numbers are per
kind, so `WL-SPEC-73` and `WL-PLAN-1` are different documents. A shorthand
naming a project this checkout can't reach resolves as `unresolved`, not an
error; `lode show <ref>` is what actually verifies one.

## Section anchors and document amendments

Once a spec is accepted, its `{#sec-N}` anchors never move and never get
renumbered — inserting between `2.1` and `2.2` uses a letter suffix
(`2.1a`), never a renumber. A superseded *section* keeps its heading and
anchor and gets a note saying what replaced it; deleting it breaks whoever
linked it.

A bare `lode show <ref>` reads the current stored body, including landed
revisions. `--version` reads a historical snapshot. Use `--inline` when acting
on a spec: it also folds in-force rule amendments (`lode rule link --amends`)
into the sections they affect, attributed to the amending rule.
`lode doc show <ref> --json` exposes `amendments` when you need the edges
behind that reading.

## Coverage as a query, never a stored flag

"Is this spec implemented?" is answered by walking `covers` edges from
accepted plans, not by a status a human flips. Coverage follows supersession:
a successor rule counts as covered when its predecessor was, so a refactor
never reopens planning gaps a prior plan already closed.

```bash
lode doc list --needs-planning     # accepted specs with a section no accepted plan covers
lode doc list --needs-execution    # accepted plans whose minted task set still has an open task
lode doc list --bare-superseded    # withdrawn rules no rule supersedes
lode doc todo <slug> --deps        # one spec's remaining work, recursively through its dependencies
lode doc progress                  # the whole project at a glance: each spec's state and next act
```

`implements` (component → doc section, "this code realises this intent") is
the separate, code-side edge — distinct from `covers` (plan → rule,
"this plan promises to see it built"). `covers` used to also mean the
code-evidence case; that spelling is retired but still parses.

## The doc-lifecycle watcher

Two rules, both pure functions of one event, both suppressed while a task of
the relevant kind already references the document (so a re-submit or
double-accept never double-mints):

| Event | Mints | Guard |
|---|---|---|
| `lode doc submit` (any kind) | a `review` task, `about_doc` = the doc | suppressed if a review task on it is already open |
| `lode doc accept` **of a spec** | a `design` task charged with decomposing it into plans, `about_doc` = the doc | suppressed if a design task on it is already open; accepting an ADR or a plan mints nothing here — a plan's own acceptance mints its *task set* directly, not through this watcher |

Minting the prompt is not performing the act: nothing here reviews, accepts,
or decomposes anything on its own — those stay manual, owner-gated
commands.

## The spec / plan / task model, in one paragraph

Writing or revising a document is itself an ordinary claimable task
(`kind: design`) that closes on submission for review — not on acceptance,
which is a document status transition, not a task state. A plan's execution
*is* the task set minted at its acceptance; there is no container row above
them — "this plan's tasks" is the query `tasks WHERE plan_doc = <this
plan>`, not a row you create. Do not create a long-lived umbrella task per
spec, and do not create a free-standing container task: containment is
always inferred from `child_of` edges.
