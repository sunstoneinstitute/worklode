---
status: accepted
issued: 2026-07-31
requires:
  - 002-github-app-auth.md
  - 011-delivery-lifecycle.md
  - 018-task-hierarchy.md
  - 019-project-scoping.md
---
# Spec 020 — Inbox import (onboarding a repo with history)

## Why

The inbox is fed only by live webhooks. Everything that existed before a repo
was registered stays invisible to Worklode forever.

| Path into `issues` / `prs` | Caller |
|---|---|
| `store.UpsertIssue` | `internal/hooks/github.go:244` — the `issues` webhook, and nothing else |
| `store.UpsertPR` | `internal/hooks/github.go:293` — the `pull_request` webhook, and nothing else |
| Any import, backfill, poll, or sync | **none** |

`internal/cmd/` holds `admin, board, claude, currentproject, githooks, hook,
inbox, install, lifecycle, login, logout, migrate, project, root, serve, task,
timeline, watch` — no import verb. Spec 013 puts inbox promotion explicitly
out of its scope (`013-reconciliation.md:24-26`).

So a repo's backlog is unreachable, and both workarounds are bad. Fabricating
webhook traffic (touch every issue so GitHub redelivers — `applyFunc` matches
on the event, not the action) writes two junk events per issue into public
timelines and notifies subscribers. Writing to the database directly needs
server DB access that no CLI or API exposes.

Onboarding `sunstoneinstitute/horndb` (41 open issues) used the first: a label
added and removed on all 41, 82 events, and only open *issues* — closed issues
and every PR stayed absent.

Three gaps compound it, all reachable from the same command:

- **Promote lands in `ready`.** `promoteRequest` (`internal/api/admin.go:411`)
  carries no `Draft`, so `PromoteIssue` → `CreateTask` makes every promoted
  task claimable at once. Survivable when triaging arrivals one at a time;
  not survivable when 41 arrive together, and there is no un-ready command.
- **Promote cannot reach the hierarchy.** `lode task add` gained `--parent`
  (`internal/api/tasks.go:111`), promote did not — so an imported backlog
  lands as loose tasks, never as an epic's children.
- **No way to link an issue to an existing task.** `issues.task_id` is written
  in exactly one place (`internal/store/inbox.go:77`), inside `PromoteIssue`,
  which unconditionally creates a new task. The 35 horndb tasks already seeded
  with `lode task add` carry issue URLs in their bodies and no link; the only
  remedy today is abandoning all 35 and re-promoting, losing their priorities,
  concerns, and 12 `blocks` edges.

## How the gap stayed invisible

Every repo's inbox was empty in every triage state, including `worklode`'s.
The `sunstonework` App had the Issues *permission* but had never been
subscribed to the Issues *event* — the permissions page looks correct either
way. `deployment_status` was missing too, which would have stranded tasks at
`merged` for any repo whose `done_state` is `deployed_dev`/`deployed_prod`.
Nothing surfaced the mismatch; the cost was diagnosis time, not data.

## Decisions

Taken here with rationale.

| Decision | Choice |
|---|---|
| Where import runs | Server-side — it holds the App installation token |
| Request shape | Synchronous, page-capped; no job table |
| Write path | `store.UpsertIssue` / `store.UpsertPR`, unchanged |
| Lifecycle replay | **Never** — import is inventory only |
| Default selection | `--state open`, issues only |
| Authorization | Admin, matching `add-repo` |
| Idempotency | `(repo, number)` upsert; promoted rows untouched |
| Linking an issue to a task | `lode inbox link`, a third triage verb |
| `triage_state` for a link | `promoted` — no new value, no migration |
| Event-subscription mismatch | Warn on `add-repo`; never gate |

### Import is inventory, not replay

This is the load-bearing decision. `applyPullRequest`
(`internal/hooks/github.go:253`) does two things: it upserts the PR row, and
it drives the lifecycle — `Transition` to `in_review`, `CloseActiveLease`,
`InsertTaskCommit`, `ResolveDelivery`. Import does the first and never the
second.

Those transitions encode *this just happened*. Replaying two years of merged
PRs through them would resolve delivery for tasks that were never in flight,
and — since spec 018 — `store.Transition` now ends in `resolveParent`
(`internal/store/tasks.go:220`), so it would roll that fiction up into epic
state as well. Import therefore calls the two `Upsert*` functions directly and
nothing else. Correlation still happens, because it lives inside `UpsertPR`
(head_ref, then body) and no-ops when the task does not exist.

### Synchronous and capped

`POST /api/v1/inbox/import` pages GitHub inline and returns counts. The cap is
20 pages of 100 per kind, so a repo with 2000 issues and 2000 PRs completes
inside one request; beyond it the response says `truncated` and the caller
reruns with `--since`. A job table, a status endpoint, and retry semantics buy
nothing at the scale onboarding actually has.

