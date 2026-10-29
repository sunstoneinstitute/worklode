
`internal/store/progress.go` `ProjectProgress(ctx, project, specs)` reads
every fact the derivation needs (specs, sections, plans, covers through
`covered_sections`, minted tasks, open planning tasks, leases, PRs, branch
rules, CI runs, rallies) as one `progress.Input` in a fixed number of
project-wide queries; a non-empty `specs` narrows the read to those specs and
the plans covering them, for the fragments. `internal/progress` is the pure
derivation (`Derive`, `Position`, `MergeAct`, the rally membership and the
frame resolver) and returns `model.ProjectProgress`.
`internal/api/progress.go` and `internal/api/progressmerge.go` hold the
handlers; `internal/ui/progress.templ` renders page and fragments from
`ui.ProgressView`; `internal/ui/assets/progress.js` holds the two-step
buttons, stream client, fragment swap and tooltips under `script-src 'self'`.

