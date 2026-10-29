
An executor that hits something its plan did not anticipate does not improvise and does not stop for a human unless human judgment is required. It spawns a fixer subagent at the tier the fix needs (plan defects to the planning tier, spec defects above it) and waits. A subagent has bounded latency. A minted task has unbounded queue latency. The rule generalises: a synchronous subagent when the work is bounded and tier is the only thing missing, a minted task when the work needs human judgment or another operator.

| Fixer outcome | Executor | Document |
|---|---|---|
| Resolved, non-substantive | continues | amended in place, note attached |
| Resolved, substantive | stops | amended in place, sections marked `patched`, review task minted for the original approvers |
| Not resolved, or needs human judgment | stops | unchanged. `lode task escalate` mints a `design` task |

**`lode task escalate --to plan|spec --reason "..."`** runs one transaction: release the executor's lease and move the task to `blocked`, mint a `design` task against the referenced document carrying the reason and failing context, add a `dependsOn` edge from the blocked task to it, assign it to the plan's author, and deduplicate (a second escalation on the same section joins the open task).

