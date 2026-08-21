# Ranking the cockpit's highest-signal exception

Research note, 2026-08-22 (WL-187). Question: the cockpit right rail's
"Highest-signal exception" card is not chosen — it is `SecondaryConcerns[0]`,
whatever `blockerConcerns()` (`internal/api/cockpit.go`) happens to emit first
for the first ready-and-blocked task, rendered by `exceptionCard` in
`internal/ui/cockpit.templ`. Picking it well plausibly needs a model call
(rank the concerns, write the evidence line), which would be wasteful per
render, so any model-backed refresh needs a per-project task-churn gate. What
should the churn metric be, and is the model call worth it at all?

**Short answer: the model call is not worth it, and the churn gate falls away
with it.** The exception is a pure function of facts the cockpit assembler
already fetches — the blocker edges, blocking plans, task states, priorities,
and last-transition timestamps in `store.ListProjectWorkFacts` — so a
deterministic root-cause ranking can be computed in-memory on every render,
always current, with zero extra queries, zero storage, and zero refresh
machinery. Replaying this repo's own project history (908 state/edge/priority
changes over 10.2 days of `state_log`, out of 31,663 events over 28 days)
shows the right answer changed 9 times — 0.88 changes/day — while 99% of
churn-weighted events left it unchanged, so *any* event-count gate in front of
a model call fires overwhelmingly for nothing. And on the live concern set (12
concerns, 5 root blockers), the deterministic rule's pick coincides with
expert judgment: the one unclaimed review task that has held the entire
spec-029 planning chain — four tasks, one of them high priority — for 7.2
days. There is nothing left for a model to decide; the one thing it would add
is phrasing, and the evidence sentence is templatable from the computed facts.

Recommendation: **fix the ranking deterministically (rule det-v1, §4);
decline the model call and the churn gate, with a named trigger** (§6). The
follow-on is a `kind: feature` task, not a spec amendment — filed as
**WL-280**: order
`SecondaryConcerns` by det-v1 in `assembleProjectCockpit` and template the
evidence line from the root-cause computation. §7 records the model-gated
design anyway — prompt input, output contract, storage, loop, provider
surface, metrics — so that if the trigger fires, the spec amendment can be
written from this note without redoing the measurement.

Method: one empirical pass on 2026-08-22 against the live dev backbone
(`worklode.dev.sunstoneinstitute.ai`), read-only, through public API surfaces
only: the full event log paged via `GET /api/v1/events?after=` (31,663 events,
2026-07-24 → 2026-08-21), all 279 tasks of project `worklode` with their full
timelines via `GET /api/v1/tasks/{id}/timeline` (1,219 `state_log` entries:
910 state transitions, 254 edge changes, 3 priority changes), and the live
cockpit projection via `GET /api/v1/projects/worklode/cockpit`. The replay
reconstructs task states by rewinding current state through the recorded
transitions, then replays forward, recomputing the deterministic answer and a
candidate churn score after every event. Conclusions marked **Synthesis** are
ours.

