
| Capability | Section | Builds it |
|---|---|---|
| `projects.default_deliverables`, `tasks.deliverables`, `store.ValidDoneRef`, resolution and project defaults | 1, 1.1, 1.2 | WL-PLAN-121 |
| Done-declaration API fields, `lode task edit --deliverable / --deliverable-inherit / --deliverable-none`, `lode project set default-deliverables` | 1.3 | WL-PLAN-121 |
| Brief `definition_of_done` and `deliverables` populated (the brief has no such field today) | 2 | WL-PLAN-121 |
| The done check on the manual close: merge probe, per-reference warnings | 3 | WL-PLAN-121 |
| Lease and worktree retention keyed on done | 4 | WL-PLAN-121 |
| `projects.workflows`, `tasks.workflow`, the workflow-aware guard and its error naming the workflow | 8.2, 8.3, 8.5, 8.6, 8.7 | WL-PLAN-8 |
| Workflow validation, including the `done_state` warning | 8.8, 9 | WL-PLAN-8 |
| Workflow-resolved readers: `ResolveDelivery`'s tail steps and the PR-opened hook consult the governing workflow | 8.9 | WL-PLAN-8 |
| `GET`/`PUT /api/v1/projects/{id}/workflows`, `workflow.write`, `lode project workflow`, `lode project set workflow --file [--dry-run]`, `lode task edit --workflow`, the `review-on-workflow-change` watcher rule | 8.10 | WL-PLAN-8 |
| The automation engine: `wl:TaskTransitioned`, evaluator, `workflow-automations` subscriber, `internal/llm`, `wl:AutomationFired` | 10 | WL-PLAN-11 |
| Metrics `worklode_workflow_writes_total`, `worklode_automation_passes_total`, `worklode_automation_llm_calls_total`, `worklode_automation_llm_duration_seconds` | 11 | WL-PLAN-8, WL-PLAN-11 |

