---
status: accepted
covers:
  - docs/specs/001-identity-and-authentication.md#sec-3
  - docs/specs/001-identity-and-authentication.md#sec-9.2
replaces:
  ".":
    - 2026-08-02-keycloak-primary-auth-1-keycloak-only.md
---
# Keycloak-Primary Auth — the identity core

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement spec 001 §3 and §3.2: Keycloak becomes the only login
(GitHub web login, the provider chooser, and org/team role derivation are
removed), every login records the Keycloak-asserted `github_username` on the
actor as `expected_github_login`, and the pre-existing `github:169939` actor is
merged into `stig@sunstoneinstitute.ai` by a reviewed one-off script.

**Relation to the 2026-08-02 series:** the three
`2026-08-02-keycloak-primary-auth-*` plans were written pre-production and never
executed. This plan carries part 1's scope forward against production reality
(actor merge instead of orphaning, migration number 0014); parts 2–3 (§3.3–3.6)
remain deferred — see Non-goals.

**Read first:** `docs/specs/001-identity-and-authentication.md` §3.1–§3.2,
`internal/api/githubweb.go` (deleted here), `internal/api/oidcweb.go`,
`internal/api/oidcauth.go` (`provisionActor`), `internal/api/cliauth.go`,
`internal/store/actors.go`.

## Global Constraints

- After every task: `go build ./... && go test ./...` green. Store and API
  tests need Postgres with pgvector (default DSN
  `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`,
  override `TEST_POSTGRES_DSN`); without a reachable Postgres they skip
  silently, which proves nothing — run them against a real database.
- `go generate ./...` produces no diff (templ/Tailwind artifacts are committed).
- New migrations are listed in `deploy/base/kustomization.yaml`;
  `./scripts/check-migrations.sh --no-fix` passes. If it renumbers 0014, use
  the number it assigns everywhere.
- The `/api/v1` bearer-token contract is untouched. The GitHub App
  installation identity (`LODE_GITHUB_APP_ID`/`LODE_GITHUB_APP_PRIVATE_KEY`,
  `s.appAuth`) and the HMAC-signed webhooks (`/hooks/github`, `/hooks/flux`)
  are untouched — inbox import, delivery discovery, and skill sync do not use
  user login.
- `LODE_GITHUB_APP_CLIENT_ID`, `LODE_GITHUB_APP_CLIENT_SECRET`, and
  `LODE_TOKEN_ENC_KEY` stay (spec 023 retains them for the future link flow).
  Only `LODE_GITHUB_ORG` and `LODE_GITHUB_ADMIN_TEAM` are removed.
- No new endpoint, background loop, or outbound call is added, so no new
  `worklode_*` metrics are required (spec 022); removed routes simply drop out
  of the `worklode_http_*` label space. If scope shifts, follow 022.
- The production actor merge (Task 3's script) is out-of-band and human-gated:
  no task in this plan executes anything against the production database.
- The Keycloak protocol mapper exposing `github_username` (admin-cluster repo,
  spec 001 §9.2) is a deployment prerequisite for the claim to arrive; login
  tolerates its absence, so it does not block any task here.

## Non-goals / follow-up

Spec 001 §9.3–§3.6 — the GitHub *link* flow, the `github_user_tokens` rework,
the `lode auth` command group, and token refresh — are already drafted as the
accepted-but-deferred plans `2026-08-02-keycloak-primary-auth-2-link-and-tokens.md`
and `2026-08-02-keycloak-primary-auth-3-cli-and-e2e.md` (part 2 now requires this
plan in place of the superseded part 1). They are executed when the first "write
to GitHub as a user" feature needs them (023 itself says that plumbing has no
consumer yet). Until then the
`github_user_tokens` table and its store methods
(`internal/store/github_tokens.go`) stay in place, dormant. The hzprod GitHub
App rollout stays out of scope per 001 §12.

## Tasks

### Task 1 — Remove the GitHub web login

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Delete GitHub as a login provider (spec 001 §3). Keycloak (`/auth/login`)
becomes the only interactive login, web and CLI.

**`internal/api/githubweb.go` — delete the file.** It holds only login-flow
code: `githubLogin`, `githubCallback`, `authChoose`, `provisionGitHubActor`,
`storeGitHubToken`, `githubUserToken`, `githubCallbackURL`, and `loginTarget`
(which moves, see below). Removing `storeGitHubToken` removes the only writer
of `github_user_tokens`; the table and `internal/store/github_tokens.go`
(`UpsertGitHubUserToken`/`GetGitHubUserToken`, now caller-free outside tests)
are **deliberately kept dormant** for the deferred §3.5 rework — do not flag
or delete them.

