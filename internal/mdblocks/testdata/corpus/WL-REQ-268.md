
`store.TaskIDFromRef` maps a pushed branch or PR head ref back to a task id using a pattern derived from the same template, built once beside it: render with sentinels per field, `regexp.QuoteMeta` the result, substitute `.id` with `([A-Z][A-Z0-9]*-[0-9]+)` and every other field with `[^/]*`, anchor with `^`/`$`. The default template yields `^([A-Z][A-Z0-9]*-[0-9]+)-[^/]*$`. Every correlation path gates on `taskExists` before writing a binding, so a shape match on a non-existent id is dropped. Two accepted consequences: a bare `WL-3` branch does not correlate under the default template, and a template that renders `.id` adjacent to another field is ambiguous and is not prevented.

`store.TaskIDFromBody` correlates on a `Worklode-Task: <id>` line in a PR body regardless of branch shape.