Caveats: the replay covers the task-edge blocker subgraph only — plan-ordering
blockers (025 §9.3, the source of today's WL-66 → WL-22 chain) enter the live
snapshot analysis (§2) but not the replay (§3), because doc-edge history is
not exposed per task. Including them could only *lower* the measured
answer-change rate: that chain has been the stable top answer since ~08-14,
and several of the 9 measured flips would have happened beneath it.
`state_log`-backed history starts 2026-08-11, so the replay spans 10.2 days
inside the 28-day event log. The LLM side of §5 was adjudicated against the
live concern set, not executed — there is no completion path in the repo to
A/B, which is itself part of the cost being weighed.

---

## 1. What the card actually shows today

`assembleProjectCockpit` walks `ListProjectWorkFacts` in its fixed order —
priority rank, then project key, then numeric id — and appends
`blockerConcerns(f)` for every ready-and-blocked task. The web page renders
`SecondaryConcerns[0]` as the "Highest-signal exception". So the pick is: the
first open blocker (in SQL result order) of the highest-priority,
lowest-numbered blocked task.

That is half an accident and half a heuristic. Ordering blocked tasks by
priority means the card tends to name a blocker of *important* work — today it
shows "WL-66 blocks WL-22", which §2 shows is also the genuinely right answer.
But the coincidence is fragile: the replay (§3) shows the incumbent pick
disagreeing with the deterministic right answer for **10% of wall-clock time**
over the replay window (26 hours of 10.2 days), and its within-task blocker
choice is unordered SQL output. It also cannot see the strongest signal there
is — that one blocker transitively holds four tasks — because it never looks
past the first pair.

## 2. The live concern graph, ranked

On 2026-08-22 the project has 12 secondary concerns (blocker → blocked
pairs), which root-cause analysis collapses to 5 root blockers — a root being
a blocker that is not itself blocked, i.e. the thing someone could actually
act on:

| root | state | kind | transitively holds | best held priority | oldest held blocked for |
|---|---|---|---|---|---|
| WL-66 | ready | design | WL-22, WL-23, WL-49, WL-50 | high | 7.2 d |
| WL-241 | draft | chore (human) | WL-240, WL-206, WL-225, WL-231 | medium | 1.2 d |
| WL-242 | draft | design | WL-212 | medium | 1.1 d |
| WL-193 | ready | review | WL-103 | medium | 0.1 d |
| WL-248 | draft | chore | WL-192 | low | 0.9 d |

The graph has real structure the flat concern list hides: WL-66 → WL-22 →
{WL-23, WL-49} → WL-50 is a four-deep planning chain whose sole unblocked
head is an unclaimed review task, and WL-240 — which the flat list names
three times as a blocker — is itself blocked by the human provisioning task
WL-241, so naming WL-240 as the exception would name a symptom. Ranking roots
by (best held priority, fan-out, age) — rule det-v1, §4 — puts WL-66 first by
every component, and correctly demotes WL-240 in favour of WL-241.

**Synthesis:** this table *is* the head-to-head of §5. Ask what a competent
staff engineer — or a frontier model given the same evidence — would name as
the highest-signal exception, and the answer is WL-66, for exactly the
reasons the three score components encode: it holds the most work, the most
important work, the longest, and it is actionable now. The ranking problem
is mechanical once the graph is computed.

## 3. How often does the right answer move — the replay

Replaying the 908 recorded state transitions, blocks-edge changes, and
priority changes forward from 2026-08-11, recomputing det-v1's answer after
every event:

- **9 answer changes in 10.2 days — 0.88/day.** Six of the nine landed in one
  36-hour burst (08-20/08-21) when the blob-storage blocker chain was being
  wired up; whole days pass with none.
- **Every answer change was triggered by a churn-classified event** (a
  blocks-edge add/remove, a transition into/out of `ready` or into a closed
  state, or a priority change). Zero changes came from events outside those
  classes — the class list in §4 is complete.
- **99% of churn-classified events left the answer unchanged** (787 of 796).
  Churn volume and answer movement are almost uncorrelated day to day: 08-19
  had 82 churn events and zero answer changes; the answer's busiest day,
  08-20, had 302.
- The incumbent arbitrary pick changed 7 times over the same window and
  matched det-v1's answer 90% of the time — agreeing mostly because this
  project's blocked set is small. The 10% disagreement windows are precisely
  the multi-chain moments the card exists for.

Answer stability also follows analytically, not just empirically: det-v1 is a
pure function of the declared graph, and between mutating events the graph
does not change. (Age is a tie-breaker *across* concerns, and all concerns
age at the same rate, so time alone never flips the order.) The answer moves
only at events; everything between is free.

## 4. The churn metric — defined, validated, and demoted

The churn signal the task brief asked for, concretely. Per project, classify
events (equivalently `state_log` entries, which carry the entity id that
several event payloads lack — see §8) and weight:

| class | weight | rationale |
|---|---|---|
| `blocks` edge added/removed (`task_edges` and `doc_edges` type `blocks`) | 4 | rewires the concern graph directly |
| state transition into or out of `ready`, or into a closed state (`merged`, `deployed_*`, `released`, `abandoned`) | 2 | changes blocked-set or open-blocker membership |
| task priority change | 2 | changes the score of every chain holding it |
| doc status change on a plan (025 §9.3) | 2 | plan-ordering blockers appear/vanish |
| any other task state transition | 1 | changes evidence strings, not the pick |
| everything else (webhook noise, lease renewals, body edits) | 0 | cannot affect the answer |

Validation against the replay: the weighted classes catch 100% of answer
changes (no zero-weight event ever moved the answer), so as a *safety* gate
the metric is sound. As an *efficiency* gate it is poor — that same replay
shows the false-positive problem no threshold fixes:

| gate | refreshes/28d | per day | max staleness |
|---|---|---|---|
| every weighted event | 796 | 77.7 | 0 |
| weight ≥ 1, 10 min debounce | 217 | 21.2 | 34 min |
| weight ≥ 6, 30 min debounce | 96 | 9.4 | 57 min |
| weight ≥ 20, 60 min debounce | 49 | 4.8 | 57 min |

Against 0.88 real changes/day, even the bluntest gate wastes ~80% of its
refreshes, and the worst observed day (08-20: 302 churn events, weight 568)
would drive 20–30 model calls under any threshold loose enough to keep
staleness under an hour.

The gate that actually works is not a count but a **fingerprint**: recompute
the deterministic concern set (cheap, in-memory) on each weighted event and
invoke the expensive step only when the set changes. That fires 21 times in
10.2 days — 2.1/day, worst observed day 17 — a ~10× improvement over any
weighted threshold at zero staleness. **Synthesis:** the honest reading is
that the only good gate for a model call already contains the deterministic
computation, at which point the deterministic computation *is* the answer and
the model call defends only its phrasing.

## 5. Deterministic vs. model — the verdict

What a model call could add over det-v1, examined against the live graph:

- **Picking a different concern.** No case found. Root-cause + fan-out +
  priority + age reproduces the expert pick on the live set, and nothing in
  the 9 replayed answers turns on semantics the graph does not carry.
- **The evidence sentence.** det-v1's pick comes with its own explanation —
  root, state, what it holds, for how long — and the sentence is a template
  over computed facts: *"WL-66 (ready, unclaimed) has held 4 tasks for 7
  days — WL-22 (high) → WL-23, WL-49 → WL-50. Reviewing the spec-029 plan
  split unblocks the chain."* No judgment is being exercised that the
  computation has not already done.
- **Semantic judgment the graph lacks** — e.g. recognising that a `low`
  chore actually gates a release, or that two blockers are the same
  underlying problem. Real, but marginal on observed data (the WL-240/WL-241
  near-duplicate is already resolved structurally by root-finding), and it
  buys a new provider dependency, credentials in the server environment, a
  storage row, a background loop, and the §4 gate.

Verdict: **deterministic wins, plainly.** This is not "close enough" — on
everything measurable the deterministic ranking is not an approximation of
the model's answer; it is the same answer, cheaper, explainable, and always
current. The task becomes a ranking fix.

## 6. The recommended fix, and the trigger

Order, don't sample: in `assembleProjectCockpit`, build the concern pairs as
today, then rank them by det-v1 — score each root `(best priority among
transitively held tasks, fan-out, oldest held blocked-since)`, order concerns
by their root's score, within a root by the directly-blocked task's priority
— and emit `SecondaryConcerns` in that order. `SecondaryConcerns[0]` then
*is* the highest-signal exception, per render, always current. Blocked-since
comes from `ProjectWorkFact.StateEvent.At` (the newest state transition — for
a ready-and-blocked task, when it became ready), already fetched. The
computation is O(edges) over ~tens of concerns; no new queries, no storage,
no loop, no gate. The evidence line becomes the §5 template; its category
stays `declared` — it is derived from declared facts, and spec 032's
`recommended` stays reserved for AI-produced content.

"Auto-refresh" needs no machinery either: a projection computed per render
refreshes exactly when the page does, and since the answer only moves on
events (§3), a rendered page is stale only in the way every cockpit fact
already is.

Trigger to revisit the model call — either of:

- **The evidence-sentence template reads wrong in practice** — leads report
  the card names the right task with a misleading explanation — *and* the
  fix is judgment, not a better template.
- **A completion provider lands in the backbone for some other reason**
  (e.g. spec-029 research summarisation), making the marginal cost of §7 a
  prompt file rather than an integration.

Then build §7 as specified, gated on the fingerprint, never on render.

## 7. Contingency: the model-gated design, if the trigger fires

Recorded so the spec amendment can be written from this note. Follow the
`doc-lifecycle` pattern exactly: pure rules in `internal/watcher`, executor
in `internal/api` as an `internal/eventbus` subscriber, started only when
`NewServer` gets a `BackgroundCtx`.

- **Loop.** Subscriber `exception-refresh` consumes the log. Pure rule
  (in `internal/watcher`): classify the event per §4's table; on weight > 0,
  signal "recompute fingerprint for project P". Executor: recompute det-v1's
  concern set for P (one `ListProjectWorkFacts` call); if the fingerprint —
  a hash of the ordered (blocker, blocked, blocker-state, priority) tuples —
  matches the stored row, stop (`skipped_unchanged`); else call the model,
  debounced to one call per project per 10 minutes (worst observed need:
  17/day, §4).
- **Prompt input.** The det-v1-ranked concern table of §2 — root, state,
  kind, held tasks with priorities and blocked-durations, plus each task's
  title and one-line body excerpt. Bounded: top 10 roots, titles truncated.
  The model re-ranks and writes the sentence; it never discovers concerns.
- **Output contract.** Strict JSON: `{"concern": "<blocker-id>/<blocked-id>",
  "summary": "<one line, ≤200 chars>"}` — the pair must be one the input
  named, else the response is discarded and det-v1's own pick stands
  (`outcome="error"`, fallback is always available).
- **Storage.** A projection row, written through the event log for
  provenance: the executor calls `RecordEvent` (source `watcher`, external id
  `exception-refresh:<project>:<fingerprint>` — idempotent per graph state,
  the same two-layer idempotency `docwatch.go` documents) whose apply upserts
  one row per project: project, fingerprint, concern ref, summary, model id,
  event id, computed_at. Render reads the row when its fingerprint matches
  the freshly computed one, else falls back to det-v1 live. The evidence
  category of a model-written summary is `recommended` (032) — the reserved
  category finally earns its keep.
- **Provider surface.** `internal/complete`, a sibling of `internal/embed`
  with the same shape: one small `Provider` interface, an OpenAI-compatible
  chat implementation, nil-safe metrics. Config mirrors the embedding
  precedent in `serve.go`: `LODE_COMPLETION_URL`, `LODE_COMPLETION_MODEL`,
  `LODE_COMPLETION_API_KEY`; unset means the subscriber never starts and the
  cockpit stays deterministic. Pricing joins the effective-dated
  `model_prices` rows — never hardcoded.

## 8. Metrics (spec 022)

For the recommended deterministic fix: **no new series.** No new endpoint,
loop, outbound call, or store operation is added; assembly outcomes are
already observed by `worklode_cockpit_projection_requests_total`.

For the §7 contingency, the gate needs four series (owning package, nil-safe
struct, bounded labels — project ids are bounded in practice but should be
capped or hashed if project count grows):

- `worklode_exception_churn_events_total{project, class}` — counter, §4's
  classes as the bounded label; the raw churn signal.
- `worklode_exception_refreshes_total{project, outcome}` — counter, outcome ∈
  `refreshed | skipped_unchanged | skipped_debounce | error`; the gate's
  effectiveness is the `skipped_*`-to-`refreshed` ratio, and the replay
  predicts roughly 40:1 for weighted-events-to-model-calls.
- `worklode_exception_refresh_tokens_total{project, direction}` — counter,
  direction ∈ `input | output`; actual refresh cost, priced through
  `model_prices` like agent sessions.
- `worklode_exception_staleness_seconds{project}` — gauge, now minus the
  stored row's computed_at while its fingerprint is behind the live one;
  zero when current. The §4 simulation says a healthy loop keeps this under
  the debounce interval.

## 9. What we did not establish

- No actual LLM was invoked in §5; the adjudication is judgment over the
  live snapshot, not an A/B over the replay. Given the 9-change sample and
  the structural argument, running one would mean building the §7 loop to
  disprove needing it.
- Plan-ordering blocker *history* was not replayed (§3 caveat); the live
  snapshot includes it. The direction of the bias is known (answer-change
  rate is overstated, not understated), which is why it does not threaten
  the recommendation.
- Whether det-v1's tie-breakers matter in bigger projects: this project's
  concern graph (12 pairs, 5 roots) never produced a tie the rule could not
  split. A project with hundreds of blocked tasks may want the fan-out
  component capped or log-scaled.
- A provenance gap noticed in passing: several event payloads
  (`lease.claimed` is null; `task.updated`/`task.done` often carry no task
  id) are unattributable from the events API alone — `state_log.entity_id`
  is what saves the analysis. Filed as WL-281 rather than fixed here.
