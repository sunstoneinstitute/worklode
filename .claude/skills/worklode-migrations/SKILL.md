---
name: worklode-migrations
description: Use when adding or changing a database migration in this repo — "add a migration", "add a column", "change the schema", "new table", "alter the tasks table", "migration number collision", "fix migration NNNN", "down.sql". Covers the deploy/base/migrations layout, the never-edit-a-shipped-migration rule, and the kustomization.yaml listing. For general golang-migrate CLI usage, see the golang-migrate plugin skills.
---

# Database migrations

`deploy/base/migrations/`, golang-migrate, `NNNN_name.up.sql`/`.down.sql`
pairs.

They are **not** embedded in the binary or auto-applied — `lode serve` expects
the schema to exist (the compose `migrate` service and the K8s initContainer
apply them).

Rules:

- Never edit a shipped migration; add a new pair with the next number.
- New migrations must also be listed in `deploy/base/kustomization.yaml`.
- The pre-commit collision check renumbers your migration automatically when
  two branches claimed the same number. Run it by hand with
  `./scripts/check-migrations.sh --no-fix`.

Store tests that exercise a new migration need a reachable Postgres with
pgvector — see the Commands section of CLAUDE.md for the DSN and the
skip-silently caveat.
