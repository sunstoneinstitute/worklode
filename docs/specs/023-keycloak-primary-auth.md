---
status: draft
issued: 2026-07-31
replaces:
  "#sec-3.1":
    - 002-github-app-auth.md#sec-3.2
    - 002-github-app-auth.md#sec-3.3
    - 002-github-app-auth.md#sec-3.4
amends:
  "#sec-3.5":
    - 002-github-app-auth.md#sec-3.5
---
# Spec 023 — Keycloak-primary auth and GitHub account linking

## 0. Summary {#sec-0}

Keycloak becomes the **sole** login mechanism for web and CLI. GitHub is
demoted from a parallel identity provider to a **link-only** OAuth flow: an
already-authenticated actor connects their GitHub account once, and worklode
stores the resulting user-to-server token so it can later call GitHub
attributed as "SunstoneWork on behalf of `<user>`". The link is verified
**strictly** against a Keycloak-asserted `github_username` attribute, happens
**lazily** at the point of first need, and is managed from the CLI via a new
`lode auth` command group. This spec delivers plumbing only — link flow,
storage, refresh — verified end to end; the first feature that writes to
GitHub as a user is a follow-up spec.

## 1. Motivation {#sec-1}

- **One actor per person.** Spec 002's dual-provider model creates two actor
  rows for the same human (`preferred_username` and `github:<id>`), which it
  explicitly punted on reconciling. Making Keycloak the only login removes
  the split instead of mapping it.
- **Attribution without escalation.** A stored user token is bounded by the
  intersection of App permissions, installation scope, and the user's own
  access. An org owner's token therefore ≈ the full App permission set. The
  App's ceiling is capped read-mostly (§F) so a compromise of the server —
  which holds both the DB and `LODE_TOKEN_ENC_KEY` — cannot push code or
  reach CI-trusted branches with anyone's token. Nothing shipped or specced
  needs `contents: write` (spec 011 requires Contents: **read**; spec 008's
  issue mirror needs Issues: write).
- **Roles from one source.** GitHub org/team role derivation duplicated what
  Keycloak groups already provide.

## 2. Current state (what changes) {#sec-2}

- Two login paths coexist: Keycloak (`/auth/login`) and GitHub
  (`/auth/github/login`), with `/auth/choose` between them
  (`internal/api/oidcweb.go`, `internal/api/githubweb.go`).
- GitHub login provisions separate `github:<id>` actors and derives roles
  from org/team membership (`internal/githubauth.Roles`).
- `github_user_tokens` is written by the GitHub login callback (AES-GCM via
  `internal/tokencrypt`) but **never read**: no refresh logic, no consumer.
- The OIDC client reads only `preferred_username`, `name`, `groups`
  (`internal/oidc/oidc.go`); no GitHub identity appears anywhere in the
  Keycloak path.
- `lode login` performs the provider-neutral browser loopback
  (`docs/specs/031-provider-neutral-cli-login.md`).

## 3. Design {#sec-3}

### 3.1 A. Keycloak is the only login {#sec-3.1}

- Remove `/auth/choose`, GitHub web login, GitHub actor provisioning, and
  org/team role derivation. `/auth/github/*` routes survive only as the link
  flow (§C), reachable exclusively from an authenticated session.
- Roles come from Keycloak `groups` only (`user` required, `admin` optional),
  unchanged from spec 001.
- Config removed: `LODE_GITHUB_ORG`, `LODE_GITHUB_ADMIN_TEAM`. The App no
  longer needs the Organization → Members: read permission.
- Existing `github:<id>` actor rows are left in place, orphaned (worklode is
  not in production; nothing meaningful references them). Cleanup is recorded
  in `docs/follow-ups.md` when this ships.

### 3.2 B. Expected GitHub identity from Keycloak {#sec-3.2}

- The realm's worklode client gains a protocol mapper exposing the user
  attribute `github_username` as a claim in ID tokens (deployment
  prerequisite, wired via the app-deployment Keycloak config).
- `internal/oidc.Claims` gains `GitHubUsername string
  `json:"github_username"``. `provisionActor` stores it on the actor as
  `expected_github_login`, re-synced on every login exactly like the admin
  flag. Login never fails for a missing attribute — it is only required to
  *link*.
- Storing the expectation at login is what makes strict matching possible
  later: the link flow runs long after login, when no Keycloak token is in
  hand (sessions are stateless cookies).

### 3.3 C. Link flow (web) {#sec-3.3}

1. `GET /auth/github/link` (authenticated) redirects to GitHub's authorize
   endpoint with signed state, as the login flow does today (confidential
   client, no PKCE).
