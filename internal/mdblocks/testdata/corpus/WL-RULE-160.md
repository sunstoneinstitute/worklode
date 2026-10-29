
- Push emitters and the poll prober for deliverable state (WL-SPEC-75 §13.3). The done check warns on every declared deliverable whose state is unreported.
- The second hop of external evidence routing (section 5).
- Per-project strictness on a failed merge probe.
- `ns/` mirrors: the `wlc:DoneCheck` scheme for `merge` and `manual`, and the property for a project's default.
- Provenance of a delivery state. Each task would carry `observed` (a recorded fact produced the state) or `asserted` (an actor set it), `ResolveDelivery` would not advance an asserted state, and reads would render it as `merged (asserted)`. A second `resolution` state enum is declined: resolution is tracked as work.
- Verification tasks for bugs. A `bug` reaching its repo mapping's `done_state` with an observed state would mint one `review` task, "Verify <KEY>-<n> no longer reproduces", `follow_up_to` the bug, quoting the reproduction, the landed commits and the environment reached. Kinds other than `bug` and executable reproductions are out of scope with it.
- Workflows as spec documents. Per-repo workflow selection. Workflow-driven cockpit columns.
- Writing `tasks.workflow` from automations (needs a task-creation event and an `assign_workflow` action). Trigger types beyond `wl:TaskTransitioned`. Time-based triggers. Richer `when` predicates. Wider LLM context. Cockpit surfacing of automations and firings.

