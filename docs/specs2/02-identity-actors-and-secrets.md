# Identity, actors and secrets

Every request to Worklode is made by an actor holding an opaque `wl_` token. Humans get their token by logging in through the org Keycloak realm, from the web UI or from `lode login`. Agents get task-scoped tokens minted against a small roster of agent actors. Every route is named in one guard table and every permission is decided by one default-deny policy over a `grants` table. Tasks declare the secrets they need by symbolic name against an org catalog; Worklode never stores, transports or sees a secret value. 1Password holds the values, the operator's own `op` session decrypts them at claim time, and `lode secret exec` injects them into a child process.

## 1. Actors

An actor is an accountable principal with credentials. An actor row does four things: it holds tokens, it can be granted or denied a permission by the `grants` table, it holds leases and assignments, and costs roll up to it. Nothing about an actor describes capability.

| Column | Meaning |
|---|---|
| `id` | Stable text id. Humans: Keycloak `preferred_username`. Agents: the dispatch mechanism (`sandbox`, `watcher`). |
| `kind` | `human`, `agent` or `service`. No other kind exists. |
| `display_name` | Humans: the Keycloak `name` claim. |
| `admin` | Boolean. Humans: re-synced from the Keycloak `admin` role on every login. Agents: never admin. |
| `groups` | Humans: the full `groups` claim from the last login, stored as JSON. Gates check group membership by name at request time. Stored groups go stale between logins. |
| `email` | Humans: the `email` claim from the last login. Used to invite crew members to a project's chat space (03-tasks-and-execution.md §13). |
| `expected_github_login` | Humans: the Keycloak `github_username` attribute, re-synced on every login. Used only to link a GitHub account (section 9). |

Actor ids are foreign keys from `tasks.created_by`, `tasks.assignee`, `tokens.actor_id`, `tokens.minted_by`, `leases.actor_id` and `project_participants.actor_id`. An id is therefore permanent.

### 1.1 One actor per person

Keycloak is the only login. Each person has exactly one `human` actor, auto-provisioned on first login. Two actor rows for one person are merged into the Keycloak actor by a reviewed one-off SQL script: every referencing row is repointed (columns come from `pg_constraint` rows with `confrelid = 'actors'`, never a hand-kept list), rows that would collide on a unique constraint are dropped, then the duplicate is deleted. The event log keeps the historical ids as provenance and is never rewritten. A login whose `preferred_username` collides with a non-human actor (the bootstrap admin) is a 409.

### 1.2 Agent actors

Agent actors are provisioned per authority. The one test for whether a split deserves its own actor row: would we ever revoke this one's credential without revoking the other's, or grant it a permission the other lacks? If no, it is one actor. Wanting to see two things apart in a report is answered by a query over data already recorded, never by a new identity.

| Rejected dimension | Where that fact already lives |
|---|---|
| Specialty (`security-agent`, `reviewer`) | The task's `kind`, the skills the task declares, the project's role-labelled participant rows. Agents are never Crew members (03-tasks-and-execution.md). |
| Harness (`claude`, `codex`) | `agent_sessions.agent` and `agent_version` on every session (08-agent-harness-and-sessions.md). No permission differs between harnesses. |
| Model and effort (`sonnet-worker`) | Per-usage-bucket rows priced from `model_prices`. Model is chosen during execution, after the credential exists. |
| Launch profile (prompt, model, skill set) | Configuration owned by the plugin and repo that dispatch the agent. A task that needs a specific setup says so in its body or its declared skills. |

The roster:

| Actor | Kind | Use |
|---|---|---|
| `sandbox` | `agent`, never admin | Every piece of work a human launched that runs under a task-scoped token: interactive sessions, sandboxes dispatched from a laptop or from CI on someone's behalf. Auto-provisioned on first task-token mint. The launching human is recorded on the token (`minted_by`, section 2). |
| `watcher` | `agent` | `lode-watch`'s pod informer. Reports runtime events and can do nothing else. |
| `janitor` | `agent` | Unattended sweeps. Can claim `kind = 'chore'` and cannot accept a document. |

