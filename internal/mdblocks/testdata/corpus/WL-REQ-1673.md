
| Query | Reads |
|---|---|
| Unimplemented intent | accepted sections with no `implements` edge |
| Coverage of a document | implemented sections over non-superseded sections |
| Stale claim | pinned at vN where `lastRevisedIn` exceeds vN |
| Orphaned claim | anchor absent from the current version |
| Delivered coverage | implemented sections whose Deliverable is deployed to an Environment, via `wl:deliveredBy` |

`lode graph drift` reports violations and stale intent, and `lode graph gaps` reports documentation and coverage gaps. No timestamp heuristic determines whether intent is satisfied. `wl:implements` names only Component-to-Section (and task-to-section from plan declarations); a Task `wl:produces` a Deliverable and `wl:affects` a Component.

