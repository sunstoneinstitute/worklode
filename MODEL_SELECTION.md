# Model selection for agentic development

Which model tier each role in the plan → execute workflow runs on, and why.
The controlling variable is plan precision: the sharper the plan, the cheaper
the executor can be. Claude Code and Codex use the same role classes; model
names and reasoning-effort controls differ between the two harnesses.

| Role | Claude Code | Codex | Rationale |
|---|---|---|---|
| Planning: spec reading, scope decisions, writing implementation plans | Fable 5 | GPT-5.6-Sol, `ultra` | Highest reasoning tier; its job is to make plans precise enough that cheaper models can execute them. |
| Execution coordination (subagent-driven-development loop) | Opus | GPT-5.6-Sol, `high` | Judgment across tasks: sequencing, reviewing results, handling plan-vs-reality deviations. |
| Implementation subagents — plan task is fully specified (exact files, code, tests; no spikes, no open design decisions) | Sonnet | GPT-5.6-Terra, `medium` | Precise plans make implementation mechanical; the balanced tier executes them reliably at lower cost. |
| Implementation subagents — task involves unknowns (debugging, spikes, design gaps, plan conflicts with reality) | Opus | GPT-5.6-Sol, `high` | Ambiguity needs judgment; escalate rather than let the balanced tier improvise. |
| Spec review / code review between tasks | Opus | GPT-5.6-Sol, `high` | Review is judgment, not mechanics. |
| Exploration / fact-finding (codebase mapping, doc lookups) | Agent-definition default (Explore, claude-code-guide, ...) | GPT-5.6-Terra, `low` | Read-only work normally needs the cheapest tier that can synthesize the findings. |

## Claude Code dispatch

- **Always set `model` explicitly on every Agent dispatch. Never omit it.**
  An omitted model does NOT inherit the dispatching agent's model — it
  resolves to the top-level session model (Fable when a Fable session is
  driving), silently running mechanical work on the most expensive tier.
  Reviewers: `model: "opus"`. Coordinators: dispatched with `model: "opus"`.
  Fully-specified implementation: `model: "sonnet"`.
- **"Agent-definition default" (the Exploration row) only applies when you set
  `subagent_type` to a dedicated read-only agent that itself pins a cheaper
  model (Explore, claude-code-guide).** A generic dispatch — no
  `subagent_type`, or `subagent_type: "claude"`/`"general-purpose"` — has no
  such pinned default, so the omission rule above applies and it runs on the
  expensive top-level model. If a step is genuinely trivial (a single
  `cat`/`grep`/file read, no synthesis), the cheapest fix is not to dispatch a
  subagent at all — do it directly with Read/Bash/Grep in the current agent.
  If a subagent boundary is still required (e.g. subagent-driven-development
  forces one per task), set `model: "sonnet"` explicitly.
- **Exception: forks (`subagent_type: "fork"`) always run on the parent
  model and ignore `model` overrides** — that's how they share the parent's
  prompt cache. This is correct and expected; don't try to force a fork onto
  Sonnet, and don't flag fork model usage as a violation of the rule above.
- A coordinator dispatches Sonnet implementers by default when the plan task
  meets the "fully specified" bar, and escalates that task to Opus on the
  first sign the plan doesn't match reality.
- Fable does not execute; Opus/Sonnet do not (re)plan. If execution reveals a
  plan defect, fix the plan at the planning tier, don't improvise downstream.

## Codex dispatch

- Set both `model` and `reasoning_effort` explicitly when spawning an agent.
  Fully specified implementation uses `gpt-5.6-terra` at `medium`; exploration
  uses Terra at `low`; coordination, ambiguous implementation, and review use
  `gpt-5.6-sol` at `high`; planning uses Sol at `ultra`.
- A full-history fork inherits the parent's model and reasoning effort and does
  not accept overrides. To choose a cheaper or stronger tier, fork no history
  or only the recent turns and provide the required context in the task.
- A coordinator starts fully specified implementation on Terra and escalates
  to Sol as soon as the plan conflicts with reality or leaves a design choice
  open.
- Sol at `ultra` plans; Sol at `high` and Terra execute. If execution exposes a
  plan defect, return it to the planning tier instead of redesigning downstream.