An unattended automation gets its own actor only when it needs a `grants` row `sandbox` does not have. It lands together with that permission difference and a written reason. An actor with the same grants as `sandbox` is `sandbox` under another name. Ids name the dispatch mechanism and its policy: stable, lowercase, no vendor name, so replacing the harness behind a mechanism is a configuration change.

A session working under a human's own `lode login` token attributes every write to that human. An interactive CLI session mints no task token.

## 2. Credentials: `wl_` tokens

The only API credential is an opaque token, `wl_` plus 40 hex characters. The server stores its hash. Anything that is not that shape is treated as a hash, never as a plaintext. Single sign-on is one way to mint an actor-scoped token.

| Token shape | `tokens.task_id` | Acts as | Minted by | Expiry |
|---|---|---|---|---|
| Actor-scoped | NULL | The actor it was minted for | `lode login`, `POST /auth/oidc/token`, an admin, or `LODE_BOOTSTRAP_TOKEN` at boot | 30 days for login tokens |
| Task-scoped | The bound task | An agent actor, `sandbox` by default | `POST /api/v1/tasks/{id}/tokens`, called by an operator or dispatcher | The task lease's TTL by default. Renewing the lease extends it. |

`tokens.minted_by` (`REFERENCES actors ON DELETE SET NULL`) records the actor that minted a token when it differs from the actor the token acts as. It is NULL for a token an actor minted for itself. Authorization never reads it. It exists so "which human dispatched the agent that made this change" is a join.

A task-scoped token:

- can only write actions that name its bound task;
- reads only within the bound task's project;
- can mint tokens;
- has no administrative access;
- is revoked, together with every other outstanding token of the task, in the same transaction as any path that ends the lease: release, done, abandon, or the expiry sweep.

Enforcement of these constraints lives in the `grants` table, never in per-handler checks.

## 3. Authorization

Every request resolves to a `Subject` (actor, admin flag, bound task if any). `internal/api/authz.go` holds a `grants` table mapping permission to the roles that hold it and a default-deny `Decide`. The only roles are `user` and `admin`, the two Keycloak client roles. Adding a role means editing that table, never adding a check inside a handler. A narrower per-actor RBAC model is introduced only when an unattended agent actor needs a grant `sandbox` lacks; that narrowing is the change that introduces it.

Every route is listed in `internal/api/router.go`'s `routeGuards` with the permission it requires, or `open("why")` when it deliberately needs no Worklode identity. `NewServer` refuses to boot when a route is missing from the table or a table entry names no route, so an endpoint cannot ship unguarded. Cockpit page-script writes pass one more gate, `beginJSONPost` (same-origin, `X-Requested-With`, JSON body, actor from the session); see 10-cockpit.md.

Open routes, in the sense above: `/healthz`, `/metrics`, `/hooks/*`, `/.well-known/lode-login`, `/auth/*`, and `POST /auth/cli/token`.

## 4. Keycloak login

Keycloak (`https://auth.sunstoneinstitute.ai`, realm `sunstone`) is the sole interactive login for web and CLI. GitHub is link-only (section 9).

### 4.1 Server configuration

The whole feature is off unless `LODE_OIDC_ISSUER` and `LODE_OIDC_CLIENT_ID` are set.

| Var | Meaning |
|---|---|
| `LODE_OIDC_ISSUER` | `https://auth.sunstoneinstitute.ai/realms/sunstone` |
| `LODE_OIDC_CLIENT_ID` | `worklode` |
| `LODE_PUBLIC_URL` | External base URL, used for the web callback redirect URI and CLI discovery |
| `LODE_SESSION_SECRET` | HMAC key for session and intent cookies. Required when OIDC is enabled. |
| `LODE_WEB_OPEN` | Serve web pages to anonymous callers when no provider is configured. Ignored when OIDC is enabled. A non-boolean value fails the boot. |

