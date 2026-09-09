# Codex agent command center and Worklode

## Recommendation

Use Codex as a live execution view for work running through one Codex host.
Keep Worklode as the durable coordination graph across hosts and agent
harnesses.

Do not copy Worklode's whole graph into Codex or treat the Codex thread tree as
the source of truth. The two systems describe different things:

- Worklode records what work exists, why it exists, what blocks it, who holds
  it, and how delivery changes its state.
- Codex records the conversations and agent threads that happen while some of
  that work is being done.

The useful integration is a projection between them. A person should be able
to enter a Worklode reference in Codex's **New task** prompt. Codex should
resolve the reference, choose the right workflow, and show the resulting agent
threads. Worklode should retain enough session links to show the same run in
its project cockpit, including work done by Claude Code or another harness.

## What Codex provides

Codex has a real nested agent model, not only a flat list of workers.

- A main thread can spawn subagents.
- A subagent can delegate to another subagent.
- The app exposes spawned agents as inspectable threads with Active and Done
  states.
- The command-line interface can switch between agent threads with `/agent`.
- The App Server can query direct children with `parentThreadId` and every
  descendant at any depth with `ancestorThreadId`. These filters are currently
  experimental.
- Threads can be named and given goals.
- The maximum number of concurrent spawned threads is configurable per
  session.

The agent tree is therefore useful for live supervision. A root agent can own
the overall outcome, delegate bounded work, collect results, and let a person
inspect or steer each branch.

Codex warns about an important limit: parallel read-heavy work is easy, while
parallel write-heavy work can cause conflicts. Worklode's task worktrees are a
good answer to that limit, provided each writing agent runs in the worktree of
the task it claimed.

## Host and protocol boundaries

The command center is operationally single-host. One Codex App Server owns a
set of threads and one execution environment. Remote clients can control that
host, but they do not turn several independent machines into one distributed
scheduler.

The App Server can listen over standard input and output, a Unix socket, or a
WebSocket. The protocol is bidirectional JSON-RPC 2.0. Its main objects are:

- **Thread:** a conversation between a person and a Codex agent.
- **Turn:** one request and the work that follows.
- **Item:** a message, command, file change, tool call, approval, or other unit
  within a turn.

The protocol streams thread, turn, item, tool, and approval events. A client
can start or resume a thread, steer a running turn, interrupt work, read
history, and inspect runtime status.

App Server can accept remote WebSocket clients, and the Codex terminal UI can
connect to one. OpenAI currently marks remote WebSocket App Server operation as
experimental and unsupported for production workloads. Every thread in an App
Server process also shares that process's selected Code Mode host.

This makes App Server suitable for a close Codex integration and an early
prototype. It should not become Worklode's distributed coordination protocol.

## Other agent harnesses

A Claude agent cannot join a Codex thread tree as a native child. Codex custom
agents are Codex session configurations: they select instructions, a model,
reasoning effort, sandbox settings, Model Context Protocol (MCP) servers, and
skills. The documented protocol does not let an arbitrary external agent
register itself as a Codex subagent thread.

A Claude service could be wrapped as a Codex tool, but it would appear as a
tool call rather than a native thread. It would not automatically gain Codex's
thread inspection, steering, approvals, or lifecycle behavior.

Worklode is the natural meeting point instead:

```text
                         Worklode
                 durable coordination graph
                  /          |           \
           Codex host    Claude host    other host
             /   \           /   \
       Codex agents      Claude agents
```

Each harness owns its local execution tree. Worklode coordinates the trees
through tasks, worktrees, leases, dependencies, session records, pull
requests, continuous integration, and deployment facts.

This follows WL-SPEC-8's existing rule: one coordination model with several
harness adapters. That spec already defines shared skills, common lifecycle
events, Codex and Claude Code adapters, and worktree-bound leases.

## Mapping Worklode work to Codex threads

Worklode's design graph is not a strict tree:

- A spec contains anchored sections.
- A plan covers one or more sections.
- More than one plan can cover a section.
- Accepting a plan mints tasks linked through `plan_doc`.
- Tasks can have `blocks`, `child_of`, `follow_up_to`, and other edges.
- Dependencies can cross plan boundaries.

For that reason, Codex's thread hierarchy should be an execution projection,
not a structural copy:

```text
Spec coordinator thread
├── Plan coordinator: WL-PLAN-136
│   ├── Task worker: WL-721
│   └── Task worker: WL-722
├── Plan coordinator: WL-PLAN-137
│   └── Task worker: WL-723
└── Reviewer or integration agent
```

