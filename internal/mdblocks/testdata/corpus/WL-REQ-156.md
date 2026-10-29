
One `wl:TaskTransitioned` event produces one pass:

1. Skip if the event's actor is the engine, the task is deleted, or the project has no automations.
2. Load the task's facts and the project's automations once. Evaluate against that snapshot.
3. In order, test trigger fields against the event and `when` against the task facts. On the first automation that passes: no `prompt` means the automation matches; with `prompt`, ask the model. Holds means match. Does not hold, or any failure, means continue down the list.
4. Fire the first match, then stop.

A pass makes at most 10 LLM calls, each with a per-call timeout (default 20s, deployment-configurable). Prompt automations beyond the budget do not match.

Firing calls `Transition` with `from = on.to` under the same guard as every other caller. Two refusals are benign: **stale** (the task moved since the event, from-state check fails, no-op, no falling through) and **refused** (edge is vocabulary-legal but undeclared in the governing workflow, no-op). Any other error ends the pass.

