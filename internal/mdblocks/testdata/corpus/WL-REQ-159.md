
| Metric | Labels |
|---|---|
| `worklode_local_merge_reports_total{result}` | shared by the `post-merge` hook and the manual close's merge probe |
| `worklode_workflow_writes_total{outcome}` | `ok` / `invalid` / `error` |
| `worklode_automation_passes_total{outcome}` | `fired` / `no_match` / `stale` / `refused` / `skipped` / `error` |
| `worklode_automation_llm_calls_total{outcome}` | `match` / `no_match` / `choice` / `no_choice` / `timeout` / `error` / `unparseable` / `unconfigured` / `budget` |
| `worklode_automation_llm_duration_seconds` | histogram in `internal/llm` |

The workflow-change watcher rule reports through `worklode_watcher_actions_total{rule, outcome}` with `rule = review-on-workflow-change`. Automation names and state names are never metric labels.

