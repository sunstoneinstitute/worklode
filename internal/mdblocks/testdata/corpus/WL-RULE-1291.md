
Branch naming and the path guard (section 3) are held by these tests:

- Round-trip: for the default template and several custom ones, render branches from generated task ids and assert that `TaskIDFromRef` recovers the id. This property is what makes the §3.2 derivation trustworthy, so asserting one hardcoded pattern string is not enough.
- Path guard: paths under the default and a custom base, a flattened namespaced branch, a path deeper than one segment below the base, paths outside any base, and a base name appearing more than once.
- Directory round-trip: for each template shape, assert that the directory `Layout.Dir` chooses clears the guard and yields the id back. Flattening is the step most likely to break this.

Task activity (§8.6 to §8.9) is held by these tests:

- A fixture OTLP/JSON batch is stored against its stamped task, with only allowlisted attributes kept.
- An upstream test server receives the exact bytes and bearer token.
- A task-scoped token for another task is refused with 403; a protobuf body with 415.
- Records without a task id are forwarded and not stored.
- Purge removes closed-task rows older than two hours and any row older than seven days, and keeps an open task's fresh rows.
- The stream delivers a row inserted after the cursor and resumes from `Last-Event-ID`.
- `lode install` writes the log exporter keys and the header helper; uninstall strips them and preserves foreign entries.

