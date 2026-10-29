
One bounded, machine-assembled payload tells the agent what a task is about. It contains: the task row and body; the server-rendered branch; open blockers and the unfinished plans ordered before this task's plan; the parent task; the active lease; the governing rules (WL-SPEC-77 §4b); pinned and recommended skills with content inline; attached blobs as absolute URLs; and the stale-plan slug when the plan the task was minted from is stale. The brief is injected on `SessionStart` and right after claim. If the brief is insufficient, the task needs decomposition.