ID tokens are verified with `github.com/coreos/go-oidc/v3` (JWKS fetch and cache, signature, `iss`, `aud`, `exp`). Claims used: `preferred_username`, `name`, `groups`, `github_username`.

### 4.2 Realm configuration

Kept as GitOps in the admin cluster's `keycloak-config/rbac.yaml`. Only group memberships are edited in the console.

| Item | Value |
|---|---|
| Client `worklode` | Public client, standard flow, PKCE S256, `fullScopeAllowed: false`. One client for every environment. |
| Redirect URIs | `http://localhost:8080/auth/callback`, `https://<host>/auth/callback` |
| Protocol mappers | `client-roles-as-groups` (client roles arrive in the `groups` claim); user attribute `github_username` as an ID-token claim |
| Client roles | `user` (required to log in), `admin` (maps to `actors.admin`) |
| Groups | `/apps/worklode` carries `worklode:user`; `/apps/worklode/admins` carries `worklode:admin` |

### 4.3 Provisioning on login

Both the web callback and the direct token exchange run the same steps: verify the ID token (invalid, expired or wrong audience gives 401); require `user` in `groups` (else 403); upsert the `human` actor with `admin = groups contains admin` and `expected_github_login = github_username`; both flags re-sync on every login, so a demotion takes effect at the next login. A missing `github_username` never fails a login.

## 5. Web sessions

When OIDC is enabled, every cockpit page requires a valid session cookie and otherwise redirects (302) to `/auth/login`. `/healthz` and `/metrics` stay open. When OIDC is unconfigured, the web routes answer 503 naming the missing configuration, unless `LODE_WEB_OPEN` is set.

| Route | Behaviour |
|---|---|
| `GET /auth/login` | 302 to the Keycloak authorize URL. Auth-code plus PKCE. `state` and the PKCE verifier travel in a short-lived signed cookie. |
| `GET /auth/callback` | Redeems the code at Keycloak, verifies the ID token, provisions the actor (section 4.3), then calls `finishLogin`. |

`finishLogin(w, r, actorID)` is the shared tail of every login and has three branches, chosen by the CLI-intent cookie (section 7): absent sets the session cookie and redirects to the requested page; loopback intent mints a one-time code and redirects to the CLI; manual intent renders the code on a page.

The session cookie is HMAC-SHA256-signed `{username, expiry}` under `LODE_SESSION_SECRET`, about 12 hours, `HttpOnly`, `Secure`, `SameSite=Lax`. There is no server-side session state and no logout endpoint. Cookies expire.

## 6. Direct OIDC token exchange

`GET /auth/oidc/config` and `POST /auth/oidc/token` serve a client that holds its own Keycloak ID token. `POST /auth/oidc/token` with body `{"id_token": "..."}` runs section 4.3 and mints a 30-day token with description `sso login for <user> at <RFC3339>`, returning `{"token": ...}` once. It returns 404 when OIDC is unconfigured. There are no refresh tokens; the user logs in again after expiry.

## 7. CLI login

`lode login` is provider-neutral and server-mediated. The CLI speaks no provider protocol and never sees a provider token. It opens a URL and waits.

| Endpoint | Purpose |
|---|---|
| `GET /.well-known/lode-login` | Discovery. Returns `{authorize_url: {public}/auth/cli/login, token_url: {public}/auth/cli/token, providers: ["keycloak"]}`. 404 when no interactive provider is configured; the CLI then tells the user to ask an admin for a token. |
| `GET /auth/cli/login?redirect_uri=…&state=…` | Validates `redirect_uri` is loopback only (`localhost`, `127.0.0.1` or `::1`, scheme `http`, explicit non-zero port), stores `{redirect_uri, state, mode}` in a short-lived cookie signed with the session secret, and redirects into the normal web login. With `mode=manual`, `redirect_uri` is omitted and `state` is still required. Any other combination is 400. |
| `POST /auth/cli/token` `{code, state}` | Validates the one-time code (exists, unexpired, unused, `state` matches the value bound at mint), mints a 30-day token with description `lode login`, marks the code used, returns `{token, actor_id, expires_at}`. Unauthenticated: holding the code is proof the browser flow completed. |

