
Not built (see "Designed, not built").

The Operations cockpit carries a fixed set of four panels. A panel appears when
the project holds the facts it renders, and an empty panel is omitted rather
than rendered blank: a software project whose CI panel disappears has lost its
webhook, and an empty box would hide that. There is no project type, no `kind`
column and no per-project panel configuration; a research project that
acquires a repository starts showing delivery panels on its own. Nothing is
stored: every panel is assembled per request from `docs`, `tasks`,
`github_pull_requests` and `github_ci_runs`. Every value is *observed* except the documents
panel's coverage, which is *declared*. A fifth panel is a change to this
rule.

| Panel | Appears when | Content |
|---|---|---|
| Documents | the project owns at least one spec | one row per spec: reference, title, sections covered by a plan against sections total, planning outcome (full, partial, none, per WL-REQ-191), open tasks referencing it. Coverage is per document and never summed or rolled up. |
| Work mix | the project has an open task | open tasks per `wlc:TaskKind`; open is the task board's own store predicate; kinds with no open task omitted |
| Pull requests | `github_pull_requests` holds an open row for a mapped repository | repository, number, title, link, age; oldest first |
| Continuous integration | `github_ci_runs` holds a run for a mapped repository | running now: every run not `completed`, with workflow and elapsed time; then the last 20 completed runs across all the project's repositories, newest first, with repository, workflow, conclusion, start, duration |
