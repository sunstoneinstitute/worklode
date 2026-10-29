
`POST /api/v1/inbox/import`, admin:

```json
{"repo": "owner/name", "state": "open", "include_prs": false,
 "since": "2026-01-01T00:00:00Z", "dry_run": false}
```

Preconditions in order: 503 when no App is configured, 422 on an unknown `state`, 404 when `ProjectForRepo` finds no mapping. GitHub round trips run outside any transaction under a 60s bound; the result is applied in one `RecordEvent("cli", extID, "inbox.imported", payload, apply)`. `apply` first reads the existing `(repo, number)` set so the response distinguishes new from updated:

```json
{"repo": "owner/name", "issues": {"new": 38, "updated": 3},
 "prs": {"new": 0, "updated": 0}, "truncated": false, "dry_run": false}
```

`--dry-run` fetches and counts, returns the same shape with `dry_run: true`, and records no event. CLI: `lode inbox import <repo> [--state open|closed|all] [--include-prs] [--since <date>] [--dry-run]`. The CLI prints the exact rerun command for truncated issues.

