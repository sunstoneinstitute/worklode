
`GET /inbox` lists what is waiting on the signed-in actor, ranked so the item
that unblocks the most other work comes first. It adds ordering only: every
item is reachable without it, and it filters nothing an actor may read. It is
guarded by `permWebRead` like the other cockpit pages and adds no permission,
role or stored state. With no signed-in actor it renders an empty state and
reads nothing. It reports through `worklode_*` metrics. It is unrelated to
`/api/v1/inbox*`, the GitHub triage import.

**Items.** Reviews are open `approvals` rows (`awaiting`, `changes_requested`)
with `entity_kind = 'pr'` whose pull request is still open: *assigned to* the
actor when `required_actor` is the actor; *owned by* the actor when they
authored the PR (the PR author matched case-insensitively against
`actors.github_username`) and someone else is the required reviewer. Work is
tasks in an active state (`ready`, `in_progress`, `in_review`): *assigned*
when the actor is assignee, *owned* when they created it and are not the
assignee. Approvals on other entity kinds do not appear. WL-SPEC-84 change
reviews join the review buckets once WL-PLAN-133 adds them (see "Designed, not
built").