### 7.1 Loopback flow

1. `GET /.well-known/lode-login`.
2. Bind a loopback listener on an ephemeral port (`localhost:0`).
3. Open the browser at `{authorize_url}?redirect_uri=http://localhost:PORT/&state=CLISTATE`. The server runs its Keycloak login, reusing any existing browser session, provisions the actor, mints a one-time code and redirects to the loopback URI. Because the server performs that last redirect, the loopback URI needs no registration with Keycloak.
4. The listener receives `?code=…&state=…`; the CLI checks `state`.
5. `POST {token_url}`.
6. Store the token (section 8) and write only `server` to `config.toml`.

### 7.2 Manual mode

`lode login --no-browser`, or automatically when the platform opener (`xdg-open`, `open`, `rundll32`) is not on `PATH`, or the platform is neither macOS nor Windows and both `DISPLAY` and `WAYLAND_DISPLAY` are empty. Any other launch failure stays fatal, because a browser probably did open. Manual mode binds no listener. The CLI prints `{authorize_url}?mode=manual&state=CLISTATE`; the user opens it anywhere; after login the page shows the one-time code with a copy button; the user pastes it into the CLI prompt; steps 5 and 6 proceed unchanged. The page carries the code, never the token, so the long-lived credential appears only inside the CLI process and the keychain. Stdin at EOF fails with guidance instead of blocking.

### 7.3 One-time codes