**`internal/api/server.go`:**

- Delete the three route registrations (~lines 353–355):
  `GET /auth/github/login`, `GET /auth/github/callback`, `GET /auth/choose`.
- Delete the `GitHubOrg` and `GitHubAdminTeam` fields from `Config`
  (~lines 62–65) and the `LODE_GITHUB_ORG is required` startup guard
  (~lines 262–264).
- Change the constructor call to the two-argument form:
  `s.gh = githubauth.New(cfg.GitHubClientID, cfg.GitHubClientSecret)`.
- Reword the `gh`/`tokenCipher` field comment: nil unless the GitHub App
  OAuth client is configured; reserved for the future account-link flow
  (spec 001 §9.3); login never touches them.

**`internal/api/oidcweb.go`:**

- Move `loginTarget` here, simplified — Keycloak is the only destination:
  `return "/auth/login?next=" + url.QueryEscape(next)` (add `net/url` import).
- `webAuth` gates on `s.oidc == nil` alone (drop `&& s.gh == nil`).
  Deliberate behavior change: a server configured with only the App OAuth
  client serves the web UI open, matching the rule that no configured login
  provider means development mode.
- Update the package comment (it references `githubweb.go` and the chooser).

**`internal/api/cliauth.go`:** `cliLogin` (~line 157) and `wellKnownLogin`
(~line 216) gate on `s.oidc == nil` alone; `wellKnownLogin` drops the
provider-accumulating block and always responds
`"providers": []string{"keycloak"}`. Update both doc comments.

**`internal/githubauth/githubauth.go`:** delete the `Roles` type, the `Roles`
method, `activeMembership`, `membershipResp`, and the `Org`/`AdminTeam`
fields; narrow `New` to `New(clientID, clientSecret string)`. Keep
`AuthCodeURL`, `Exchange`, `FetchIdentity`, and `Token` — dormant raw material
for the §3.3 link flow. Update the package comment. `app.go` (installation
auth) is untouched.

**`internal/cmd/serve.go`:** delete the `GitHubOrg`/`GitHubAdminTeam` env
lines (91–92).

**`deploy/overlays/hzdev/kustomization.yaml`:** delete the two `op: add`
patch entries for `/data/LODE_GITHUB_ORG` and `/data/LODE_GITHUB_ADMIN_TEAM`.
hzprod never set either (verified) — nothing to change there.

**Tests:**

- Delete `internal/api/githubweb_test.go`, first re-homing the two tests that
  cover surviving config validation — `TestNewServerRejectsMalformedPublicURL`
  and `TestNewServerRejectsBadTokenEncKey` — into
  `internal/api/server_test.go` (bring the `newGitHubTestStore` helper or
  reuse an existing store helper; drop `GitHubOrg` from their `Config`
  literals).
- In `internal/api/oidcweb_test.go`, add `TestGitHubLoginRoutesRemoved`: build
  the server via `NewServer` with OIDC configured and assert `GET
  /auth/choose`, `/auth/github/login`, `/auth/github/callback` all return 404.
  Re-home `TestLoginTarget` here, rewritten: `loginTarget` returns
  `/auth/login?...` both with and without `s.gh` set.
- In `internal/api/oidcauth_test.go`, add `TestNewServerAcceptsGitHubWithoutOrg`:
  `NewServer` succeeds with OIDC + `GitHubClientID`/`GitHubClientSecret`/
  `TokenEncKey` and no org.
- In `internal/api/cliauth_test.go`: rewrite
  `TestWellKnownLoginReportsProviders` to expect exactly `["keycloak"]` (use a
  real verifier against the `oidctest` fake issuer); rework any test that
  builds a gh-only server for login (`TestCLILoginValidatesLoopback`) to use
  OIDC and expect the final redirect at `/auth/login`; add
  `TestCLILoginRequiresOIDC` asserting `/auth/cli/login` and
  `/.well-known/lode-login` 404 on a gh-only server.
- In `internal/githubauth/githubauth_test.go`, delete the `Roles`/membership
  tests and fix remaining `New(...)` calls; keep `app_test.go` untouched.

Proof: `go build ./... && go test ./internal/api/ ./internal/githubauth/
./internal/cmd/...` green against Postgres, and
`grep -rn "GitHubOrg\|GitHubAdminTeam\|LODE_GITHUB_ORG\|LODE_GITHUB_ADMIN_TEAM\|provisionGitHubActor\|authChoose" --include='*.go' .`
returns nothing.

