# Spec 002 — GitHub App authentication for worklode

**Date:** 2026-07-20
**Status:** Design — approved shape, pending spec review
**Umbrella:** `000-umbrella-architecture.md`
**Amended by:** CLI login superseded by `docs/plans/2026-07-20-provider-neutral-cli-login-design.md`

## Summary

Add a GitHub App as an **additional** identity provider for the web UI and CLI,
**alongside** the existing Keycloak OIDC (which is left in place, unchanged).
Beyond login, capture a per-user **user-to-server** token so worklode can call
the GitHub API attributed as "SunstoneWork on behalf of `<user>`". For
GitHub-authenticated users, authorization derives from GitHub org and team
membership.

Scope for this project: **hzdev only**. hzprod is migrated later (no cluster yet).
No migration of existing users — worklode is not yet in production use, so
Keycloak-provisioned actors are not mapped to GitHub identities.

## Motivation

- A GitHub App acting on behalf of the user gives correct attribution when
  worklode interacts with GitHub (comment on PRs/issues, set commit status).
  Keycloak OIDC only proves identity; it cannot act on GitHub.
- GitHub org/team membership is already the source of truth for who works here,
  removing a layer of Keycloak role plumbing.

## Current state (what changes)

- **Web login:** `GET /auth/login` → `GET /auth/callback`, auth-code + PKCE
  against Keycloak (`internal/api/oidcweb.go`, `internal/oidc/oidc.go`).
- **CLI login:** `lode login` runs an OIDC loopback flow, stores a 30-day
  worklode token in `~/.config/worklode/config.toml` **in plaintext**
  (`internal/cli/login.go`).
- **Identity:** actor keyed on `preferred_username`; `provisionActor` upserts a
  human actor and syncs the admin flag (`internal/api/oidcauth.go`).
- **Roles:** Keycloak client roles delivered as a `groups` claim — `user`
  (required) and `admin`.
- **GitHub today:** inbound webhooks only (`POST /hooks/github`, HMAC). **Zero
  outbound GitHub API calls.**

The Keycloak path (`internal/oidc`, `oidcweb.go`, `oidcauth.go`, its routes and
CLI loopback flow) is **left as-is**. The GitHub provider is added next to it,
reusing the shared `provisionActor` upsert shape and the "re-evaluate roles on
every login" behavior. What is new: an `internal/githubauth` package, GitHub-
specific routes, encrypted token storage, and outbound GitHub calls.

## Design

### A. GitHub App(s) — one per environment

Create `worklode-dev` now (`worklode-prod` later). One App per env
because a GitHub App has a single webhook URL and single set of callback URLs;
this mirrors the existing Keycloak client-per-env split.

App configuration:

- **User authorization:** enabled, with **expiring user tokens** on (8h access /
  ~6-month refresh).
