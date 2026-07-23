# Provider-Neutral `wl login` — Design

**Status:** Design (approved for planning)
**Date:** 2026-07-20
**Supersedes the CLI half of:** `2026-07-19-keycloak-sso-3-cli-login.md` (Keycloak-only, in-CLI PKCE).

## Problem

`wl login` today speaks Keycloak OIDC + PKCE **directly from the CLI**: it fetches
`GET /auth/oidc/config`, runs the auth-code flow against Keycloak itself, and
exchanges the ID token at `POST /auth/oidc/token`. On a server configured for
**GitHub** auth (the hzdev deployment) there is no Keycloak, so `wl login` 404s on
`/auth/oidc/config` and reports "this worklode server does not have SSO
enabled." GitHub auth is web-only — it sets a browser session cookie and never
mints a `wl_` token — so there is no CLI login path for GitHub-auth servers at
all. The only way to get a CLI token there is an admin minting one by hand.

## Goal

`wl login` becomes **provider-neutral**: it discovers how to authenticate from the
server and kicks off whichever web login the server is configured with (Keycloak,
GitHub, or a chooser when both are enabled), reusing the browser session the user
already has, and ends with a `wl_` token stored securely. The CLI speaks **no**
provider protocol and never sees a provider token.

## Architecture: server-mediated loopback

The provider-specific logic lives entirely in the **server**, reusing the existing
web login flows. The CLI only opens a URL and waits on a localhost listener.

```
wl login
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

Because the **server** performs the final redirect to the loopback URI (the
provider redirects to the server's own callback, not to localhost), the loopback
URI needs no pre-registration with Keycloak or GitHub. The CLI is therefore free to
bind an **ephemeral port** and is immune to port conflicts.

## Server changes

### New endpoints

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
  `wl_` token via `st.CreateToken(actor, "wl login", exp)`, marks the code used, and
  returns `{ token, actor_id, expires_at }` — the same JSON shape as the existing
  `/auth/oidc/token`. Unauthenticated, like `/auth/oidc/token`: possession of the
  one-time code proves the browser flow completed.

### The reuse seam: `finishLogin`

Both `authCallback` (Keycloak, `oidcweb.go`) and `githubCallback` (`githubweb.go`)
end identically today: provision actor → set the session cookie → redirect to
`next`. Extract that shared tail into:

```
func (s *server) finishLogin(w http.ResponseWriter, r *http.Request, actorID string)
```

`finishLogin` checks for the CLI-intent cookie:

- **present** → mint a one-time code bound to `actorID` + the CLI `state`, clear the
  intent cookie, and `302` to `redirect_uri?code=…&state=…` (no browser session is
  set — this browser tab exists only to complete the CLI login);
- **absent** → behave exactly as today (set session cookie, redirect to `next`).

Both providers gain CLI login from this one change; neither provider handler is
otherwise touched.

### One-time code store

In-memory `map[string]cliCode` (`{actorID, state, expiresAt, used}`) guarded by a
mutex. 60-second TTL, single-use, 32 bytes of entropy. In-memory is sufficient
because the server is single-instance (one PVC + litestream); a restart drops
pending 60-second codes and the user simply re-runs `wl login`. No DB migration.
(If we ever go multi-replica, promote to a table — noted, not built.)

## CLI changes

- **`RunLogin` rewritten** to the six-step flow above. It reuses `callbackHandler`,
  `randState`, and `openBrowser` unchanged. `listenLocal` is simplified to bind an
  ephemeral port (`localhost:0`) and report the chosen port — no fixed port list,
  no port-in-use failure mode.
- **Dead code removed:** `fetchOIDCConfig`, `exchangeWTToken`, and the in-CLI
  `internal/oidc` PKCE usage in `login.go` are deleted — the server-mediated flow
  replaces them fully. The server `/auth/oidc/*` endpoints stay (see below).
- **`cmd/login.go`** is unchanged except its Keycloak-specific help text becomes
  provider-neutral.

## Token storage: OS keychain

Today `SaveConfig` writes `server` **and** `token` into
`~/.config/worklode/config.toml` at 0600 — cleartext on disk. Split secret from
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
- **`wl login`** writes the token to the keychain, writes only `server` to
  config.toml, and if it finds a legacy cleartext `token` there it strips it and
  prints a one-line note — migrating people off cleartext.
- **`wl logout`** (new command) deletes the token from the keychain for the current
  server.
- **Headless / no keychain:** `wl login` opens a browser, so it runs on a desktop
  with a keychain. If the keychain write genuinely fails it errors with guidance to
  use `WL_TOKEN` instead — it never silently falls back to cleartext. Automation
  (`wl watch` in-cluster) already uses `WL_TOKEN` and is unaffected.

## Security

- `redirect_uri` is loopback-only, blocking code exfiltration / open redirect.
- `state` is round-tripped and checked by the CLI (CSRF).
- One-time codes are single-use, 60-second, high-entropy, bound to actor + `state`.
- The CLI-intent cookie is signed with `SessionSecret` and short-lived.
- The token-exchange endpoint is unauthenticated by design; only a valid one-time
  code (proof the browser flow completed) yields a token.

## Kept / removed

- **Keep (server):** `/auth/oidc/config`, `/auth/oidc/token` — dormant direct-Keycloak
  contract preserved at zero cost for a future client (e.g. self-hosted Forgejo).
  Server-mediated login already covers Keycloak-as-a-provider via the web flow; these
  endpoints are an additional direct integration point, not what keeps Keycloak alive.
- **Add (server):** `/.well-known/wl-login`, `/auth/cli/login`, `/auth/cli/token`.
- **Add (CLI):** `wl logout`; keychain-backed `tokenStore`.
- **Remove (CLI):** dead OIDC/PKCE helper code in `login.go`.

## Testing

- **Server unit** (fake oidc/gh, existing harness): discovery reflects providers /
  404s when none; `/auth/cli/login` rejects a non-loopback `redirect_uri` and
  threads intent through the flow; `finishLogin` CLI branch mints a code and
  redirects to the loopback; `/auth/cli/token` valid / expired / reused /
  wrong-state paths; one-time-code store TTL and single-use.
- **CLI unit** (mirrors `login_test.go`): injected browser driver + `httptest`
  server implementing `/.well-known/wl-login` and `/auth/cli/token`, driving the
  loopback callback end to end; ephemeral-port listener; `tokenStore` via
  `keyring.MockInit()`; `LoadConfig` resolution order incl. legacy fallback;
  `wl logout` deletes the keychain entry.
- **e2e** (`e2e/`): happy-path CLI login against a server with GitHub auth stubbed.
