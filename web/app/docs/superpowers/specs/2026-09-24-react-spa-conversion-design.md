# Convert the prototype into a React single-page app for a Go backend

Date: 2026-09-24
Status: Approved 2026-09-24

## Goal

Until now, the plan for handing off this prototype was to export each page as
static HTML (`scripts/export-static.mjs`) and have the backend developer turn
that HTML into Go templates. That plan has changed. The Go backend will now
serve this code as a real React single-page app (SPA: one HTML page, and
React swaps screens in the browser without reloading).

The backend developer does not want Next.js. He asked for TypeScript 7 and
pnpm (and esbuild, see "Build tool" below for why we keep Vite).

We hand him working screens with dummy content. He writes the Go API and
switches the app from dummy data to his endpoints.

### What success looks like

- Every existing screen looks and behaves the same as today.
- `pnpm dev`, `pnpm typecheck`, `pnpm lint`, `pnpm test` and `pnpm build` all
  pass on the new toolchain.
- Every read and write of data goes through one folder (`src/api/`), and every
  endpoint the frontend calls is listed in `docs/API.md` with its request and
  response JSON.
- The backend developer can turn off the dummy data and point the app at his
  Go server without editing any React component.
- Dark mode, column layouts, sort/filter/search and unsent form drafts survive
  a page refresh.
- Every form is built from a schema the server sends. A new form, or a new
  field on an existing form, needs no frontend code.

### Out of scope

- Login and authentication.
- Pagination and server-side sort/filter/search. `docs/API.md` lists them as
  future query parameters.
- The Go server itself.

## Decisions

| Topic | Decision |
|---|---|
| Package manager | pnpm, enabled through corepack |
| Type checker | TypeScript 7.0.x (`typescript@7`, the native Go port of the compiler), used for type checking only |
| Build tool and dev server | Vite 8 (kept) |
| Test runner | Vitest (kept) |
| Router | TanStack Router, code-based route tree, browser history (real paths, not `#/`) |
| Server data | TanStack Query, calling real `/api/...` URLs with `fetch` |
| Dummy data | MSW (Mock Service Worker), which answers those URLs inside the browser and in tests |
| Static HTML export | Removed |
| Vanilla scripts in `public/scripts/` | Ported to React |
| Dark mode | localStorage |
| Column layout | Saved on the server, per user |
| Sort, filters, search, chip category | URL search parameters |
| Form drafts | localStorage |
| Forms | Built from a server response: JSON Schema (data and validation) + our layout object + data + submit target |
| Form renderer | Our own (`src/forms/`), validating with Ajv |
| Non-field form content | Markdown from the server, rendered without raw HTML |

### Build tool: why we keep Vite instead of switching to esbuild

The backend developer asked for esbuild because of speed. We measured this
repo on 2026-09-24 (Apple Silicon, no caches):

| Step | Tool | Time |
|---|---|---|
| Production build | Vite 8 | 0.25 s |
| Type check | `tsc` (TypeScript 6) | 1.9 s |
| Tests (17 files) | Vitest + jsdom | 4.9 s, about a quarter of it spent creating jsdom |

Vite 8 builds with Rolldown, a bundler written in Rust in the same speed class
as esbuild. Its build already takes a quarter of a second, so esbuild could
save almost nothing. Switching would also lose Vite's hot module reload in
development and the `@tailwindcss/vite` plugin. The speed gain the developer
wants comes from TypeScript 7, which type-checks several times faster than
TypeScript 6. That gain does not depend on the bundler.

## 1. Tooling

- Add `"packageManager": "pnpm@<version>"` to `package.json`. Delete
  `package-lock.json` and commit `pnpm-lock.yaml`. First-time setup is
  `corepack enable && pnpm install`.
- Replace `typescript@~6` with `typescript@^7.0.2`. TypeScript only checks
  types (`noEmit` stays on); Vite still turns TypeScript into JavaScript. The
  existing tsconfig files use `moduleResolution: "bundler"` and `paths`, which
  TypeScript 7 supports. Fix any option TypeScript 7 rejects.
- `package.json` scripts:

  | Script | Command |
  |---|---|
  | `dev` | `vite` |
  | `build` | `tsc -b && vite build` |
  | `typecheck` | `tsc -b` |
  | `test` | `vitest run` |
  | `test:watch` | `vitest` |
  | `lint` | `oxlint` |
  | `preview` | `vite preview` |
  | `iconfont` | unchanged |

  `export:html` is removed.

## 2. Routing

TanStack Router with a code-based route tree (routes declared in TypeScript,
not generated from file names). Browser history, so URLs are real paths.

