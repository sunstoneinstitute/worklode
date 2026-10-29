
The kind CHECK, the Go `validKinds` list and `wlc:TaskKind` in `ns/` change together, and a test fails when they disagree.

| Kind | Meaning |
|---|---|
| `feature`, `bug`, `chore` | Ordinary claimable, worktree-bound work that lands a diff. `reconcile` and similar activities are `chore`. |
| `design` | Author or revise a Worklode document (spec or plan). Closes when the document is submitted for review. The document is reachable via `prov:wasGeneratedBy`. |
| `review` | Review a submitted document. Minted by the `doc-lifecycle` subscriber (§9.6). |
| `spike` | Time-boxed experiment, throwaway by convention. |
| `decision` | Answer the questions posed on the task and record the answers (§11). Never claimed into a worktree. Worked through the lease-free assign/start/submit path. |
| `rally` | A hand-assembled goal whose only content is its `dependsOn` edges to its members (§8.3). Never claimed, takes no children, is no task's prerequisite, carries no decision. |

No kind is a container. Being a container is inferred from having `child_of` children (§5). `rally` is the one declared structural role, because a rally is assembled one edge at a time and has no creating act that could make its edges the test.