- **Device flow:** enabled (for the CLI).
- **Callback URL (hzdev):** `https://worklode.dev.sunstoneinstitute.ai/auth/github/callback`
  (distinct from Keycloak's existing `/auth/callback`, since both providers coexist).
- **Webhook URL (hzdev):** `https://worklode.dev.sunstoneinstitute.ai/hooks/github`
  (unchanged from today; HMAC via `LODE_GITHUB_WEBHOOK_SECRET`).
- **Permissions:**
  - Organization → **Members: read** (org + team membership for role mapping).
  - Repository → **Pull requests: R/W**, **Issues: R/W**, **Commit statuses: R/W**
    (the "act as user" writes; trim to what the first outbound feature needs).
- **Installation:** installed in the `sunstoneinstitute` org with access to the
  target repos. User-to-server tokens can only reach resources the App is
  installed on.

The "Identifying and authorizing users" callback URL — previously unused — is now
the GitHub web-login callback.

### B. Web login — new GitHub provider (alongside Keycloak)

New GitHub-specific routes are added; the existing `/auth/login` and
`/auth/callback` Keycloak routes are untouched. The login page offers both
options ("Sign in with Keycloak" / "Sign in with GitHub").

1. `GET /auth/github/login` redirects to GitHub's authorize endpoint with a
   signed-state CSRF token. No PKCE: this is a confidential client (the App
   client secret stays server-side and the code exchange is server-to-server),
   and GitHub's App user-authorization web flow does not honor PKCE — the signed
   state is the CSRF protection.
2. `GET /auth/github/callback` exchanges the code for a **user-to-server access
   token** (+ refresh token).
3. Fetch identity: `GET /user` → numeric `id`, `login`, name.
4. Evaluate authorization (Section D).
5. `provisionActor` upserts the human actor (Section E).
6. Encrypt and store the token pair (Section E).
7. Set the signed session cookie and 302 to the original destination.

A new `internal/githubauth` package exposes the GitHub surface (authorize URL
construction, code exchange, identity + membership fetch), paralleling
`internal/oidc` without touching it. A new `githubweb.go` holds the GitHub route
handlers and reuses the shared session helpers; `oidcweb.go` stays as-is.

### C. CLI login (device flow, server-mediated)

> **Superseded by the provider-neutral CLI login design** (`docs/plans/2026-07-20-provider-neutral-cli-login-design.md`). No device flow was built; `lode login` uses a server-mediated browser loopback with a one-time code, for both providers.

A new GitHub device-flow login is added; the existing Keycloak loopback
`lode login` path stays (e.g. selected via `lode login --github` or a prompt). The
device flow runs **through worklode** so the GitHub App client secret and the
user-to-server token never reach the client:

1. `lode login --github` → `POST /auth/github/device/start`. worklode calls
   GitHub's device endpoint and returns `user_code` + `verification_uri` + a
   worklode poll handle.
2. CLI prints the code and URL; user approves in any browser.
3. CLI polls `POST /auth/github/device/poll`. worklode polls GitHub; on
   approval it receives the user token, provisions the actor, stores the token
   pair, and issues a **worklode** token to the CLI.
4. CLI stores **only** the worklode token, in the OS keychain via
   `github.com/zalando/go-keyring`, with a `0600` file fallback
   (`~/.config/worklode/config.toml`) when no keychain is available. This replaces the
   current plaintext token storage.

The CLI never holds a GitHub token.

### D. Authorization

For **GitHub-authenticated** users (Keycloak users keep their Keycloak-role
evaluation, unchanged). Evaluated on every GitHub login (web and CLI), matching
today's role-refresh behavior:

- Member of `LODE_GITHUB_ORG` (`sunstoneinstitute`) → `user` role. **Required** —
  non-members are denied (same 403 shape as the current missing-`user`-role path).
- Member of the `LODE_GITHUB_ADMIN_TEAM` team (`worklode-admins`) → `admin`.

Membership is read with the user-to-server token:
`GET /user/memberships/orgs/{org}` and `GET /orgs/{org}/teams/{team}/memberships/{username}`
(App must have Members: read).

### E. Identity and token storage

- **Actor key:** the immutable GitHub **numeric user ID**, namespaced as
  `github:<id>` so GitHub actors cannot collide with Keycloak actors keyed on
  `preferred_username` (both provider namespaces coexist in the actors table).
  `login` is stored as display name (logins are renameable, so must not be the
  key). A GitHub user and a Keycloak user are distinct actors even if they map to
  the same person — acceptable because there is no user migration and the app is
  not yet in production.
- **Token storage:** the `(access_token, refresh_token, access_expires_at)`
  tuple is stored per actor, **encrypted at rest** with AES-GCM using a key from
  a new secret `LODE_TOKEN_ENC_KEY`. Stored in a dedicated `github_user_tokens`
  table keyed by actor id (tokens have their own lifecycle and null-until-first-
  login state, so they do not belong as columns on the actor row). Tokens are
  refreshed lazily before an outbound GitHub call when
  the access token is within a skew window of expiry; refresh failure forces
  re-login.

### F. Config and secrets (hzdev)

New env vars, **added** to the existing Keycloak/OIDC config (nothing removed):

| Var | Kind | Value / source |
|---|---|---|
| `LODE_GITHUB_APP_CLIENT_ID` | config | GitHub App client id |
| `LODE_GITHUB_APP_CLIENT_SECRET` | secret | 1Password → ExternalSecret |
| `LODE_GITHUB_ORG` | config | `sunstoneinstitute` |
| `LODE_GITHUB_ADMIN_TEAM` | config | `worklode-admins` |
| `LODE_TOKEN_ENC_KEY` | secret | random 32-byte key, 1Password → ExternalSecret |
| `LODE_PUBLIC_URL` | config | `https://worklode.dev.sunstoneinstitute.ai` (already required) |

Nothing removed: `LODE_OIDC_ISSUER`, `LODE_OIDC_CLIENT_ID`, and the OIDC secret stay.

Deployment impact: the app-deployment **Keycloak/SSO wiring stays**; the GitHub
App is added alongside the existing Keycloak client (not a replacement).
`LODE_GITHUB_WEBHOOK_SECRET` is unchanged.

## Non-goals

- hzprod migration (later project; create `worklode-prod` App then).
- Migrating existing Keycloak actors (fresh start).
- Building specific outbound GitHub features (PR comments, status checks). This
  design establishes the *capability* (stored user tokens + App permissions); the
  first concrete outbound action is a follow-up.

## Error handling

- Non-org-member login → 403, "must be a member of the sunstoneinstitute org".
- GitHub code exchange / API failure → 502 with a retriable message; no session set.
- Token refresh failure on an outbound call → mark the stored token invalid and
  require re-login; the outbound action reports "GitHub authorization expired".
- Device-flow expiry/denial → surfaced to the CLI as a clear terminal error.
- Actor id conflict with a non-human actor (e.g. bootstrap admin) → 409, reusing
  the existing `errActorKindConflict` path.

## Testing

- `internal/githubauth`: unit tests for authorize-URL construction, code
  exchange, and membership → role mapping (against a stubbed GitHub API), mirroring
  the existing `internal/oidc` tests.
- Web: `/auth/github/login` and `/auth/github/callback` handler tests with a fake
  GitHub server (state mismatch, non-member denial, admin team, actor conflict),
  mirroring existing `oidcweb`/`oidcauth` tests.
- CLI: device-flow start/poll tests with a fake worklode server; keychain
  storage behind an interface with a memory fake, plus the file fallback.
- Token store: AES-GCM round-trip and lazy-refresh-before-expiry tests.

## Open questions

None blocking. The exact repository write permissions (Section A) are trimmed to
the first outbound feature when it is specified.
