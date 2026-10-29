
Written inside the same `RecordEvent` transaction as the webhook that produced them, as natural-key upserts (`ON CONFLICT DO NOTHING`).

| Table | Columns | Meaning |
|---|---|---|
| `task_commits` | `task_id, repo, sha, source, seen_at` | attributes commits to tasks. Sources: pushes to task branches (pattern from `LODE_BRANCH_TEMPLATE`), PR correlation (head ref or `Worklode-Task:` body), the `Worklode-Task:` commit trailer, task-key references in default-branch commit messages, and `local_merge` from the git-side hooks |
| `main_commits` | `repo, sha, seq, pushed_at` | every default-branch push appends its commits in order. Main is linear in push order, so "X is included at Y" is `seq(X) <= seq(Y)`. A task's landed seq is the seq of the main commit that matched it |
| `env_deploys` | `repo, environment, main_seq, gh_status, flux_status, flux_seen, updated_at` | the per-environment deployed frontier, environment normalized to `dev`/`prod` |
| `github_release_frontiers` | `repo, tag, main_id, published_at` | the seq a published release covers; forward-only per tag |
