
| Need | Owner |
|---|---|
| How far along is spec N | `lode doc progress` and the Progress page (WL-SPEC-85) |
| Which specs need planning | `lode doc list --needs-planning`: accepted requirements that are not planned (WL-SPEC-78 §1.3) |
| Which plans need execution | `lode doc list --needs-execution`: accepted plans whose task set is unpublished or unfinished |
| One spec's remaining work | `lode doc todo <ref> [--deps]` (WL-SPEC-78 §1.7) |
| Plan B after plan A | `blockedBy` edge from plan B to plan A |
| These land together | Milestone over deliverables |
| Task waits on part of spec N | its plan is `blockedBy` the covering plan, or it `dependsOn` individual tasks |

"All of spec N is done" is never a completion event, because coverage grows as rules are added.

