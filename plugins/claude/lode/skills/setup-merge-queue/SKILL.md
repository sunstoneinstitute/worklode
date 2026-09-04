---
name: setup-merge-queue
description: One-time repo setup so worker PRs land without a human — allow auto-merge, run PR checks on merge_group, require one aggregating check, turn on the merge queue
argument-hint: "[owner/repo]"
disable-model-invocation: true
allowed-tools: Bash(gh *) Bash(git *)
---

Invocation arguments: $ARGUMENTS

This applies the setup described in worklode's `docs/github-advanced-setup.md`
to one repo. It needs repo admin. The repo is the argument if given, else
`gh repo view --json nameWithOwner -q .nameWithOwner` from the current
directory. Every step is idempotent, so re-running after a partial run is fine.

## 1. Allow auto-merge

```bash
gh api -X PATCH repos/<repo> -F allow_auto_merge=true --jq .allow_auto_merge
```

## 2. The PR workflow

Find the workflow under `.github/workflows/` whose `on.pull_request` targets
the default branch. Check four things and add whichever is missing:

- `merge_group: {types: [checks_requested]}` under `on`. The queue builds a
  synthetic ref and waits for the same check names on it; a workflow that only
  triggers on `pull_request` never reports, and the entry times out.
- The `concurrency.group` must not collapse every queue build into one group.
  Key it on `github.event.pull_request.number || github.ref`.
- Any gate or trust job that reads `github.event.pull_request` must, when
  `github.event_name == 'merge_group'`, treat the build as trusted and run
  every check. There is no PR payload on that event, and every PR in the
  group already passed the gate on its own.
- A final aggregating job, and nothing else, is what the ruleset will require.
  Requiring conditional jobs directly blocks a PR whenever one of them never
  reports. Fill `needs` with every other job in the workflow:

  ```yaml
  checks:
    needs: [gate, lint, test]
    if: always()
    runs-on: ubuntu-latest
    timeout-minutes: 2
    steps:
      - name: Fail if any job failed or was cancelled
        env:
          RESULTS: ${{ toJSON(needs) }}
        run: |
          echo "$RESULTS" | jq -r 'to_entries[] | "\(.key): \(.value.result)"'
          echo "$RESULTS" | jq -e 'all(.[]; .result == "success" or .result == "skipped")' > /dev/null
  ```

If anything changed: commit on a branch, push, open a PR, and stop. Tell the
user to run this skill again once it has landed. Steps 3 and 4 need the
`checks` job and the `merge_group` trigger on the default branch, or the first
queued PR sits until the check timeout.

## 3. The ruleset

```bash
gh api repos/<repo>/rulesets --jq '.[] | {id,name,target,enforcement}'
gh api repos/<repo>/rulesets/<id>
```

Take the ruleset targeting the default branch (`~DEFAULT_BRANCH` in its
conditions). If there is none, `POST repos/<repo>/rulesets` with the same body
shape. The `PUT` replaces the whole rule list, so carry every existing rule
and existing `bypass_actors` over, and add these two rules:

```json
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
```

`merge_method` follows how the repo's PRs already land; `SQUASH` is the
default. Strict status checks stay off: the queue already rebuilds on top of
the default branch, so requiring the PR branch to be current would only bring
back the rebase round-trips the queue removes.

If the ruleset had no `bypass_actors`, add
`{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}` so
repo admins can still push to the default branch directly, and say so in the
report; a PR with auto-merge armed goes through the queue regardless. Removing
the entry makes the queue mandatory for everyone.

Show the user the full JSON before writing it, then:

```bash
gh api -X PUT repos/<repo>/rulesets/<id> --input ruleset.json
```

## 4. Verify and report

```bash
gh api repos/<repo> --jq .allow_auto_merge
gh api repos/<repo>/rulesets/<id> --jq '[.rules[].type]'
```

Report what each step changed. From now on `gh pr merge --auto --squash`
right after `gh pr create` hands a PR to the queue, which is what `/lode:done`
does.
