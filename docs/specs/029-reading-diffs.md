---
status: draft
issued: 2026-08-05
requires:
  - 004-execution-backbone.md
  - 011-delivery-lifecycle.md
  - 012-agent-sessions.md
  - 022-prometheus-metrics.md
  - 027-event-watchers.md
  - 028-escalation-and-document-lifecycle.md
---
# Spec 029 — Reading diffs

## 0. Why {#sec-0}

Worklode's premise is that most code is written by agents and a human reviews what matters.
`MODEL_SELECTION.md` and 028 §0 make the same argument one level up: a high tier writes,
a cheaper tier executes, and attention is the scarce resource. Review is where that argument
has no mechanism behind it. A task reaches `in_review` and what a reviewer gets is the raw
diff — every import, every mechanical rename ripple, every generated file — with no signal
about which twelve of its nine hundred lines carry the decision.

The consequence is not that review is slow. It is that review silently stops happening: a
reviewer skims a 2,000-line agent-authored diff, approves it, and the gate that 011's
`in_review` state exists to be has become a formality. The same applies to the reviewer agents
028 §8 routes `kind = 'review'` work to — a cheap tier reading an unfiltered diff spends its
context on noise and reports on the noise.

[meat](https://github.com/boldsoftware/meat) (Apache 2.0, Go, no external dependencies) solves
exactly this and nothing else: it reduces a unified diff to the part a senior reviewer needs to
read. This spec adopts it as a library, defines where the reduced diff — the **reading diff** —
is produced, cached, priced and served, and states the property that makes it safe to put in
front of a gate.

**What this spec is not.** It defines an input to review, not review itself. The review surface
— threads, resolve, approve, the accept gate of 028 §6 — is a separate design, and nothing here
depends on it existing.

---

## 1. What a reading diff is, and what it is not {#sec-1}

meat does not ask a model to summarise a diff. It asks for an **edit plan** — a list of
removals, folds and single-line elisions against the numbered original — and applies that plan
to the immutable input itself (`meat/editplan.go`). The model never authors the displayed text.

That is the whole reason this is adoptable. A reading diff is provably a **subset** of the real
diff: the model can choose what you do not see, but it cannot show you a line that was never
in the change, and it cannot rewrite one that was. A "model summarises the diff" design has
neither property and could not be put in front of an accept gate at all, because approving a
summary is approving the model's prose.

Three rules follow, and they are normative:

- **A reading diff is a rendering, never the artifact of record.** The raw diff is always one
  action away on every surface that shows one (§7). Nothing in the backbone is derived from
  a reading diff.
- **No automated gate reads it.** It never advances a task, never satisfies a review, never
  feeds a policy check. It is shown to a reviewer — human or agent — who decides.
- **Its provenance travels with it.** Every surface labels it, names the model and states the
  omission, in the same spirit as 028 §4's notes: an abridgement whose abridging is invisible
  is worse than none.

The reviewer-facing claim is therefore narrow and true: *these are the lines worth reading
first; the rest is still here.*

## 2. `internal/reading` {#sec-2}

A new package wrapping meat. Its whole exported surface:

```go
// Diff is a produced reading diff. It mirrors meat.Result plus the identity
// under which it was cached (§5).
type Diff struct {
    SmartDiff    string
    Summary      string
    RubricHash   string
    Model        string
    InputTokens  int64
    OutputTokens int64
}

// Abridger produces reading diffs. One implementation wraps meat; tests use a
// fake, so no test in the tree makes a model call.
type Abridger interface {
    Abridge(ctx context.Context, unifiedDiff string, repoRoot string) (*Diff, error)
}
```

meat is a dependency, not a fork. `meat.Abridge` takes a `meat.Model` the caller supplies, so
worklode configures the endpoint and credentials the way it configures every other outbound
model call, and no vendoring or patching is required to route it at an internal gateway.

**The model is wrapped, not used raw.** `internal/reading` decorates the `meat.Model` it passes
down so every turn is observed (§9) before delegating. Per-turn observation is the point: the
`Result` totals price the run, but only the decorator can see that a run took nineteen turns
against a four-turn median, which is the shape a stuck run has.

**v1 uses meat's built-in Anthropic and OpenAI clients** under that decorator rather than a
worklode-owned HTTP client. `internal/embed` owns its client because there was no other; here
there is one, it is maintained by the package that owns the prompt surface, and the
`meat.Model` interface makes replacing it later a change in one constructor.

**Configuration mirrors embeddings** (`internal/embed`, 022 §6), because an operator
configuring two outbound model calls should not learn two schemes:

| Variable | Meaning |
|---|---|
| `LODE_READING_MODEL` | Model id. Anthropic ids route to Messages, everything else to OpenAI Responses — meat's own `ResolveModel` rule. |
| `LODE_READING_API_KEY` | Credential. |
| `LODE_READING_BASE_URL` | Optional gateway origin, for a proxied or self-hosted endpoint. |

Unset `LODE_READING_MODEL` disables the feature entirely (§8).

## 3. Getting the diff, and getting the tree {#sec-3}

Production is server-side. The alternative — abridging client-side in `lode`, where a worktree
already exists — was rejected: the operator whose worktree it is has moved on by the time the
PR is reviewed, review happens on another machine days later, and a cache that lives in one
developer's checkout serves one developer. The value of this is that it is produced once for
the org.

The server holds a GitHub App installation (`internal/githubauth`), so both inputs are already
reachable:

- **The diff** — `GET /repos/{owner}/{repo}/pulls/{n}` with
  `Accept: application/vnd.github.v3.diff`, a new sibling of `AppAuth.Tarball`.
- **The tree** — `AppAuth.Tarball(ctx, repo, headSHA)`, extracted to a temporary directory
  passed as meat's `RepoRoot`.

`RepoRoot` is worth the tarball. meat gives the model read-only `read_file` and `grep` confined
to that directory so it can decide what is load-bearing from the surrounding source rather than
from the diff text alone; with `RepoRoot` empty the tools are disabled and quality degrades.
The extraction is bounded, is deleted when the run ends including on failure, and a tarball that
cannot be fetched degrades the run to diff-text-only rather than failing it.

**Bounds, because the failure mode is spending.** meat splits a diff over ~400 KB into chunks
of up to 32, each with its own four-minute budget, so an unbounded 4 MB input can occupy the
better part of an hour and cost accordingly. Worklode therefore refuses above
`LODE_READING_MAX_DIFF_BYTES` (default 1 MB) and records the refusal as a terminal
`too_large` (§5) rather than a retryable failure. A diff nobody can read unaided is precisely
the one worth abridging, so the ceiling is deliberately generous and deliberately present.

## 4. Production is queued, never inline {#sec-4}

The trigger is a subscriber on 027's log, named `reading-diff`, on two event types:

- a `pull_request` ingest event for `opened` or `synchronize`;
- a task entering `in_review` (011 §1).

**The subscriber enqueues and acks; it never abridges.** 027 §2 gives one in-order consumer per
subscriber, and a handler that blocked for the minutes an abridgement takes would hold the
offset and stall every later event behind it — including, once other subscribers exist, ones
with nothing to do with review. So the handler resolves the diff identity, inserts a `pending`
row (§5), and acks. That insert is idempotent on the row's primary key, which is what makes
at-least-once redelivery harmless.

A separate worker in `lode serve` drains pending rows with `FOR UPDATE SKIP LOCKED`, bounded by
`LODE_READING_CONCURRENCY` (default 2). Draining is not ordered — reading diffs are independent
and a slow 900 KB PR must not delay a 3 KB one behind it.

**The cache is the queue.** A pending row is the promise that a reading diff is coming; the
worker fills it in place. A second PR whose diff hashes identically joins that row rather than
minting a rival — the same deduplication 028 §2 applies to escalations, for the same reason.
There is no separate job table, so there is no way for the queue and the cache to disagree
about whether work is outstanding.

Failure is retried with backoff to `attempts = 3`, then terminal. A `failed` or `too_large`
row is never retried by the subscriber and never blocks anything: the surfaces of §7 fall back
to the raw diff and say why.

## 5. Schema {#sec-5}

Migration `0010_reading_diffs`, listed in `deploy/base/kustomization.yaml`.

```sql
-- One row per (diff content, prompt surface, model). Keyed by the hash of the
-- diff rather than by (repo, pr) so a cherry-pick, a rebase or the same change
-- opened against two branches costs one abridgement.
CREATE TABLE reading_diffs (
    diff_sha256   text NOT NULL,
    -- meat's RubricHash: a hash of the RENDERED prompt surface, so rewording
    -- the instructions invalidates every cached result rather than silently
    -- serving output the current prompt would not produce.
    rubric_hash   text NOT NULL,
    model         text NOT NULL,
    speed         text NOT NULL DEFAULT 'standard',
    state         text NOT NULL,
    smart_diff    text,
    summary       text,
    input_tokens  bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    chunks        integer NOT NULL DEFAULT 0,
    attempts      integer NOT NULL DEFAULT 0,
    last_error    text,
    -- The day the tokens were spent, for the effective-dated rate lookup (§6).
    produced_day  date,
    requested_at  timestamptz NOT NULL,
    produced_at   timestamptz,
    PRIMARY KEY (diff_sha256, rubric_hash, model, speed),
    CONSTRAINT reading_diffs_state_known
        CHECK (state IN ('pending', 'ready', 'failed', 'too_large')),
    CONSTRAINT reading_diffs_ready_has_content
        CHECK (state <> 'ready' OR (smart_diff IS NOT NULL AND produced_day IS NOT NULL))
);

CREATE INDEX reading_diffs_pending ON reading_diffs (requested_at)
    WHERE state = 'pending';

-- What asked for it. Many sources may point at one reading diff; the row above
-- is content-addressed and knows nothing about repos.
CREATE TABLE reading_diff_sources (
    repo         text NOT NULL,
    pr_number    integer NOT NULL,
    head_sha     text NOT NULL,
    base_sha     text NOT NULL,
    task_id      text REFERENCES tasks (id) ON DELETE RESTRICT,
    diff_sha256  text NOT NULL,
    rubric_hash  text NOT NULL,
    model        text NOT NULL,
    speed        text NOT NULL DEFAULT 'standard',
    requested_at timestamptz NOT NULL,
    PRIMARY KEY (repo, pr_number, head_sha),
    FOREIGN KEY (diff_sha256, rubric_hash, model, speed)
        REFERENCES reading_diffs (diff_sha256, rubric_hash, model, speed)
        ON DELETE RESTRICT
);
```

`smart_diff` is `NULL` while pending and may be the empty string when ready: meat returns an
empty `SmartDiff` when nothing meaningful survives abridging, and that is a real answer —
"this change is entirely mechanical" — not a failure. Surfaces render it as such.

Rows are never garbage-collected in v1. A reading diff is small relative to the transcripts
already stored, and an expiry policy needs a retention argument this spec does not have; it is
listed in §12.

## 6. Cost {#sec-6}

Tokens are stored; **cost is derived**, exactly as `agent_session_usage` derives it (0008). The
lookup is `store.ModelPriceFor(model, speed, produced_day)`, so a reading diff produced in
March keeps pricing at March's rate however long it is cached, and no rate is ever written into
this table or into code.

`Result.InputTokens`/`OutputTokens` are all meat reports, so v1 prices the two headline classes
and does not split cache reads and writes. Under-reporting cache reads understates cost
slightly and never overstates it; a per-class split needs meat to expose the classes, so §12
defers it rather than assuming a ratio here.

**Attribution follows 027 §7's rule and lands on the task**, via `reading_diff_sources.task_id`
— the PR is already correlated to a task (011 §2), so no new correlation is invented. Cost is
attributed once, to the row's producer; a later source joining a cached row adds no cost,
because none was spent. `lode task cost` gains a line for it, distinct from session cost,
because tokens Worklode itself spent on a task are not tokens an agent spent working it and
merging the two would corrupt the number 012 exists to produce.

## 7. Surfaces {#sec-7}

| Surface | Behaviour |
|---|---|
| `GET /api/v1/pull-requests/{repo}/{number}/reading-diff` | The row's `state`, `summary`, `smart_diff`, `model`, tokens and derived cost. `pending` returns 202 with no body, so a client can poll without treating "not yet" as an error. |
| `lode pr read <repo>#<n>` | Renders the reading diff with the summary above it. `--raw` prints the unabridged diff. `--wait` blocks on a pending row. |
| `lode task show` | Prints the summary line when the task's PR has a `ready` row. |
| Web task page | Reading diff inline where the task has a PR, with a control to reveal the raw diff. |
| `lode task brief` for `kind = 'review'` | The reading diff is part of the brief a reviewer agent claims with (028 §8). |

Every one of these labels the artifact — `reading diff · <model> · N of M lines shown` — and
offers the raw diff. §1's rules are enforced at the surface, not left to the caller.

The brief entry is the one that matters most. 028 §8 routes review work by tier; a review task
whose brief carries the reading diff spends the reviewer's context on the decision instead of
on import churn, which is the same economy the tier split rests on.

## 8. Degradation {#sec-8}

With `LODE_READING_MODEL` unset the subscriber is not registered, the worker does not start,
the API endpoint returns 404, and every other surface renders exactly as it does today. This is
the same shape as embeddings without `LODE_EMBEDDING_URL`, and it is a hard requirement: no
deployment may be made to depend on an outbound model call to show a task.

A `failed` or `too_large` row degrades the same way, per-PR, with the reason shown.

## 9. Metrics {#sec-9}

Per 022's conventions and §8 of that spec — a nil-safe `Metrics` in `internal/reading/metrics.go`
with the registerer threaded from `serve.go`:

| Metric | Type | Labels / buckets |
|---|---|---|
| `worklode_reading_diff_requests_total` | counter | `outcome` ∈ `ready` \| `failed` \| `too_large` |
| `worklode_reading_diff_duration_seconds` | histogram | 5, 15, 30, 60, 120, 300, 600 |
| `worklode_reading_diff_cache_lookups_total` | counter | `result` ∈ `hit` \| `miss` \| `joined_pending` |
| `worklode_reading_diff_model_turns_total` | counter | `result` ∈ `ok` \| `error` |
| `worklode_reading_diff_tokens_total` | counter | `class` ∈ `input` \| `output` |
| `worklode_reading_diff_pending` | gauge | — (queue depth) |
| `worklode_reading_diff_chunks` | histogram | 1, 2, 4, 8, 16, 32 |

The cache-hit ratio is the one that decides whether this is affordable, and the chunk histogram
is the early warning that the §3 ceiling is set wrong: a distribution piling up at 32 means
diffs are arriving at a size where the cost is dominated by splitting.

As with the sweeper (022 §4) and embeddings (022 §6), a cancelled call at shutdown records
nothing; a deadline exceeded counts as an error.

## 10. Events {#sec-10}

One event type, emitted in the transaction that writes the terminal row, per 027 §4:

| Event | Emitted when |
|---|---|
| `wl:ReadingDiffProduced` | a row reaches `ready`, `failed` or `too_large` |

`ns/ontology.ttl` gains `wl:ReadingDiffProduced rdfs:subClassOf wl:Event`, with `wl:subject`
naming the pull request and per-type properties for the outcome, model, token counts and chunk
count. Per 025 §9 the term lands in `ns/` alongside this spec's implementation, and the Go
constant is generated from it.

No new domain class is added. A reading diff is a cached rendering of a fact the backbone
already owns (the diff of a correlated PR), not a fact anyone claims, so it gets no `wl:` class
of its own and nothing projects it to the knowledge graph. Modelling it as an object would put
a second owner on the change it renders, which the split in `CLAUDE.md` forbids.

## 11. Testing {#sec-11}

- **No test in the tree makes a model call.** `Abridger` is faked; the meat-backed
  implementation is exercised against a stub `meat.Model` returning canned tool calls.
- Store tests cover the cache identity (same diff bytes under two PRs produce one row), the
  pending-join path, `FOR UPDATE SKIP LOCKED` under two concurrent workers, retry to terminal,
  and that a `ready` row survives a rubric-hash change as a *miss* rather than a stale hit.
- Subscriber tests assert the handler acks without abridging, and that redelivery of the same
  event inserts no second row.
- Metrics: a fresh `prometheus.NewRegistry()` per 022 §7, plus the family check in
  `TestMetricsEndpointDomainFamilies`.
- Degradation: with no model configured, `lode serve` starts, the endpoint 404s, and the task
  page renders — asserted, because §8 is the property most likely to rot.
- e2e (`e2e/`, public surfaces only): a signed `pull_request` webhook, then the API endpoint
  polled to `ready` against a stubbed model endpoint, then the task page showing the summary.

## 12. Out of scope {#sec-12}

- **The review surface.** Threads, resolve, approve and the 028 §6 accept gate are a separate
  spec. This one produces an input to review and stops there.
- **Design documents.** meat is tuned for code; abridging a spec's prose diff is a different
  problem with a different failure mode, and specs are section-addressable (014 §7) so they
  already have a better decomposition.
- **Drift (007).** The two-layer diff is a different comparison that happens to share a word.
- **Retention.** Rows are kept forever in v1 (§5).
- **Per-class token accounting.** Follows meat exposing the classes (§6).
- **Local pre-processing.** `meat` as a developer's own CLI is orthogonal and needs nothing
  from Worklode.

## 13. Acceptance criteria {#sec-13}

1. `internal/reading` wraps meat behind `Abridger`; no worklode code re-implements the prompt,
   the edit plan or the chunker.
2. A `pull_request` `opened` event results in a `ready` row without any inline abridgement in
   the subscriber, and the subscriber's ack is not delayed by the work.
3. Two PRs carrying byte-identical diffs produce exactly one `reading_diffs` row and one
   model run.
4. A rubric-hash or model change produces a new row rather than serving the old one.
5. Cost for a reading diff is derived from `model_prices` at `produced_day`, appears on
   `lode task cost` as a line distinct from session cost, and is counted once.
6. Every surface that renders a reading diff labels it and offers the raw diff.
7. With `LODE_READING_MODEL` unset, every existing surface behaves exactly as before.
8. A diff over the ceiling records `too_large`, is not retried, and does not block the PR.
9. The metric families of §9 appear on `/metrics` and are asserted by `testutil`.
