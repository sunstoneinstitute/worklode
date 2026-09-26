---
paths:
  - "deploy/base/migrations/**"
  - "deploy/base/kustomization.yaml"
---

# Database migrations

`deploy/base/migrations/`, golang-migrate, `NNNN_name.up.sql`/`.down.sql`
pairs.

They are **not** embedded in the binary or auto-applied — `lode-server` expects
the schema to exist (the compose `migrate` service and the K8s initContainer
apply them).

Rules:

- Never edit a shipped migration; add a new pair.
- Never pick a number. Name a new pair `NEW-<slug>.up.sql`/`.down.sql`; for
  several in one PR use `NEW1-<slug>`, `NEW2-<slug>`, which apply in that
  order. The `number-migrations` workflow renames them to the next free
  numbers on the PR branch when auto-merge is enabled. The merge queue
  rejects any `NEW` file left unnumbered.
- New migrations must also be listed in `deploy/base/kustomization.yaml`,
  by their `NEW` name.
- An accepted or approved migration task authorizes pushing its branch,
  opening its pull request, and merging it after review and required CI pass.
  Do not ask for separate merge approval.
- The pre-commit collision check renumbers a numbered migration when two
  branches claimed the same number, and leaves `NEW` files alone. Run it by
  hand with `./scripts/check-migrations.sh --no-fix`.
- `lode-migrate` and `Store.Migrate` refuse a `NEW` file. Store tests number
  them in a temp copy, so a PR's new migration is still tested.

Store tests that exercise a new migration need a reachable Postgres with
pgvector — see the Commands section of CLAUDE.md for the DSN and the
skip-silently caveat.

For general golang-migrate CLI usage, see the golang-migrate plugin skills.