| Path | Page |
|---|---|
| `/` | Home |
| `/tasks` | Tasks |
| `/ideas` | Ideas |
| `/ideas/new` | New Idea |
| `/ideas/$ideaId/edit` | Edit Idea |
| `/ideas/$ideaId/intake` | Idea Intake |
| `/projects` | Projects |
| `/projects/$projectId` | Project Overview |
| `/projects/$projectId/tasks` | Project Tasks |
| `/projects/$projectId/documents` | Project Documents |
| any other path | Not Found page |

- Project pages take a real `$projectId` instead of today's single hard-coded
  project. `/projects/the-grid` and `/projects/melting-glaciers` both work.
- A root layout route renders what every page shares: `Header`, `MobileMenu`
  and the `ScrollLogo` slot. Its loader fetches `/api/me` and
  `/api/me/preferences` before any page renders (see section 4).
- The router's built-in scroll restoration replaces the scroll-to-top effect in
  today's `App.tsx`: a new page starts at the top, Back returns to where you
  were.
- Every `href="#/..."` becomes a typed `<Link to="...">`, so a link to a route
  that does not exist fails the type check. This includes
  `src/lib/project-nav.ts`, `Header`, `ProjectQuickLinks` and the idea
  row actions. Nav items whose pages do not exist yet (Activity, Settings)
  stay as disabled items, not `#` links.
- `App.tsx`'s hash router and the `intakeIdeaId` / `editIdeaId` URL parsers are
  deleted.

### How Go serves the app

- `pnpm build` writes everything to `dist/`.
- Go serves files under `dist/assets/` directly. Their names contain a content
  hash, so they can be cached forever.
- Go handles every `/api/*` request itself.
- For every other GET request, Go returns `dist/index.html`. This is what makes
  a refresh on `/ideas/i1/edit` work: the browser gets the app, and the router
  picks the page from the URL.

## 3. Data layer

### Folder layout

```
src/api/
  types.ts       every data shape: Task, Project, Idea, ProjectDocument,
                 Meeting, CrewMember, ProjectSummary, Preferences, ...
  client.ts      small fetch wrapper: sends and parses JSON, throws ApiError
                 (with status and message) on any non-2xx response
  queries.ts     read hooks: useTasks({ projectId? }), useIdeas(), useIdea(id),
                 useProject(id), useCrew(), useMeetings({ limit }), ...
  mutations.ts   write hooks per resource: useCreateTask(), useUpdateTask(),
                 useDeleteTask(), ...
src/mocks/
  db.ts          all dummy data, moved out of the components, held in memory
  handlers.ts    one MSW handler per endpoint, reading and writing db.ts
  browser.ts     starts MSW in the browser
  node.ts        starts MSW in tests
```

Dummy data moves out of these files into `src/mocks/db.ts`:
`src/lib/crew.ts`, `src/lib/ideas.ts` (the module-level store is deleted),
`TaskList.tsx`, `ProjectList.tsx`, `ProjectQuickLinks.tsx`, `DocumentList.tsx`,
`UpcomingMeetings.tsx`, `ProjectSummary.tsx`, and the litmus tests,
pre-research findings and sources in `IdeaIntake.tsx`.

Table select options (task status, priority, idea type, document type, and so
on) stay in the frontend as constants, because they carry UI colors. Their
`value` strings are part of the API contract and are listed in `docs/API.md`.
Forms get their options from the form schema instead (section 5); the `value`
strings must match.

### Endpoints

These are the contract the Go server must implement.

| Method and path | Purpose |
|---|---|
| `GET /api/me` | Current user |
| `GET /api/crew` | Everyone: used to show any person's name and avatar, and as the picker list where no project applies (Ideas authors, Projects) |
| `GET /api/projects/:id/crew` | The project's crew: the picker list for that project's tables, and the people on the Overview card's "What is everyone working on?" view |
| `GET /api/projects` | Project list |
| `POST /api/projects` | Create a project, returns it with its real id |
| `GET /api/projects/:id` | One project |
| `PATCH /api/projects/:id` | Change fields on a project |
| `DELETE /api/projects/:id` | Delete a project |
| `GET /api/projects/:id/summary` | Past due, awaiting review and workloads for the Overview card |
| `GET /api/tasks?projectId=` | Tasks, all or for one project |
| `POST /api/tasks` | Create a task, returns it with its real id |
| `PATCH /api/tasks/:id` | Change fields on a task |
| `DELETE /api/tasks/:id` | Delete a task |
| `GET /api/projects/:id/documents` | A project's documents |
| `POST /api/projects/:id/documents` | Add a document |
| `DELETE /api/documents/:id` | Delete a document |
| `GET /api/ideas` | Idea list |
| `POST /api/ideas` | Create an idea |
| `GET /api/ideas/:id` | One idea |
| `PATCH /api/ideas/:id` | Change fields on an idea (including status) |
| `DELETE /api/ideas/:id` | Delete an idea |
| `PATCH /api/ideas/:id/intake` | Save intake progress and decision |
| `GET /api/ideas/:id/litmus-tests` | Litmus tests shown on the intake page |
| `GET /api/ideas/:id/pre-research` | Pre-research findings |
| `GET /api/ideas/:id/sources` | Sources list on the intake page |
| `POST /api/ideas/:id/sources` | Add a source |
| `GET /api/meetings?limit=` | Upcoming meetings |
| `GET /api/me/preferences` | Saved preferences (column layouts) |
| `PUT /api/me/preferences/columns/:tableId` | Save one table's column layout |
| `DELETE /api/me/preferences/columns/:tableId` | Reset one table's column layout |
| `GET /api/forms/:formId` | A form for creating a record: schema, layout, default data, submit target (section 5) |
| `GET /api/forms/:formId/:recordId` | A form for editing a record, filled with its current data |

