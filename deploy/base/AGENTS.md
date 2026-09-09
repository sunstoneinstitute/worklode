Applies only to `deploy/base/migrations/**` and `deploy/base/kustomization.yaml`.

# Database migrations

`deploy/base/migrations/`, golang-migrate, `NNNN_name.up.sql`/`.down.sql`
pairs.

They are **not** embedded in the binary or auto-applied — `lode-server` expects
the schema to exist (the compose `migrate` service and the K8s initContainer
apply them).

Rules:

- Never edit a shipped migration; add a new pair with the next number.
- New migrations must also be listed in `deploy/base/kustomization.yaml`.
- An accepted or approved migration task authorizes pushing its branch,
  opening its pull request, and merging it after review and required CI pass.
  Do not ask for separate merge approval.
- The pre-commit collision check renumbers your migration automatically when
  two branches claimed the same number. Run it by hand with
  `./scripts/check-migrations.sh --no-fix`.

Store tests that exercise a new migration need a reachable Postgres with
pgvector — see the Commands section of CLAUDE.md for the DSN and the
skip-silently caveat.

For general golang-migrate CLI usage, see the golang-migrate plugin skills.
