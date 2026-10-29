
`project_repos.done_state` is the per-repo marker for "fully delivered", feeding `taskClosed` and the resolver's frontier logic. Workflow owns which edges exist. `done_state` must be a state of the governing workflow, and disagreement is a warning on the write. A task's definition of done is its deliverables and its workflow's delivered states together.

