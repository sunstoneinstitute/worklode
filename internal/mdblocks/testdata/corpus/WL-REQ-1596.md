
`ResolveDelivery(tx, taskID)` reads the task's landed seq, env frontiers and release frontier, works out the furthest milestone the facts support along the repo's `done_state` branch (WL-REQ-85), and issues forward-only transitions, several in one resolve when signals arrived out of order. It returns early for a task with children and for a deleted task.

Delivery advances a task through the repo its own commits landed in, so shared repos need no special handling and project-to-repo links play no part. A task spanning several repos tracks delivery through its primary repo only.

