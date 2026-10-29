
The server's `spec-reconciler` subscriber turns the `Spec:` trailer (WL-REQ-7)
into a governing link (WL-REQ-110). On a GitHub `pull_request.*` or `push`
event it finds the task from the branch name or, for a pull request whose
branch does not resolve, from a `Worklode-Task:` trailer (WL-SPEC-72#sec-4a)
in the pull request body. It reads the `Spec:` trailer from the pull request
body or the pushed commits, newest first, and writes a `governedBy` link with
`source = 'gate'` when no plan governs the task. A `none` trailer writes
nothing. A trailer naming a rule that does not exist, or naming a section, is
counted and logged and writes nothing. The subscriber always reads the default `Spec:` key,
so a repository that renames the key with `[gate] trailer` gets a working CI
check and a reconciler that ignores its trailers.