That resume only works because the fetch fixes the ordering. GitHub defaults
these endpoints to `sort=created&direction=desc`, which would put the *newest*
items on page 1 and truncate the oldest — and since `since` filters on
`updated_at >= T`, rerunning could only ever return a subset of what the first
run already had. Paging `sort=updated&direction=asc` instead puts the truncated
tail at the *newest* end, so `--since` becomes a cursor rather than a filter —
but only for issues: `/issues` accepts `since` server-side, `/pulls` does not.
`issues.truncated` and `prs.truncated` are therefore reported independently,
and `newest_updated_at` is computed from the issues stream only, set when
issues truncate. A truncated PR list cannot be resumed via `--since` at all
(rerunning refetches the same first `maxPages` pages and merely re-filters
them client-side); the CLI instead tells the caller to narrow the run, e.g.
`--state open`. The CLI prints the exact rerun for issues.

### `--state open` by default

Whether closed and merged history is worth importing is a per-repo judgement,
so it is opt-in. It also has a trap: `--state all` on a mature repo drops
hundreds of rows into `triage_state = 'new'`, and `lode inbox dismiss` takes
one issue at a time. Bulk dismiss is a follow-up, not part of this spec —
which is the second reason the default stays narrow.

## Design

### Fetch — `internal/githubauth/list.go`

Two functions beside `DiscoverDoneState` (`app.go:138`), reusing
`InstallationToken` (`app.go:94`) and `githubJSON` (`githubauth.go:93`):

```go
func (a *AppAuth) ListIssues(ctx context.Context, repo, state string, since time.Time, maxPages int) ([]Issue, bool, error)
func (a *AppAuth) ListPulls(ctx context.Context, repo, state string, maxPages int) ([]PullRequest, bool, error)
```

Both page `?sort=updated&direction=asc&per_page=100&page=N` until a short page
or `maxPages`, returning `truncated` rather than parsing Link headers. The
ordering is what makes `--since` a resume cursor, as above. They return plain structs
carrying exactly the fields the webhook appliers read, so the new file needs no
`store` import at all — assembling `store.Issue` / `store.PullRequest` is the
API layer's job, as it already is for webhook payloads.

`ListIssues` **skips every entry with a `pull_request` key**. GitHub's
`/repos/{repo}/issues` returns pull requests as issues; without the filter,
every PR in the repo lands in the inbox as an issue. `/pulls` has no `since`
parameter, so `--since` is applied server-side for issues and filtered on
`updated_at` client-side for PRs.

### API — `POST /api/v1/inbox/import`

```json
{"repo": "owner/name", "state": "open", "include_prs": false,
 "since": "2026-01-01T00:00:00Z", "dry_run": false}
```

Registered `s.auth(requireAdmin(s.importInbox))` alongside the other inbox
routes (`internal/api/server.go:404-406`). Admin, because this is an
onboarding act like `add-repo` (`server.go:395`), not a triage act like
promote.

Preconditions, in order: `503` when `s.appAuth` is nil (no App configured);
`422` on an unknown `state`; `404` when `ProjectForRepo` finds no mapping — an
unmapped repo's webhooks are recorded `.ignored`
(`internal/hooks/github.go:164`), so its import must be refused for the same
reason.

The GitHub round trips happen outside any transaction under a 60s bound, then
the whole result is applied in one `RecordEvent("cli", extID, "inbox.imported",
payload, apply)` — one event, one transaction, matching how promote and dismiss
record. `apply` first reads the existing `(repo, number)` set for the repo, so
the response can distinguish new rows from updated ones, then upserts.

```json
{"repo": "owner/name", "issues": {"new": 38, "updated": 3},
 "prs": {"new": 0, "updated": 0}, "truncated": false, "dry_run": false}
```

`--dry-run` fetches, counts against the existing set, and returns the same
shape with `dry_run: true` — skipping `RecordEvent` entirely, so no event row
either.

CLI: `lode inbox import <repo> [--state open|closed|all] [--include-prs]
[--since <date>] [--dry-run]`.

### Staging what was imported

Three changes to `promoteRequest` (`internal/api/admin.go:411`),
`cli.PromoteInput`, and the `lode inbox promote` flags:

- **`draft`** → `store.TaskInput.Draft`, which already exists
  (`internal/store/tasks.go:44`) and already yields state `draft`. Published
  with the existing `lode task ready`. This is what makes a 41-issue backlog
  reviewable before it becomes claimable.
- **`parent`** → `store.AddEdge(tx, now, t.ID, parent, "child_of")` inside the
  same `RecordEvent` as the promotion, mirroring `createTask`
  (`internal/api/tasks.go:154-159`) including its named-404 pre-check
  (`:104-114`), which exists so `AddEdge`'s `ErrNotFound` is not reported
  anonymously. `AddEdge` remains the authority on the spec-018 invariants.