- [ ] Delete `githubweb.go`; move `loginTarget` into `oidcweb.go`; fix `webAuth`
- [ ] Drop routes, config fields, guard, and constructor args in `server.go`
- [ ] Gate `cliauth.go` on OIDC alone; fixed `["keycloak"]` provider list
- [ ] Trim `githubauth.Client`; keep `app.go` and the OAuth primitives
- [ ] Remove env plumbing in `serve.go` and the hzdev overlay
- [ ] Update/re-home/add the tests listed above; run the proof commands

### Task 2 — Record the Keycloak-asserted GitHub login

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

Spec 001 §9.2: carry the ID token's `github_username` claim onto the actor as
`expected_github_login`, re-synced on every login exactly like the admin flag.
Login never fails when the attribute is absent. Blocked by Task 1 so the
`UpsertHumanActor` signature change touches only the OIDC path (the GitHub
provisioner's call site is already gone).

**Migration** `deploy/base/migrations/0014_actor_github_expectation.up.sql`:

```sql
-- The GitHub login Keycloak asserts for this actor (spec 001 §9.2), recorded
-- at login so the future link flow can strict-match long after login. NULL
-- means the Keycloak account carries no github_username attribute.
ALTER TABLE actors ADD COLUMN expected_github_login text;
```

`0014_actor_github_expectation.down.sql`:

```sql
ALTER TABLE actors DROP COLUMN expected_github_login;
```

List both files in `deploy/base/kustomization.yaml`'s `worklode-migrations`
configMapGenerator, after the `0013_project_focus_decision` pair. Run
`./scripts/check-migrations.sh --no-fix`.

**`internal/oidc/oidc.go`:** add to `Claims`:
`GitHubUsername string `json:"github_username"`` with a comment noting it is
the realm's `github_username` user attribute, empty when unset, never required
to log in.

**`internal/store/actors.go`:**

- `Actor` gains `ExpectedGitHubLogin string`.
- `UpsertHumanActor(ctx, id, displayName string, admin bool, expectedGitHubLogin string)`:
  store empty as `NULL`; the `ON CONFLICT` update overwrites
  `expected_github_login` alongside `display_name` and `admin`, so a cleared
  Keycloak attribute clears the column at the next login.
- `GetActor` selects and scans the new column (`sql.NullString`).

**Call sites:** `provisionActor` (`internal/api/oidcauth.go`) passes
`c.GitHubUsername` — it is shared by the token exchange and the web callback
(`internal/api/oidcweb.go`), so one change covers both provisioners. Fix the
remaining call sites with `grep -rn "UpsertHumanActor" --include='*.go' .`
(test files append `""`).

**Tests:**

- `internal/oidc/oidc_test.go`: extend `TestVerifyValidToken` (or add a
  sibling) to sign a token carrying `github_username` and assert
  `Claims.GitHubUsername` parses, and stays empty when the claim is absent.
- `internal/store/actors_test.go`: `TestUpsertHumanActorSyncsGitHubExpectation`
  — first upsert with `"stigsb"` persists it; second upsert with `""` clears
  it (NULL round-trips as empty string).
- `internal/api/oidcauth_test.go`: `TestOIDCTokenExchangeSyncsGitHubUsername`
  — exchange with the claim sets `expected_github_login`; a second exchange
  without the claim clears it and still returns 201.
- `internal/api/oidcweb_test.go`: extend `TestAuthCallbackRoundTrip`'s claims
  with `github_username` and assert the actor's `ExpectedGitHubLogin` after
  the callback.
- Migration round-trip: the store suite migrates every test database from
  scratch, so a broken up file fails everything; verify the down file with an
  up→down→up pass against a scratch database (the golang-migrate CLI or the
  golang-migrate:test-roundtrip skill).

Proof: `go test ./internal/oidc/... ./internal/store/ ./internal/api/` green
against Postgres.

- [ ] Migration pair + kustomization listing + collision check
- [ ] `Claims.GitHubUsername`; oidc parse test
- [ ] Store column, upsert re-sync, scan; store test
- [ ] `provisionActor` passes the claim; api tests (exchange + web callback)
- [ ] Migration up→down→up verified

### Task 3 — Actor-merge script for github:169939

```yaml
kind: chore
priority: high
skills:
  - superpowers:verification-before-completion
blockedBy: [2]
```

Produce and dry-run-verify — **never execute against production** — the one-off
SQL script that merges the GitHub login actor into the Keycloak actor
(spec 001 §3). Blocked by Task 2: the script sets `expected_github_login`,
so migration 0014 must exist wherever it runs.

Create `scripts/merge-github-actor-169939.sql` with exactly this content
(transaction control deliberately lives outside the file so the same script
serves both the rollback dry run and the real single-transaction run):

```sql
-- One-off merge of the GitHub login actor into the Keycloak actor
-- (spec 023 #sec-3.1): repoint every reference from github:169939 to
-- stig@sunstoneinstitute.ai, record the expected GitHub login, delete the
-- GitHub actor row. Idempotent — a re-run finds no github:169939 rows and
-- changes nothing. Requires migration 0014 (actors.expected_github_login).
-- Run wrapped in a transaction (psql --single-transaction, or an explicit
-- BEGIN/ROLLBACK for a dry run). Historical events/state_log payloads keep
-- github:169939 as append-only provenance and are not rewritten.

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM actors
                 WHERE id = 'stig@sunstoneinstitute.ai' AND kind = 'human') THEN
    RAISE EXCEPTION 'target actor stig@sunstoneinstitute.ai missing: log in via Keycloak once, then re-run';
  END IF;
END $$;

UPDATE tasks  SET created_by = 'stig@sunstoneinstitute.ai' WHERE created_by = 'github:169939';
UPDATE tasks  SET assignee   = 'stig@sunstoneinstitute.ai' WHERE assignee   = 'github:169939';
UPDATE leases SET actor_id   = 'stig@sunstoneinstitute.ai' WHERE actor_id   = 'github:169939';
UPDATE tokens SET actor_id   = 'stig@sunstoneinstitute.ai' WHERE actor_id   = 'github:169939';

-- github_user_tokens.actor_id is the primary key: repoint only when the
-- target has no row of its own, then drop whatever remains under the old id.
UPDATE github_user_tokens SET actor_id = 'stig@sunstoneinstitute.ai'
 WHERE actor_id = 'github:169939'
   AND NOT EXISTS (SELECT 1 FROM github_user_tokens
                   WHERE actor_id = 'stig@sunstoneinstitute.ai');
DELETE FROM github_user_tokens WHERE actor_id = 'github:169939';

UPDATE actors SET expected_github_login = 'stigsb'
 WHERE id = 'stig@sunstoneinstitute.ai';

DELETE FROM actors WHERE id = 'github:169939';
```

Notes for the file's reviewer, stated in the task rather than re-derived:
these five columns are the complete set of foreign keys into `actors`
(baseline 0001 + 0010); production counts verified 2026-08-10 were
`tasks.created_by` 59, `tokens.actor_id` 1, `github_user_tokens.actor_id` 1,
`tasks.assignee` 0, `leases.actor_id` 0 — the runner compares psql's `UPDATE`
tallies against these (created_by may have grown). The admin flag is not set
here: Keycloak owns it and re-syncs it at the next login.

**Dry-run verification (the task's proof, against a scratch database only):**

- [ ] Create a scratch database and apply all migrations through 0014
      (`migrate -path deploy/base/migrations -database "$SCRATCH_DSN" up`, or
      the compose `migrate` service)
- [ ] Seed the production shape: actors `github:169939` (human) and
      `stig@sunstoneinstitute.ai` (human), one task with
      `created_by='github:169939'`, one `tokens` row and one
      `github_user_tokens` row (any bytea) for it
- [ ] `psql "$SCRATCH_DSN" -v ON_ERROR_STOP=1 -c 'BEGIN' -f scripts/merge-github-actor-169939.sql -c 'ROLLBACK'`
      — completes without error; a second dry run after a committed pass
      changes zero rows (idempotency)
- [ ] After a committed scratch run, assert: no row in any table references
      `github:169939`; the target actor has `expected_github_login='stigsb'`;
      the token authenticates as the target actor
- [ ] Record the dry-run transcript in the task/PR description

**Production run (out-of-band, after this plan's code deploys — the deploy's
initContainer applies 0014):** a human, with explicit approval, runs

```
kubectl --context dev/default -n worklode exec -i worklode-postgres-1 -- \
  psql -U postgres -d worklode --single-transaction -v ON_ERROR_STOP=1 -f - \
  < scripts/merge-github-actor-169939.sql
```

No agent runs this; the plan is complete when the script is merged and the
dry run is evidenced.
