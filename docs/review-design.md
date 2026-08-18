# Worklode review surface — design

Design baseline, 2026-08-16. This document consolidates PR #29's two draft
specs (reading diffs; the review surface) with the
[review tooling refresh](research/review-tools-refresh.md) research, under the
constraints settled in discussion: no Node/npm in the build or the server,
documents expected to outnumber code in review volume, both supported well,
and control over markdown/qmd rendering as a bonus we now know is cheap.

This is the document to iterate on with crit. When approved, its "Spec
decomposition" section becomes one `lode` task per spec to be written; the
PR #29 branch is then superseded by those specs and closed.

## Problem

The spec corpus has asserted a review gate since its earliest documents —
"reviewed via crit", `draft → accepted` gated on review resolution — and
none of it exists. There is no comment, no thread, no resolution, no approval.
Spec 025's accept gate (a document is not accepted until every assigned
reviewer approves) reads a table nobody has designed. One layer down, a task's
`in_review` state (spec 004) has no content: a reviewer gets a raw diff with
no signal about which twelve of its nine hundred lines carry the decision, so
review silently degrades into skim-and-approve.

Reviewers are humans *and* agents — Claude reviews Codex's work and the
reverse — so comment, thread, resolve, and approve must exist as API and
`lode` verbs first, with any UI as one client among several.

## Decisions

These are settled unless this review overturns them. Rationale lives in the
research note and PR #29's drafts; this section states positions.

### 1. Contract / client / store split

The durable object is a **review** — subject, assigned reviewer set, anchored
threads, ordered rounds, per-reviewer verdict — living in Postgres, written
through `/api/v1`, emitted to the event log. Any desk is a rendering client.
PR #29 got this shape right and it survives unchanged.

### 2. One review object over documents and changes

A review's subject is either a document or a task's change (the **task**, not
a PR — a task's change may span repos, and `in_review` is a task state).
Threads, rounds, resolution, verdict invalidation are identical across the
two; only the anchor kind differs:

- **Document threads**: a frozen `{#sec-N}` anchor plus the quoted text.
  Section anchors are frozen at acceptance (spec 025), which makes them a
  durable coordinate — document review is the easier case.
- **Change threads**: `(repo, path, side, line)` plus the anchored text,
  re-anchored by content when lines shift, `drifted`/`obsolete` when the
  anchor no longer resolves — kept, never silently re-pointed.

### 3. crit replaces galley as the interim client

[crit](https://github.com/tomasz-tomczyk/crit) (Go, MIT, ~903 stars, weekly
releases) reviews markdown documents as its headline mode and diffs, live
pages, and static HTML besides. It is a single Go binary with vendored,
`go:embed`ded browser libraries — no Node at build or runtime — and it ships
a documented JSON contract plus command hooks. galley (13 stars, static since
July, Node ≥22, unauthenticated desk) is dropped as a client; its
`ReviewResult` round semantics remain the contract worth copying (§Contract).

Integration is through crit's `on_finish_*` command hooks: crit pipes the
review JSON to stdin after persisting it, so the bridge is
`lode review ingest < /dev/stdin` — no polling loop, no background process
holding a token. `lode review desk` launches crit with the hook configured.

Known gaps, accepted for the interim: crit's document anchoring is
line-based (`{#sec-N}` is not honored; `.qmd` renders as code), so the ingest
maps lines to section anchors via the export sidecar (§5). crit is pre-1.0
and single-author; a golden copy of its contract is checked in so upstream
drift is a red test.

### 4. meat stays for reading diffs

[meat](https://github.com/boldsoftware/meat) (Apache-2.0-intent, Go, zero
external deps) abridges a diff by having the model emit an edit plan applied
to the immutable input, so the reading diff is provably a subset of the real
diff — it can hide lines but never invent or rewrite one. That property is
what makes it safe in front of a gate, under three normative rules: a
rendering, never the artifact of record; no automated gate reads it; its
provenance (model, omission counts) travels with it.

Amendments to PR #29's draft, from the research:

- meat has no releases; the spec states an explicit pseudo-version pin.
- Its edit-plan types are unexported, so "retain the edit plan" is not
  available; skim spans are recovered by line-diffing `smart_diff` against
  the raw diff (sound because of the subset property).
- Default model flipped to OpenAI and oversized diffs now chunk (400 KB/run,
  4 MB ceiling, 32 chunks); the spec's bounds are restated against current
  behavior.
- The incomplete upstream LICENSE file (header only) is raised upstream
  before the dependency lands.

Reading diffs remain code-only. Documents are section-addressable and get a
better decomposition for free; crit's story mode (a coverage-validated
narrative layer, explicitly "an explainer, not a reviewer") arrives with the
client and composes with reading diffs rather than competing.

### 5. Document rendering is owned, in Go

