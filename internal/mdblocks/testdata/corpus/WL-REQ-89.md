
```
lode task decompose <id> --into "Title A" "Title B" "Title C"
```

One transaction: clear `needs-decomposition`, create the N children inheriting project, priority and concern, wire the `child_of` edges, leave the children `draft`. The parent's kind is untouched. Rejected while the parent holds an active lease. The oversized task keeps its id and every reference to it, and its children become the claimable units. `POST /api/v1/tasks/{id}/decompose` is the endpoint.

