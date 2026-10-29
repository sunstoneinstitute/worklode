
`GET /reviews` is the awaiting-approvals queue: one row per approval in state
`awaiting`, naming its kind, lane and project, with a decide form. Rows come
in three groups, oldest first within each. First come the rows waiting on the
signed-in actor: `required_actor` is them, `required_role` is one of their
groups, or they authored the entity under a flow that allows self-review of
its kind. Next come rows assigned to nobody (neither `required_actor` nor
`required_role` set). Rows assigned to someone else come last and stay
listed, since a decision is not limited to `required_actor`. A pull request
row links out to GitHub, which stays the review surface for a PR; every other kind links to its cockpit page. A row with no revision
says so in place of the form, since the store refuses that decide.

