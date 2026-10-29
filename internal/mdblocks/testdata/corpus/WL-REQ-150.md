
Every reader of task state falls in one of four classes:

| Class | Meaning | Examples |
|---|---|---|
| Core | Depends only on mandatory states and core edges. Correct in every workflow. | `Claim`, lease sweeper, `StartTask`/`StopTask`, ranking, `CreateTask`, the manual close, `abandon`, `reopen`, hierarchy roll-up |
| Vocabulary-static | Enumerates a vocabulary subset as a prefilter or ordering. Sound because workflows only omit states. | `deliveryRanks`, `deliveredStateSet`, `taskClosed`, `TasksBelowFrontier`, `mergeCandidateStates`, "open" status filter |
| Workflow-resolved | Consults the governing workflow at runtime. | `ResolveDelivery`'s tail steps, the PR-opened hook |
| Display | Unrecognised states render raw. | Cockpit buckets, `StateChip`/`StateLabel`, CLI help |

The two workflow-resolved readers: `ResolveDelivery`'s pre-merge jump is core; its tail steps advance only along declared entries. A dev frontier does nothing for a workflow without `deployed_dev`, and the task moves `merged -> deployed_prod` directly when that entry is declared and the prod frontier covers it. The PR-opened hook's `in_progress -> in_review` is attempt-and-tolerate: where the workflow declares no `in_review`, the guard refuses and the hook drops the transition silently.

