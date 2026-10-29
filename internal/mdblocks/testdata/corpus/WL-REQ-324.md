
Kept as-is: `handoff`, `domain-modeling`. Remixed, one skill per family:

| Remix | Produces | Sources |
|---|---|---|
| `assay` (`/lode:assay`) | specs | `superpowers:brainstorming` + `mattpocock:grilling` + `mattpocock:to-spec` |
| `planning` | plans | `superpowers:writing-plans` + `mattpocock:to-tickets` |
| `writing-for-agents` | skills, instruction files | `superpowers:writing-skills` + `mattpocock:writing-for-agents` |
| `tdd` | | `superpowers:test-driven-development` + `mattpocock:tdd` |
| `debugging` | | `superpowers:systematic-debugging` + `mattpocock:diagnosing-bugs` |

Not vendored: everything else, including the execution-loop skills (`executing-plans`, `subagent-driven-development`, `using-git-worktrees`, `finishing-a-development-branch`), whose ownership of branches and task state overlaps `lode work next`/`work submit` and `working-under-worklode`. They are neither vendored nor suppressed.

