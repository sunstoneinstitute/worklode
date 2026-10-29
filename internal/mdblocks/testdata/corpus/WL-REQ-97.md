
`project.focus` is an ordered list of concerns, the project's current steering. It is a soft filter: it directs the choice only when no higher signal decides, and it never removes rows. Agents never idle while ready work exists. `concern_rank(task)` is the task's index in `focus`; an unlisted or null concern takes the worst rank.

