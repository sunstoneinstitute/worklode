# v1 follow-ups

Non-blocking items from the v1 review rounds. Import these as tracker tasks
once an instance is running (dogfooding); until then this file is the list.

- **Artifact correlation hardening.** Ingest `registry_package` webhooks so
  `docker_image` artifacts exist; resolve `release.target_commitish` branch
  names to commit SHAs; normalize OCI digests (`sha256:`) in flux
  `revisionSHA`. Today only `git_tag` artifacts are created, so the
  flux-revision → artifact → task chain rarely connects.
- **`assignee` filter** on `GET /api/v1/tasks` (join active leases).
- **PR closed without merge**: release the lease and surface the task on the
  board (today it stays `in_review`; `wl task reopen` is the manual path).
- **`wl import horndb-tasks`**: one-off importer for TASKS.md + GitHub issues
  (spec §Migration).
- **k8s deployment manifests** (flux) for the server and the watcher; RBAC
  for `wl watch` in-cluster.
- **Claude Code skill** in the claude-plugins repo teaching the
  claim → work → report → complete loop.
- **Watcher test timing**: `TestBelowRestartThresholdNotReported` uses a 5s
  `eventually` timeout and flaked once under heavy host load; bump the fence
  timeout or make it load-tolerant.
- **`LogChange` clock**: `state_log.at` uses the wall clock, not the store's
  injectable `nowFn`; plumb `now` through for fully deterministic timelines.
- **Shared HTTP helpers**: `writeJSON`/`writeErr` are duplicated between
  `internal/api` and `internal/hooks`; consolidate if a third copy appears.
- **Notifications** (Slack/email) and the HornDB/RDF projection remain
  deliberate non-goals until the tracker has real usage.
