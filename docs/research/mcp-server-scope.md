# Worklode MCP server: scope

Draft. Moves into the backbone as a spec (`lode doc add`) before the
implementation lands; this file is the working copy until then.

Goal: manage Worklode tasks and documents from Claude Code, claude.ai and
Cowork without the `lode` CLI.

## Transport

- Streamable HTTP at `/mcp` on lode-server, built on `modelcontextprotocol/go-sdk`.
- Tools only in v1. No resources, no prompts.
- The tool server is its own package, `internal/mcp`: `mcp.New(mcp.Config)`
  returns the `http.Handler` that `internal/api` mounts on `POST`, `GET` and
  `DELETE /mcp`. `Config` hands it the `/api/v1` mux as an `http.Handler`,
  plus callbacks to authenticate a `wl_` token and to write the 401
  challenge. `internal/api` keeps the OAuth side: the login
  (`mcpauth.go`), the protected-resource metadata and the challenge
  (`mcpbase.go`), and the `routeGuards` rows.
- Each tool calls that `/api/v1` handler in-process with the caller's
  `wl_` token, through `cli.Client`. Route guards, `Decide`, validation and
  metrics apply unchanged. Text output reuses `cli.*Render`.
  `internal/mcp` is a client of `/api/v1` like `internal/cli`: it may not
  import `internal/api`, `internal/store` or `internal/cmd`, and
  `internal/disttest` fails the build if it does.
- `show` with inline folding runs `internal/designdoc` on the server.
- New `routeGuards` rows: `/mcp`, `/oauth/*`, `/.well-known/oauth-*`.
- Metric: `worklode_mcp_tool_calls_total{tool,outcome}`.

## Auth

Worklode is the OAuth authorization server for MCP clients. Keycloak stays
upstream through the existing `worklode-<env>` client and `/auth/callback`.
No realm change.

- `GET /.well-known/oauth-protected-resource[/mcp]` (RFC 9728). `/mcp`
  answers 401 with `WWW-Authenticate: Bearer resource_metadata=...`.
- `GET /.well-known/oauth-authorization-server` (RFC 8414). PKCE S256 only.
- `GET /oauth/authorize`: checks `client_id`, `redirect_uri`,
  `code_challenge`, `resource`. Sets a signed MCP-intent cookie carrying a
  nonce, then runs the normal Keycloak login with that nonce in its signed
  oauth state. `finishLogin` gets a third branch next to the CLI one, taken
  only when the intent's nonce is the login's: an intent cookie left by an
  abandoned connector or `lode login` cannot turn a later plain web login
  into theirs, and a plain `/auth/login` clears it.
- `resource` is required when two MCP origins are configured (prod), because
  only it says which issuer the client discovered, and RFC 9207 requires a
  correct `iss` on every response once the metadata advertises it. With one
  origin it may be left out. The MCP authorization spec already requires
  clients to send it.
