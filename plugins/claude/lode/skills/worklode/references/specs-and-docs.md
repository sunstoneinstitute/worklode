# Specs, rules and plans: the document model

Deep reference for the `worklode` skill. Documents live *in the backbone*
(spec 025). Use `lode doc` for document bodies and `lode rule` for individual
rules. Scratch files are editor buffers.

## Kinds and lifecycle

A **rule** is a design requirement with its own identity (`WL-RULE-12`),
heading, body, status and version history. A **spec** arranges rules into a
readable document. Each placement records the rule and version, position,
depth and section anchor. A rule ref names the requirement; a ref such as
`WL-SPEC-25#sec-9` names a place in that spec's arrangement.

- **Spec** — a standing description, revised as the design changes. Each
  anchored section becomes a rule; content under deeper, unanchored headings
  belongs to the nearest anchored rule. Acceptance accepts its draft rules.
- **Plan** — an executable document. Its `covers` entries select existing
  rules into its arrangement; its own prose creates no rules. Accepting its
  `## Tasks` declarations mints tasks governed by the arranged rules. No
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
lode rule list --doc <spec-ref>              # rules in arrangement order; also works for a plan
lode show <rule-ref> --json                  # text, arrangements, governed tasks and edges
lode rule versions <rule-ref>
lode show <rule-ref> --version <n>
lode rule edit <rule-ref> --file <body-file> # text under the heading; optional --heading
```

Create rules by writing anchored spec sections through `lode doc`. The store
assigns their refs. On a later document write it matches existing rules in
three passes: anchor and heading, heading alone, then anchor alone. Each rule
can match once. An unmatched section creates a rule. Replacing text at the
same anchor can therefore revise the existing rule; it does not declare that
rule withdrawn. Check identities after a structural edit.

A rule edit regenerates the arranging spec's body through its document write
path. On a draft it rewrites the draft rule version. On an accepted spec it
opens or updates a candidate revision; the change lands with
`lode doc revise <spec-ref> --accept`. Direct editing requires exactly one
arranging spec or ADR; covering plans do not count toward that limit. Sharing
one rule across several specs is not yet supported by this edit path.

## Plans arrange rules; tasks are governed by them

Keep writing `covers` with **document/section references**, for example
`WL-SPEC-25#sec-9`, and the existing `coverage` and `fullCoverageWith` keys.
A rule ref is not a replacement for the `spec` field in this frontmatter.
A section edge reaches the rule at that anchor and its descendant rules;
a whole-document edge reaches all its rules. Unresolved refs reach none.
The plan arrangement is rebuilt on each plan write and again at acceptance.
Rule membership and section coverage are different questions: a whole-doc
edge can populate an arrangement without discharging section planning gaps.

Tasks minted from a plan receive `governedBy` links to its arranged rules,
including standing constraints declared with `coverage: none`. For work
filed outside a plan, name the governing rules explicitly:

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

Use `lode rule link <ref>` with one of `--refines`, `--constrains`,
`--conflicts-with`, `--references` or `--derived-from <other-ref>`.
`lode rule unlink` removes a manually written link. References in rule text
also produce derived `references` edges; edit the text to change those.

A split starts with a spec edit creating the narrower rule, then
`lode rule link <new-ref> --derived-from <old-ref>` records its origin.
A merge edits the surviving rule to absorb the other text. Record successors
with a map, one line per old rule: `<old-ref> -> <successor-ref> ...`.
An empty right side withdraws the rule without a successor.

```bash
lode rule supersede --map <file> --dry-run
lode rule supersede --map <file>
```

The refactor withdraws the old rules, writes `supersededBy` edges and marks
accepted plans arranging withdrawn rules stale. It preserves task links;
`governed_by[].resolves_to` reports live successors. Moving text or changing
document order alone performs none of this and completes no work. Inspect
arrangements, stale plans and governed tasks after a refactor.

## Authoring flow

```bash
lode doc lint <file>                                       # local lint before creating/editing
lode doc add --kind spec --slug <slug> --file <file>       # creates a draft spec
lode doc edit <ref> --file <file>                    # replace a draft's body
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
| `issued` | `YYYY-MM-DD` | specs, ADRs |
| `covers` | spec section reference(s), optionally `coverage: full\|partial\|none` (with `fullCoverageWith` for partial) — or `NO-SPEC` | **plans**, mandatory |
| `defers` | list of `{spec, to}`: a section this plan hands off (`spec`, with `#sec-N`) and the document that owns it (`to`, no fragment) — reported `deferred` with its owner by `--needs-planning` until some plan covers it (026 §5.3) | **plans** |
| `requires` / `isRequiredBy` | reference list | all |
| `blocks` / `blockedBy` | plan references — orders whole-plan execution; both ends must be plans in the same project | **plans** |
| `wasDerivedFrom` | scalar reference | specs |
| `amends` / `amendedBy` | map: your-section-key → their-section-value (`"."` = whole doc) | all |
| `replaces` / `isReplacedBy` | same map shape, per section | all |

`covers: NO-SPEC` is the reserved sentinel for a plan answering to no
governing spec (a mechanical refactor, a build fix) — write it explicitly;
an absent `covers` reads as a forgotten one, not a deliberate choice.

### The `<KEY>-SPEC-<n>` shorthand

Cross-project reference, since a doc reference cannot cross a repository:

```
<PROJECTKEY>-SPEC|ADR-<n>[#sec-<anchor>]
```

`WL-SPEC-1` · `WL-SPEC-25#sec-9` · `WL-ADR-7` · `WL-PLAN-7` · `CMS-SPEC-4`.
`<n>` is the document's own corpus number, unpadded. The `SPEC`/`ADR`/`PLAN`
token disambiguates it from a task id (`WL-4` the task vs `WL-SPEC-4` the
document) and is checked against the document's actual kind. Numbers are per
kind, so `WL-SPEC-1` and `WL-PLAN-1` are different documents. A shorthand
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
on a spec: it also folds in-force `amends` and `replaces` edges into the
sections they affect, attributed to the source document. Rule versioning and
these document edges are separate mechanisms; changing a rule version does
not require inventing an amending document. `lode doc show <ref> --json`
exposes `edges_in` when you need the relationships behind that reading.

## Coverage as a query, never a stored flag

"Is this spec implemented?" is answered by walking `covers` edges from
accepted plans, not by a status a human flips:

```bash
lode doc list --needs-planning     # accepted specs with a section no accepted plan covers
lode doc list --needs-execution    # accepted plans whose minted task set still has an open task
lode doc list --bare-superseded    # superseded docs with a section nothing replaces
lode doc todo <slug> --deps        # one spec's remaining work, recursively through its dependencies
lode doc progress                  # the whole project at a glance: each spec's state and next act
```

`implements` (component → doc section, "this code realises this intent") is
the separate, code-side edge — distinct from `covers` (plan → section,
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