- **`kind = "epic"` is rejected with 422.** `validKinds` admits `epic`
  (`internal/api/tasks.go:18`), but `epicForbiddenStates`
  (`internal/store/tasks.go:108`) bars an epic from `in_review`,
  `deployed_dev`, `deployed_prod`, and `released`. An issue promoted as a
  childless epic can therefore never leave `in_progress`. This is reachable on
  `main` today; import makes it reachable 41 times in a row.

`needs_decomposition` and `lode task decompose` are the other staging lever
for a bulk-promoted backlog. They need no change here; noted so the two are
not reinvented separately.

### Link — `lode inbox link <repo> <number> <task-id>`

The third triage outcome: *this issue is already covered by task X.*

`store.LinkIssue(tx, repo, number, taskID)` requires `triage_state = 'new'`
(else `ErrBadTransition`, matching `PromoteIssue` and `DismissIssue`) and an
existing task (else `ErrNotFound`), then sets `triage_state = 'promoted'` and
`task_id`. It reuses `promoted` rather than adding a value: the baseline
CHECK allows `new`, `promoted`, `dismissed`
(`0001_baseline.up.sql:92`), and "this issue has a task" is exactly what
`promoted` means — so no migration. `POST /api/v1/inbox/link`, event type
`issue.linked`.

### Event-subscription check

`AppAuth.SubscribedEvents(ctx)` reads `events` from `GET /app` under the App
JWT. `hooks.HandledEvents()` exports the seven strings `applyFunc` routes
(`internal/hooks/github.go:192-227`) **and `applyFunc` switches over that same
list**, so the check cannot drift from the handler when an eighth event is
added.

`addRepo` compares the two and returns any missing events in a `warnings`
field, which `lode project add-repo` prints. Non-gating, the same posture as
`discoverDoneState` (`internal/api/admin.go:231`): the mapping is already
committed and a slow or failing GitHub must not hold the response.

## Testing

`internal/api/appauth_test.go:55-82` already stands up a fake GitHub behind an
overridable `BaseURL`; import tests extend it with paged issue and pull
fixtures.

| Case | Asserts |
|---|---|
| Issues feed containing a `pull_request` entry | It is skipped — no PR in the inbox |
| Two pages then a short page | Both pages imported, `truncated` false |
| `maxPages` exceeded | `truncated` true, rows still written |
| Import run twice | Second run reports 0 new, 0 changed rows |
| Import over a promoted row | `triage_state`, `task_id`, `applies_to_versions` unchanged |
| `--dry-run` | No `issues` rows, no `events` row |
| Unmapped repo | 404, nothing written |
| `include_prs` over a merged PR | PR row exists; task state and epic roll-up unchanged |
| `promote --draft` | Task state `draft` |
| `promote --parent` | `child_of` edge in the same event; bad parent → 404 |
| `promote --kind epic` | 422 |
| `LinkIssue` | Happy path; already-promoted → `ErrBadTransition`; missing task → `ErrNotFound` |
| `SubscribedEvents` missing `issues` | `add-repo` warns, mapping still created |

## What this spec does not cover

Spec 014 §*Adoption is out of scope* forward-declares a spec 020 that onboards
an existing project wholesale: issues → Tasks, `docs/specs/**` and
`docs/adr/**` → published documents, repos → Components, GitHub projects →
Workstreams. **This spec delivers the first of those four and defers the rest.**

The reason is dependency order, not preference. The three deferred halves all
land in spec 014's document and component model, which is Status `draft` with
no implementation: there is no document or section table, and no RDF layer at
all. Spec 014 in turn depends on 006 (the vocabulary it amends, its SHACL gate
and closure tests) and 007 (the deriver contract and named-graph
partitioning), both unimplemented, plus a `wl:` ontology PR against
rdf-registry. Importing documents therefore has no destination, and inventing
one here would prejudge exactly the questions 014 reserves: what anchors a
corpus that never had them receives, and whether a first publication of legacy
prose is `accepted` or `draft`.

The issues half has no such dependency — `issues`, `prs`, and `tasks` all
exist — so it ships on its own. The two were never coupled in code.

One asset survives for later: `AppAuth.Tarball`
(`internal/githubauth/content.go:28`), built for skill sync, is already the
right mechanism for pulling `docs/specs/**` out of a repo. When 014 lands,
fetching is solved and only the mapping is new.

## Follow-ups this leaves open

- **Bulk dismiss.** `--state all` on a mature repo makes one-at-a-time
  dismissal untenable. Not in scope; the narrow default is the mitigation,
  and it is recorded in `docs/follow-ups.md`.
- **Documents, components, and workstreams.** The other three quarters of
  014's adoption story, blocked as above.
