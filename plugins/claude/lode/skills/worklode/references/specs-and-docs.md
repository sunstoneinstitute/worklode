# Specs, ADRs, plans: the document model

Deep reference for the `worklode` skill. Documents live *in the backbone*
(spec 025) — there is no git corpus to browse for them; `lode doc` is the only
authoring path.

## Kinds and lifecycle

Three kinds: `spec`, `adr`, `plan`. Every one moves `draft → accepted →
superseded`; `proposed` was retired — a document under review just stays
`draft` (submitting it records a review event, no status column moves).

- **Spec / ADR** — a durable statement, numbered per (project, kind):
  `WL-SPEC-25`, `WL-ADR-7`. Carries sections with permanent `{#sec-N}` anchors
  once accepted.
- **Plan** — an executable document, unnumbered, no addressable sections
  (plans are not DesignDocs). Its `## Tasks` block is what `lode doc accept`
  mints into real tasks — accepting a plan is the only way its tasks come
  into existence; nothing mints a root/container row above them. An accepted
  plan stays editable: re-accept it to mint declarations added since, which
  leaves every already-minted task alone (a declaration's identity is its
  title, so keep titles unique and retitle only to withdraw).

## Authoring flow

```bash
lode doc anchors <file>                                    # local lint before creating/editing
lode doc new --kind spec --slug <slug> --file <file>       # kind: spec, adr, plan — creates it, draft
lode doc edit <ref> --file <file>                    # replace a draft's body
lode doc revise <ref> --file <file>                  # open a candidate revision on an accepted doc; --accept lands it
lode doc revise <ref> --discard                      # withdraw it without landing: owner or its author
lode doc submit <ref>                                # records a review event; mints a review task
lode doc accept <ref>                                # owner-gated; on a plan, mints the declarations that have no task yet
lode doc transfer <ref> --to <actor>                 # owner-gated; reassigns the document to another actor
```

Draft the markdown — frontmatter included — in a scratch file first; it's an
editor buffer, nothing reads it again after `lode doc new`. Read a document
back with `lode show <ref>` (see shorthand below) or `lode doc get
<ref> --json` for the body plus parsed sections and edges.

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

## Anchors are frozen, there is no "inlined" view

Once a spec is accepted, its `{#sec-N}` anchors never move and never get
renumbered — inserting between `2.1` and `2.2` uses a letter suffix
(`2.1a`), never a renumber. A superseded *section* keeps its heading and
anchor and gets a note saying what replaced it; deleting it breaks whoever
linked it.

There is no consolidated/inlined document that folds amendments into one
reading. **What a section says now is that section plus whatever amends it**
— `lode doc get <ref> --json`'s `edges_in` names `amendedBy` and
`isReplacedBy` on it; follow those before treating the body on screen as
current. Both directions of an amendment are always recorded (the amending
doc's `amends`, the amended doc's `amendedBy`) so either document alone
answers "what still constrains this section" without a corpus-wide scan.

## Coverage as a query, never a stored flag

"Is this spec implemented?" is answered by walking `covers` edges from
accepted plans, not by a status a human flips:

```bash
lode doc list --needs-planning     # accepted specs with a section no accepted plan covers
lode doc list --needs-execution    # accepted plans whose minted task set still has an open task
lode doc list --bare-superseded    # superseded docs with a section nothing replaces
lode doc todo <slug> --deps        # one spec's remaining work, recursively through its dependencies
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
