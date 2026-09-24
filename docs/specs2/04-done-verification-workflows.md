# Done, verification and workflows

Definition of done is one idea carried by two peer objects. A **deliverable declaration** carries the evidence side: what closes a task. A **workflow** carries the state side: which task states a project uses and which transitions between them are legal. A **rule** is a project's standing instruction for when to take one of those legal transitions. On top of these, a task's delivery state carries a **provenance** (observed or asserted), and a delivered bug mints a **verification task** so that "is it actually fixed" becomes someone's job. The task state vocabulary and the transition mechanics are defined in 03-tasks-and-execution.md. Permissions are defined in 02-identity-actors-and-secrets.md. Deliverable rows themselves (`<KEY>-DEL-<n>`) and their reported state are defined in 03-tasks-and-execution.md.

## 1. Done declarations

A **done declaration** is an ordered list of **done references**. Both a project and a task carry one. A done reference is either a deliverable id (`<KEY>-DEL-<n>`, a row in `deliverables`) or one of exactly two **reserved references**, `merge` and `manual`. Reserved references are checks with no row behind them. They are lowercase and deliverable ids are uppercase with a `-DEL-` infix, so the two never collide. The reserved set is closed. A third reserved word needs a spec amendment.

Merge is a reserved reference because a deliverable is a named output the project ships once, while a merge is a fact about one task's own branch.

### 1.1 Resolution

A task's **effective** declaration resolves in one step:

1. The task's own list, when non-null. An empty list is a legal, explicit "this task declares nothing" and does not fall through.
2. Otherwise the project's default list.

An override replaces the default. It never extends it. A task that wants the default plus one more writes both. When the effective list is empty, the brief's `definition_of_done` is null and the task body is the contract.

### 1.2 Project defaults

Set at project creation:

| Project | Default |
|---|---|
| Has at least one repo mapping | `[merge]` |
| Has no repo mapping | `[manual]` |

A project that ships a named output adds that deliverable's id to the default (when every task serves one output) or puts it on the tasks that produce it (when the project ships several). Neither is inferred.

### 1.3 Storage, API, CLI

```sql
projects.default_deliverables text[] NOT NULL DEFAULT '{}'
tasks.deliverables            text[]           -- NULL = inherit
```

`text[]` because the list is ordered, short, and mixes two reference kinds. There is no CHECK constraint: a reference is valid only against the deliverables of its own project, which the store checks (`store.ValidDoneRef`). It rejects an unknown reserved word and a deliverable id from another project.

| Surface | Behaviour |
|---|---|
| `PATCH /api/v1/tasks/{id}` | `deliverables *[]string`: null means inherit, `[]` means explicit empty, a list is an override |
| `PATCH /api/v1/projects/{id}` | `default_deliverables` |
| `GET /api/v1/tasks/{id}/brief` | `deliverables` array and a populated `definition_of_done` (section 2) |
| `lode task edit <id> --deliverable <ref>` | repeatable, replaces the list |
| `lode task edit <id> --deliverable-inherit` | sets NULL |
| `lode task edit <id> --deliverable-none` | sets the empty list |
| `lode project set default-deliverables <ref>...` | replaces the list |

## 2. The brief

The brief resolves the effective declaration and carries both halves, computed at brief time and stored nowhere:

- `definition_of_done`: prose, one line per reference, non-null whenever the effective list is non-empty. A deliverable reference renders as its name, description and URL. A reserved reference renders as the sentence stating its check.
- `deliverables`: the resolved list in structured form: reference, kind (`deliverable` | `merge` | `manual`), and for a deliverable row its name, description and URL.

## 3. The done check

`lode task close` **asserts and reports. It never refuses.** This holds for all three reference kinds. What varies is what `close` does on the way through.

