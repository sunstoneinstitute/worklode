
A task's **effective** declaration resolves in one step:

1. The task's own list, when non-null. An empty list is a legal, explicit "this task declares nothing" and does not fall through.
2. Otherwise the project's default list.

An override replaces the default. It never extends it. A task that wants the default plus one more writes both. When the effective list is empty, the brief's `definition_of_done` is null and the task body is the contract.