goldmark v1.8.x (already an indirect dependency) is promoted to direct with
`parser.WithAttribute()`: `{#sec-7}` and dotted `{#sec-1.1}` render as real
`id` attributes, and `Pos()` gives exact byte ranges per section — the
document-anchoring substrate in ~30 lines, verified against our own specs.
`stefanfritsch/goldmark-fences` is added when Quarto documents arrive: a
report rendered from its committed `_freeze/` (Pandoc markdown with executed
outputs as `::: {.cell-output-*}` divs) gives code↔output toggling with no
Quarto in the request path and no kernel. A Quarto sidecar container (no
daemon mode; cold Deno boot per render) covers what freeze cannot; goldmark
v2 is not adopted.

`lode doc export --anchors` writes a document plus a sidecar mapping line
ranges to `{#sec-N}` anchors. Every line-oriented client (crit today) writes
into the frozen anchor space through that map, so no client re-implements
heading parsing.

### 6. Owned natively, and only these

Persistence; identity on every verdict; anchors that survive a force-push;
the multi-reviewer set and the accept gate that reads it; provenance through
the event log; cross-machine question delivery; blocking a task on a review.
Each is something a single-seat desk deliberately does not do.

### 7. The honesty rule

No agent may lower the gate on its own work. Server-enforced: the guide
handed to a reviewer agent is produced by the backbone; an author's proposed
skim on a non-mechanically-edited file is refused with the rule that fired;
self-approval (the lease-holding actor, or the session that authored the
round) is refused at the API. Refusals are counted, and `focused` reviews are
flagged so a verdict cast over a narrowed diff is distinguishable forever.

### 8. Degradation is a hard requirement

With no crit, no Node, no browser: every verb works — comment, reply,
resolve, approve, await — and `lode review show` prints the guide as text and
the diff raw. The moment a verdict requires a browser, the agent-reviewer
path is second-class and the premise is lost. With no reading-diff model
configured, every surface renders as today.

## Contract

The wire shape is a blend, each piece taken from the tool that got it right:

- **crit's `Comment`** for anchoring: `anchor` text as identity with line
  numbers as hints; the four-stage recovery ladder (LCS remap → fuzzy match
  with a min-length guard → whole-file scan picking the hit nearest the
  predicted line → `drifted`); `FocusKey` scoping a comment to the view it
  was authored in.
- **galley's `ReviewResult`** for round semantics: `accepted` / `rejected` /
  `requestedChanges` / `approvedFiles` / `openQuestions` / `overallNote`,
  `baseDiffHash` pinning what was reviewed, a new round invalidating every
  verdict cast against an earlier one.
- **Thread intents** `note` | `change_request` | `question`, with the
  question channel read-only and server-enforced: while a question is open
  the author replies in-thread and a new round is refused. crit has no
  question channel, so this piece is built natively regardless of client.
- **The anchor, W3C-shaped**: coarse structural anchor (`{#sec-N}`, frozen by
  policy) + text-quote selector (`exact`, `prefix`, `suffix`) + positional
  hint, resolved cheap→expensive with the quote as verifier. reviewdog's MIT
  `proto/rdf`, `diff`, and `filter` packages are imported rather than
  rewritten where they fit.

Golden copies of the adopted upstream schemas (crit's `CritJSON`/`Comment`,
galley's `ReviewResult` and guide) are checked in; drift is a failing test.

## Spec decomposition

Proposed `lode` items on approval, ordered by `blocks`:

1. **WL-SPEC-NN "Document rendering and anchoring"** — goldmark promotion,
   `{#sec-N}` rendering, section byte-range index, `lode doc export
   --anchors`, the freeze-rendering path for Quarto reports. No review
   semantics; the cockpit and future clients consume it independently.
2. **WL-SPEC-NN "The review surface"** — PR #29's 030 rewritten: schema,
   API, `lode review` verbs, contract blend, crit as interim client via
   hooks, honesty rule, gates, metrics, events. Requires 1 for document
   anchors. References rewritten against the folded corpus (former 000, 011,
   014, 023, 027, 028 → 001/004/025); `tasks.assignee` dropped (shipped as
   migration 0010).
3. **WL-SPEC-NN "Reading diffs"** — PR #29's 029 with the §4 amendments,
   renumbered (029 is taken on `main`). Independent of 2; its output feeds
   review surfaces when both exist.

One review task per spec, humans assigned; the PR #29 branch closes in favor
of these.

## Open questions for this review

1. Is line-based document anchoring in crit's own UI acceptable for the
   interim, given the export sidecar maps lines → `{#sec-N}` at ingest? The
   alternative is an upstream PR to crit for Pandoc-attribute support —
   realistic (Go, active maintainer) but on someone else's timeline.
2. Is three specs the right cut, or does rendering/anchoring (item 1) fold
   into the review surface? It stands alone only if the cockpit wants
   rendered documents before review ships.
3. `crit-web` (Elixir + Postgres + generic OIDC, self-hosted) as an optional
   read-only share target for external reviewers: worth a follow-up item, or
   out of scope entirely? As a store it is excluded — that would put two
   owners on "is this approved".
4. Does the reading-diff pipeline ship before any review surface exists (it
   improves `lode task brief` for reviewer agents on its own), or wait so
   its first consumer is the guide?
5. Quarto 2 (Rust rewrite, Automerge-based collaborative editor, October
   2026 at the earliest, no announced review/annotation features): anything
   here worth deferring for, or watch and ignore?
