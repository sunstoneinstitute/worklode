# API

This document is for whoever builds the Go server. The React frontend calls
the endpoints below. In development, MSW (Mock Service Worker) answers them
with dummy data instead of a real server — `src/mocks/handlers.ts` is a
working reference implementation of this contract, and `src/mocks/db.ts`
holds the dummy data the JSON examples below are copied from. The Go server
replaces MSW; nothing else in the frontend needs to change.

## Running the frontend against Go

- `pnpm dev` starts with the mock API (MSW) on. Every `/api/...` request is
  answered in the browser, with no network call.
- `VITE_API_MOCKS=off pnpm dev` starts with mocks off. Vite then forwards
  (proxies) every `/api/...` request to `http://localhost:8080`, or to
  `VITE_API_TARGET` if you set that instead. Both variables are documented
  with comments in `.env.example`; copy it to `.env.local` to change them on
  your machine.

## Serving the built app

`pnpm build` writes the whole frontend to `dist/`. The Go server serves it
like this:

- Serve everything under `dist/assets/` as static files, with long cache
  headers. Their file names contain a content hash, so a given file name
  never changes contents — it's safe to cache forever.
- Answer every `/api/*` request yourself.
- For every other GET request, return `dist/index.html`. This is what makes
  a refresh on a page like `/projects/the-grid/tasks` work: the browser
  loads the app, and the router picks the right page from the URL. Without
  this fallback, refreshing any page but `/` would 404.

`dist/mockServiceWorker.js` (copied from `public/` by the build, like any
other static asset) doesn't need special handling — it's dev-only and never
registered in a production build (`src/main.tsx` only starts MSW under
`import.meta.env.DEV`), so it's harmless if served and fine to ignore.

## Conventions

- Request and response bodies are JSON.
- Dates are ISO strings: `YYYY-MM-DD` for a date (e.g. `"2026-09-15"`),
  `YYYY-MM-DDTHH:mm:ss` for a date-time (e.g. `"2026-09-24T09:00:00"`).
- People are referenced by user id (e.g. `"u1"`), never by display name. The
  frontend resolves ids against the crew roster (`GET /api/crew`) to show an
  avatar and to mark the current selection in a picker; a display name can't
  be resolved that way.
- An error response is `{ "message": string }`.
- A validation error is `422` with
  `{ "message": string, "errors": { "<field>": "<message>" } }` — one entry
  per invalid field.
- A request for an id that doesn't exist gets `404`, body
  `{ "message": string }`.
- A successful delete returns `204` with no body.

## Endpoints

### `GET /api/me`

The signed-in user. There's no login yet, so this is always the same user —
see "Not yet on the API".

Response `200`:

```json
{ "id": "u1", "name": "Claudicia" }
```

### `GET /api/crew`

Everyone on the crew roster, used to show any person's name and avatar.

Response `200`:

```json
[
  { "id": "u1", "name": "Claudicia" },
  { "id": "u2", "name": "Marcus Chen" },
  { "id": "u3", "name": "Priya Patel" },
  { "id": "u4", "name": "Tobias Smith" }
]
```

A crew member's `handle` and `avatarUrl` fields are optional; if a member has
no `handle`, the frontend derives one from their name.

### `GET /api/projects`

Every project.

Response `200`:

```json
[
  {
    "id": "the-grid",
    "title": "The Grid",
    "author": "u1",
    "type": "data-story",
    "status": "accepted",
    "submitted": "2026-07-14",
    "progress": 55,
    "daysRemaining": 20,
    "attention": "5 past due tasks"
  }
]
```

`attention` is a short headline shown on the quick-link card (the frontend
bolds its first word); it's plain text the server composes, not a template.

### `GET /api/tasks?projectId=`

Tasks. `projectId` (optional): when given, only that project's tasks;
omitted, every task.

Response `200`:

```json
[
  {
    "id": "t1",
    "name": "Draft launch copy",
    "project": "the-grid",
    "assignee": "u1",
    "due": "2026-09-15",
    "status": "in-progress",
    "priority": "high"
  }
]
```

`assignee` is `""` when nobody is assigned. `due` is `""` when there's no due
date.

### `POST /api/tasks`

Create a task. Request body: a task without `id` — the server assigns one.

```json
{
  "name": "Draft launch copy",
  "project": "the-grid",
  "assignee": "u1",
  "due": "2026-09-15",
  "status": "in-progress",
  "priority": "high"
}
```

Response `201`: the task, with its new `id`.

```json
{
  "id": "t5",
  "name": "Draft launch copy",
  "project": "the-grid",
  "assignee": "u1",
  "due": "2026-09-15",
  "status": "in-progress",
  "priority": "high"
}
```

`422` if `status` or `priority` isn't one of the allowed values (see below).

### `PATCH /api/tasks/:id`

Change one or more fields on a task. Request body: only the fields that
change.

```json
{ "status": "done" }
```

Response `200`: the full, updated task (same shape as `POST`'s response).

`404` if `:id` doesn't exist. `422` if `status` or `priority` is given and
isn't one of the allowed values.

### `DELETE /api/tasks/:id`

Delete a task. Response `204`, no body. `404` if `:id` doesn't exist.

### `GET /api/meetings?limit=`

Upcoming meetings. `limit` (optional): a positive number returns only the
first `limit` meetings; omitted, `0`, or not a number returns every meeting.

Response `200`:

```json
[
  {
    "id": "m1",
    "title": "The Grid Startup Meeting",
    "start": "2026-09-25T09:00:00",
    "end": "2026-09-25T13:45:00"
  }
]
```

## Allowed values

The `value` strings below are the contract. Labels and colors (e.g. "In
Progress", which badges get a tint) live in the frontend, not the API.

Task `status`: `todo`, `in-progress`, `done`, `blocked`

Task `priority`: `low`, `medium`, `high`

Project `status`: `litmus`, `on-hold`, `accepted`

Project `type`: `data-story`, `short-story`

## Tasks table: still to build

Two gaps that aren't about the API contract above, so they're easy to miss:

- The row-level Edit (pencil) action is a no-op — there's no edit form yet.
- Column show/hide and reordering has no UI yet, though the table's state
  model supports both.

## Not yet on the API

These still use inline dummy data in the frontend and aren't covered by this
document yet. They follow in later plans:

- Ideas
- The Projects table's writes (create, edit, delete)
- Documents
- Intake page data (litmus tests, pre-research findings, sources)
- The project summary card
- Project crew (per-project picker lists, as opposed to the global roster)
- Column-layout preferences
- Forms
