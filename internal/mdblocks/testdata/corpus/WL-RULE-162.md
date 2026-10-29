
A row exists because an act created it. Everything cross-cutting is derived: a plan's task set, a spec's coverage, a milestone's membership, "all plans of spec N with unfinished work".

| Candidate row | Verdict |
|---|---|
| Plan root task grouping one plan's tasks | Never minted. Query over `tasks.plan_doc` |
| Spec-level container over its plans | Never minted. Query over `covers` plus task-set states |
| Spec umbrella task open while unimplemented | Never minted. Coverage query |
| Sprint or iteration container | Never minted. Time-boxing is ranking and deadlines |
| Milestone | Row (declared intent that deliverables ship together), membership derived |

