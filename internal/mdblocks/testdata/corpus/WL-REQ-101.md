
```
lode task claim --next [--project <id>] [--strict-focus] [--dry-run] [--json]
```

| Flag | Effect |
|---|---|
| `--project <id>` | restrict to one project and its `focus`; omitted means every project the caller may work, each task ranked by its own project's focus |
| `--strict-focus` | drop `is_critical` from the key: `(in_rally, concern_rank, priority, blocking_fan_out)`; the rally arm survives |
| `--dry-run` | return the task that would be claimed without leasing it; never a pick-then-claim step |
| `--json` | `{ "claimed": true, "task": { id, slug, concern, priority, fan_out, project, lease: { worktree, expires_at } } }` |

None ready is exit 0 with `{ "claimed": false, "reason": "no-ready-task" }`; the 24/7 loop backs off. Errors exit non-zero with `{ "error": … }`. Two concurrent calls get different tasks, or one gets "none ready". There is no persistent per-project strict setting.

