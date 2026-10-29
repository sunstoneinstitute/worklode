
All webhook handlers use HMAC signature checks and the idempotency of `RecordEvent`. The GitHub App and its webhook secret are in WL-REQ-63.

| Handler | Behaviour |
|---|---|
| `push` | routed by ref: a task branch inserts `task_commits`; the default branch appends `main_commits` and sets landed seqs; `last-deploy/<env>` records deploy-branch SHA to main seq |
| `deployment_status` | normalizes the environment, resolves the SHA to a main seq, upserts `gh_status`. A SHA unknown on main is dropped and self-heals on the next deploy |
| Flux | resolves the revision SHA to `(repo, main seq)`, updates `flux_status`; failures mark the attempt failed. A revision matching no repo records nothing and does not latch |
| `pull_request` | merged-PR handling records facts only; the transition is the resolver's |
| `release` | creates the artifact and records the release frontier |
| `registry_package` | a published container version mints a `docker_image` artifact keyed by image name and tag with its OCI digest. Other package types and untagged versions are events without artifacts. Resolves no frontier |
| `POST /api/v1/merges` | the local reporter: `{repo, sha, tasks[]}` from `lode-hook post-merge`/`post-commit`. Appends the main commit, attributes it with source `local_merge`, resolves. Carries an actor, requires the task-mutation permission. `worklode_local_merge_reports_total{result}` counts `advanced|duplicate|unknown_task`; a steady stream of duplicates is what a healthy webhook plus clone pair looks like |

Every handler calls the delivery resolver (WL-SPEC-75#sec-10.2a) for affected tasks at the end of its apply.