### Global crew and project crew

Person columns do two jobs, and each needs a different list:

- **Showing a person** (name and avatar for a user id) uses the global roster
  from `GET /api/crew`. A task can stay assigned to someone who has left the
  project; their name must still show.
- **Choosing a person** in a picker uses the project's crew from
  `GET /api/projects/:id/crew` wherever a project applies.

`DataTable`'s `user` column type gets this split: `users` stays the lookup
list, and a new optional `pickableUsers(row)` returns the picker list for that
row. Without it, the picker falls back to `users`.

| Where | Picker list |
|---|---|
| Project Tasks, Project Documents | `useProjectCrew(projectId)` |
| Tasks (all projects) | Each row's own project crew, via `pickableUsers(row)` and `useProjectCrew` for each project that appears in the list |
| Ideas (author), Projects | Global crew |
| Project Overview crew view | `useProjectCrew(projectId)` replaces the lookup into global `CREW` |

`src/mocks/db.ts` holds project membership (which user ids belong to which
project), and the handler for `/api/projects/:id/crew` answers from it. Both
dummy projects get a crew that differs by at least one person, so the
difference is visible in the UI and testable.

Dates are ISO strings (`YYYY-MM-DD`). People are referenced by user id, never
by display name. `docs/TASKS-BACKEND.md` already explains why both matter; that
content moves into `docs/API.md`.

### Turning dummy data on and off

- `pnpm dev` starts with MSW on.
- `VITE_API_MOCKS=off pnpm dev` starts with MSW off. Vite then forwards
  (proxies) `/api` requests to `http://localhost:8080`, or to
  `VITE_API_TARGET` if set. Both variables go in `.env.example` with comments.
- MSW is never included in a production build. `main.tsx` loads it with a
  dynamic import guarded by `import.meta.env.DEV`, so the bundler drops it.
- Dummy data lives in memory: changes persist while you click around and reset
  on reload, as today. One exception: column-layout preferences, see section 4.
- Every MSW handler waits about 150 ms before answering, so loading states are
  visible and can be designed.

### DataTable becomes controlled

Today `DataTable` copies its `data` prop into its own state
(`useState(data)`) and changes that copy through `addRow`, `updateRow` and
`removeRow`. With a server, the server's list is the truth, so:

- `DataTable` renders the `data` prop directly and keeps no copy of the rows.
- `addRow`, `updateRow` and `removeRow` keep their names and signatures for row
  actions, but now call callbacks the page supplies (`onAddRow`,
  `onUpdateRow(id, patch)`, `onRemoveRow(id)`).
- Pages connect those callbacks to mutation hooks.
- Mutations are optimistic: the change shows immediately, and TanStack Query
  rolls it back if the server returns an error. A toast says what failed.
- Creating a row sends a POST and shows the new row once the server returns
  its real id. The prototype's client-side `blankTask()`-style ids go away.

`DataTable` stays generic. Only how rows flow in and out changes.

### Loading and errors

- While a page's data loads, it shows a simple skeleton in place of the table
  or card.
- If a request fails, the page shows a short message and a Retry button.
- `ApiError` carries the HTTP status so later code (for example, a login
  redirect on 401) has what it needs.
- The project has no toast component yet. Add shadcn's Sonner toast
  (`src/components/ui/sonner.tsx`), styled with the existing design tokens,
  and mount its `<Toaster />` once in the root layout.

## 4. Keeping state across refreshes

Two kinds of state, stored in different places:

- **Personal preferences** follow the person: dark mode (per device) and column
  layout (per user, on the server).
- **View state** belongs to the link: sort, filters, search and chip category
  go in the URL, so a refresh, a copied link and the Back button all keep them.

### Storage helper (`src/lib/storage.ts`)

- Keys are namespaced and versioned: `worklode:v1:<name>`. If a stored shape
  changes, bump the version and old values are ignored. No migration code.
- Every read is validated and wrapped in try/catch. Missing or bad data,
  private windows and blocked storage all fall back to the default. Storage
  problems never break the app.
