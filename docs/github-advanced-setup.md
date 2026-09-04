# GitHub setup: merge queue and auto-merge

How a repo worked by lode workers is configured so a PR lands the moment its
checks pass, with no human merging or rebasing it. Worklode itself is the
worked example; the horndb section at the end says what differs there.
`/lode:setup-merge-queue` in the lode plugin walks these steps for any repo.

## Why

Measured over the PRs opened between 2026-08-24 and 2026-09-04:

- Every merge and every rebase was done by hand by one person.
- A third of worklode PRs waited more than an hour after their checks went
  green. The median wait after green was 8 minutes, p90 was 5 hours.
- A PR whose files overlapped a PR that landed first took 8 times longer to
  merge (median 5.2 h against 0.6 h). Hand-rebasing several PRs in one
  sitting then cancelled each other's CI runs.

A merge queue removes both waits: the worker arms auto-merge when it opens
the PR, GitHub builds each queued PR on top of main, and merges it when the
checks pass.

## The three pieces

1. **Repo setting: allow auto-merge.** Without it `gh pr merge --auto` is
   refused.
2. **Ruleset on `main`: required status checks plus a merge queue.** Required
   checks are what auto-merge waits for. The merge-queue rule is what makes
   the merge itself sequential and rebased.
3. **Workflow: run the checks on `merge_group` events.** The queue builds a
   synthetic ref and waits for the same check names to report on it. A
   workflow that only triggers on `pull_request` never reports, and the entry
   times out.

The worker's side is one command after `gh pr create`: `gh pr merge --auto
--squash`. The `done` skill, the `lode-worker` agent and the
`working-under-worklode` skill all say so.

## Order of operations

Both ruleset rules wait for the workflow change: the required check is the
`checks` job that change adds, and the merge-queue rule needs `merge_group`
support on `main`, or the first queued PR sits until the check timeout. So:

1. Enable auto-merge on the repo.
2. Land the workflow change (this is what the PR for WL-659 does).
3. Add the required-status-checks and merge-queue rules in one `PUT`.

## Commands

All of these need repo admin.

### 1. Allow auto-merge

```bash
gh api -X PATCH repos/sunstoneinstitute/worklode -F allow_auto_merge=true
```

### 2. The workflow

`.github/workflows/pr-checks.yml` has four parts that the queue relies on:

- `on.merge_group.types: [checks_requested]` next to `pull_request`.
- The `concurrency.group` falls back to `github.ref` when there is no PR
  number, so queued entries do not cancel each other.
- The `gate` job's first branch: on a `merge_group` event it sets
  `trusted=true`, `run=true`, `obsidian=true` and exits. There is no PR
  payload to read file lists or author association from, and every PR in
  the group already passed the gate on its own.
- The `checks` job at the end: `if: always()`, needs every other job, and
  is the one check the ruleset names.

### 3. The ruleset

Worklode's `main` ruleset is id `19780760` ("protect main"). Find another
repo's with `gh api repos/<owner>/<repo>/rulesets`. The `PUT` replaces the
whole rule list, so the file below carries the existing `deletion` and
`non_fast_forward` rules too.

The only required check is the `checks` job at the end of the workflow. It
has `if: always()`, needs every other job, and passes when each of them
either succeeded or was skipped by its own `if:`. Requiring the conditional
jobs directly (the docs-only skip, the subtree-scoped `obsidian` job) leaves
a PR blocked whenever one of them never reports.

The bypass entry lets repository admins push to `main` directly, which
keeps the small direct commits this repo also carries working. A PR with
auto-merge armed always goes through the queue regardless of who opened it.
Remove the entry to make the queue mandatory for everyone.

```bash
cat > ruleset.json <<'EOF'
{
  "name": "protect main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
  "bypass_actors": [
    {"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}
  ],
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"},
    {"type": "required_status_checks", "parameters": {
      "strict_required_status_checks_policy": false,
      "do_not_enforce_on_create": false,
      "required_status_checks": [{"context": "checks"}]}},
    {"type": "merge_queue", "parameters": {
      "merge_method": "SQUASH",
      "grouping_strategy": "ALLGREEN",
      "max_entries_to_build": 5,
      "max_entries_to_merge": 5,
      "min_entries_to_merge": 1,
      "min_entries_to_merge_wait_minutes": 0,
      "check_response_timeout_minutes": 60
    }}
  ]
}
EOF
gh api -X PUT repos/sunstoneinstitute/worklode/rulesets/19780760 --input ruleset.json
```

`strict_required_status_checks_policy` stays false: the queue already
rebuilds on top of `main`, so requiring the PR branch itself to be current
would only force the rebase round-trips the queue exists to remove.
`SQUASH` matches how PRs have been landing (`<title> (#N)` commits).

## Checking it works

```bash
gh api repos/sunstoneinstitute/worklode --jq .allow_auto_merge
gh api repos/sunstoneinstitute/worklode/rulesets/19780760 --jq '[.rules[].type]'
gh pr view <n> --json autoMergeRequest,mergeStateStatus
```

A PR in the queue shows `mergeStateStatus: "BLOCKED"` until its group
builds, and a `PR Checks` run with event `merge_group` appears under
Actions. `lode task timeline <id>` shows the same `ci` and `landed` events it
always did; nothing on the lode side changes.

## What still needs a person

A PR the queue cannot rebase cleanly is dequeued with a conflict. Nothing
retries it yet: the worker has already released the lease, so the PR sits
until someone rebases it. The follow-up is server-side, since the
`pull_request` webhook already carries the mergeable state: mint a rebase
task for the worker the way `doc-lifecycle` mints review tasks.

## horndb

Same three pieces. Its `main` ruleset is id `17377439`. `ci.yml` needs the
`merge_group` trigger and a `gate` branch that treats the event as trusted
(the existing one only trusts non-`pull_request` events by name, so a
`merge_group` event falls into the code-owner check with an empty author and
skips the build). Its concurrency group is already keyed on `github.ref`.
It also needs an aggregating `checks` job like the one above, and that is
the one check its ruleset names.