2. `GET /auth/github/callback` exchanges the code, fetches `GET /user`
   (numeric `id`, `login`), and **strict-checks**: `login` must equal the
   session actor's `expected_github_login`, case-insensitively. Missing
   attribute or mismatch → the link is refused with an error naming the fix
   ("your Keycloak account has no/another GitHub username; get the
   `github_username` attribute corrected"). No row is written.
3. On success, upsert the link row keyed by the **worklode actor id** (§E).
   The numeric GitHub user id is the durable external identity; `login` is
   display metadata (renameable).
4. Linking is **lazy**: nothing prompts at login. Features that need GitHub
   surface a "Connect GitHub" redirect at the point of need; a settings/
   profile entry allows proactive linking. Re-linking (e.g. after `broken`,
   §E) repeats the same flow; GitHub skips the consent screen for an
   already-authorized App.

### 3.4 D. CLI — `lode auth` command group {#sec-3.4}

- `lode login` moves to `lode auth login`; the old spelling remains as a
  hidden alias. The loopback flow itself is unchanged.
- `lode auth link github`: the CLI calls a bearer-authed endpoint that mints
  a one-time, short-lived signed nonce bound to the calling actor and
  returns a link URL; the CLI opens the browser and polls link status until
  linked, refused, or the nonce expires. The nonce ties the browser flow to
  the CLI's actor without requiring a web session cookie.
- `lode auth status`: shows the logged-in identity, token expiry, and GitHub
  link state (unlinked / linked as `<login>` / broken — reconnect).

### 3.5 E. Token storage and refresh {#sec-3.5}

`github_user_tokens` is reworked (new migration) so the token row **is** the
link — a link exists iff a row exists, unlink = delete the row:

| column | notes |
|---|---|
| `actor_id` | PK, references `actors` |
| `github_user_id` | unique; durable external identity |
| `github_login` | display only, refreshed on re-link |
| `token_ciphertext` | AES-GCM (`LODE_TOKEN_ENC_KEY`) sealing `{access_token, refresh_token, access_expires_at}` |
| `status` | `active` \| `broken` |
| timestamps | created/updated |

Accessor: `store.UserToken(ctx, actorID)` returns a valid access token,
refreshing when expired or within a skew window. GitHub refresh tokens are
**single-use**, so the row is locked (`SELECT … FOR UPDATE`) for the duration
of a refresh; concurrent callers wait and reuse the fresh pair. A failed
refresh (revoked App authorization, >6-month lapse) sets `status = broken`;
callers translate that into "reconnect GitHub" guidance. No background
refresher — on-demand refresh suffices given the ~6-month refresh-token
lifetime.

### 3.6 F. GitHub App constraints (ops-side, recorded as requirements) {#sec-3.6}

- **Permission ceiling:** Repository → Contents: **read**, Actions: **read**,
  Deployments: **read**, Pull requests: **read**; Issues: **write** when the
  spec 008 issue mirror lands. **Never `contents: write`** — it is what turns
  a stolen token (user or installation) into a push to CI-trusted branches.
  A future feature needing repo writes gets a separate, narrowly-installed
  App rather than a widened ceiling here.
- **Installation scope:** selected repositories only; the provisioning and
  admin-cluster repos (whose CI holds cluster credentials) are excluded.
- **Independent guard:** no-bypass rulesets (require PR + review, no admin
  bypass) on the provisioning/admin repos — protects against *any* stolen
  org-owner credential, not just this table.
- Token encryption is unchanged: AES-GCM, key in a separate trust zone
  (1Password → ExternalSecret → pod env). A Postgres dump alone yields
  nothing usable.

Config after this spec: `LODE_GITHUB_APP_CLIENT_ID`,
`LODE_GITHUB_APP_CLIENT_SECRET`, `LODE_TOKEN_ENC_KEY`, `LODE_GITHUB_APP_ID`,
`LODE_GITHUB_APP_PRIVATE_KEY`, `LODE_GITHUB_WEBHOOK_SECRET` stay;
`LODE_GITHUB_ORG` and `LODE_GITHUB_ADMIN_TEAM` are removed.

## 4. Non-goals {#sec-4}

- Any feature that writes to GitHub as a user (issue mirror, comment relay)
  — own spec, first consumer of `store.UserToken`.
- Background/scheduled token refresh.
- Unlink UI beyond row deletion via admin/CLI.
- Cleanup migration for orphaned `github:<id>` actors (follow-up).
- hzprod rollout (with the `worklode-prod` App, as in spec 002).

## 5. Error handling {#sec-5}

- Link with missing `expected_github_login` → refused: "no GitHub username on
  your Keycloak account".
- Link with mismatched login → refused, naming both logins.
- OAuth code exchange / `GET /user` failure → 502, retriable, no row written.
- Refresh failure → row marked `broken`; `lode auth status` and the web UI
  surface "reconnect GitHub"; the triggering caller gets a typed error.
- CLI link nonce expired/consumed → clear terminal error, re-run to retry.
- Actor-kind conflict paths are unchanged (`errActorKindConflict`).

## 6. Testing {#sec-6}

- **Store:** link upsert and uniqueness (`github_user_id`), delete-as-unlink,
  refresh rotation persisting the new pair, the concurrent-refresh race
  (second caller blocks and reuses), broken-marking on refresh failure,
  AES-GCM round-trip (existing).
- **Handlers:** link + callback against a fake GitHub server — strict-match
  refusal (missing and mismatched), success upsert, state mismatch; removal
  of the login paths (routes gone, `/auth/choose` gone).
- **OIDC:** `github_username` claim parsed and synced onto the actor across
  logins.
- **CLI:** `lode auth login` alias behavior, `link github` start/poll against
  a fake server, `auth status` rendering of all three link states.
- **E2E:** OIDC login → `lode auth link github` → fake GitHub authorize →
  callback → `lode auth status` shows linked; admin diagnostics report link
  and token validity. Public surfaces only, per `e2e/` policy.

## 7. Open questions {#sec-7}

None blocking. The claim name is fixed to `github_username`; make it
configurable only if a second realm ever needs a different mapping.