- `usePersistedState(key, defaultValue, validate)` is the one hook that uses
  it. It listens to the browser's `storage` event, so a change in one tab (for
  example, switching to dark mode) shows up in other open tabs.

### Dark mode (localStorage)

- Key `worklode:v1:theme`: `"light"`, `"dark"`, or not set.
- Not set means follow the operating system's setting
  (`prefers-color-scheme`).
- A short inline script in `index.html` reads the key and sets the `dark` class
  on `<html>` before React loads. Without it, a dark-mode user would see a
  light flash on every refresh.
- The toggle in `ColorModeToggle` writes an explicit `"light"` or `"dark"`.
- Stays local, not on the server, for two reasons: it must apply before any
  network request can finish, and people often want different themes on
  different devices.

### Column layout (server, per user)

- Each table gets a stable `tableId` prop: `tasks`, `project-tasks`, `ideas`,
  `projects`, `project-documents`, `idea-sources`.
- The layout is `{ order: string[], hidden: string[] }`, keyed by column key.
- Loaded once by the root route's loader (`GET /api/me/preferences`) before any
  page renders, so tables start with the saved layout and columns never jump.
- Dragging a column pill or clicking its eye applies immediately. Saving
  (`PUT /api/me/preferences/columns/:tableId`) waits until changes stop for
  about 500 ms, so a drag across five positions sends one request.
- The existing Reset link calls
  `DELETE /api/me/preferences/columns/:tableId` and returns to the default
  layout.
- If a save fails, the layout stays as set for this session and a toast says
  "Couldn't save column layout". It does not snap back mid-work.
- Stored layouts can outlive a deploy, so: column keys that no longer exist are
  ignored, and new columns appear in their default position.
- In mock mode, the MSW handler also writes preferences to localStorage (key
  `worklode:v1:mock-preferences`), so layouts survive reloads during design
  review, like the real server will. This code lives only in `src/mocks/`.
- Until login exists, "per user" has no user to attach to. The Go server can
  treat every request as one user for now. `docs/API.md` says so.

### Sort, filters, search, chip category (URL)

- Each route that has a table declares typed search parameters with a
  validator, for example `/tasks?status=blocked&sort=due.asc&q=launch` or
  `/ideas?category=story-pitch`.
- Invalid parameters fall back to defaults instead of erroring.
- Parameters equal to their default are left out of the URL, so plain links
  stay clean.
- `DataTable`'s sorting, column filters and global search become controlled
  props, fed by the page from the URL.
- `ChipNav` in `DocumentList` and `IdeaList` reads and writes its category the
  same way.

### Form drafts (localStorage)

- Keys follow the form (section 5):
  `worklode:v1:draft:<userId>:<formId>` for a new record and
  `worklode:v1:draft:<userId>:<formId>:<recordId>` when editing. Every
  schema-built form gets drafts this way, with no per-form code.
- When a draft is restored, values for fields that are no longer in the form's
  schema are ignored.
- Data reaches the server only when the person presses the form's Save (or
  Submit) button. Nothing is ever sent to the server automatically.
- The local copy in localStorage is written as you type, after about 500 ms of
  no typing. This copy is what survives a refresh, a crash or a closed tab.
  Leaving the form on purpose goes through the unsaved-changes dialog, which
  either saves or discards.
- When a form opens with a saved draft, it fills the fields from the draft and
  shows "Draft restored · Discard". Discard deletes the draft and puts back the
  data the server sent.
- Deleted after a successful save, and by Discard.
- Leaving a form with unsaved changes asks first; see "Leaving a form with
  unsaved changes" below.
- Drafts can hold unpublished pitches, so they must not outlive the session
  of the person who wrote them. There is no login yet, so this spec only
  prepares for it:
  - The key includes the current user's id from `/api/me`, so a second
    person in the same browser never sees someone else's draft restored.
  - `storage.ts` exports `clearDrafts()`, which removes every
    `worklode:v1:draft:*` key. It has a test, but nothing calls it yet.
  - **When logout is built, it must call `clearDrafts()`.** The same applies
    when the server answers 401 (session expired). `docs/API.md` and
    `AGENTS.md` both state this, so whoever builds login sees it.
  - When logout is built, and drafts exist, logout first asks with the same
    `ConfirmDialog`: "You have unsaved drafts. Logging out deletes them."
    with Log out / Cancel. `storage.ts` exports `hasDrafts()` for this.
  - Dark mode is not cleared on logout; it is a device setting, not personal
    data.

### Leaving a form with unsaved changes

A form has unsaved changes when its current values differ from the data the
server sent. Opening a form and changing nothing, or changing a field and
changing it back, does not count.

