---
status: accepted
issued: 2026-07-30
requires:
  - 004-execution-backbone.md
  - 010-per-project-task-keys.md
  - 008-worklode-plugin.md
---
# Spec 019 — Repo-scoped CLI commands

## Why

Working from a worktree, `lode` should already know which project you mean.
Today it half does.

| Command | `--project` | Uses `current_project` |
|---|---|---|
| `task add` | yes | yes (hard error if neither resolves) |
| `task list` | yes | yes |
| `task claim --next`, `next` | yes | yes |
| `board [project]` | positional only | **no** |
| `inbox list` | **no** | **no** — the API has no project filter |
| `task show/edit/timeline`, … | n/a (task id) | n/a |

Three gaps follow. `lode board` in a worktree shows every project's board.
`lode inbox list` shows every repo's issues. And `current_project` only exists
where someone hand-wrote `.worklode/config.toml`, so a fresh clone or a new
worktree is unscoped until it is configured — the mapping the server already
holds in `project_repos` is never consulted by the CLI.

## Decisions

Taken here with rationale, pending sign-off.

| Decision | Choice |
|---|---|
| Default scope | The current repo's **project**, not the repo |
| Repo-level task filter | None — tasks have no repo column |
| `--repo owner/name` | Sugar for naming a project by one of its repos |
| Projects per invocation | One, or all (`--project=`) — no multi-select |
| Detection | Config first, git remote as fallback |
| Remote normalization | Server-side |
| Lookup cost | Cached under `~/.cache/worklode` |
| Resolution failure | Silent fallthrough to unscoped |
| Bare task numbers | `lode task show 12` → `WL-12` via the current project's key |
| `inbox` | Gains project scoping, server-side |
| `project list` | Stays org-wide |

**Project, not repo.** A task belongs to a project (`tasks.project_id`); no
task carries a repo. `project_repos` maps repo → project, one project per repo.
Filtering by repo would therefore mean either "the project this repo belongs
to" (identical to project scoping, for the common one-repo project) or
inventing a task-repo association that does not exist. `--repo` is accepted
only as a second way to *name* a project.

**One project or all.** `store.TaskFilter.Project` is a single id and the SQL
is `project_id = $n`. Widening it to a set touches the store, both handlers,
and the client for a case nobody has asked for. `--project=` already covers
"show me everything".

**Server-side normalization.** A git remote is spelled
`git@github.com:owner/name.git`, `https://github.com/owner/name`,
`git+ssh://git@github.com/owner/name.git`, and more. Normalizing once on the
server keeps the CLI to `git remote get-url origin` plus an HTTP call, and
means a normalization fix ships without a CLI upgrade.

## Resolution chain

Every project-aware command resolves its scope the same way, first hit wins:

1. `--project X` or `--repo owner/name` on the command line. An explicitly
   empty `--project=` means *all projects* and stops here.
2. `current_project` in the repo-local `.worklode/config.toml` (or `.lode/`),
   found by walking up from the working directory (`findRepoConfig`,
   `internal/cli/client.go:66`).
3. `current_project` in `~/.config/worklode/config.toml`.
4. **New:** the git remote. `git remote get-url origin` in the working
   directory, resolved against `project_repos` by the server.
5. Nothing resolved → unscoped, i.e. all projects.

Step 4 never fails a command. Not a git repo, no `origin`, server unreachable,
repo not mapped — each falls through to step 5. A command that cannot proceed
unscoped (`task add`) reports the missing project itself, as it does today.

`--project` and `--repo` together is an error: they name the same thing.

## Server changes

### `GET /api/v1/projects/resolve?remote=<url>`

Returns the project a remote URL maps to, or 404 when the repo is unmapped.
Accepts every form `git remote get-url` emits, plus a bare `owner/name` so
`--repo` uses the same endpoint:

| Input | Normalized |
|---|---|
| `git@github.com:sunstoneinstitute/worklode.git` | `sunstoneinstitute/worklode` |
| `https://github.com/sunstoneinstitute/worklode` | `sunstoneinstitute/worklode` |
| `https://github.com/sunstoneinstitute/worklode.git` | `sunstoneinstitute/worklode` |
| `git+ssh://git@github.com/sunstoneinstitute/worklode.git` | `sunstoneinstitute/worklode` |
| `ssh://git@github.com:22/sunstoneinstitute/worklode.git` | `sunstoneinstitute/worklode` |
| `sunstoneinstitute/worklode` | `sunstoneinstitute/worklode` |

Normalization: strip a `scp`-style `user@host:` prefix or a URL scheme and
authority, drop a leading `/`, drop a trailing `.git` and any trailing `/`,
then require exactly two non-empty path segments. Anything else is a 422, the
validation status the rest of `internal/api` uses. The
host is discarded — `project_repos.repo` is `owner/name` and unique, so a
GitHub Enterprise mirror of a mapped repo resolves to the same project.

Lookup is `store.ProjectForRepo` (`internal/store/projects.go:157`), already
used by the webhook path. The response is the same project object
`GET /api/v1/projects` returns, so it carries `key` for the task-id shorthand.

Read-only, so plain `s.auth` — no `requireAdmin`. Registered before
`POST /api/v1/projects/{id}/repos`; Go's mux prefers the literal segment over
`{id}`, so `resolve` cannot be shadowed by a project named `resolve`.

### `GET /api/v1/inbox?project=<id>`

`listInbox` (`internal/api/admin.go:371`) gains a `project` query param
alongside `state`. Empty or absent keeps today's org-wide behavior. The store
filter joins `project_repos` on the issue's `repo`:

```sql
JOIN project_repos pr ON pr.repo = i.repo AND pr.project_id = $n
```

