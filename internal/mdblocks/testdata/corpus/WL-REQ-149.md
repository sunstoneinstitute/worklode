
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