Use these default mappings:

| Worklode object | Codex representation | Responsibility |
|---|---|---|
| Spec | Root coordinator thread | Recompute outstanding work and choose the next safe wave |
| Plan | Optional coordinator subagent | Coordinate a phase when its tasks need local sequencing or synthesis |
| Task | Worker thread in its leased worktree | Complete one claimable unit and its definition of done |
| Child task | Nested worker when independently claimable | Preserve `child_of` while using its own lease and worktree |
| Blocker | Scheduling constraint | Prevent dispatch until Worklode reports the task ready |
| Review task | Reviewer thread | Judge an artifact without sharing implementation ownership |
| Rally | Root run scope | Select the work the coordinator should finish now |

Plan coordinators are optional. If a plan has two simple independent tasks,
the root coordinator can start both workers directly. An agent should exist
because it has coordination or judgment to perform, not merely because a row
exists in Worklode.

## Dispatching from the New task prompt

The desired entry point is a bare reference:

```text
WL-SPEC-66
```

```text
WL-PLAN-136
```

```text
WL-721
```

The reference type selects the workflow.

### Task reference

1. Read `lode task show <ref> --json`.
2. Check its state, blockers, assignment, and existing lease.
3. Claim that exact task through `lode work next <ref>` when it is ready.
4. Start a dedicated worker thread whose working directory is the new
   Worklode worktree.
5. Inject `lode task brief <ref> --json` as the bounded starting context.
6. Follow the normal Worklode done, block, release, review, and delivery
   lifecycle.

The task brief remains the context contract from WL-SPEC-8 §11. A worker
should not rediscover the governing design by searching the repository.

### Plan reference

1. Read the plan and its edges with `lode doc show <ref> --json`.
2. Find the tasks minted from the plan through `plan_doc`.
3. Ask Worklode for the ready frontier rather than inferring readiness from
   document order.
4. Start one worker for each safely independent ready task.
5. Re-read the frontier after each wave.
6. Finish when every non-abandoned task is landed, or report the remaining
   real blockers.

The coordinator must treat the plan task set as live. Tasks can be blocked,
reworked, abandoned, or followed by newly discovered work after the initial
dispatch.

### Spec reference

1. Read the current effective spec with `lode show <ref> --inline`.
2. Read its derived progress and dependencies with `lode doc todo <ref>
   --deps --json` or the equivalent structured endpoint.
3. Separate the next acts:
   - sections that need planning;
   - draft plans that need acceptance;
   - accepted plans with ready tasks;
   - work already active or in review;
   - historic plans with no execution record;
   - sections whose work is built.
4. Start planning, decision, review, or implementation agents only for acts
   that can proceed now.
5. Recompute the spec state after every wave.
6. Stop only when Worklode reports nothing outstanding, or when every
   remaining path has a recorded blocker or human decision.

This follows WL-SPEC-66: progress and the next act are derived from current
document, edge, and task facts. They are never copied into a stored percentage
or a frozen orchestration plan.

## Skill and hook design

Start with a shared Worklode dispatch skill. Give it a narrow description that
triggers on Worklode spec, plan, task, and rally references. Both Codex and
Claude Code can use the same procedure through Worklode's shared skill store.

Add a Codex `UserPromptSubmit` hook after the skill works. Codex passes this
hook the exact prompt before it reaches the model. The hook can recognize a
bare Worklode reference, resolve its type with `lode`, and add developer
context for the matching workflow.

Keep the boundary from WL-SPEC-8:

- The hook performs deterministic recognition and context assembly.
- The skill contains orchestration judgment.

Do not rely only on implicit skill selection for a bare reference. A reference
contains little natural language, so deterministic recognition removes an
avoidable source of variance.

A prompt hook can add context but cannot move the current thread into a new
worktree. For a task reference, the full integration should use App Server to
create or resume a named worker thread with its `cwd` set to the claimed
worktree.

## Worklode session-model changes

WL-SPEC-12 records an agent session against a lease. That is enough for a task
worker, but not for a spec coordinator running in the main checkout. It also
does not preserve the parent-child relationship among agent sessions.

Add a harness-neutral orchestration run and its participating sessions. The
exact schema needs design, but it should express these facts:

```text
orchestration run
- root Worklode reference: spec, plan, task, or rally
- originating harness
- originating host
- root external session id
- started and ended timestamps

run session
- orchestration run id
- harness
- external session id
- parent external session id, when present
- Worklode subject reference
- role: coordinator, planner, worker, or reviewer
- host
- optional lease id
```

For example:

