---
status: draft
---
# Spec 001 — Identity & authentication

## 0. Purpose & scope {#sec-0}

Tokens are currently minted only by an admin (or the bootstrap env var).
Humans should instead authenticate against the org Keycloak
(https://auth.sunstoneinstitute.ai, realm `sunstone`) and get a worklode
token from their SSO identity. The read-only web UI must also be gated behind
the same OAuth2 login. Agent/service token issuance is unchanged.

Keycloak is the **sole** login mechanism for web and CLI. GitHub is a
**link-only** OAuth flow: an already-authenticated actor connects their GitHub
account once, and worklode stores the resulting user-to-server token so it can
later call GitHub attributed as "SunstoneWork on behalf of `<user>`". The link
is verified **strictly** against a Keycloak-asserted `github_username`
attribute, happens **lazily** at the point of first need, and is managed from
the CLI via the `lode auth` command group. This spec delivers plumbing only —
link flow, storage, refresh — verified end to end; the first feature that
writes to GitHub as a user is a follow-up spec.

## 1. Motivation {#sec-1}

- A GitHub App acting on behalf of the user gives correct attribution when
  worklode interacts with GitHub (comment on PRs/issues, set commit status).
  Keycloak OIDC only proves identity; it cannot act on GitHub.
- **One actor per person.** The earlier dual-provider model created two actor
  rows for the same human (`preferred_username` and `github:<id>`) and punted
  on reconciling them. Making Keycloak the only login stops the split from
  growing; the rows it already created are merged into the Keycloak actor
  (§3).
- **Attribution without escalation.** A stored user token is bounded by the
  intersection of App permissions, installation scope, and the user's own
  access. An org owner's token therefore ≈ the full App permission set. The
  App's ceiling is capped read-mostly (§9.1) so a compromise of the server —
  which holds both the DB and `LODE_TOKEN_ENC_KEY` — cannot push code or
  reach CI-trusted branches with anyone's token. Nothing shipped or specced
  needs `contents: write` (spec 004 requires Contents: **read**; spec 008's
  issue mirror needs Issues: write).
- **Roles from one source.** GitHub org/team role derivation duplicated what
  Keycloak groups already provide.

## 2. Decisions {#sec-2}

| Decision | Choice |
|---|---|
| Credential model | Unchanged: opaque `wl_` tokens remain the only API credential. SSO is a front door for minting them. |
| CLI login | `lode login` is provider-neutral and server-mediated: the CLI discovers the server's login endpoints, waits on an ephemeral loopback port, and the server runs its own web login (§8). The browser login benefits transparently from macOS Platform SSO when that lands. |
| Token exchange | Unauthenticated endpoint `POST /auth/oidc/token` validates a Keycloak ID token and mints a `wl_` token; kept as a dormant direct contract (§7). |
| Web UI gating | Native OIDC sessions in the server (auth-code + PKCE redirect, signed cookie). Not oauth2-proxy — no k8s deployment exists yet, and native works the same in compose and k8s. |
| Authorization | Keycloak client `worklode` with client roles `user` and `admin`, delivered in the `groups` claim (org-standard `client-roles-as-groups` mapper). `user` required to log in; `admin` maps to `Actor.Admin`, re-synced on every login. |
| Actor provisioning | Auto-provision `human` actors on first login: id = `preferred_username`, display name = `name` claim. |
| Feature flag | All of this is off unless `LODE_OIDC_ISSUER` + `LODE_OIDC_CLIENT_ID` are set; unset behaves exactly as today. |

## 3. Keycloak is the only login {#sec-3}

- `/auth/choose`, GitHub web login (`/auth/github/login`), GitHub actor
  provisioning and org/team role derivation are removed. `/auth/github/*`
  routes survive only as the link flow (§9.3), reachable exclusively from an
  authenticated session.
- Roles come from Keycloak `groups` only (`user` required, `admin` optional),
  unchanged from §2.
- Config removed: `LODE_GITHUB_ORG`, `LODE_GITHUB_ADMIN_TEAM`. The App no
  longer needs the Organization → Members: read permission.
- Existing `github:<id>` actor rows are **merged** into the person's Keycloak
  actor: every row referencing them (`tasks.created_by`, `tasks.assignee`,
  `tokens.actor_id`, `github_user_tokens.actor_id`, `leases.actor_id`) is
  repointed to the Keycloak actor id, then the GitHub row is deleted.
  Worklode is in production with tasks and tokens referencing these rows, so
  the merge runs as a reviewed one-off SQL script against the production
  database with explicit human approval rather than as a schema migration.
  The append-only event log keeps historical `github:<id>` ids as provenance
  and is never rewritten.

## 4. Keycloak realm configuration {#sec-4}

In `clusters/admin/keycloak-config/rbac.yaml` (GitOps only — never the admin
console, except for group memberships):

- Client `worklode`: `publicClient: true`, standard flow, PKCE S256,
  `fullScopeAllowed: false`. One client, no dev/prod split, until the service
  itself gets one. Redirect URIs:
  - `http://localhost:8080/auth/callback` (local web UI)
  - `https://<host>/auth/callback` once the service has a public host
- Protocol mapper `client-roles-as-groups` (same config as the `k8s-*`
  clients) so client roles arrive in the `groups` claim of the ID token.
- Client roles: `user`, `admin`.
- Groups: `/apps/worklode` carries `worklode:user`;
  `/apps/worklode/admins` carries `worklode:admin`.
  Memberships are managed in the admin console (runtime data).

## 5. Server OIDC configuration and verification {#sec-5}

New env (all optional; feature disabled when issuer/client unset):

| Var | Meaning |
|---|---|
| `LODE_OIDC_ISSUER` | e.g. `https://auth.sunstoneinstitute.ai/realms/sunstone` |
| `LODE_OIDC_CLIENT_ID` | e.g. `worklode` |
| `LODE_PUBLIC_URL` | External base URL, for the web callback redirect URI |
| `LODE_SESSION_SECRET` | HMAC key for session cookies (required if OIDC enabled) |

ID-token verification uses `github.com/coreos/go-oidc/v3` (JWKS fetch +
cache; checks signature, `iss`, `aud`, `exp`). Shared by both flows below.
Claims used: `preferred_username`, `name`, `groups`.

## 6. Web UI sessions {#sec-6}

When OIDC is enabled, the web UI routes (`/`, `/tasks/{id}`,
`/projects/{id}`) require a valid session cookie; otherwise they 302 to
`/auth/login`. `/healthz` and `/metrics` stay open. When OIDC is
unconfigured the UI stays open as today.

- `GET /auth/login` → 302 to Keycloak authorize URL (auth-code + PKCE,
  `state` + PKCE verifier in a short-lived signed cookie).
- `GET /auth/callback` → redeem code at Keycloak token endpoint, verify ID
  token, require `user` role, upsert actor (same logic as token exchange),
  set session cookie, 302 to the originally requested page.
- Session cookie: HMAC-SHA256-signed `{username, expiry}` under
  `LODE_SESSION_SECRET`; ~12 h lifetime; `HttpOnly`, `Secure`,
  `SameSite=Lax`. No server-side session state. No logout endpoint —
  cookies expire.

## 7. Direct OIDC token exchange (dormant) {#sec-7}

`GET /auth/oidc/config` and `POST /auth/oidc/token` are kept as a dormant
direct-Keycloak contract, preserved at zero cost for a future client (e.g.
self-hosted Forgejo). Server-mediated login (§8) already covers
Keycloak-as-a-provider via the web flow; these endpoints are an additional
direct integration point, not what keeps Keycloak alive.

`POST /auth/oidc/token`, body `{"id_token": "..."}` — registered outside the
`/api/v1` auth middleware, like `/healthz` and `/hooks/*`. Returns 404 when
OIDC is unconfigured.

1. Verify the ID token. Invalid/expired/wrong audience → 401.
2. `groups` must contain `user` → else 403.
3. Upsert `human` actor; set `Admin` = `groups` contains `admin`
   (re-synced every login, so demotion takes effect at next login).
4. Mint a `wl_` token via `store.CreateToken`: 30-day expiry, description
   `sso login for <user> at <RFC3339 timestamp>`. Return `{"token": ...}`
   once. Re-login after expiry; no refresh tokens.

## 8. CLI login: server-mediated loopback {#sec-8}

`lode login` is **provider-neutral**: it discovers how to authenticate from the server
and kicks off whichever web login the server is configured with (Keycloak, GitHub, or a
chooser when both are enabled), reusing the browser session the user already has, and ends
with a `wl_` token stored securely. The CLI speaks **no** provider protocol and never sees a
provider token.

The provider-specific logic lives entirely in the **server**, reusing the existing web login
flows. The CLI only opens a URL and waits on a localhost listener.

```
lode login
  1. GET  {server}/.well-known/wl-login       -> { authorize_url, token_url, providers }
  2. bind loopback listener on an ephemeral port (localhost:0)
  3. open browser: {authorize_url}?redirect_uri=http://localhost:PORT/&state=CLISTATE
        server runs its normal web login (Keycloak / GitHub / chooser),
        reusing the session the browser already has -> provisions the actor
        -> mints a one-time code -> 302 to the loopback redirect_uri
  4. loopback receives ?code=OTC&state=CLISTATE   (state checked by the CLI)
  5. POST {token_url} {code, state}            -> { token, actor_id, expires_at }
  6. store token in the OS keychain; write only `server` to config.toml
```

Because the **server** performs the final redirect to the loopback URI (the provider redirects
to the server's own callback, not to localhost), the loopback URI needs no pre-registration
with Keycloak or GitHub. The CLI is therefore free to bind an **ephemeral port** and is immune
to port conflicts.

### 8.1 Discovery and CLI login endpoints {#sec-8.1}

- **`GET /.well-known/wl-login`** — discovery. Returns
  `{ "authorize_url": "{public}/auth/cli/login", "token_url": "{public}/auth/cli/token", "providers": ["github"] }`.
  `providers` is informational (lets the CLI print "Signing in with GitHub…").
  Returns **404** when the server has no interactive provider configured
  (`s.oidc == nil && s.gh == nil`); the CLI then prints a clear message telling the
  user to ask an admin for a token. Registered outside the bearer-auth middleware,
  like `/healthz` and `/auth/oidc/*`.

- **`GET /auth/cli/login?redirect_uri=…&state=…`** — validates `redirect_uri` is
  **loopback-only** (host in `localhost` / `127.0.0.1` / `::1`, scheme `http`,
  explicit non-zero port), stores the CLI intent (`redirect_uri` + `state`) in a
  short-lived signed cookie (signed with `SessionSecret`), then redirects into the
  existing `loginTarget(next)` entrypoint — the chooser when both providers are on,
  or the single provider otherwise. No new provider code.

- **`POST /auth/cli/token` `{code, state}`** — validates the one-time code (exists,
  unexpired, unused, `state` matches the value bound at mint time), mints a 30-day
  `wl_` token via `st.CreateToken(actor, "lode login", exp)`, marks the code used, and
  returns `{ token, actor_id, expires_at }` — the same JSON shape as the existing
  `/auth/oidc/token`. Unauthenticated, like `/auth/oidc/token`: possession of the
  one-time code proves the browser flow completed.

### 8.2 The reuse seam: `finishLogin` {#sec-8.2}

Both `authCallback` (Keycloak, `oidcweb.go`) and `githubCallback` (`githubweb.go`) ended
identically: provision actor → set the session cookie → redirect to `next`. That shared tail
is extracted into:

```
func (s *server) finishLogin(w http.ResponseWriter, r *http.Request, actorID string)
```

`finishLogin` checks for the CLI-intent cookie:

- **present** → mint a one-time code bound to `actorID` + the CLI `state`, clear the
  intent cookie, and `302` to `redirect_uri?code=…&state=…` (no browser session is
  set — this browser tab exists only to complete the CLI login);
- **absent** → behave exactly as before (set session cookie, redirect to `next`).

Both providers gain CLI login from this one change; neither provider handler is otherwise
touched.

### 8.3 One-time code store {#sec-8.3}

In-memory `map[string]cliCode` (`{actorID, state, expiresAt, used}`) guarded by a mutex.
60-second TTL, single-use, 32 bytes of entropy. In-memory is sufficient because the server is
single-instance (one PVC + litestream); a restart drops pending 60-second codes and the user
simply re-runs `lode login`. No DB migration. (If we ever go multi-replica, promote to a table
— noted, not built.)

### 8.4 Client behavior {#sec-8.4}

- **`RunLogin`** implements the six-step flow of §8. It reuses `callbackHandler`,
  `randState`, and `openBrowser` unchanged. `listenLocal` binds an ephemeral port
  (`localhost:0`) and reports the chosen port — no fixed port list, no port-in-use
  failure mode.
- **Dead code removed:** `fetchOIDCConfig`, `exchangeWTToken`, and the in-CLI
  `internal/oidc` PKCE usage in `login.go` are deleted — the server-mediated flow
  replaces them fully. The server `/auth/oidc/*` endpoints stay (§7).
- **`cmd/login.go`** is unchanged except its Keycloak-specific help text becomes
  provider-neutral.

### 8.5 Token storage: OS keychain {#sec-8.5}

`SaveConfig` previously wrote `server` **and** `token` into
`~/.config/worklode/config.toml` at 0600 — cleartext on disk. Secret is split from
non-secret:

- **`config.toml` keeps only `server`.** The token never touches disk.
- **Token lives in the OS keychain**, keyed by server URL (service `worklode`,
  account = server URL) so one machine can hold tokens for several servers and
  `WL_SERVER` selects which. Backed by **`github.com/zalando/go-keyring`** (macOS
  Keychain · Linux Secret Service · Windows Credential Manager; no cgo).
- **`tokenStore` abstraction** in the `cli` package — `Get/Set/Delete(server)` —
  with a keychain implementation and a mock (`keyring.MockInit()`) for tests, since
  CI has no Secret Service.
- **Token resolution order** in `LoadConfig`: `WL_TOKEN` env → keychain(server) →
  legacy cleartext `token` in config.toml (read-only fallback so existing installs
  keep working).
- **`lode login`** writes the token to the keychain, writes only `server` to
  config.toml, and if it finds a legacy cleartext `token` there it strips it and
  prints a one-line note — migrating people off cleartext.
- **`lode logout`** deletes the token from the keychain for the current server.
- **Headless / no keychain:** `lode login` opens a browser, so it runs on a desktop
  with a keychain. If the keychain write genuinely fails it errors with guidance to
  use `WL_TOKEN` instead — it never silently falls back to cleartext. Automation
  (`lode watch` in-cluster) already uses `WL_TOKEN` and is unaffected.

### 8.6 Security {#sec-8.6}

- `redirect_uri` is loopback-only, blocking code exfiltration / open redirect.
- `state` is round-tripped and checked by the CLI (CSRF).
- One-time codes are single-use, 60-second, high-entropy, bound to actor + `state`.
- The CLI-intent cookie is signed with `SessionSecret` and short-lived.
- The token-exchange endpoint is unauthenticated by design; only a valid one-time
  code (proof the browser flow completed) yields a token.

## 9. GitHub account linking {#sec-9}

### 9.1 The GitHub App and its configuration {#sec-9.1}

Create `worklode-dev` now (`worklode-prod` later). One App per env
because a GitHub App has a single webhook URL and single set of callback URLs;
this mirrors the existing Keycloak client-per-env split.

App configuration:

- **User authorization:** enabled, with **expiring user tokens** on (8h access /
  ~6-month refresh).
- **Callback URL (hzdev):** `https://worklode.dev.sunstoneinstitute.ai/auth/github/callback`
  (distinct from Keycloak's own `/auth/callback`).
- **Webhook URL (hzdev):** `https://worklode.dev.sunstoneinstitute.ai/hooks/github`
  (unchanged from today; HMAC via `LODE_GITHUB_WEBHOOK_SECRET`).
- **Permission ceiling:** Repository → Contents: **read**, Actions: **read**,
  Deployments: **read**, Pull requests: **read**; Issues: **write** when the
  spec 008 issue mirror lands. **Never `contents: write`** — it is what turns
  a stolen token (user or installation) into a push to CI-trusted branches.
  A future feature needing repo writes gets a separate, narrowly-installed
  App rather than a widened ceiling here.
- **Installation scope:** installed in the `sunstoneinstitute` org, selected
  repositories only; the provisioning and admin-cluster repos (whose CI holds
  cluster credentials) are excluded. User-to-server tokens can only reach
  resources the App is installed on.
- **Independent guard:** no-bypass rulesets (require PR + review, no admin
  bypass) on the provisioning/admin repos — protects against *any* stolen
  org-owner credential, not just this table.

The "Identifying and authorizing users" callback URL — previously unused — is
the GitHub link callback (§9.3).

Token encryption is unchanged: AES-GCM, key in a separate trust zone
(1Password → ExternalSecret → pod env). A Postgres dump alone yields
nothing usable.

Env vars, **added** to the existing Keycloak/OIDC config:

| Var | Kind | Value / source |
|---|---|---|
| `LODE_GITHUB_APP_CLIENT_ID` | config | GitHub App client id |
| `LODE_GITHUB_APP_CLIENT_SECRET` | secret | 1Password → ExternalSecret |
| `LODE_TOKEN_ENC_KEY` | secret | random 32-byte key, 1Password → ExternalSecret |
| `LODE_PUBLIC_URL` | config | `https://worklode.dev.sunstoneinstitute.ai` (already required) |

Nothing is removed on the Keycloak side: `LODE_OIDC_ISSUER`,
`LODE_OIDC_CLIENT_ID`, and the OIDC secret stay. Counting the App credentials
the webhook path already required, the full GitHub-side inventory after this
spec is therefore `LODE_GITHUB_APP_CLIENT_ID`, `LODE_GITHUB_APP_CLIENT_SECRET`,
`LODE_TOKEN_ENC_KEY`, `LODE_GITHUB_APP_ID`, `LODE_GITHUB_APP_PRIVATE_KEY`,
and `LODE_GITHUB_WEBHOOK_SECRET`; `LODE_GITHUB_ORG` (`sunstoneinstitute`)
and `LODE_GITHUB_ADMIN_TEAM` (`worklode-admins`) are removed.

Deployment impact: the app-deployment **Keycloak/SSO wiring stays**; the GitHub
App is added alongside the existing Keycloak client (not a replacement).
`LODE_GITHUB_WEBHOOK_SECRET` is unchanged.

### 9.2 Expected GitHub identity from Keycloak {#sec-9.2}

- The realm's worklode client gains a protocol mapper exposing the user
  attribute `github_username` as a claim in ID tokens (deployment
  prerequisite, wired via the app-deployment Keycloak config).
- `internal/oidc.Claims` gains a `GitHubUsername` string field tagged
  `json:"github_username"`. `provisionActor` stores it on the actor as
  `expected_github_login`, re-synced on every login exactly like the admin
  flag. Login never fails for a missing attribute — it is only required to
  *link*.
- Storing the expectation at login is what makes strict matching possible
  later: the link flow runs long after login, when no Keycloak token is in
  hand (sessions are stateless cookies).

### 9.3 Link flow (web) {#sec-9.3}

1. `GET /auth/github/link` (authenticated) redirects to GitHub's authorize
   endpoint with signed state, as a confidential client with no PKCE.
2. `GET /auth/github/callback` exchanges the code, fetches `GET /user`
   (numeric `id`, `login`), and **strict-checks**: `login` must equal the
   session actor's `expected_github_login`, case-insensitively. Missing
   attribute or mismatch → the link is refused with an error naming the fix
   ("your Keycloak account has no/another GitHub username; get the
   `github_username` attribute corrected"). No row is written.
3. On success, upsert the link row keyed by the **worklode actor id** (§9.5).
   The numeric GitHub user id is the durable external identity; `login` is
   display metadata (renameable).
4. Linking is **lazy**: nothing prompts at login. Features that need GitHub
   surface a "Connect GitHub" redirect at the point of need; a settings/
   profile entry allows proactive linking. Re-linking (e.g. after `broken`,
   §9.5) repeats the same flow; GitHub skips the consent screen for an
   already-authorized App.

### 9.4 `lode auth` command group {#sec-9.4}

- `lode login` moves to `lode auth login`; the old spelling remains as a
  hidden alias. The loopback flow itself is unchanged.
- `lode auth link github`: the CLI calls a bearer-authed endpoint that mints
  a one-time, short-lived signed nonce bound to the calling actor and
  returns a link URL; the CLI opens the browser and polls link status until
  linked, refused, or the nonce expires. The nonce ties the browser flow to
  the CLI's actor without requiring a web session cookie.
- `lode auth status`: shows the logged-in identity, token expiry, and GitHub
  link state (unlinked / linked as `<login>` / broken — reconnect).

### 9.5 GitHub token storage and refresh {#sec-9.5}

`github_user_tokens` holds the stored user token and is reworked by a new
migration so that the token row **is** the link — a link exists iff a row
exists, unlink = delete the row. The tokens sit
in a dedicated table rather than as columns on the actor row because they have
their own lifecycle and a null-until-first-link state:

| column | notes |
|---|---|
| `actor_id` | PK, references `actors` |
| `github_user_id` | unique; durable external identity |
| `github_login` | display only, refreshed on re-link |
| `token_ciphertext` | AES-GCM (`LODE_TOKEN_ENC_KEY`) sealing `{access_token, refresh_token, access_expires_at}` |
| `status` | `active` \| `broken` |
| timestamps | created/updated |

The `(access_token, refresh_token, access_expires_at)` tuple is **encrypted at
rest** with AES-GCM under a key from the secret `LODE_TOKEN_ENC_KEY`; the
sealing itself lives in `internal/tokencrypt`.

Accessor: `store.UserToken(ctx, actorID)` returns a valid access token,
refreshing lazily when the access token is expired or within a skew window of
expiry. GitHub refresh tokens are **single-use**, so the row is locked
(`SELECT … FOR UPDATE`) for the duration of a refresh; concurrent callers wait
and reuse the fresh pair. A failed refresh (revoked App authorization,
>6-month lapse) sets `status = broken`; callers translate that into "reconnect
GitHub" guidance. No background refresher — on-demand refresh suffices given
the ~6-month refresh-token lifetime.

## 10. Error handling {#sec-10}

- Link with missing `expected_github_login` → refused: "no GitHub username on
  your Keycloak account".
- Link with mismatched login → refused, naming both logins.
- OAuth code exchange / `GET /user` failure → 502, retriable, no row written.
- Refresh failure → row marked `broken`; `lode auth status` and the web UI
  surface "reconnect GitHub"; the triggering caller gets a typed error.
- CLI link nonce expired/consumed → clear terminal error, re-run to retry.
- Actor id conflict with a non-human actor (e.g. bootstrap admin) → 409,
  reusing the existing `errActorKindConflict` path.

## 11. Testing {#sec-11}

- Fake OIDC issuer in unit tests (local JWKS endpoint + locally signed test
  tokens): valid login mints a token; missing `user` role → 403; admin flag
  syncs on and off; expired/wrong-audience token → 401; endpoint 404 when
  unconfigured.
- Web session tests: no cookie → redirect; full callback round-trip against
  the fake issuer; tampered/expired cookie → redirect.
- `internal/githubauth`: unit tests for authorize-URL construction and code
  exchange against a stubbed GitHub API, mirroring the existing
  `internal/oidc` tests.
- **Store:** link upsert and uniqueness (`github_user_id`), delete-as-unlink,
  refresh rotation persisting the new pair, the concurrent-refresh race
  (second caller blocks and reuses), broken-marking on refresh failure,
  AES-GCM round-trip (existing).
- **Handlers:** link + callback against a fake GitHub server — strict-match
  refusal (missing and mismatched), success upsert, state mismatch, actor
  conflict; removal of the login paths (routes gone, `/auth/choose` gone).
  Mirrors the existing `oidcweb`/`oidcauth` tests.
- **OIDC:** `github_username` claim parsed and synced onto the actor across
  logins.
- **CLI:** `lode auth login` alias behavior, `link github` start/poll against
  a fake server, `auth status` rendering of all three link states.
- **E2E:** OIDC login → `lode auth link github` → fake GitHub authorize →
  callback → `lode auth status` shows linked; admin diagnostics report link
  and token validity. Public surfaces only, per `e2e/` policy.
- **Server unit** (fake oidc/gh, existing harness): discovery reflects providers /
  404s when none; `/auth/cli/login` rejects a non-loopback `redirect_uri` and
  threads intent through the flow; `finishLogin` CLI branch mints a code and
  redirects to the loopback; `/auth/cli/token` valid / expired / reused /
  wrong-state paths; one-time-code store TTL and single-use.
- **CLI unit** (mirrors `login_test.go`): injected browser driver + `httptest`
  server implementing `/.well-known/wl-login` and `/auth/cli/token`, driving the
  loopback callback end to end; ephemeral-port listener; `tokenStore` via
  `keyring.MockInit()`; `LoadConfig` resolution order incl. legacy fallback;
  `lode logout` deletes the keychain entry.
- **e2e** (`e2e/`): happy-path CLI login against a server with GitHub auth stubbed.

## 12. Non-goals {#sec-12}

- Refresh tokens (re-run `lode login`), web-session logout, device flow (not
  enabled in this Keycloak), web-UI write actions, per-user authz beyond the
  existing `Admin` bool, and any change to agent/service token issuance.
- Any feature that writes to GitHub as a user (issue mirror, comment relay)
  — own spec, first consumer of `store.UserToken`. This design establishes
  the *capability* (stored user tokens + App permissions); the first concrete
  outbound action is a follow-up.
- Background/scheduled token refresh.
- Unlink UI beyond row deletion via admin/CLI.
- hzprod rollout — a later project; create the `worklode-prod` App then
  (§9.1).

## 13. Open questions {#sec-13}

None blocking. The exact repository write permissions (§9.1) are trimmed to
the first outbound feature when it is specified. The claim name is fixed to
`github_username`; make it configurable only if a second realm ever needs a
different mapping.
