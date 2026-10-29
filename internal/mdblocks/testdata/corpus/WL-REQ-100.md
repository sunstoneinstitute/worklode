
Default sort key, descending signal:

```
(in_rally, is_critical, concern_rank, priority, blocking_fan_out)
```

| Arm | Meaning |
|---|---|
| `in_rally` | member of the active rally's closure; true first |
| `is_critical` | `priority == critical`; true first; critical bypasses focus |
| `concern_rank` | index in `project.focus`; lower first |
| `priority` | `critical > high > medium > low` |
| `blocking_fan_out` | how many tasks transitively depend on the task over `dependsOn`; higher first; the estimate-free criticality proxy |

Ties resolve by oldest `created_at`, then task id, so ordering is reproducible and starvation-free. The server takes the top row and leases it in the same transaction.