- Consent: after the login, before any code exists, the browser shows a
  consent page naming who is signing in and where the token goes ("the
  Worklode connector in claude.ai", or "an app on this computer
  (localhost:PORT)"), with Allow and Deny. Keycloak signs a logged-in user in
  silently and the client is public, so without this a crafted authorize link
  would hand that user's token to whoever chose the redirect. The pending
  consent lives server-side for 5 minutes, single use, keyed on a random id
  that the page posts back together with a cookie of the same value.
  `POST /oauth/consent` accepts same-origin submissions only. Allow mints a
  single-use 5-minute code bound to the challenge and redirects; Deny
  redirects with `error=access_denied`.
- `POST /oauth/token` (form-encoded): checks the PKCE verifier and that
  `resource` is one of this server's MCP URLs. Returns a 30-day `wl_` token
  (`ssoTokenTTL`), described as `mcp: <client>`. No refresh token.
- Client registration: one static public client `worklode-mcp`, configured in
  worklode, with redirects:
  - `http://localhost:33418/callback`, `http://127.0.0.1:33418/callback`
    (Claude Code, same port as `grafana-mcp`)
  - `https://claude.ai/api/mcp/auth_callback` (claude.ai and Cowork)
  No dynamic client registration.

## Reachability

| Env | Host for /mcp | Who uses it |
|---|---|---|
| dev | `worklode.dev.sunstoneinstitute.ai` (already public) | all clients |
| prod | `worklode.prod.sunstoneinstitute.ai` (public, MCP paths only) | claude.ai, Cowork |
| prod | `worklode.sunstone.internal` (mesh) | Claude Code |

Prod public ingress (`nginx`, not `nginx-internal`):

- Paths: `/mcp`, `/oauth/token`, `/.well-known/oauth-*` only.
- `nginx.ingress.kubernetes.io/whitelist-source-range: 160.79.104.0/21`
  (Anthropic's published outbound range; no outbound IPv6 is listed).
- The browser leg (`/oauth/authorize`, `/auth/callback`) is not on the public
  host. The authorization-server metadata names
  `https://worklode.sunstone.internal/oauth/authorize` as
  `authorization_endpoint`, so the user's browser does the login over the
  mesh, and Keycloak's already-registered internal callback is reused. The
  user's device must be on the mesh when connecting.
- Server config: `LODE_PUBLIC_URL` stays the internal host; a new
  `LODE_MCP_PUBLIC_URL` names the public MCP URL, and both are accepted as
  `resource`.

### Deploy notes

The ingress is `deploy/overlays/hzprod/mcp-public-ingress.yaml`; the overlay
sets `LODE_PUBLIC_URL=https://worklode.sunstone.internal` and
`LODE_MCP_PUBLIC_URL=https://worklode.prod.sunstoneinstitute.ai`. What the
other repos say, read on 2026-10-17 (provisioning at `fb08ddc`):

- **Hostname.** hzprod's public names are `<name>.prod.sunstoneinstitute.ai`:
  `otel.prod.sunstoneinstitute.ai`
  (`provisioning:clusters/hzprod/observability/otel-gateway-ingress.yaml`) and
  `flux-webhook.prod.sunstoneinstitute.ai`
  (`provisioning:clusters/hzprod/flux-system/kustomization.yaml`). Bare
  `<name>.sunstoneinstitute.ai` names (`auth`, `headscale`, `fleetdm`) are
  admin-cluster hosts written in OpenTofu. So prod is
  `worklode.prod.sunstoneinstitute.ai`, the twin of hzdev's
  `worklode.dev.sunstoneinstitute.ai`.
- **DNS.** external-dns on hzprod makes the record from the Ingress; no
  annotation and no change in another repo. It watches Ingresses
  (`provisioning:infrastructure/base/external-dns/release.yaml`, `sources:
  [ingress]`) and owns all of `sunstoneinstitute.ai` except `admin.*`,
  `dev.*`, `hzdev.*` (`provisioning:clusters/hzprod/external-dns/kustomization.yaml`).
  Nothing in provisioning sets `--cloudflare-proxied` or the
  `cloudflare-proxied` annotation, so the record is DNS-only and the client
  IP reaches the LB unchanged. The zone's tofu file
  (`provisioning:opentofu/live/prod/cloudflare/domain-sunstoneinstitute-ai.tf`)
  has no wildcard and no `prod` NS delegation, and its redirect ruleset only
  matches the apex and `www`. The TLS certificate comes from `letsencrypt-prod`,
  a DNS-01 Cloudflare solver
  (`provisioning:clusters/hzprod/cert-manager-issuers/clusterissuer.yaml`), so
  the allowlist does not block issuance.
- **Client IP.** The public controller (class `nginx`) sees the real source
  address. `provisioning:clusters/hzprod/ingress-nginx/kustomization.yaml` sets
  `load-balancer.hetzner.cloud/uses-proxyprotocol: "true"` on the LB Service
  and `use-proxy-protocol: "true"`, `use-forwarded-headers: "false"` in the
  controller config, with the comment that this is for source-range
  allowlists and that nothing sits in front of the LB. So
  `whitelist-source-range` matches the caller, and an `X-Forwarded-For` header
  cannot spoof it.

Not done yet, outside this overlay:

- provisioning has no Flux `Kustomization` for worklode on hzprod
  (`clusters/hzprod/flux-system/` has none; hzdev has `worklode.yaml` and
  `gotk-worklode-repo.yaml`). Until one exists, nothing applies this overlay.
- The hzprod overlay turns OIDC on but its ExternalSecret has no
  `LODE_SESSION_SECRET`, so `lode-server` refuses to boot, and
  `LODE_MCP_PUBLIC_URL` needs OIDC. The 1Password item needs the field before
  it is wired in, because ExternalSecret reconciliation is all or nothing.

## Project scope

A session's project is never picked silently: not from an exact match, and
not from an argument alone.

- The session's project is set only by `project_use(project, confirmed?,
  from_instructions?)`, keyed on `Mcp-Session-Id`, never per user. `project`
  must name a project exactly: its key, id or full name, ignoring case.
  - `from_instructions=true`: the user's Claude project instructions contain
    `Worklode project: <name>`. Claude calls this at the start without
    asking. It needs an exact match; anything else answers `confirm_project`.
  - `confirmed=true`: only after the user explicitly confirmed this exact
    project.
  - Neither: when the client supports form elicitation, the server asks the
    user directly ("Use the Worklode project Worklode (WL) for this
    conversation?") and an accept counts as confirmed. Otherwise, or on a
    near-miss name, it answers `confirm_project` and sets nothing.
- Every other tool takes an optional `project`, which may only name the
  session's confirmed project; any other value answers `confirm_project` and
  the tool does nothing. A full ref (`WL-12`, `WL-SPEC-3`, `WL-REQ-9`) carries
  its key, so a tool given one acts on that item without a session project;
  it names one item and sets nothing.
- No project at all: `project_required` with the project list.
- `confirm_project`, `project_required` and `project_resolve` carry the
  candidates (key and full name) and this instruction: "Confirm the project
  with the user before calling project_use. In a text or graphical
  interface, ask with your question tool (e.g. AskUserQuestion) offering
  these projects as options. In a voice conversation, read back the full
  project name and wait for the user to say yes." It also names the
  `from_instructions` shortcut.
- Tool descriptions say the same: use `Worklode project: <name>` from the
  project instructions with `from_instructions=true`, otherwise always
  confirm with the user first.

## Voice

- `project_list`: key and name.
- `project_resolve(spoken)`: fuzzy and phonetic match over projects only;
  ranked candidates with confidence. It never selects one, not even a single
  perfect match: Claude reads back the full name (or the top two or three)
  and waits for a yes before `project_use(confirmed=true)`.
- `ref` arguments accept spoken forms ("spec seventy-seven", "task 512",
  "spec-77") resolved against the session project. A leading kind word wins
  over the task-id shape, and `<PREFIX>-<n>` is read as a task id only when
  PREFIX is a project key; anything else (a slug like `rollout-2`) reaches
  the API's resolver as written.
- Every result starts with one speakable sentence, then detail. Lists cap at
  10 with a cursor.

## Kind aliases

The kinds already have one registry: `ns/concept.ttl` (SKOS schemes),
generated into `internal/ns/gen.go` by `scripts/nsgen.py`.

- `wlc:TaskKind`: feature, bug, chore, design, review, spike, decision, rally.
- `wlc:RuleKind`: requirement, catalogue, invariant, definition, principle.
- Document kinds (spec, plan) are not in it yet; only the `docs.kind` CHECK
  holds them. Add a `wlc:DocKind` scheme so all three live in one place.

Add `skos:altLabel` to each concept for the words people say, and have
`nsgen.py` emit a `KindAliases` map: spoken word → (entity, kind). A
generated-code test fails when one alias maps to two kinds. Proposed
aliases, to edit before building:

| Says | Files as |
|---|---|
| bug, defect, problem, issue, broken | task / bug |
| feature, enhancement, story, capability | task / feature |
| chore, cleanup, maintenance, housekeeping, upgrade | task / chore |
| spike, experiment, investigation, prototype | task / spike |
| decision, question, choice | task / decision |
| review | task / review |
| design task | task / design |
| spec, specification, design doc, design document, ADR | doc / spec |
| plan, implementation plan | doc / plan |
| requirement, rule | rule / requirement |
| invariant, definition, principle, catalogue | rule / that kind |

"design" alone is ambiguous (task kind vs. spec), so it resolves to neither
and the tool asks.

One write tool takes the spoken word: `file(what, title, body?, ...)`.
"File this as a bug" becomes `file(what: "bug", ...)` and creates a task of
kind bug. A word with no alias returns the closest kinds for Claude to read
back. `task_add`, `doc_add` and `rule_add` stay as the typed paths behind it;
`task_list`/`doc_list` accept the same words as a kind filter ("show me open
bugs").

## Tools (19)

| Area | Tool | Backing route |
|---|---|---|
| project | `project_list` | `GET /projects` |
| | `project_resolve(spoken)` | `GET /projects` + matcher |
| | `project_use(project, confirmed?, from_instructions?)` | session state |
| find | `search(query, kinds?)` | `GET /search` |
| | `show(ref, inline=true, section?)` | docs, rules, tasks, projects |
| file | `file(what, title, body?, parent?, doc?)` | resolves `what` via `KindAliases`, then one of the three add routes |
| tasks | `task_list(kind?, state?, assignee?)` | `GET /tasks` |
| | `task_add(kind, title, body, parent?, blocked_by?, governed_by?)` | `POST /tasks` (+ edges, governed-by) |
| | `task_update(id, title?, body?, priority?)` | `PATCH /tasks/{id}` |
| | `task_set_state(id, state, reason?)` | `/state`, `/abandon`, `/reopen` |
| docs | `doc_list(kind?, status?)` | `GET /docs` |
| | `doc_add(kind, title, body, covers?)` | `POST /docs` |
| | `doc_edit(id, body or patch, if_version)` | `PUT /body`, `POST /patch` |
| | `doc_transition(id, submit/accept/revise/withdraw)` | lifecycle routes |
| rules | `rule_list(doc?)` | `GET /rules` |
| | `rule_add`, `rule_edit` | `POST`, `PATCH /rules` |
| | `rule_link(id, rel, target, remove?)` | `/rules/{id}/edges` |
| | `rule_supersede` | `/projects/{id}/rules/supersede` |

## Out of v1

Claiming and worktrees (`work next`, `task claim`, leases, agent sessions),
blobs, decisions, approvals, checklists, deliverables, milestones, admin,
token minting, event tail, `rule arrange`/`unarrange`.

## Client setup

- Claude Code `.mcp.json`: `type: http`, URL, `oauth.clientId: worklode-mcp`,
  `oauth.callbackPort: 33418`. `lode install` could write this later.
- claude.ai / Cowork: org custom connector with URL
  `https://worklode.prod.sunstoneinstitute.ai/mcp` (prod) or
  `https://worklode.dev.sunstoneinstitute.ai/mcp` (dev), and client id
  `worklode-mcp` in advanced settings. The user connecting must be on the mesh
  for prod, since the login page is on `worklode.sunstone.internal`.
