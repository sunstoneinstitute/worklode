
A branch name is rendered from `LODE_BRANCH_TEMPLATE`, a Go `text/template` that produces the whole name. Default `{{ .id }}-{{ .slug }}`, yielding `WL-3-fix-the-thing`. The server is the sole authority: it renders once per claim and returns the result. The CLI's only fallback, when a response carries no branch, is the literal `<id>-<slug>`.

| Field | Value |
|---|---|
| `.id` | task id, e.g. `WL-3` |
| `.slug` | slugified task title |
| `.projectId` | project id the task belongs to, slugified |
| `.kind` | task kind, slugified |

Rendering uses `missingkey=error`. There is no bare `.project`. `.projectId` and `.kind` pass through the same slugify rule as `.slug`, so any template yields a legal ref.

Validation runs at `lode-server` startup and a bad template fails startup. Three conditions: it parses and renders against sample values; it references `.id` (otherwise no branch could be correlated back to a task); the result is a legal git ref (non-empty, no control characters, none of space `~^:?*[\`, no `..`, `//`, leading or trailing `/`, `@{`, and no path component starting with `.` or ending in `.` or `.lock`). The check is in Go; the server needs no git binary.