```text
Codex thread 9a3…  coordinates WL-SPEC-66
Codex thread b71…  coordinates WL-PLAN-136, parent 9a3…
Codex thread d42…  executes WL-721, parent b71…, lease 881
Claude session …   executes WL-722 in the same orchestration run
```

Do not require a lease on every run session. A spec or plan coordinator often
runs from the main checkout and should be charged to project overhead. Keep
task execution linked to its lease so existing ownership and cost attribution
remain intact.

Codex subagent hooks use the parent session identifier, so Worklode may need
the subagent identifier and ancestry from App Server events rather than from
the lifecycle hook alone. Verify this during the prototype before fixing the
schema.

## Cockpit opportunity

The Worklode cockpit can provide the cross-harness view the Codex command
center cannot:

- group active orchestration runs by their root spec, plan, task, or rally;
- show every participating host and harness;
- nest sessions where the harness exposes ancestry;
- link task sessions to leases and worktrees;
- show task and plan progress from Worklode facts, not agent claims;
- mark sessions stale, interrupted, waiting for approval, blocked, or done;
- link a Codex session to its Codex command-center thread when a stable deep
  link exists;
- preserve completed run history after local agent threads are archived.

Codex remains the richer view for inspecting a live Codex thread. The cockpit
remains the authoritative view of the work and all participating harnesses.

## Implementation sequence

1. **Reference dispatch skill.** Implement and test one shared skill for task,
   plan, spec, and rally references.
2. **Codex prompt hook.** Recognize bare references with `UserPromptSubmit`
   and inject the resolved workflow context.
3. **Single-session prototype.** Run one spec coordinator with nested plan and
   task agents. Give each writing worker its own Worklode worktree.
4. **App Server adapter.** Create named threads, set goals and working
   directories, and inspect descendant status through the protocol.
5. **Orchestration records.** Add harness-neutral runs, session subjects, and
   session ancestry to Worklode.
6. **Cockpit projection.** Join durable Worklode progress with live session
   state from every harness.
7. **Multi-host dispatch.** Let Codex and Claude workers claim from the same
   Worklode frontier without pretending they share one native agent tree.

The first three steps can test the central idea without committing Worklode to
a new durable schema. The schema should follow evidence from that prototype,
especially around Codex thread identifiers, subagent identifiers, restarts,
and worktree selection.

## Questions to settle in the prototype

- Can a native Codex subagent be started directly with a different `cwd`, or
  must the integration create its worker thread through App Server?
- Which App Server identifiers remain stable across desktop restarts,
  compaction, resume, archive, and remote control?
- What thread or item event identifies a subagent strongly enough to join it
  to Worklode without reading unstable transcript files?
- Can the desktop app deep-link to a thread from an external cockpit?
- How should a coordinator react when another host claims one of the tasks it
  intended to dispatch?
- Should one orchestration run span a rework cycle, or should rework create a
  new run linked to the first?
- When should a plan coordinator exist instead of letting the spec coordinator
  dispatch plan tasks directly?
- Which task states count as complete for the coordinator: merged,
  deployed to development, deployed to production, or released?
- How should decisions and approvals surface in Codex without letting an agent
  answer a question reserved for a person?
- What concurrency limit keeps parallel work useful without exhausting the
  host or creating too many simultaneous pull requests?

## Sources

### OpenAI documentation

- [Subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents) —
  nested delegation, agent-thread visibility, controls, configuration, and
  sandbox inheritance.
- [Codex App Server](https://learn.chatgpt.com/docs/app-server) — protocol,
  transports, thread lifecycle, ancestry queries, goals, status, and remote
  host limits.
- [Hooks](https://learn.chatgpt.com/docs/hooks) — `UserPromptSubmit`,
  `SubagentStart`, `SubagentStop`, session identifiers, and additional
  developer context.
- [Build skills](https://learn.chatgpt.com/docs/build-skills) — explicit and
  implicit skill invocation and repository skill discovery.

### Worklode documents

- `WL-SPEC-8` — Worklode plugin and agent-harness integration, especially
  §§11–17.
- `WL-SPEC-12` — agent-session schema and lifecycle.
- `WL-SPEC-25` — documents, plan coverage, and tasks minted from accepted
  plans.
- `WL-SPEC-66` — derived spec progress and the next act.
- `WL-PLAN-23` — implementation of the first agent-session model.

Read the effective text of a Worklode document with `lode show <ref>
--inline`. Use `--section sec-N` for a targeted section and `lode doc show
<ref> --json` for structured section and edge metadata.
