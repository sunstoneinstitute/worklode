
A round asks the whole frontier at once, each question with a recommended answer, rendered as a document and reviewed in `crit`:

| Grilling | Crit |
|---|---|
| One round's frontier | One review round |
| A numbered question | A block the reviewer comments on |
| An answer | An inline comment anchored to that question |
| Accepting the recommendation | Resolving with no comment |
| Frontier empty | Review approved |

The skill writes `.worklode/cache/grilling/<session>/round-<n>.md`, opens `crit`, blocks, reads the answers back, recomputes the frontier, and writes the next round. The session directory is the interview transcript and feeds write-back. Crit is optional: when absent, the round is asked in the terminal, the upstream behaviour.


