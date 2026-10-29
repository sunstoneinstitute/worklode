
`lode review add` mints a `kind = 'review'` task per reviewer in
`doc_reviewers` or `task_reviewers`, assigned via `tasks.assignee`, so an
agent reviewer picks it up through `lode task claim --next --kind review`. This routes
the work and grants no authority.

