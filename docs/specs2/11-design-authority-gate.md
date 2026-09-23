# The design authority gate

Specs are the durable statement of what Worklode does. Code changes that alter
that statement must be preceded by, or land together with, the spec sentence
that now describes them. This document defines which changes that rule
applies to, how a change declares its relation to the spec, what the merge gate
checks mechanically, and what it leaves to a person. It also states that plans
are never back-patched.

## 1. The problem the gate solves

Quick fixes are legitimate. What is not legitimate is a quick fix that changes
an observable behaviour and leaves the spec describing the old one. After a few
of those the spec stops being the design authority and becomes a lagging
summary that the architect corrects reactively. The gate moves the decision
point ahead of the merge: a change either cites the spec sentence it
satisfies, or it carries the amendment that gives it one.

## 2. "What" versus "how"

A spec describes the "what": behaviour an outside party can observe or rely on.
A plan and the code describe the "how". The gate applies only to changes to the
"what". The test is the citation test:

> Name the spec sentence this change makes true. If that sentence exists today
> and the code violates it, the change is a fix and the spec stands. If the
> sentence you would cite is one you would have to write, the change alters the
> "what" and the spec is amended first, or in the same change.

The citation test replaces any estimate of whether a planner would have minted
the task. That estimate depends on the model and the prompt. Whether a sentence
exists in the spec does not.

### 2.1 Changes to the "what"

A change alters the "what" when it adds, removes or changes the meaning of any
of these, as seen from outside the process:

| Kind | Examples |
|---|---|
| A visible noun | an entity, a document kind, a task kind or state, a table or column that crosses the wire, a route, a command, a flag, a permission, an event type, a config key, a metric name or label |
| A rule | which state transitions are allowed, who may do what, what makes a task done, how ranking orders work, when something happens automatically (a hook, a subscriber, a sweep) |
| A promise | atomicity, idempotence, retention, ordering, a security boundary, what a token can reach, what a webhook is trusted to say |
| The meaning of an output | a field's semantics, an exit code, the shape of `--json` output, the text of a refusal an agent acts on |

### 2.2 Changes to the "how"

These never need a spec change, whatever files they touch:

| Kind | Test |
|---|---|
| A fix | the cited sentence exists and the old code violated it |
| A refactor | no visible noun, rule, promise or output meaning changes; tests unchanged or moved |
| Performance | same observable results, faster or cheaper |
| Rendering and copy | the same information, laid out or worded differently; `www/` copy |
| Tests, build, dependencies, CI plumbing | `_test.go`, `Makefile`, `go.mod`, workflow files, Dockerfiles |
| Operator configuration values | a value changes, the key and its meaning do not |

### 2.3 The grey zone

Some changes are "how" by intent and "what" by accident: a migration that adds a
column also used by an API response, a refactor that changes error text an
agent matches on. The author classifies. The gate records the classification
on the task so `lode graph drift` can surface a pattern of misclassification
later (07-knowledge-graph-and-search.md). Misclassifying is a review finding,
never a merge failure.

## 3. Guarded paths

The gate is opt-in per project. A project enables it in `.worklode/config.toml`
by naming the paths it guards and the line it requires:

```toml
[gate]
paths = ["internal/cmd/**", "internal/model/**", "deploy/base/**", "ns/*.ttl"]
regex = ["^internal/store/(tasks|claim|ranking)\\.go$"]
trailer = "Spec:"
```

`paths` are globstar patterns, `regex` is for the cases a glob cannot say,
`trailer` is the key the commit or PR body must carry (section 4). A project
with a `[gate]` table must also require pull requests on its default branch,
since the gate runs on pull requests; `lode doctor` warns when the repository
ruleset does not. Without a `[gate]` table nothing runs.

`lode gate check --base <sha> --head <sha> [--body-file <path>]` is the check
itself: it reads the table, diffs the two revisions, and exits 1 with the
reason when a guarded path changed and no valid trailer is present on the pull
request body or in a commit message. It checks the trailer's form offline and
never calls the server; the server resolves the clause the trailer names
(§4). `lode doctor` reports whether the table parses. Whether the repository
requires pull requests is not checked yet, since no API route exposes the
branch rules the server records.

The gate runs when a pull request touches a guarded path, where "what" changes
concentrate. The list is a heuristic that decides whether the gate asks, never
whether a change is a "what" change. Worklode's own list:

| Path | Why |
|---|---|
| `internal/api/router.go`, `internal/api/authz.go` | routes, guards, permissions |
| `internal/cmd/**` (excluding `_test.go`) | the command surface |
| `internal/model/**` | every wire shape |
| `deploy/base/**` (migrations) | stored nouns |
| `internal/store/*.go` where a state machine or ranking lives | rules |
| `internal/hooks/**`, `internal/watcher/**`, `internal/eventbus/**` | automatic behaviour |
| `ns/*.ttl` | the ontology |
| `plugins/**/skills/**`, `plugins/**/commands/**` | what agents are told to do |

Everything else merges without the gate asking.

## 4. Declaration

Once clauses exist (12-spec-refactoring-design-tree.md S30) the trailer names a
clause: `Spec: WL-CL-456`, or `Spec: WL-CL-456 amended` when the PR ships a new
version of it. Section refs (`WL-SPEC-4 sec-5`) are accepted only while a
project's server-side gate setting allows them, a transitional per-project
setting and never a `.worklode/config.toml` key, since it binds the project and
not one worktree.


Every commit made inside a task worktree already carries a `Worklode-Task:
<id>` trailer, stamped by the commit-msg hook that `lode install` binds
(03-tasks-and-execution.md §1). The gate starts from that task.

