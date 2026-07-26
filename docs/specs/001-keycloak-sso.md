# Spec 001 — Keycloak SSO for worklode

**Status:** approved design
**Date:** 2026-07-19
**Umbrella:** `000-umbrella-architecture.md`
**Amended by:** 002 (GitHub App auth, additional provider); CLI login superseded by `docs/plans/2026-07-20-provider-neutral-cli-login-design.md`

## Why

> **Amended by 002.** Keycloak is no longer the only front door; GitHub App login is an additional provider alongside it, and this spec is otherwise unchanged.

Tokens are currently minted only by an admin (or the bootstrap env var).
Humans should instead authenticate against the org Keycloak
(https://auth.sunstoneinstitute.ai, realm `sunstone`) and get a worklode
token from their SSO identity. The read-only web UI must also be gated behind
the same OAuth2 login. Agent/service token issuance is unchanged.

## Decisions (settled)

| Decision | Choice |
|---|---|
| Credential model | Unchanged: opaque `wl_` tokens remain the only API credential. SSO is a front door for minting them. |
| CLI login | `lode login`: auth-code + PKCE against Keycloak with localhost redirect (kubelogin shape). Benefits transparently from macOS Platform SSO when that lands. |
| Token exchange | New unauthenticated endpoint `POST /auth/oidc/token` validates a Keycloak ID token and mints a `wl_` token. |
| Web UI gating | Native OIDC sessions in the server (auth-code + PKCE redirect, signed cookie). Not oauth2-proxy — no k8s deployment exists yet, and native works the same in compose and k8s. |
| Authorization | Keycloak client `worklode` with client roles `user` and `admin`, delivered in the `groups` claim (org-standard `client-roles-as-groups` mapper). `user` required to log in; `admin` maps to `Actor.Admin`, re-synced on every login. |
| Actor provisioning | Auto-provision `human` actors on first login: id = `preferred_username`, display name = `name` claim. |
| Feature flag | All of this is off unless `LODE_OIDC_ISSUER` + `LODE_OIDC_CLIENT_ID` are set; unset behaves exactly as today. |

> **Authorization amended by 002 §D.** Keycloak client roles govern only Keycloak-authenticated actors; GitHub-authenticated actors derive `user`/`admin` from org and team membership.

## Keycloak configuration (admin-cluster repo)

In `clusters/admin/keycloak-config/rbac.yaml` (GitOps only — never the admin
console, except for group memberships):

- Client `worklode`: `publicClient: true`, standard flow, PKCE S256,
  `fullScopeAllowed: false`. One client, no dev/prod split, until the service
  itself gets one. Redirect URIs:
  - `http://localhost:8000`, `http://localhost:18000` (CLI callback)
  - `http://localhost:8080/auth/callback` (local web UI)
  - `https://<host>/auth/callback` once the service has a public host
- Protocol mapper `client-roles-as-groups` (same config as the `k8s-*`
  clients) so client roles arrive in the `groups` claim of the ID token.
- Client roles: `user`, `admin`.
- Groups: `/apps/worklode` carries `worklode:user`;
  `/apps/worklode/admins` carries `worklode:admin`.
  Memberships are managed in the admin console (runtime data).

## Server

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

### Token exchange (CLI flow)

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

### Web UI sessions

> **Amended by 002 §B.** When both providers are configured the session flow starts at `/auth/choose`; `/auth/login` and `/auth/callback` remain the Keycloak path.

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

## CLI: `lode login`

> **Superseded by the provider-neutral CLI login design** (`docs/plans/2026-07-20-provider-neutral-cli-login-design.md`). The CLI no longer speaks OIDC; it opens the server's `/auth/cli/login` and redeems a one-time code at `/auth/cli/token`.

1. Start a localhost callback listener on port 8000 (fallback 18000).
2. Open the browser to the Keycloak authorize URL (auth-code + PKCE).
3. Redeem the code directly against Keycloak's token endpoint for an ID
   token.
4. `POST /auth/oidc/token` to the worklode server (`--server` /
   `LODE_SERVER` / config file, as today).
5. Write the returned `wl_` token to `~/.config/worklode/config.toml` and print
   the actor id and token expiry.

## Testing

- Fake OIDC issuer in unit tests (local JWKS endpoint + locally signed test
  tokens): valid login mints a token; missing `user` role → 403; admin flag
  syncs on and off; expired/wrong-audience token → 401; endpoint 404 when
  unconfigured.
- Web session tests: no cookie → redirect; full callback round-trip against
  the fake issuer; tampered/expired cookie → redirect.
- CLI callback flow tested against a stubbed Keycloak (httptest).

## Out of scope

- Refresh tokens (re-run `lode login`), logout, device flow (not enabled in
  this Keycloak), web-UI write actions, per-user authz beyond the existing
  `Admin` bool, and any change to agent/service token issuance.
