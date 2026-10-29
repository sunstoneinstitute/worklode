
**No agent may lower the gate on its own work.** A skim is advisory to a human
and authoritative to a reviewer agent, so three rules are enforced server-side:
the guide handed to a reviewer agent is produced by the backbone from the
author's proposal plus policy, and `GET .../guide` is its only source; an author's proposed skim is refused on a file it edited
non-mechanically (the mechanical set: lockfiles, generated output, vendored
code, snapshots, import-only and formatting-only blocks), with the rule that
fired, and the guide is stored with that skim removed; self-approval is refused, so a verdict from the actor holding the task's
lease or the agent session that authored the round is rejected at the API.
`focused` is stored on the round so "was this approval cast over a narrowed
diff, and who narrowed it" is a query.