A task minted from a plan already knows its spec: the plan covers sections, and
the task carries the plan ref (05-documents.md). Planned work is meant to pass
the gate with no extra declaration. The CI check does not read plans yet, so
today planned work carries a trailer too (§5, condition 1).

Unplanned work declares itself with one trailer on the pull request body or
the final commit:

```
Spec: WL-SPEC-4 sec-5          # satisfies this section as written
Spec: WL-SPEC-4 sec-5 amended  # this section was amended for this change
Spec: none refactor            # one word from the "how" list in 2.2
```

The server's `spec-reconciler` subscriber reads the same trailer from the
pull request body or the pushed commits (the final commit wins), finds the
task from the branch name or, when the branch does not resolve, from a
`Worklode-Task:` trailer in the pull request body, and writes a `governedBy`
link with `source = 'gate'` when no plan governs the task. It reads the
default `Spec:` trailer key, so a
project that renames the key with `[gate] trailer` gets a working CI gate and
a silent reconciler until the key reaches the server. A `none` trailer writes
nothing. A trailer naming a clause or section that does not exist is counted
and logged and writes nothing. Section refs are accepted everywhere until the
per-project switch `gate_trailer_sections` arrives with the plan lifecycle
increment (12-spec-refactoring-design-tree.md S50 to S52).

The `none` reasons are the closed list: `fix`, `refactor`, `perf`, `copy`,
`tests`, `build`, `config`. A `fix` still cites the section it restores:
`Spec: WL-SPEC-4 sec-5 fix`. A `none fix` with no section is refused, because a
fix with nothing to cite is a "what" change by the test in §2.

## 5. What the gate checks

The gate is one CI job, `spec-gate`, on pull requests whose diff touches a
guarded path. The job runs `lode gate check`, which decides offline from the
repository checkout, the pull request body and the commit messages (§3). The
four conditions below are the design. What the offline check evaluates today
is stated under each one.

1. The task has a plan ref and that plan covers at least one section of an
   accepted spec. Not built. The offline check never reads the task, so
   planned work passes CI today by carrying a trailer like any other change.
   The server side of this condition does hold: the `spec-reconciler` sees
   the plan link and leaves a planned task alone (§4).
2. A `Spec:` trailer names a section that resolves through `GET
   /api/v1/docs/{ref}` and is not withdrawn. Half built. The offline check
   reads the ref's form and accepts a clause ref (`WL-CL-456`) or a section
   ref (`WL-SPEC-4 sec-5`). Whether the ref resolves is decided on the server
   when the reconciler governs the task, which counts and logs a ref that
   names nothing (§4). The CI job makes no API call.
3. A `Spec: ... amended` trailer names a section whose current version was
   written after the pull request's base commit. Half built. The offline
   check accepts the `amended` qualifier and compares no versions.
4. A `Spec: none <reason>` trailer carries a reason from the closed list, and
   for `tests`, `build` or `copy` the diff touches only the matching paths.
   The reason list is checked offline, including the refusal of a bare `none
   fix`. The path condition on `tests`, `build` and `copy` is not built.

On fail the job names the guarded paths that changed and the reason the
trailer was missing or malformed. The gate never judges whether the cited
sentence really covers the change. That judgment stays with the reviewer.

Two parts of the record are not built. The job posts no comment quoting the
cited section's first paragraph under the diff. The gate writes no `spec_gate`
event on the task carrying the classification, the cited section and the pull
request. Once that event exists it is the audit trail behind §2.3 and the
input to a standing drift query: tasks that landed against a guarded path with
`none` classifications, grouped by path. What the gate records today is the
`governedBy` link with `source = 'gate'` and the `task.governed` event the
reconciler writes for it (§4).

## 6. Plans are never back-patched

A plan is the reconciliation between a spec and the code at the moment it was
written. Once its tasks are minted the plan has done its work. Reality then
diverges from it in two ways, and neither is repaired in the plan:

- The "how" turned out different. The task records that, in its notes, its
  block reason, or the pull request itself. The task is the durable record of
  what was actually done.
- The "what" turned out different. That is a spec amendment, made through
  `lode doc edit` on the owning section. The edit path already marks every
  unexecuted plan covering that section `stale` (05-documents.md), and a
  fresh plan is written from the amended spec and the current code.

Editing a plan after minting creates a document that describes neither the
original reconciliation nor the current one. The rule is one line: a plan with
a minted task is read-only.

## 7. What this does not do

It does not stop a quick fix. A fix cites the sentence it restores and merges.
It does not require a spec for internal structure. It does not make the
architect approve every change ahead of time. It makes every change to a
guarded path say, in one line, what sentence it stands on, and it puts that
sentence in front of the reviewer. Where the sentence does not exist yet,
writing it is the ahead-of-time decision.

## Sources

New. Builds on 03-tasks-and-execution.md (task trailers, events),
05-documents.md (sections, coverage, `stale`), 07-knowledge-graph-and-search.md
(drift queries), and 04-done-verification-workflows.md (project workflow
rules, which may later carry the gate's classification list per project).

## Open questions

- Whether `Spec: none config` should exist, or whether config-value changes
  should not touch guarded paths in the first place.
- Whether the `spec_gate` event should also feed the Progress page's
  next-act derivation (10-cockpit.md), so a section with many `fix`
  citations shows as one that needs rewriting.
- Which token the CI job uses. A per-repo read-only token is the smallest
  choice; the actor model in 02-identity-actors-and-secrets.md decides.
