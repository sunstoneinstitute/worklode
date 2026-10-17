# Worklode MCP server: scope

Draft. Moves into the backbone as a spec (`lode doc add`) before the
implementation lands; this file is the working copy until then.

Goal: manage Worklode tasks and documents from Claude Code, claude.ai and
Cowork without the `lode` CLI.

## Transport

- Streamable HTTP at `/mcp` on lode-server, built on `modelcontextprotocol/go-sdk`.
- Tools only in v1. No resources, no prompts.
- Each tool calls the existing `/api/v1` handler in-process with the caller's
  `wl_` token, through `cli.Client`. Route guards, `Decide`, validation and
  metrics apply unchanged. Text output reuses `cli.*Render`.
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
  `code_challenge`, `resource`. Sets a signed MCP-intent cookie, then runs the
  normal Keycloak login. `finishLogin` gets a third branch next to the CLI
  one: mint a single-use 5-minute code bound to the challenge and redirect.
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
| prod | new public host, e.g. `worklode.sunstoneinstitute.ai` | claude.ai, Cowork |
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

To check before building: the ingress sees the real client IP (Hetzner LB
proxy protocol or `externalTrafficPolicy: Local`); otherwise the allowlist
matches the LB address. That lives in `provisioning`.

## Project scope

- Every tool takes an optional `project`.
- Order: explicit argument, then the MCP session default (set by
  `project_use`, keyed on `Mcp-Session-Id`, never per user), then fail.
- On failure the tool returns `project_required` with the project list and
  the text "Ask the user which Worklode project to use for this session, then
  call project_use."
- Tool descriptions tell Claude to look for `Worklode project: <key>` in the
  claude.ai or Cowork project instructions and pass it.

## Voice

- `project_list`: key, name, one-line description, spoken aliases.
- `project_resolve(spoken)`: fuzzy and phonetic match over projects only;
  ranked candidates with confidence. One clear winner: use it. Otherwise read
  back the top two or three.
- `ref` arguments accept spoken forms ("spec seventy-seven", "task 512")
  resolved against the session project.
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
| | `project_use(project)` | session state |
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
- claude.ai / Cowork: org custom connector with the public URL and client id
  `worklode-mcp` in advanced settings.
