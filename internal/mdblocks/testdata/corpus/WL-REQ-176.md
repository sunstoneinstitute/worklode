
The state machine, ranking and leases are in WL-SPEC-75. The kind set is fixed here because plans mint into it:

```sql
CHECK (kind IN ('feature','bug','chore','design','review','spike','decision','rally'))
```

| Kind | Meaning |
|---|---|
| `feature`, `bug`, `chore` | claimable, worktree-bound work that lands a diff. `reconcile` work is `chore` |
| `design` | author or revise a spec or plan. Closes when the document is submitted. Never an umbrella held open against coverage |
| `review` | minted by the lifecycle. Plans cannot declare it |
| `spike` | time-boxed throwaway experiment. Its outcome feeds planning. Plans cannot declare it |
| `decision` | answer the questions posed on the task and record the answers. One accountable assignee via `lode task assign`. It has no worktree, no branch and no PR, and is excluded from `readyCandidates`. Closes when the last question is answered, in the same transaction |
| `rally` | a hand-assembled set that steers `lode work next` (WL-SPEC-75#sec-8.3). Not claimable. Plans cannot declare it |

No kind is structural. Which document a task produced is carried by the document (`prov:wasGeneratedBy`). `validKinds` and `wlc:TaskKind` are generated from `ns/` and tested to agree.

