
`GET /api/v1/projects/{id}/summary` answers with one `ProjectSummary`, derived per request and never stored:

- `past_due`: open tasks in the project whose `due` is before today, each as a `SummaryItem` (`id`, `label` = title, `due`, `url` = canonical task URL), oldest due first.
- `awaiting_review`: tasks in state `in_review`, and backbone documents of the project whose `reviewers_awaiting` is non-empty, as `SummaryItem` (a document's `due` is `""`).
- `workloads`: one `Workload` per Crew member (`GET /api/v1/projects/{id}/participants`), in roster order: `actor`, `availability`, `done` (closed tasks assigned to the actor), `total` (all tasks assigned to the actor, open and closed), and the actor's own `past_due`, `upcoming` (open tasks with a `due` today or later, soonest first, at most 5) and `awaiting_review` lists. `availability` is `overbooked` when the actor has at least one past-due task, else `open`. A member with no tasks still gets a `Workload` with zero counts and empty lists.

Every date is a `YYYY-MM-DD` string; the client turns it into "2 days late" or "3 weeks left". A project with no tasks answers empty lists and one empty workload per member. An unknown project is a 404. Guarded by `permProjectRead`.

Rationale: the SPA's Overview summary card asks three questions, what is late, what waits on review, and what each person carries, and each must come from recorded facts (WL-REQ-329), so the server derives them and the client only formats.
