---
status: draft
covers:
- docs/specs/046-workflow-rule-engine.md#sec-1
- docs/specs/046-workflow-rule-engine.md#sec-2
- docs/specs/046-workflow-rule-engine.md#sec-3
- docs/specs/046-workflow-rule-engine.md#sec-4
- docs/specs/046-workflow-rule-engine.md#sec-5
- docs/specs/046-workflow-rule-engine.md#sec-6
- docs/specs/046-workflow-rule-engine.md#sec-7
---
# Workflow rule engine — implementation plan

Implements spec 046: per-project first-match rules over the workflows of
spec 045, triggered by a new `wl:TaskTransitioned` domain event, with
prompt-carrying rules whose LLM answers are structurally bounded.

**This plan executes only after the per-project-workflows plan
(`2026-08-21-per-project-workflows.md`, which `blocks` this one) has
landed**: it extends `model.ProjectWorkflows`, `ValidateWorkflows`, the
workflow-aware guard, and the `…/workflows` PUT that plan creates. If that
plan's review changes those surfaces, revise this plan before accepting it.

Throughout: rule wire shapes live in `internal/model` (ADR 036), the
evaluator is a pure function (no store, no HTTP, no LLM handle), and no
model output is ever interpreted as a state name.

## Tasks

### Task 1 — Rule types and write-time validation

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

In `internal/model`: `WorkflowRule` (`Name`, `On` (`To`, `From`, `Actor`),
`When` (`Kind`, `Workflow`), `Prompt`, `Then` (`To`, `Choose`)), and
`ProjectWorkflows` gains `Rules []WorkflowRule`. Stdlib-only, wire field
names, `rule_test.go`/`deps_test.go` stay green.

In `internal/store/workflow.go`: `ValidateWorkflows` grows every 046 §2.2
check — rule count/name/slug/uniqueness, states in the vocabulary, **every
rule edge ∈ core edges ∪ entry table** (reusing Task 1 of the workflows
plan's `coreEdge` and entry table), `when` values valid, prompt/choose
arity rules — each refusal naming the offending rule. `rules` without
`workflows` and vice versa both valid.

- [ ] model types; one-model tests green
- [ ] validation table tests incl. the unstorable `ready → deployed_prod` rule

### Task 2 — `wl:TaskTransitioned` and `wl:WorkflowRuleFired` events

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

`ns/ontology.ttl`: two `wl:Event` subclasses; properties `wl:fromState`,
`wl:toState`, `wl:ruleName` (reuse `wl:subject`, `prov:wasInformedBy`).
Mirror in `internal/eventbus/vocab.go` (+ payload property table +
`vocab_test.go`), `riot --validate ns/*.ttl`.

Emit `wl:TaskTransitioned` in the same transaction as **every**
`state_log` append (`Transition` and the two-endpoint variants in
`internal/store/tasks.go`), payload per 046 §1.2. Store test: claim, hook,
resolver, manual, and edit paths each emit exactly one event with the
right edge and actor.

- [ ] ontology + vocab mirror + validation
- [ ] in-tx emission on every state_log path, with tests

### Task 3 — Pure evaluator: `internal/workflowrules`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

New package, shaped like `internal/watcher`: no store handle, no HTTP, no
LLM client. `Evaluate(rules, event, taskFacts)` returns the pass plan —
per candidate rule in order, `Matched` or `Ask{Frame, Schema}` — applying
046 §3.1 steps 1–3's pure parts (trigger match, `when` filters,
engine-actor skip). A strict answer parser implements 046 §4.2: accepts
exactly `{"match": bool}` / `{"choice": index|null}`, rejects everything
else as `unparseable`. Prompt-frame builder implements 046 §4.1 (field
list, 2000-char body cap, untrusted-content delimiters).

Table tests: first-match ordering, prompt rules interleaved with
structured rules, filter semantics, parser accept/reject matrix
(free text, out-of-range index, state name, JSON-in-prose).

- [ ] Evaluate + plan types with table tests
- [ ] strict parser with accept/reject matrix
- [ ] frame builder with caps and delimiters

### Task 4 — LLM client: `internal/llm`

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Minimal OpenAI-compatible chat-completions client shaped like
`internal/embed.OpenAI`: full endpoint URL, key, model from
`LODE_RULE_LLM_URL` / `LODE_RULE_LLM_KEY` / `LODE_RULE_LLM_MODEL`;
temperature 0, small max_tokens, per-call timeout (default 20s,
configurable); nil-safe `metrics.go` with
`worklode_rule_llm_calls_total{outcome}` and
`worklode_rule_llm_duration_seconds`, registered from `serve.go`.
Unconfigured client returns a typed `unconfigured` error. `httptest`
coverage for success, timeout, HTTP error, garbage body.

- [ ] client + config + typed errors
- [ ] metrics with tests

### Task 5 — Executor: the `workflow-rules` subscriber

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3, 4]
```

Second eventbus subscriber beside `docwatch.go` in `internal/api`, started
under `BackgroundCtx` only. Per `wl:TaskTransitioned` event: skip
engine-actored/deleted/rule-less (046 §3.1); snapshot facts; walk the
Task 3 plan, calling the LLM on `Ask` with 046 §4.3's failure→non-match
mapping and the 10-call budget; fire the first match via the store —
`Transition(from = on.to)` attributed to a `wl:WorkflowRuleFired` event
with dedup identity `workflow-rules` / `<event id>/<rule name>`, actor =
the engine's service actor. Stale and undeclared-edge refusals are benign
outcomes; offset commits after every pass.
`worklode_rule_passes_total{outcome}` in the owning package.

Tests (fake LLM): every failure outcome falls through; budget; cascade
guard; stale no-op; redelivery double-fires nothing (dedup + from-state).

- [ ] subscriber wiring + pass loop
- [ ] firing with provenance and dedup
- [ ] failure/idempotency test matrix + metrics

### Task 6 — Review task names rule changes

```yaml
kind: feature
priority: low
skills: [ ]
blockedBy: [1]
```

Extend the `review-on-workflow-change` rule (internal/watcher, minted by
the workflows plan's Task 5) so the task body also names added, removed,
and changed rule names, per 046 §5.4 / 045 §6.2 as amended. Extend `lode
project workflow validate` note in help text if it enumerates checks.

- [ ] body diff naming rules, with table test

### Task 7 — e2e: rules through the public surface

```yaml
kind: feature
priority: medium
skills: [ ]
blockedBy: [5, 6]
```

In `e2e/` (public surfaces only): PUT workflows+rules where a
`release-on-merge` rule takes `merged → released`; land a task's PR; the
task reaches `released` with a `wl:WorkflowRuleFired` event visible via
`lode event tail`. A prompt rule ordered first with no LLM configured
falls through to the structured rule behind it. Invalid rule
(`ready → deployed_prod`) is refused with the rule named.

- [ ] scenario green under `make test-e2e`
