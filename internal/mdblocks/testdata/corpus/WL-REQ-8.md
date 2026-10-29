
The gate is the CI job `spec-gate`. It runs on every pull request the CI
trust check admits, except one that touches only `docs/`, `www/` and
markdown files. It skips merge-queue builds, which have no pull request body
to read. It calls `lode gate check` with the pull request's base, head and
body. The check decides
offline from the repository checkout, the pull request body and the commit
messages (§3). A change to a guarded path passes when one of these holds:

1. The task has a plan ref and that plan covers at least one rule of an
   accepted spec.
2. A `Spec:` trailer names a rule that resolves and is not withdrawn.
3. A `Spec: ... amended` trailer names a rule whose current version was
   written after the pull request's base commit.
4. A `Spec: none <reason>` trailer carries a reason from the closed list, and
   for `tests` or `build` the diff touches only the matching paths. `tests`
   matches `_test.go` files and anything under a `testdata/` directory.
   `build` matches `Makefile`, `go.mod`, `go.sum`, `Dockerfile*` and
   `.github/**`. `copy` has no path restriction.

The offline check evaluates the trailer's form for conditions 2 to 4: a
well-formed rule ref, the refusal of a section value, an optional qualifier, a reason from the
closed list, and the refusal of a bare `none fix`. Whether a ref resolves is
decided on the server, where the reconciler counts and logs a ref that names
nothing (§4). The CI job makes no Worklode API call. "Designed, not built"
lists the parts of the four conditions the check does not evaluate.

On fail the job names the guarded paths that changed and the reason the
trailer was missing or malformed. The gate never judges whether the cited
sentence really covers the change. That judgment stays with the reviewer.

What the gate records is the `governedBy` link with `source = 'gate'` and the
`task.governed` event the reconciler writes for it (§4).

