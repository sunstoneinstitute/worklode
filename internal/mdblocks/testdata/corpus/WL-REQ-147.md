
Resolved at transition time:

1. `tasks.workflow`, a per-task override naming one of the project's workflows (`lode task edit --workflow <name>`).
2. The project's `default` entry in `projects.workflows`.
3. The built-in `default`.

Editing a workflow applies immediately to every task governed by it. A `tasks.workflow` naming a workflow that no longer exists resolves to the project default. Initial state is not workflow business: every task is created `ready`, or `draft` with the `Draft` flag.

