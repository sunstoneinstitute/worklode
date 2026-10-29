
A plan declares its tasks under one `## Tasks` heading, one `### Task N — Title` subsection each, numbered 1, 2, 3 in document order without gaps. Each subsection opens with a YAML fence: `kind` (required), `priority` (default `medium`), `skills` (`plugin:skill` ids) and `blockedBy` (task numbers within the plan, acyclic). The rest of the subsection is the task's body. `kind` is one of the plan-mintable kinds, `feature`, `bug`, `chore`, `design` or `decision`; `review`, `spike` and `rally` are refused. A declaration's identity is its title, recorded on the minted task as `plan_task_key`.

Once a plan has minted tasks, a body edit that leaves its `## Tasks` section unreadable is refused. A declaration that disappears or is retitled is not an error: the minted task outlives it, and a retitled declaration is a new one the next accept mints.

