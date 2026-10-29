
The durable object is a **review**: a subject, threads anchored into it, an
ordered series of rounds, and a verdict per reviewer. All of it lives in
Postgres, is written through `/api/v1`, is emitted to the event log, and is
readable by anything holding a token. A terminal, an agent with no browser, and
a browser desk are three equal clients. Every review verb works with no crit
and no browser; the moment a verdict can only be cast from a browser, the
agent-reviewer path is second-class.

One review object covers documents and changes, with two anchor kinds. The
subject of a change review is the **task** (a task may carry several PRs and
`in_review` is a task state), which makes a cross-repo change reviewable.
Threads, intents, resolution, rounds and verdict invalidation are identical
across the two subjects.

