# Model selection for agentic development

Which Claude model each role in the plan → execute workflow runs on, and why.
The controlling variable is plan precision: the sharper the plan, the cheaper
the executor can be.

| Role | Model | Rationale |
|---|---|---|
| Planning: spec reading, scope decisions, writing implementation plans | Fable 5 | Highest reasoning tier; its job is to make plans precise enough that cheaper models can execute them. |
| Execution coordination (subagent-driven-development loop) | Opus 4.8 | Judgment across tasks: sequencing, reviewing results, handling plan-vs-reality deviations. |
| Implementation subagents — plan task is fully specified (exact files, code, tests; no spikes, no open design decisions) | Sonnet | Precise plans make implementation mechanical; Sonnet executes them reliably at lower cost. |
| Implementation subagents — task involves unknowns (debugging, spikes, design gaps, plan conflicts with reality) | Opus 4.8 | Ambiguity needs judgment; escalate rather than let Sonnet improvise. |
| Spec review / code review between tasks | Opus 4.8 | Review is judgment, not mechanics. |
| Exploration / fact-finding (codebase mapping, doc lookups) | Agent-definition default (Explore, claude-code-guide, ...) | Read-only; the default tier is sufficient. |

Rules of thumb:

- A coordinator dispatches Sonnet implementers by default when the plan task
  meets the "fully specified" bar, and escalates that task to Opus on the
  first sign the plan doesn't match reality.
- Subagents inherit their parent's model unless overridden — set the model
  explicitly on dispatch rather than relying on inheritance.
- Fable does not execute; Opus/Sonnet do not (re)plan. If execution reveals a
  plan defect, fix the plan at the planning tier, don't improvise downstream.
