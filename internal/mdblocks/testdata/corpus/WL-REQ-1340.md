
- `github_pull_requests.queued_at`, set when a `merge_group` event with action
  `checks_requested` names the PR (parsed from the group's `head_ref`) and
  cleared when the group is destroyed or the PR closes; ingested through the
  signed-webhook path so `lode inbox import` replays it. The event also
  records the PR's task so the stream can resolve it.
- `github_branch_rules`: per repository, whether its default branch has a
  `merge_queue` rule and a `pull_request` rule, read through the App (`GET
  /repos/{owner}/{repo}/rules/branches/{branch}`). The server refreshes it at
  start, when a repository is mapped, on the `repository_ruleset` webhook and
  once a day. A failed read keeps the last known fact; an unknown repository
  reads as no queue. The page never asks GitHub on a request. `lode doctor`
  reads the `pull_request` rule through `GET /api/v1/repos/branch-rules`
  (WL-SPEC-72 §3).
- `tasks.plan_doc` is the only link from a task to its plan. The page never
  infers links from task text. `lode task edit --plan <ref>` sets it on a task
  minted before plans recorded their tasks, logged as a change event on the
  task; it refuses a task already linked to a different plan.