An unknown project id yields an empty list, matching how `GET /api/v1/tasks`
treats one.

### Unchanged

`GET /api/v1/board?project=` and `GET /api/v1/tasks?project=` already do
one-or-all. No migration: no schema or store-filter shape changes beyond the
inbox join.

## Client changes

### Resolution and the cache

A new `internal/cli` resolver owns steps 2–5 of the chain and its cache. The
cache is `~/.cache/worklode/remotes.json`, mode 0600, written atomically
(temp file + rename):

```json
{
  "servers": {
    "https://wl.example.com": {
      "remotes": {
        "git@github.com:sunstoneinstitute/worklode.git": {
          "project": "worklode", "at": "2026-07-30T12:00:00Z"
        },
        "https://github.com/acme/unmapped": {
          "project": "", "at": "2026-07-30T12:01:00Z"
        }
      },
      "keys": {
        "worklode": { "key": "WL", "at": "2026-07-30T12:00:00Z" }
      }
    }
  }
}
```

- **`servers`** is keyed by the client's base URL. A repo→project mapping is
  the answer of one server, and `LODE_SERVER` lets one machine talk to
  several, so a local test server's `wl-dev` must never be served to the team
  server. Each command reads and writes only its own server's section.
- **`remotes`** is keyed by the raw string `git remote get-url origin`
  returned, unmodified. The server normalizes, so two spellings of one repo
  get two entries — harmless, and it keeps normalization in one place.
  An empty `project` is a negative entry, written whenever the server gives a
  definite answer — 404 (no mapping) or 422 (a remote it cannot read as
  `owner/name`) — so such a repo stops re-querying on every command.
- **`keys`** caches project id → task-id key for the bare-number shorthand.
  A remote lookup fills it for free; a config-supplied `current_project` fills
  it from `GET /api/v1/projects` on first use.
- **TTL**: 7 days for a hit, 1 hour for a miss. A repo mapped on the server
  just now starts working within the hour with no manual step.
- A corrupt, unreadable, or unwritable cache is treated as empty and never
  fails a command — the lookup just does not persist.
- An explicit `--project`/`--repo` bypasses the cache entirely.

### `lode project resolve [--refresh]`

Prints the project the working directory resolves to and which step of the
chain produced it, so scoping is inspectable instead of magic:

```
$ lode project resolve
worklode (WL) — from git remote git@github.com:sunstoneinstitute/worklode.git (cached)
```

`--refresh` re-queries the server and rewrites the entry. This is the
supported way to fix a stale mapping; nobody should be told to `rm` a file.

### Bare task numbers

`lode task show 12` resolves to `WL-12` using the current project's key. One
client-side choke point handles it for every id-taking command: an argument
matching `^[0-9]+$` is prefixed with the resolved project's key, anything else
is passed through untouched. Full ids keep working from anywhere, unscoped and
unchecked — no cross-project warning, no refusal.

With no project resolved, a bare number is an error naming the reason:

```
$ lode task show 12
12 is a task number, not a task id, and no current project is set:
pass a full id like WL-12, or set current_project
```

The alternative — teaching every `/api/v1/tasks/{id}` endpoint to accept
`?project=` — touches all fifteen task routes for the same result.

### Command surface

| Command | Change |
|---|---|
| `task list` | unchanged flags; gains the remote fallback |
| `task claim --next`, `next` | unchanged flags; gains the remote fallback |
| `task add` | `--project` no longer hard-required — falls through the chain. Still an error when nothing resolves or `--project=` is explicit |
| `board` | gains `--project` and `--repo` and the default scope. The positional `[project]` stays, as shorthand for `--project` |
| `inbox list` | gains `--project` and `--repo` and the default scope |
| `task show/edit/ready/reopen/rework/claim/renew/release/done/abandon/block/unblock/brief`, `timeline`, `next <id>`, `block --on` | accept a bare task number |
| `status` | reports the resolved project and which chain step produced it |
| `project resolve` | new |
| `project list` | unchanged — enumeration is the point |
| `resume`, `done`, `hook`, `install`, `actor`, `token`, `login`, `serve`, … | unchanged. They act on a worktree, an explicit id, or the whole install |

Every command taking `--project` also takes `--repo`, and both carry help text
naming the default source.

## Testing

- **Normalization** — table test over every URL form above plus the 400 cases
  (one segment, three segments, empty owner, garbage).
- **Resolution chain** — table test with a stubbed git remote and a stub
  server, asserting each step wins over the ones below it and that each
  step-4 failure mode (no repo, no origin, HTTP error, 404, malformed
  response) falls through to unscoped rather than erroring.
- **Cache** — hit, expiry, negative entry and its shorter TTL, corrupt file,
  unwritable directory, atomic replacement.
- **Command wiring** — per changed command, assert the outgoing query params
  for: default scope, `--project X`, `--repo owner/name`, `--project=`, and
  `--project` with `--repo` (error).
- **Inbox filter** — store test for the `project_repos` join, including an
  unmapped repo's issues being excluded and an unknown project returning
  empty.
- **Bare numbers** — resolution with a project, the error without one, and
  full ids passing through unchanged.
- **e2e** — a fresh clone with no `.worklode/config.toml` scopes `task list`
  and `board` off its git remote alone.

## Non-goals

- Multi-project selection (`--project a,b`).
- A task-to-repo association, or repo-level task filtering.
- Scoping `project list`, or any command that acts on a worktree or an
  explicit id.
- Writing `current_project` back into `.worklode/config.toml`. The cache is
  invisible and regenerable; a tracked file mutated by a read-only-looking
  command is not.
- Cross-project guardrails on id-taking commands.