An in-memory mutex-guarded map of `{actorID, state, expiresAt, used}`. 32 bytes of entropy, single use, 5-minute TTL (long enough for a human to carry a code between machines, within RFC 6749's ten-minute ceiling). The server is single-instance, so a restart drops pending codes and the user re-runs login. If the server goes multi-replica this becomes a table.

### 7.4 Security properties

- `redirect_uri` is loopback only, which blocks exfiltration and open redirect.
- `state` is round-tripped and checked by the CLI (CSRF).
- Codes are single-use, short-lived, high-entropy, and bound to actor plus `state`.
- The intent cookie is signed and short-lived.
- Manual mode gives up the guarantee that the code lands in the process that requested it, the same residual risk every device-code flow carries. It is bounded by single use and the 5-minute life, and used only when loopback cannot run.

## 8. Client token storage

`config.toml` (`~/.config/worklode/`) holds only `server`. The token never goes there.

Resolution order in `LoadConfig`: `LODE_TOKEN` env, then keychain keyed by server URL, then the token file keyed by server URL.

| Store | Details |
|---|---|
| OS keychain | `github.com/zalando/go-keyring`: macOS Keychain, Linux Secret Service, Windows Credential Manager. Service `worklode`, account = server URL, so one machine holds tokens for several servers and `LODE_SERVER` selects one. |
| Token file | `~/.config/worklode/token`, mode 0600, one `<server> <token>` line per server. Written temp-file-plus-rename, which also resets a looser mode. Deleting the last token removes the file. Used only when no keychain exists; login prints the path when it takes this route. |

The `tokenStore` abstraction (`Get/Set/Delete(server)`) has keychain, file and mock (`keyring.MockInit()`) implementations. Absence earns the fallback, failure does not: `keychainAvailable` probes for an account no server URL could equal. `ErrUnsupportedPlatform`, a D-Bus `ServiceUnknown`/`NameHasNoOwner` reply, or no reachable bus count as absence. A locked collection or dismissed prompt is an error carrying `export LODE_TOKEN=…` guidance; that failing path still records `server` in `config.toml`.

`lode logout` deletes the current server's token from both stores. In-cluster automation uses `LODE_TOKEN`.

## 9. GitHub account linking

Keycloak proves identity; it cannot act on GitHub. An authenticated actor links a GitHub account once, and Worklode stores a user-to-server token so it can call GitHub attributed as "SunstoneWork on behalf of `<user>`".

### 9.1 The GitHub App

One App per environment (`worklode-dev`, `worklode-prod`), because an App has one webhook URL and one callback set.

| Setting | Value |
|---|---|
| User authorization | Enabled, expiring user tokens (8 h access, about 6-month refresh) |
| Callback URL | `{LODE_PUBLIC_URL}/auth/github/callback` |
| Webhook URL | `{LODE_PUBLIC_URL}/hooks/github`, HMAC via `LODE_GITHUB_WEBHOOK_SECRET` |
| Permission ceiling | Repository Contents: read, Actions: read, Deployments: read, Pull requests: read. Never `contents: write`. |
| Installation | `sunstoneinstitute` org, selected repositories only. The provisioning and admin-cluster repos are excluded. |

A stored user token is bounded by the intersection of the App's permissions, its installation scope and the user's own access, so a compromised server holding both the database and `LODE_TOKEN_ENC_KEY` still cannot push code. A feature that needs repo writes gets its own narrowly installed App. No-bypass rulesets on the provisioning and admin repos guard independently.

| Var | Kind |
|---|---|
| `LODE_GITHUB_APP_ID`, `LODE_GITHUB_APP_PRIVATE_KEY`, `LODE_GITHUB_WEBHOOK_SECRET` | App identity and webhook signing (07-knowledge-graph-and-search.md and 03-tasks-and-execution.md cover the webhook consumers) |
| `LODE_GITHUB_APP_CLIENT_ID` | OAuth client id (config) |
| `LODE_GITHUB_APP_CLIENT_SECRET` | OAuth client secret (1Password, ExternalSecret) |
| `LODE_TOKEN_ENC_KEY` | Random 32-byte AES-GCM key (1Password, ExternalSecret) |

### 9.2 Link flow

1. `GET /auth/github/link` (authenticated session) redirects to GitHub's authorize endpoint with signed state, as a confidential client without PKCE.
2. `GET /auth/github/callback` exchanges the code, fetches `GET /user` (`id`, `login`), and strict-checks `login` against the session actor's `expected_github_login`, case-insensitively. A missing attribute or a mismatch refuses the link with an error naming the fix and writes no row.
3. On success, upsert the row in `github_user_tokens` keyed by the Worklode actor id.

Linking is lazy. Nothing prompts at login. A feature that needs GitHub shows a "Connect GitHub" redirect at the point of need. Re-linking after `broken` repeats the flow; GitHub skips consent for an already-authorized App.

| Command | Behaviour |
|---|---|
| `lode login` | Section 7 |
| `lode actor link github` | Calls a bearer-authed endpoint that mints a short-lived signed nonce bound to the calling actor and returns a link URL. Opens the browser and polls link status until linked, refused, or expired. |
| `lode actor show` | Logged-in identity, token expiry, link state: unlinked, linked as `<login>`, or broken (reconnect). |

### 9.3 Stored GitHub tokens

The row is the link: a link exists exactly when a row exists, and unlinking deletes it.

| Column | Notes |
|---|---|
| `actor_id` | PK, references `actors` |
| `github_user_id` | Unique. The durable external identity. |
| `github_login` | Display only, refreshed on re-link |
| `token_ciphertext` | AES-GCM under `LODE_TOKEN_ENC_KEY`, sealing `{access_token, refresh_token, access_expires_at}`. Sealing lives in `internal/tokencrypt`. |
| `status` | `active` or `broken` |
| `created_at`, `updated_at` | |

`store.UserToken(ctx, actorID)` returns a valid access token, refreshing lazily when expired or within a skew window. GitHub refresh tokens are single-use, so the row is locked `FOR UPDATE` during a refresh; concurrent callers wait and reuse the new pair. A failed refresh sets `status = broken`, and callers translate that into "reconnect GitHub" guidance. There is no background refresher.

### 9.4 Errors

| Condition | Response |
|---|---|
| Link with no `expected_github_login` | Refused: "no GitHub username on your Keycloak account" |
| Link with mismatched login | Refused, naming both logins |
| Code exchange or `GET /user` failure | 502, retriable, no row written |
| Refresh failure | Row `broken`; `lode actor show` and the web UI say "reconnect GitHub"; the caller gets a typed error |
| CLI link nonce expired or consumed | Terminal error; re-run to retry |
| Actor id conflict with a non-human actor | 409 (`errActorKindConflict`) |

## 10. Task-declared secrets

Tasks declare the secrets they need by symbolic name, the same way they pin skills (09-cli-and-skills.md). The system resolves names against an org-wide catalog and materializes values through a ceremony the operator takes part in, before execution goes unattended. This removes mid-task stalls on interactive prompts, makes detached executors possible, and gives a session exactly the credentials its task declared, auditable in the event log.

Worklode is not a security broker. It stores names and templates and starts a ceremony. 1Password is the source of truth for values, `op://` URLs are the addressing scheme, and the operator's own `op` session is the decryption authority. `op://Employee/…` therefore resolves against each operator's private vault with no service account crossing that boundary.

### 10.1 Names

Secret names match `^[A-Z][A-Z0-9_]*$` and are org-unique, never per-project, because a repo may serve several projects. The grammar also rejects loader-sensitive names at every gate: anything starting with `LD_` or `DYLD_`, and `PATH`, `IFS`, `ENV`, `BASH_ENV`, `PYTHONPATH` and the rest of the listed shell and runtime loading variables. The same grammar governs `cred.<PLACEHOLDER>` keys and `env` names.

### 10.2 The catalog

The catalog is the 1Password item `worklode-secrets-catalog`. An ExternalSecret using `dataFrom.extract` projects it per environment (via `ClusterSecretStore` `onepassword-hzdev-worklode` / `onepassword-hzprod-worklode`) into a Kubernetes Secret of the same name, mounted into the server. `catalog.toml` and each template are field labels on that item. The server reads `catalog.toml` from `LODE_SECRETS_CATALOG_PATH` and template files from the same directory. Catalog changes are 1Password edits. The catalog carries no values and no per-user state.

`catalog.toml` is a hand-rolled TOML subset (`internal/secrets.ParseCatalog`). An entry has one of two shapes.

```toml
[GITHUB_TOKEN]                      # plain
ref = "op://Employee/GitHub agent token/credential"
description = "GitHub credential the agent operates as"
baseline = true

[KUBECONFIG_HZDEV]                  # templated
description = "Kubernetes access to the hzdev cluster"
template = "kubeconfig-hzdev.yaml"   # sibling key in the same Secret
env = "KUBECONFIG"
cred.CLIENT_CERT = "op://Infrastructure/hzdev kubeconfig/client-cert"
cred.CLIENT_KEY = "op://Infrastructure/hzdev kubeconfig/client-key"
```

| Key | Rule |
|---|---|
| `ref` | One `op://` reference. Mutually exclusive with `template`; every entry has exactly one of the two. |
| `template` | Names a sibling key in the projected Secret holding the template text. Requires at least one `cred.` key. |
| `cred.<PLACEHOLDER>` | Placeholder to `op://` reference. Invalid without `template`. |
| `env` | Exported environment-variable name at exec. Defaults to the entry name. Any shape. Two entries for one task resolving to the same `env` is an exec-time error naming both. |
| `description` | Shown at the consent prompt. |
| `baseline` | `true` marks a secret every task needs (commit signing key, GitHub credential). Packed for every claim, exempt from consent. Default `false`. |

A templated entry exists because an OS keystore item is size-capped (about 2.9 KB raw on macOS through go-keyring, 2560 bytes on Windows) and a kubeconfig is 3 to 6 KB. A value over the cap is a modelling error: only the client credentials are secret, and the server URL, CA certificate and context names are configuration. Chunking across items is rejected. Every credential of a templated entry must itself fit the cap.

Template syntax: a placeholder is `{{ PLACEHOLDER }}` (inner whitespace optional). Rendering is verbatim single-pass byte substitution with no escaping, conditionals or nesting; a credential value containing `{{ … }}` is substituted literally. Every placeholder must have a `cred.` key and every `cred.` key must be used. Any other `{{` is an error. There is no escape for a literal `{{`.

Serving: `GET /api/v1/secrets/catalog`, authenticated with any actor token. There is no unauthenticated route, because the name-to-`op://` map exposes vault and item structure. A templated entry's response inlines its template text, exported name and placeholder-to-reference map. The server validates on read: the template file exists, the placeholder set equals the `cred.` set, the template is valid UTF-8, and derived item names `<NAME>__<PLACEHOLDER>` are unique catalog-wide and disjoint from every entry name. A validation failure is a 500 with a log line naming the entry.

### 10.3 Declaration

- A `secrets` name list on the task (`lode task add --secrets A,B`, settable at create and update). The task model is in 03-tasks-and-execution.md.
- `secrets: [NAME, …]` in design-doc frontmatter (05-documents.md).
- Resolution at claim: task pins, union governing-doc pins, union the baseline set.
- A declared name missing from the catalog is a brief warning, never a failure.

Declaring is the planner's job. The `lode-secrets` skill makes that a standing instruction.

### 10.4 Claim-time ceremony

Claim time is the one moment a human is guaranteed present. The claim hook (08-agent-harness-and-sessions.md) runs this after lease and worktree bind and before brief injection:

1. Fetch the catalog and resolve declared plus baseline names to `op://` refs.
2. Consent: non-baseline entries are listed once each, by name and description, and the operator answers one yes/no for the set. Decline means no materialization; the claim still succeeds.
3. Write `.worklode/secrets.env` in the worktree in `op run` env-file format, references only. A plain entry contributes `NAME=op://…`; a templated entry contributes one line per credential under the item name `<NAME>__<PLACEHOLDER>`.
4. Run `op run --env-file .worklode/secrets.env -- lode secret pack`. One `op run` resolves every reference under one 1Password authorization. `lode secret pack` writes each resolved value into the OS keystore, one item per env-file line, and exits. Values never touch disk or the shell.
5. The backbone logs a `secrets_materialized` event with task, actor and entry names only. Values, item names, templates and refs never enter the event log.

Keystore: on macOS, one keychain item per secret, service `worklode:<task-id>`, account `<NAME>` or `<NAME>__<PLACEHOLDER>`, created by the `lode` binary so later unattended reads do not re-prompt. On Linux, a file encrypted to an ephemeral key held in ssh-agent (best effort).

Manifest: a 0600 file outside the worktree records, per materialized entry, its item names (purge's only enumeration), its exported env name, its template text, and, once rendered, the rendered file's absolute path. Persisting the template is what keeps exec offline.

Re-materialization: `lode work resume` checks the keystore and re-runs the ceremony if items are missing or the declaration changed, which also propagates catalog template edits.

### 10.5 Execution

```
lode secret exec [--] <command> [args…]
```

Resolves the bound task from the worktree, reads that task's items from the keystore, builds the child environment, and `syscall.Exec`s. Nothing is written, logged or echoed. The child keeps the parent environment minus every credential-shaped name (`ANTHROPIC_API_KEY`, `AWS_*`, anything containing `TOKEN`, `SECRET`, `PASSWORD` or `AUTH`), keeping `PATH`, `HOME`, `TMPDIR` and the locale variables. Materialized names are injected after the scrub, and any ambient assignment of an exported name is stripped first. The injected set is exactly the task's materialized entries.

For a plain entry, exec injects `<env>=<value>`. For a templated entry, exec fetches the credential items, renders the manifest's template, writes the result to `.worklode/secrets/<NAME>` in the worktree (directory 0700, file 0600, temp-file-plus-rename), records the absolute path in the manifest, and injects `<env>=<absolute path>`. The path is stable and re-rendered on every exec, so the file heals after deletion or a worktree move. `.worklode/secrets/` and `.worklode/secrets.env` are in the repo's local git exclude.

The rendered file lives until purge. Exec ends in `syscall.Exec`, so per-invocation cleanup would need fork/wait, which changes the process contract and still leaks on a SIGKILLed parent, and a long-running child may read `KUBECONFIG` late. The residual exposure is a plaintext 0600 file on the operator's single-user machine for the same window the credentials sit in the keystore.

| Command | Purpose |
|---|---|
| `lode secret catalog` | List catalog names and descriptions (authenticated) |
| `lode secret status` | Declared versus materialized names for the bound task; for templated entries, credentials and whether a rendered file exists |
| `lode secret pack` | Internal child of `op run` at ceremony time |
| `lode secret purge [--task <id>]` | Remove a task's keystore items and rendered files; when bound to a worktree, also remove `.worklode/secrets/` |
| `lode secret exec -- <cmd>` | Run a command with the task's secrets injected |

### 10.6 Purge

Materialized lifetime equals worktree lifetime, matching the lease. `lode work submit`, `lode work block` and worktree removal purge unconditionally and locally. The worktree exit hook purges only after the backbone gives a definite "lease gone" answer (no lease, or the task 404s); a live lease, a timeout or any error leaves items in place with a warning, because a multi-task session exits while still holding its lease. The server-side lease sweeper cannot reach a laptop keystore. `lode doctor` (08-agent-harness-and-sessions.md) walks the local manifests, asks the backbone about each task's lease, and purges those with a definite answer. Uncertainty never purges: a purge by mistake costs a whole ceremony, a purge one run later costs nothing.

### 10.7 The `lode-secrets` skill

Loaded in both contexts. When writing plans: every task lists the catalog names it needs; a needed secret with no catalog entry is a plan-level finding, fixed by adding the entry before the task is executable. When executing: run credentialed commands through `lode secret exec`; never probe `op`, ask the operator for values, or read `.worklode/secrets.env` expecting values. A needed but unavailable secret is a block signal, `lode work block` with a `missing-secret: NAME` reason. The skill contains no `op://` refs, so it is safe wherever skills live.

### 10.8 Degradation

| Condition | Behaviour |
|---|---|
| Catalog endpoint unreachable at claim | Claim succeeds; brief warns `secrets: catalog unavailable`; credentialed steps are blocked |
| Declared name not in catalog | Brief warning naming it; ceremony proceeds for the rest |
| Operator declines consent | No materialization; brief records the declined names; credentialed steps block |
| `op` not installed or not signed in | Ceremony fails fast at claim with an install or sign-in hint |
| Keystore read fails at exec | Exit non-zero naming the item; the agent blocks, never retries or works around |
| Lease expires, worktree remains | Items persist until the next exit with a definite "gone", a resume re-materializes, or removal purges |
| Template missing, placeholder set differs from `cred.` set, invalid UTF-8, or item-name collision | Catalog read fails server-side, 500 plus log naming the entry; claims degrade as "catalog unavailable" |
| A `cred.*` value exceeds the keystore cap at pack | `lode secret pack` fails naming the item; the fix is catalog modelling |
| Rendered-file write fails | Exec exits non-zero naming the path; block signal |
| Two entries export the same `env` for one task | Exec exits non-zero naming both entries |


## Sources

WL-SPEC-1 (identity and authentication), WL-SPEC-54 (agent actors), WL-SPEC-17 (task-declared secrets), WL-SPEC-42 (secret templates), with folded amendments from ADRs 043, 047, 048 and 050.

## Open questions

- Consent granularity: one yes/no for the non-baseline set, or per name once catalogs grow (Q17.1).
- Staleness: whether to force re-materialization after N days for long-lived worktrees (Q17.2).
- Catalog visibility: the full catalog is served to any authenticated actor; whether to narrow per role or project (Q17.3).
- Catalog home: whether to promote the 1Password item to a backbone table plus admin CLI if churn grows (Q17.4).
- The first feature that writes to GitHub as a user, and the exact repository write permissions it needs, are unspecified.