- Leaving such a form shows a dialog in the style of the existing
  `ConfirmDialog` (the overlay used for deletes), not the browser's own
  dialog:
  - Message: "You have unsaved changes."
  - Text under it: "Save them before you leave, or they will be lost."
  - Three buttons:
    - **Save & leave** (filled, like ConfirmDialog's Yes): validates and
      saves to the server, then continues the navigation.
    - **Discard** (soft red, like the intake page's Stop
      button): deletes the local draft, then continues the navigation.
    - **Keep editing** (outlined): closes the dialog and stays on the form with
      its values.
  - Focus starts on Keep editing, so a stray Enter never saves or discards. The
    X, Escape and a click on the backdrop all act as Keep editing.
- Save & leave in detail:
  - While saving, all three buttons are disabled and Save & leave shows
    "Saving…".
  - If validation fails, the dialog closes, the navigation is cancelled and
    the form shows its field errors.
  - If the server answers 422, the same: the dialog closes, the navigation
    is cancelled and each message shows under its field.
  - Any other error: the dialog stays open with a short error line above the
    buttons, and the person can try again, discard, or keep editing.
  - On success: the draft is deleted, cached data is refreshed as for a
    normal save, and the navigation continues to wherever the person was
    going (not to the form's usual `onDone` destination).
- Because every deliberate exit now either saves or discards, a restored
  draft only appears after the page went away without the dialog: a
  refresh, a crash, or a closed tab.
- This is a new `UnsavedChangesDialog` component that reuses
  `ConfirmDialog`'s overlay, panel and button styles. `ConfirmDialog` itself
  stays a two-button Yes/No dialog.
- What counts as leaving:
  - Any in-app navigation: links, the header nav, Back and Forward. Uses
    TanStack Router's `useBlocker`, which lets the app show its own dialog
    and then continue or cancel the navigation.
  - Closing the slide-in panel: its X, Escape, or a click on the backdrop.
  - The form's own close X on the full-page form.
- No dialog after a successful save, since there is nothing unsaved left.
- No dialog on refresh or on closing the browser tab. The browser only allows
  its own plain "Leave site?" dialog there, and the draft survives both
  anyway.
- `ConfirmDialog` gets an optional `description` prop for the text under the
  message (used by the logout warning). Today it only has `message` and a
  preview slot.

## 5. Forms built from a server schema

Forms are built from one server response that carries the form's schema, its
layout, its data and where to submit it. The frontend has one generic
renderer. Adding a form, or a field, is a change to what Go sends; no frontend
code changes unless the form needs a new layout or a new kind of field.

### Requesting a form

| Request | Returns |
|---|---|
| `GET /api/forms/idea` | New Idea form with default data |
| `GET /api/forms/idea?type=story-pitch` | New Idea form with the type preselected |
| `GET /api/forms/idea/i1` | Edit Idea form filled with idea `i1` |
| `GET /api/forms/idea-pitch/i1` | The Edit Pitch panel on the intake page |

Query parameters on the new-record form set starting values. `formId` names
the form; `recordId` names the record being edited.

### The response

```jsonc
{
  "title": "Edit Idea",
  "schema": { /* JSON Schema, see below */ },
  "layout": {
    "type": "main-sidebar",
    "columns": {
      "main": [
        "title",
        { "field": "body", "widget": "textarea", "placeholder": "Describe your idea…", "mobileOrder": 4 }
      ],
      "sidebar": [
        "type",
        { "field": "storyType", "showWhen": { "field": "type", "equals": "story-pitch" } },
        { "field": "litmusTestId", "widget": "clearable",
          "showWhen": { "field": "type", "equals": "story-pitch" } },
        { "block": "note", "mobileOrder": 5,
          "markdown": "## What should my pitch contain?\n\nYour pitch must answer these questions:\n\n1. …",
          "showWhen": { "field": "type", "equals": "story-pitch" } }
      ]
    }
  },
  "data": { "title": "Where the glaciers went", "type": "story-pitch", "storyType": "data-story" },
  "submit": { "method": "PATCH", "url": "/api/ideas/i1", "label": "Save" }
}
```

### Schema: standard JSON Schema

JSON Schema (json-schema.org, draft 2020-12) describes the data only: which
fields exist, their types, allowed values and what is required. It says
nothing about layout.

- A field's label is its `title`. Its help text is its `description`.
- A select's options are `oneOf: [{ "const": "data-story", "title": "Data Story" }, ...]`:
  `const` is the stored value, `title` is the label.
- `format: "date"` renders the existing date picker and stores an ISO date.
- A person picker is `"type": "string"` with the extension
  `"x-options": "<url>"`, a URL that returns crew members, for example
  `/api/projects/the-grid/crew`. The frontend fetches the list from that URL.
  This is how forms follow the global crew / project crew split (section 3).
- Required fields are listed in the schema's top-level `required` array; any
  field not listed is optional. Fields required only in some cases use JSON
  Schema's `if` / `then`.
- In the form, a required field's label ends with an asterisk (`Title *`).
  The asterisk is hidden from screen readers (`aria-hidden`), and the input
  gets `aria-required="true"` instead, so assistive technology says
  "required" rather than "star". Optional fields are not marked. A field
  required through `if` / `then` shows the asterisk only while its condition
  holds. The renderer works this out from the schema; the layout does not
  repeat it. The Litmus field's hand-written "Optional." help text goes away.
- Custom error messages use Ajv's `errorMessage` keyword (from `ajv-errors`),
  for example `"errorMessage": { "required": { "title": "Give your idea a title." } }`.
  Go's validator ignores unknown keywords, so this does not affect the server.

The browser validates with Ajv (the standard JavaScript JSON Schema
validator). Go validates the submitted data against the same schema, so both
sides enforce the same rules and the rules are written once.

### Layout: our own format

- `layout.type` names a layout the frontend knows. Two to start:
  - `main-sidebar`: the current 8/4 split, with columns `main` and `sidebar`.
  - `single`: one full-width column, `main`.
- A layout type the frontend does not know falls back to one column with every
  item in order. A newer Go server never breaks an older frontend.
- Adding a layout later means adding one layout component in
  `src/forms/layouts/` and documenting its column names. The schema and the Go
  validation do not change.
- The split follows the form's own width (a container query at `@3xl`, 48rem),
  not the screen's, as `IdeaForm` does today. The same form fits the slide-in
  panel and a full page.
- Items in a column:
  - A plain string: the field with that key, default widget.
  - `{ "field": key, ... }`: a field with options:
    - `widget`: `text` (default for strings), `textarea`, `clearable` (text
      with a clear button), `select` (default when the schema has `oneOf`),
      `date` (default for `format: "date"`), `person` (default when
      `x-options` is set). `richtext` is reserved for the BlockNote editor
      later. An unknown widget renders as `text`.
    - `placeholder`
    - `showWhen`: `{ "field": key, "equals": value }` or
      `{ "field": key, "in": [values] }`.
    - `mobileOrder`: a number.
  - `{ "block": "note", "markdown": "...", ... }`: a guidance box in today's
    style (surface background, rounded card). Options: `title`,
    `collapsible: true` (renders as an accordion, for "A note about AI use"),
    `showWhen`, `mobileOrder`.
- When narrow, columns merge into one. Items with `mobileOrder` are placed by
  that number; items without it keep their column order (main first, then
  sidebar) and come after numbered items.
- The submit button always sits at the end of the main column on desktop and
  last on mobile. The layout cannot move it.

### Behavior

- Fields and blocks hidden by `showWhen` are not shown and their values are
  not submitted. Today's form already works this way (a General Idea does not
  send `storyType`). The schema's `if` / `then` rules must match the layout's
  `showWhen` rules; `docs/API.md` shows how with the idea form as the example.
- On submit, the renderer validates with Ajv. If invalid, errors show under
  each field, and after that they update as you type, as today.
- If valid, it sends the visible data as JSON with `submit.method` to
  `submit.url`.
- Values the person does not choose (the author, the submitted date, the new
  record's id) are set by the server, not by the form. Today `IdeaForm` sets
  them in the browser (`CURRENT_USER_ID`, `todayIso()`, `newIdeaId()`); that
  code goes away.
- If the server answers `422` with `{ "errors": { "<field>": "<message>" } }`,
  each message shows under its field. A message for a field not on the form
  shows at the top of the form.
- Any other error shows a toast, and the form keeps its values.
- On success, the renderer refreshes cached data for the resource in
  `submit.url` (a `PATCH /api/ideas/i1` refreshes the idea list and idea
  `i1`). Query keys are built from URL path segments so this matching works
  without Go knowing about the frontend's cache. Then it calls the `onDone`
  callback of whoever opened the form, which closes the panel or returns to
  Ideas.
- While the form loads, it shows a skeleton. If loading fails, it shows a
  message and a Retry button.
- Markdown is rendered with `react-markdown` with raw HTML turned off, so text
  from the server cannot inject scripts or markup.
- The New Idea type cards read their options from the schema's `type` field,
  so the list is not hard-coded. Choosing a card loads the form with
  `?type=<value>`. The form's own type field still lets you switch.
- The intake page's decision buttons (Authorize AI pre-research, Accept,
  Narrow, Park, Stop) are actions, not a form, and stay as they are.

### Form configs: one JSON file per form

Every form is defined in one JSON file in a top-level `forms/` folder. These
files are the single source for each form: the MSW mocks read them, and the Go
server reads the same files (for example with `go:embed`), so nobody copies a
form definition by hand from one language to the other.

```
forms/
  form-config.schema.json   JSON Schema for the config files themselves
  idea.json
  idea-pitch.json
```

A config holds everything about a form except the per-record parts:

```jsonc
{
  "$schema": "./form-config.schema.json",
  "id": "idea",
  "create": {
    "title": "New Idea",
    "submit": { "method": "POST", "url": "/api/ideas", "label": "Submit" }
  },
  "edit": {
    "title": "Edit Idea",
    "submit": { "method": "PATCH", "url": "/api/ideas/{id}", "label": "Save" }
  },
  "defaults": { "type": "general", "storyType": "data-story", "status": "draft" },
  "schema": { /* JSON Schema for the form's data */ },
  "layout": { /* layout object */ }
}
```

Merging a config with data from the database is the Go server's job. The
frontend never imports or reads `forms/*.json`; it only calls
`/api/forms/...` and renders whatever comes back. In development, the MSW
handler does the same merge with the dummy data, so the frontend cannot tell
the two apart.

To answer `GET /api/forms/:formId[/:recordId]`, the server (Go, or MSW in
development) turns a config into the response described above:

- It picks `create` or `edit` depending on whether a `recordId` was given.
  A form without an `edit` block has no edit mode (404 for a `recordId`).
- `data` is `defaults` plus any allowed query parameters for a new record, or
  the record's current values for an edit.
- Placeholders in braces are filled in: `{id}` is the record id, and any
  other `{name}` comes from `data` (for example
  `"x-options": "/api/projects/{projectId}/crew"`). A placeholder with no
  value is an error on the server, not something sent to the browser.
- Values that depend on the database (select options that come from a
  table, for example) can be added to the schema by the server at request
  time. The config holds the static version.

`form-config.schema.json` describes the config format, so editors such as VS
Code autocomplete and check the files while you write them (through the
`$schema` line). A test checks every file in `forms/`:

- It is valid against `form-config.schema.json`.
- Its file name is its form id: `forms/<formId>.json`, with `id` equal to
  `<formId>`. `GET /api/forms/idea` reads `forms/idea.json`.
- Its `schema` is valid JSON Schema and compiles in Ajv.
- Every field named in `layout` exists in `schema.properties`, and every
  `showWhen` refers to a field that exists.
- Every `mobileOrder` is unique within the form.
- Every `{placeholder}` is `{id}` or a field in `schema.properties`.

If the Go code ends up in a separate repository, it copies `forms/` from this
repository (or pulls it in as a git submodule). `docs/API.md` describes both
ways; which one to use is the backend developer's call.

### Code

```
src/forms/
  types.ts         TypeScript types for the response format
  SchemaForm.tsx   the renderer: loads the form, validates, submits
  layouts/         main-sidebar.tsx, single.tsx, and the fallback
  widgets/         one file per widget, built from today's shadcn Field,
                   Input, Select, Textarea, InputGroup and calendar, with the
                   FORM_* sizing classes
  blocks/          note.tsx
src/mocks/forms.ts the MSW handler for /api/forms: reads the configs in
                   forms/, fills data and placeholders from src/mocks/db.ts.
                   A working example of what Go has to do.
```

`SchemaForm` takes `formId`, an optional `recordId`, optional starting
`params`, `onDone`, and an optional `renderHeading` (the slide-in panel passes
its accessible title, as it does today).

`IdeaForm`'s hand-built fields and the intake page's `EditPitchForm` are
replaced by `SchemaForm`. The rendered result must look the same as today in
light and dark mode, wide and narrow.

## 6. Removals and ports

### Removed

- `scripts/export-static.mjs`
- `scripts/strip-generated-ids.mjs` and `scripts/strip-generated-ids.test.js`
- The `export:html` script, and `scripts/**` from Vitest's `include`
- `public/scripts/` and its three `<script>` tags in `index.html`
- `src/App.tsx`'s hash router
- `docs/TASKS-BACKEND.md` (content moves into `docs/API.md`)

### Vanilla scripts ported to React

These scripts existed so behavior would survive the static export. Each port
keeps the same CSS classes and data attributes, so no styles change.

| Script | Becomes | Keeps |
|---|---|---|
| `header-scroll.js` | `useHeaderScroll()` hook in the root layout | `header-hidden` class on `<html>`, so the sticky toolbar offset and `ScrollLogo` movement keep working |
| `mobile-menu.js` | `open` state in the root layout, passed to `Header` and `MobileMenu` | `mobile-menu-open` class on `<html>` and the staggered animation. New: the menu closes on navigation. |
| `project-summary.js` | `view`, `selectedId` and `openIds` state in `ProjectSummary` | `data-view`, `data-selected`, `data-open` attributes and `aria-expanded`, so every `group-data-*` variant keeps working |

## 7. Testing

- A shared `renderWithProviders()` test helper supplies a memory router, a
  fresh `QueryClient` per test, and MSW in Node mode. Tests use the same
  handlers and dummy data as the browser, so there is no second set of fake
  data to maintain.
- Update the existing test files for the new props and data flow. They must
  keep testing the same behavior.
- New tests:
  - Every route path renders its page; an unknown path renders Not Found.
  - Header hides on scroll down and shows on scroll up.
  - Mobile menu opens, closes, and closes on navigation.
  - Crew row selects on desktop and opens as an accordion on mobile.
  - Project crew: the assignee picker on Project Tasks lists only that
    project's crew; on Tasks, each row's picker lists its own project's
    crew; a task assigned to someone outside the crew still shows their
    name.
  - For each mutation hook: an optimistic change rolls back when the handler
    returns 500.
  - Theme: a stored value wins; with nothing stored, the OS setting applies.
  - Column layout: loads from preferences, saves after the debounce, Reset
    deletes it, unknown and new columns are handled.
  - `storage.ts`: corrupt JSON and throwing storage fall back to defaults.
  - URL search parameters round-trip into table state and back; invalid values
    fall back to defaults.
  - Drafts: written locally while typing but nothing sent to the server
    until Save; restored on open; deleted after a successful save and by
    Discard; never restored for a different user id; `clearDrafts()`
    removes every draft key and leaves the theme key alone; `hasDrafts()`
    reports whether any exist.
  - Leaving with unsaved changes: navigating, closing the panel (X, Escape,
    backdrop) and Back each show the dialog; no dialog when nothing changed
    or after a successful save.
    - Save & leave: saves, deletes the draft and continues to the
      destination; on invalid data or a 422 it cancels the navigation and
      shows field errors; on another error it stays open with an error
      line.
    - Discard: deletes the draft and continues; reopening the
      form shows the server's data with no "Draft restored" note.
    - Keep editing, the X, Escape and the backdrop: stay on the form with its
      values.
    - Buttons are disabled while saving.
  - Forms:
    - New and edit modes load and fill in correctly.
    - `showWhen` hides fields and blocks, and hidden fields are not
      submitted.
    - Ajv messages, including custom `errorMessage` text, show on submit.
    - Required fields show an asterisk and `aria-required`; optional fields
      do not; a field required through `if` / `then` gains and loses the
      asterisk as its condition changes.
    - A 422 response puts each message under its field, and a message for an
      unknown field at the top.
    - `mobileOrder` sets the order when narrow.
    - An unknown layout type falls back to one column; an unknown widget
      renders as text.
    - Markdown containing a `<script>` tag renders it as text.
    - A successful submit refreshes the matching cached data and calls
      `onDone`.
    - The New Idea type cards come from the schema.
    - Every config in `forms/` passes the checks listed under "Form
      configs".
    - The form handler picks `create` or `edit`, fills `{id}` and
      `{field}` placeholders, and returns 404 for an unknown form id or
      record.
- Before calling the work done: `pnpm typecheck`, `pnpm lint`, `pnpm test` and
  `pnpm build` all pass, and every route has been clicked through in a browser
  with mocks on.

## 8. Documentation

- `docs/API.md` (new): every endpoint with request and response JSON; a
  "Forms" section that fully describes the form response format (schema
  rules, layout types and their columns, widgets, blocks, `showWhen`, submit
  and 422 errors) using the idea form as the worked example, plus how Go
  turns a `forms/*.json` config into a response and how to use `forms/` from
  a separate Go repository; the
  enum `value` strings, ISO dates and user ids, error format, the single-user
  note, the rule that logout and 401 responses must call `clearDrafts()`, how
  to run the frontend against a local Go server, and the Go serving
  rules from section 2.
- `AGENTS.md`: rewrite the Stack, Interactivity, Static export, and Pages and
  routing sections for the new setup. Add a section on the data layer and on
  persisted state, and a section on schema-built forms (how to add a layout,
  a widget or a block).
- `README.md`: replace the Vite template text with setup steps
  (`corepack enable`, `pnpm install`, `pnpm dev`), how to turn mocks off, and
  the list of scripts.

## 9. Order of work

Each step leaves the app working and is its own commit.

1. pnpm and TypeScript 7. Toolchain only, no app code changes.
2. TanStack Router: route tree, root layout, typed links, URL search
   parameters for sort, filters, search and chip category.
3. Port the three vanilla scripts. Remove the static export and
   `public/scripts/`.
4. Data layer: `src/api/`, `src/mocks/`, `QueryClient`, controlled
   `DataTable`. One resource at a time: crew and projects, then tasks, then
   documents, then ideas and intake, then meetings and project summary.
5. Schema-built forms: `forms/` configs and their checks, `src/forms/`, the
   form endpoint in MSW, then replace the New/Edit Idea form and the Edit
   Pitch form.
6. Persisted state: storage helper, dark mode, column layout on the server
   (with the mock localStorage copy), form drafts (which need form ids from
   step 5).
7. Documentation.