| Reference | Behaviour |
|---|---|
| `merge` | Runs the local merge probe (`internal/hookrun/localmerge.go`'s `landedNow`: `git merge-base --is-ancestor`, falling back to `git cherry` for a squash) in the task's worktree. Found: reports it through `POST /api/v1/merges` exactly as the `post-merge` hook does, so the fact lands as a `task_commits` row with source `local_merge` and passes through `ResolveDelivery`. Then the task closes. Not found: the task still closes and `close` prints a warning naming the branch, the default branch probed, and that no merge was found. |
| `manual` | Closes the task and writes only the ordinary event and `state_log` rows. No probe, no warning. |
| deliverable id | Neither probes nor reports deliverable state. Prints each declared deliverable's current reported state and warns when one the task claims to produce is still unreported. |

The merge probe warns because it is heuristic (squashes, stale worktrees and queued merges all produce false negatives) and because it runs on the client, where the clone is. The server cannot re-run it, so a server-side refusal would be the same assertion with more steps. A per-project strictness setting is deliberately not built.

The probe reuses `worklode_local_merge_reports_total{result}`. `close` accepts a task in `ready`, `in_progress` or `in_review` and refuses `draft`.

## 4. Lease and worktree retention

A lease and its worktree live until the task is done. Neither is tied to a merge, a review or a deploy. A task declaring `merge` is not done until the merge landed, so the worktree survives review. A task declaring `manual` is done when the actor says so, and the worktree goes at the same moment. The sweeper keys on the lease only.

## 5. External evidence routing

An event from an outside service (data catalog, CMS, publisher) names an address or label. Routing it to tasks is two hops: address or label to deliverable (the deliverable declaration), then deliverable to the open tasks whose effective declaration names it. Open means not abandoned and not at or past the repo mapping's `done_state`. The second hop is why the effective declaration must be computable for any task, including one that is not being briefed.

## 6. Provenance of a delivery state

`state` is one field carrying two things. Delivery (`in_review`, `merged`, `deployed_dev`, `deployed_prod`, `released`) is factual and arrives from GitHub and Flux. Resolution, whether the reported problem is gone, is a judgment. A second `resolution` enum is declined: it would be a second state machine with its own transitions, reopen semantics and cockpit column, and would make every "is this task open" query ambiguous. Resolution is tracked as work (section 7).

Each task carries the provenance of its current state:

| Value | Meaning |
|---|---|
| `observed` | A recorded fact produced this state: a `task_commits` row, a deploy frontier, a release frontier. |
| `asserted` | An authenticated actor set it. No fact in the backbone backs it. |

Who writes which:

- `ResolveDelivery` always writes `observed`. It moves a task only on a fact it just read.
- `closeTask` writes `asserted`. When the merge probe succeeds, the merge is reported first and the resulting state came from `ResolveDelivery`, so it is already `observed`. The `asserted` write is the failed-probe case and the `manual` case.
- Reopen resets provenance along with `task_commits`.

`ResolveDelivery` does not advance a task whose current state is `asserted`. The blast radius of a premature close stops at the asserted state. When a real fact arrives, the resolver writes `observed` and promotes from there.

`lode task show`, `lode task list`, the brief and the cockpit render an asserted state as such: `merged (asserted)`.

Provenance separates an assertion from an observation. It does not make an observation true. A fact reporter that misreads its input still produces an `observed` state.

## 7. Verification tasks for bugs

When a task with `kind = bug` reaches its repo mapping's `done_state` with provenance `observed`, the eventbus mints exactly one task:

| Field | Value |
|---|---|
| `kind` | `review` |
| `state` | `ready` |
| `title` | `Verify WL-<n> no longer reproduces` |
| edge | `follow_up_to` the bug |
| body | section 7.1 |

The edge is `follow_up_to` because it is provenance only. `child_of` is unusable: a task with children may not occupy `in_review` or any `deployed_*` state, and this task is minted when the bug has reached one.

The mechanism is the doc-lifecycle one: a pure `Evaluate(event) -> []Action` rule in `internal/watcher`, an executor in `internal/api`, `prov:wasInformedBy` back to the causing event, and two idempotency layers: `external_id = <subscriber>:<rule>:<event-id>` against redelivery, and a guard that no open verification task already references this bug. Minting the prompt is not performing the act. Nothing here verifies, closes or blocks anything.

Only `bug`, because a bug body carries a reproduction a second party can run. The kind set is a constant in the rule. Only at `done_state`, because minting at every hop would mint up to three prompts per bug, and minting at `merged` would ask for verification against a deployment that has not happened. A repo whose `done_state` is `merged` mints on merge. Not for an `asserted` state, because there is no commit or environment to verify against. The prompt mints when the real fact arrives.

### 7.1 What the verification task states

An assertion with no provenance is not evidence. The body carries, resolved at mint time:

- the reproduction, quoted from the bug's body;
- the landed commits (`task_commits`) the fix arrived in;
- the environment whose frontier covers them, and the state reached;
- for a repo that ships a client binary, the client build the verifier must be on, as a commit checkable with `lode --version`.

Closing the verification task is an ordinary `close` under the `manual` reference. A verification that fails ends in `lode task reopen` on the bug, and in no state on the verification task. Re-running a reproduction automatically is out of scope.

## 8. Workflows

A **workflow** is a project's declaration of which task states exist for it and which transitions are legal. It is the authority on the task state machine. The built-in `default` workflow is what every project runs until it opts in.

### 8.1 Vocabulary versus machine

The **state vocabulary** is global and fixed: `draft`, `ready`, `in_progress`, `in_review`, `merged`, `deployed_dev`, `deployed_prod`, `released`, `abandoned`. It is enforced by the `tasks.state` CHECK constraint and `ns/shapes.ttl`'s `sh:in` list, which are never made per-project. A workflow selects a subset and an edge set over it. It never mints a state name. This is what keeps code that enumerates vocabulary subsets (delivery ranks, "open" filters, reopenable states) sound in every workflow: a workflow can only omit states.

### 8.2 The mandatory core

Five states are mandatory in every workflow: `draft`, `ready`, `in_progress`, `merged`, `abandoned`. A workflow that omits any is invalid. The **core edges** are defined over the whole vocabulary and legal in every workflow:

| Core edge | Meaning |
|---|---|
| `draft -> ready` | publish |
| `ready -> in_progress` | claim / start |
| `in_progress -> ready` | release, lease expiry, stop |
| `in_review -> in_progress` | rework |
| `ready`, `in_progress`, `in_review` `-> merged` | work landed, or manual `close` |
| `draft`, `ready`, `in_progress`, `in_review` `-> abandoned` | abandon |
| `merged`, `deployed_dev`, `deployed_prod`, `released`, `abandoned` `-> ready` | reopen |

Every core edge targets a mandatory state. Invariant: **a task can always leave its state along a core edge. A task can only enter an optional state its workflow declares.** This is also the stranding rule: a task left in a state its workflow no longer declares still has its core exits.

### 8.3 Optional states and declared entries

The optional states are the remaining four. The only edges a workflow chooses are the entries into them:

| Optional state | Legal entry edges |
|---|---|
| `in_review` | `in_progress -> in_review` |
| `deployed_dev` | `merged -> deployed_dev` |
| `deployed_prod` | `merged -> deployed_prod`, `deployed_dev -> deployed_prod` |
| `released` | `merged -> released`, `deployed_dev -> released` |

This table is the full set of legal entries and is a global invariant. `deployed_prod -> released` is illegal everywhere, and nothing can create `ready -> deployed_prod`. When a workflow declares an optional state and omits `transitions`, all entries from this table among its declared states are implied.

### 8.4 The default workflow

The built-in workflow `default` declares all nine states and all implied entries. Its edge set is the core edges plus every entry of 8.3. A test pins its edge set. Workflow names are slugs (`[a-z0-9-]{1,40}`) with an optional one-sentence `description`. `default` is reserved and cannot be redefined.

### 8.5 The guard

`Transition(tx, now, taskID, from, to, eventID)` checks membership of the edge and that the task's current state equals `from`, atomically, appending `state_log`. Membership is:

```
legal(task, from, to) = coreEdge(from, to) OR (from, to) in workflowFor(task).entries
```

`workflowFor(task)` is resolved inside the same transaction. Wrong from-state and illegal edge both return `ErrBadTransition`. The error names the governing workflow when the edge exists in the vocabulary but the workflow does not declare it.

### 8.6 Which workflow governs a task

Resolved at transition time:

1. `tasks.workflow`, a per-task override naming one of the project's workflows (`lode task edit --workflow <name>`).
2. The project's `default` entry in `projects.workflows`.
3. The built-in `default`.

Editing a workflow applies immediately to every task governed by it. A `tasks.workflow` naming a workflow that no longer exists resolves to the project default. Initial state is not workflow business: every task is created `ready`, or `draft` with the `Draft` flag.

### 8.7 Storage

`projects.workflows jsonb NULL` and `tasks.workflow text NULL`. NULL means built-in default only, no automations. It is a JSON column because it is consulted inside the transition transaction.

```json
{
  "default": "service",
  "workflows": {
    "service": {
      "description": "PR review, then prod straight from main",
      "states": ["draft", "ready", "in_progress", "in_review", "merged", "deployed_prod", "abandoned"]
    },
    "dataset": {
      "description": "datasets are released, not deployed",
      "states": ["draft", "ready", "in_progress", "in_review", "merged", "released", "abandoned"],
      "transitions": [["merged", "released"]]
    }
  },
  "automations": []
}
```

| Key | Meaning |
|---|---|
| `default` | The name a task with no override resolves to. May name the built-in `default`. |
| `workflows` | Named definitions: `states` (must include the core) plus optional `transitions`. When present, `transitions` is the complete list of declared entries. Core edges are never listed. |
| `automations` | The ordered automation list (section 10). Optional and orthogonal to `workflows`. |

### 8.8 Validation

Enforced in the store on every write of `projects.workflows` and `tasks.workflow`:

- `states` is a subset of the vocabulary, includes the mandatory core, no duplicates.
- `transitions` is a subset of the 8.3 entry table, restricted to declared states.
- Every declared optional state has at least one entry edge, implied or listed.
- `default` names a defined workflow or the built-in `default`.
- Removing or renaming a workflow that open tasks reference by name is refused, naming the tasks. Closed tasks may dangle.
- Names match `[a-z0-9-]{1,40}`.
- A repo mapping's `done_state` must be a state of the project's default workflow. A mismatch is a warning and the write succeeds.
- The `automations` key validates per section 10.2.

A workflow change never moves a task.

### 8.9 State readers

Every reader of task state falls in one of four classes:

| Class | Meaning | Examples |
|---|---|---|
| Core | Depends only on mandatory states and core edges. Correct in every workflow. | `Claim`, lease sweeper, `StartTask`/`StopTask`, ranking, `CreateTask`, `close`, `abandon`, `reopen`, hierarchy roll-up |
| Vocabulary-static | Enumerates a vocabulary subset as a prefilter or ordering. Sound because workflows only omit states. | `deliveryRanks`, `deliveredStateSet`, `taskClosed`, `TasksBelowFrontier`, `mergeCandidateStates`, "open" status filter |
| Workflow-resolved | Consults the governing workflow at runtime. | `ResolveDelivery`'s tail steps, the PR-opened hook |
| Display | Unrecognised states render raw. | Cockpit buckets, `StateChip`/`StateLabel`, CLI help |

The two workflow-resolved readers: `ResolveDelivery`'s pre-merge jump is core; its tail steps advance only along declared entries. A dev frontier does nothing for a workflow without `deployed_dev`, and the task moves `merged -> deployed_prod` directly when that entry is declared and the prod frontier covers it. The PR-opened hook's `in_progress -> in_review` is attempt-and-tolerate: where the workflow declares no `in_review`, the guard refuses and the hook drops the transition silently.

Hierarchy never consults a workflow. A task with children moves only among core states, and `containerForbiddenStates` is every non-core state (`in_review` plus the delivery tail), because those states are earned by facts about a commit a container does not have.

### 8.10 Changing a workflow

| Surface | Behaviour |
|---|---|
| `GET /api/v1/projects/{id}/workflows` | The stored object, or the implied built-in default when NULL. |
| `PUT /api/v1/projects/{id}/workflows` | Full-object replace, validated, evented. Guarded by `workflow.write`, granted to `user` and `admin`. |
| `lode project workflow [--json]` | Read. |
| `lode project set workflow --file <json>` (`-` for stdin) | Write. |
| `lode project set workflow --file <json> --dry-run` | Client-side validation against the same rules, no write. |
| `lode task edit --workflow <name>` | Per-task override. |

Authorship is not admin-gated because the review task is the control. Every accepted PUT appends a `project.workflows_set` event carrying the full new object and the actor. A watcher rule (`review-on-workflow-change`, peer of `doc-lifecycle`) mints a `review` task, "Review workflow change on <project>", naming the changed workflow names and the rules added, removed or changed. It is suppressed while an open review task from this rule exists for the project. **The change is live when the PUT commits. The review does not block it.** Rejecting a change is another evented PUT.

LLMs may propose and maintain workflows under that review. Containment is structural: the core is not editable, entries come from a fixed table, and automations have no write path to `projects.workflows`.

## 9. Relation to `done_state`

`project_repos.done_state` is the per-repo marker for "fully delivered", feeding `taskClosed` and the resolver's frontier logic. Workflow owns which edges exist. `done_state` must be a state of the governing workflow, and disagreement is a warning on the write. A task's definition of done is its deliverables and its workflow's delivered states together.

## 10. The automation engine

An **automation** is a project's standing instruction for when to take one of its workflow's legal transitions. Automations are an ordered list per project, evaluated first-match-wins against the events the backbone already records, firing at most one transition per triggering event. **Automations choose which legal edge to take and when, never which edges exist.**

Automations cover what the hardwired movers (claiming, the PR-opened hook, `ResolveDelivery`, manual transitions) do not: policy at joints with more than one legal continuation, or where no observed fact ever arrives, such as `merged -> released` for a CLI project with no Flux frontier. Automations are conveniences. Every core edge stays manually takeable, so a broken automation or an unavailable LLM degrades to "nobody moved the task automatically".

### 10.1 Trigger, ordering, cascades

Every committed transition, whatever caused it, emits a **`wl:TaskTransitioned`** domain event in the same transaction as its `state_log` row: `wl:subject` the task, `wl:fromState`, `wl:toState`, `prov:wasAssociatedWith` the actor. This is the whole trigger surface. Keying off transitions lets automations compose with every existing mover without duplicating webhook correlation.

For each event, the engine evaluates the task's project automations in array order and fires the action of the first automation whose trigger and condition both hold. No match is a no-op. The subscriber consumes events in log order, so evaluation is totally ordered and nondeterminism enters only through an automation's own prompt.

Automation-fired transitions are attributed to the engine's own service actor, and **the engine never evaluates an event whose actor is itself.** One event, at most one automation-fired transition. A project wanting `merged -> deployed_prod -> released` automated writes one automation per hop and lets a non-automation mover supply the intermediate trigger.

### 10.2 Automation shape and validation

```json
{
  "name": "release-on-merge",
  "on": { "to": "merged" },
  "when": { "kind": ["feature", "bug", "chore"] },
  "then": { "to": "released" }
}
```

| Field | Meaning |
|---|---|
| `name` | Slug `[a-z0-9-]{1,40}`, unique in the project. Never a metric label. |
| `on` | Trigger. `to` (required) names the state entered. `from` and `actor` optionally narrow it. Object form so later trigger types are additive. |
| `when` | Optional filters, all must hold: `kind` (task kinds), `workflow` (governing workflow names). Deliberately small. |
| `prompt` | Optional prose condition, at most 2000 characters, evaluated by the LLM. Required when `then.choose` has more than one target. |
| `then` | Exactly one of `to` (one target) or `choose` (2 to 5 candidate targets, LLM-picked). A single-element `choose` with a `prompt` means "fire only if the model confirms". |

Every automation names its edges statically: source `on.to`, targets from `then`. Nothing at evaluation time can change which edge an automation is about.

Validation on write, with errors naming the automation:

- At most 50 automations. Names valid, unique.
- `on.to`, `on.from` and every `then` state in the vocabulary.
- Every automation edge (`on.to`, target) is a core edge or a row of the 8.3 entry table. `ready -> deployed_prod` is unstorable.
- `when.kind` values are valid kinds. `when.workflow` values name workflows in the same object, or `default`.
- `prompt` present and non-empty when `choose` has more than one target.
- `then` has exactly one of `to` / `choose`; `choose` lists 2 to 5 distinct states (1 with `prompt`).

Edge legality is checked against the vocabulary-wide superset because the governing workflow is per task. The guard refuses the edge at fire time for any task whose workflow does not declare it.

### 10.3 Evaluation pass

One `wl:TaskTransitioned` event produces one pass:

1. Skip if the event's actor is the engine, the task is deleted, or the project has no automations.
2. Load the task's facts and the project's automations once. Evaluate against that snapshot.
3. In order, test trigger fields against the event and `when` against the task facts. On the first automation that passes: no `prompt` means the automation matches; with `prompt`, ask the model. Holds means match. Does not hold, or any failure, means continue down the list.
4. Fire the first match, then stop.

A pass makes at most 10 LLM calls, each with a per-call timeout (default 20s, deployment-configurable). Prompt automations beyond the budget do not match.

Firing calls `Transition` with `from = on.to` under the same guard as every other caller. Two refusals are benign: **stale** (the task moved since the event, from-state check fails, no-op, no falling through) and **refused** (edge is vocabulary-legal but undeclared in the governing workflow, no-op). Any other error ends the pass.

The subscriber is at-least-once. Redelivery is a no-op through two layers: the fired event's dedup identity (`source = watcher`, `external_id = automations:<triggering event id>:<automation name>`) and the guard's from-state check. The offset commits after the pass regardless of outcome. A poison event must not wedge the log.

### 10.4 LLM-backed automations

The engine builds the whole request. The author contributes only `prompt`, which arrives through the reviewed PUT and is trusted policy. The fixed frame contains: the prompt, the candidate target state names, task facts (id, title, kind, state, governing workflow), the triggering edge, the last 5 `state_log` entries, and the task body truncated to 2000 characters inside delimiters the frame marks as untrusted content not to be followed as instructions. No tools, no fetching, no repo contents, no other tasks. Temperature 0, small `max_tokens`, JSON object only.

The answer space is exact:

| Automation form | Accepted answers |
|---|---|
| `then.to` or single-target `choose` | `{"match": true}` or `{"match": false}` |
| Multi-target `choose` | `{"choice": <zero-based index into the automation's choose array>}` or `{"choice": null}` |

Anything else (free text, out-of-range index, a state name, extra keys, JSON in prose) is `unparseable` and the automation does not match. Model output never names a state.

Transport error, timeout, HTTP failure, `unparseable`, exhausted budget, or no provider configured: the automation does not match and evaluation falls through. A deployment with no LLM configured runs structured automations at full fidelity and treats every prompt automation as never matching (`unconfigured`).

Three independent layers stand between adversarial model output and an illegal transition: write-time validation (the config cannot store a forbidden edge, and the engine has no write path to config), answer-space bounding (output selects among pre-validated targets or declines), and the guard (every firing passes the per-task legality check). The worst a total compromise of the model achieves is a premature but legal transition, evented, attributed, and reversible through the core reopen edge.

### 10.5 Execution, client, provenance

`internal/automations` holds the evaluator as a pure function: given parsed automations, the event and task facts, it returns a pass plan (each candidate automation in order is *matched* or *ask(prompt frame, answer schema)*). It has no store, HTTP or LLM handle. The executor is a second eventbus subscriber, `workflow-automations`, wired in `internal/api` beside `docwatch.go`, started only under a `BackgroundCtx`. It supplies facts, makes LLM calls, applies the failure mapping, and fires through the store.

`internal/llm` is a minimal chat-completions client shaped like `internal/embed`: OpenAI-compatible endpoint, key and model from `LODE_RULE_LLM_URL`, `LODE_RULE_LLM_KEY`, `LODE_RULE_LLM_MODEL`, nil-safe metrics, per-call timeout. No vendor or model is named in code.

A firing records a **`wl:AutomationFired`** event: `wl:subject`, `wl:automationName`, `wl:fromState`/`wl:toState`, `prov:wasInformedBy` the triggering event, with the dedup identity above. The `state_log` row is attributed to that event. The chain webhook, transition, `TaskTransitioned`, automation fired, transition is walkable in one log.

Automations ride the workflow surfaces of section 8.10: `GET`/`PUT .../workflows`, `lode project workflow`, `lode project set workflow` and the `workflow.write` grant.

## 11. Metrics

| Metric | Labels |
|---|---|
| `worklode_local_merge_reports_total{result}` | shared by the `post-merge` hook and the `close` merge probe |
| `worklode_workflow_writes_total{outcome}` | `ok` / `invalid` / `error` |
| `worklode_rule_passes_total{outcome}` | `fired` / `no_match` / `stale` / `refused` / `skipped` / `error` |
| `worklode_rule_llm_calls_total{outcome}` | `match` / `no_match` / `choice` / `no_choice` / `timeout` / `error` / `unparseable` / `unconfigured` / `budget` |
| `worklode_rule_llm_duration_seconds` | histogram in `internal/llm` |

The workflow-change watcher rule reports through the doc-lifecycle rule metric with label `review-on-workflow-change`. Rule names and state names are never metric labels.

## 12. Deferred

- Push emitters and the poll prober for deliverable state (03-tasks-and-execution.md). The done check warns on every declared deliverable whose state is unreported.
- External evidence routing (section 5).
- Per-project strictness on a failed merge probe.
- `ns/` mirrors: the `wlc:DoneCheck` scheme for `merge` and `manual`, and the property for a project's default.
- Verification for kinds other than `bug`. Executable reproductions.
- Workflows as spec documents. Per-repo workflow selection. Workflow-driven cockpit columns.
- Writing `tasks.workflow` from automations (needs a task-creation event and an `assign_workflow` action). Trigger types beyond `wl:TaskTransitioned`. Time-based triggers. Richer `when` predicates. Wider LLM context. Cockpit surfacing of automations and firings.

## Sources

WL-SPEC-57 (definition of done), WL-SPEC-58 (verification after delivery), WL-SPEC-45 (per-project workflows), WL-SPEC-46 (workflow rule engine).

## Open questions

- Whether workflows and automations should move from the JSON column into spec documents (left open by WL-59).
- Whether `project_repos.done_state` should be replaced by a per-repo workflow selector.
- Which service actor id the automation engine uses, and how it is provisioned (see 02-identity-actors-and-secrets.md).
