# Generic `DataTable` component (schema-driven, replacing the bespoke `TaskList` table)

Status: approved design, not yet implemented.

## Context

`src/components/TaskList.tsx` currently renders the Tasks table as a
one-off, bespoke component: a hardcoded `COLUMNS` array, hardcoded
placeholder `TASKS` data, a decorative (non-functional) sort icon next to
each header, and non-functional edit/delete buttons. There is no shared
Table/Badge/Popover/Calendar/Command primitive in the codebase yet (only
`avatar`, `button`, `progress` exist under `src/components/ui/`), and no
generic list/table abstraction to build on.

This project is a static frontend prototype: `npm run export:html` renders
everything to plain static HTML/CSS for a Go developer to implement against
a real backend (see AGENTS.md's "Static HTML export for the Go backend"
section). There is no backend today, so this work builds a fully
interactive **React prototype** — real sorting, filtering, inline editing —
backed by in-memory state only, plus a short handoff doc telling the Go
developer what needs a real endpoint.

## Scope

**In scope:**
- A generic, schema-driven `DataTable` component (`src/components/ui/data-table.tsx`)
  usable by Tasks today and other content types later (e.g. a future
  Documents table).
- Reworking `TaskList.tsx` to be a thin wrapper: task-specific schema +
  placeholder data + `<DataTable />`, still embedded in Home and Project
  Overview exactly where it is today.
- Real interactivity: column-header sort, per-column filtering, global
  search, inline editing of `select`/`date`/`user`-type columns, row
  delete, and a state-model (not yet a UI) for column show/hide + reorder.
- `docs/TASKS-BACKEND.md`: what the Go backend needs to implement for real.

**Out of scope (explicitly deferred):**
- The standalone "Tasks" page (FIGMA.md notes this as an unbuilt Figma
  frame) — follow-up task.
- The detailed UI design for the column show/hide + reorder popover — the
  state model is wired now, but the trigger button is a stub with no
  popover yet. Needs its own design pass.
- An edit form/dialog for the row-level Edit action — `onEdit` is exposed
  as a callback for the consumer to wire up later; no form exists to open
  yet.
- Persisting anything server-side — this is React state only, per the
  "static prototype for Go handoff" context above.

## Architecture

**TanStack Table (`@tanstack/react-table`) as the headless engine, shadcn/ui
primitives for rendering.** Sorting, per-column filtering, column
visibility, and column ordering are exactly what TanStack Table's headless
hooks are built for — it's also what shadcn's own official "Data Table"
recipe is built on. We wrap it in a declarative schema so consumers never
touch TanStack's raw column-def API directly; they hand `DataTable` a plain
array of column descriptors and a data array.

Rejected alternatives:
- **Hand-rolled state, no new dependency** — would mean re-implementing
  sort/filter/visibility/ordering logic (and their edge cases: stable
  multi-column sort, drag-reorder bookkeeping) from scratch for a
  component explicitly meant to be the reused foundation across content
  types. More code, more bugs, no upside.
- **Copy shadcn's Data Table block verbatim per table** — same engine, but
  skips the schema abstraction; each new content type would hand-write its
  own TanStack column defs instead of a declarative schema. Doesn't
  satisfy "feed it a schema, load data."

New dependencies: `@tanstack/react-table` (v8 supports React 19). New
shadcn primitives via `npx shadcn add table badge popover command calendar`
— the project is already configured for this (`components.json`, style
`radix-nova`, `radix-ui` v1.6.7 and `lucide-react` already installed).
Note that `calendar` transitively pulls in `react-day-picker`, and
`command` pulls in `cmdk` — two dependencies arriving indirectly, worth
knowing about rather than discovering in the lockfile.

## Schema

```ts
type ColumnType = "text" | "select" | "date" | "user" | "actions" | "custom"

interface SelectOption {
  value: string
  label: string
  colorVar?: string // e.g. "--color-accent-success", for badge tinting
}

interface DataTableColumn<T> {
  // Data-backed columns ("text" | "select" | "date" | "user") key into T.
  // "actions" and "custom" columns have no backing field, so `key` is just a
  // unique column id — hence `string`, not `keyof T & string`. Narrowing this
  // properly needs a discriminated union on `type`; see note below.
  key: string
  label: string
  type: ColumnType
  width?: string            // Tailwind width utility, e.g. "w-40"
  options?: SelectOption[]  // required for type "select"
  actions?: RowAction<T>[]  // required for type "actions"
  sortable?: boolean        // default true; false for "actions"
  filterable?: boolean      // default true; false for "actions"
  render?: (row: T) => React.ReactNode // required for "custom"; optional override otherwise
}

interface DataTableUser {
  id: string
  name: string
  avatarUrl?: string
}

// Helpers let an action mutate table state without the consumer owning it.
// Table-scoped: available to anything, including the toolbar's "New" button.
interface TableHelpers<T> {
  addRow: (row: T) => void
}

// Row actions are declared per content type, NOT hardcoded to edit/delete.
// Tasks use [edit, delete]; a deliverables table adds review; a documents
// table might use [download, preview, delete]. Row actions get the
// table-scoped helpers plus two bound to the row they act on.
//
// `addRow` is what makes a "duplicate" action possible. The consumer supplies
// the whole new row, because only they can mint an id:
//   onSelect: (row, { addRow }) => addRow({ ...row, id: newId() })
interface RowActionHelpers<T> extends TableHelpers<T> {
  removeRow: () => void
  updateRow: (patch: Partial<T>) => void
}

interface RowAction<T> {
  id: string
  label: string        // used as aria-label and tooltip
  icon: React.ReactNode
  onSelect: (row: T, helpers: RowActionHelpers<T>) => void
  variant?: "default" | "destructive"
}

interface DataTableProps<T> {
  columns: DataTableColumn<T>[]
  data: T[]
  getRowId: (row: T) => string
  users?: DataTableUser[]        // roster for "user"-type columns
  title?: string
  titleIcon?: React.ReactNode
  /**
   * Content-type-specific toolbar buttons, e.g. TaskList's "New Task".
   * Takes a render prop when the button needs to mutate the table — which
   * "New" does, otherwise it has no way to add anything.
   */
  toolbarActions?: React.ReactNode | ((helpers: TableHelpers<T>) => React.ReactNode)
  onChange?: (data: T[]) => void   // fires after any inline edit or row action mutation
}
```

A content type declares an array of `DataTableColumn` plus its data — no
TanStack knowledge required. `TaskList.tsx` becomes:

```
task schema (columns: name [text], assignee [user], due [date],
  status [select, options: To Do/In Progress/Done/Blocked],
  priority [select, options: Low/Medium/High], actions)
+ TASKS placeholder data
+ USERS placeholder roster
+ <DataTable columns={taskColumns} data={TASKS} users={USERS}
    title="Tasks" titleIcon={<TasksIcon />}
    toolbarActions={({ addRow }) => (
      <Button onClick={() => addRow(blankTask())}>New Task</Button>
    )} />

// where the actions column declares:
// actions: [
//   { id: "edit",   label: "Edit task",   icon: <PencilIcon size={20} />,
//     onSelect: (row) => { /* no edit form exists yet — see Follow-ups */ } },
//   { id: "delete", label: "Delete task", icon: <TrashIcon size={20} />,
//     variant: "destructive", onSelect: (_row, { removeRow }) => removeRow() },
// ]
```

Optional refinement during implementation: make `DataTableColumn` a
discriminated union on `type` so `options` is *required* for `"select"` and
`render` is *required* for `"custom"`, and so data-backed types can enforce
`key extends keyof T`. Worth doing if it stays readable; not worth
contorting the API for.

### Required changes to the placeholder data

The current `TASKS` array is literal wireframe filler from Figma (the
design repeats one sample row four times) — it isn't a data model anyone
committed to, so changing it costs nothing. But it does need to change
before this component can be built or demoed:

1. **Rows need a stable `id`, and the samples need to vary.** Today
   `TASKS` is the same row four times, rendered with `key={index}`. Two
   separate reasons to change it: `getRowId`/edit/delete want a real key
   (and an `id` field models what the Go developer's real data will
   have), and — more practically — **sorting and filtering are invisible
   in a demo where every row is identical**. The whole point of this
   prototype is showing the interaction, so the sample rows need
   different names, assignees, dates, statuses, and priorities.
2. **Dates must be stored machine-readable, formatted at render.** Today
   `due` is the display string `"Sept 15, 2026"`. Sorting that
   lexicographically is wrong (`"Oct 2, 2026"` sorts before
   `"Sept 15, 2026"`), and the `Calendar` picker needs a real `Date`.
   Store ISO (`"2026-09-15"`), format for display in the `date` cell
   renderer.
3. **Assignee must reference the roster, not a display handle.** Today
   `assignee` is `"@Claudicia"`. A `user`-type cell needs to resolve its
   value against `users` to render an avatar and to know which entry is
   currently selected. Store the user `id`; render name + avatar by lookup.

This is the general contract for `DataTable`: **`data` holds values, not
presentation.** Any future content type plugging into it owes the same —
ids for rows, ISO for dates, keys for lookups.

## Cell renderers per type

- **`text`** — plain cell, as today.
- **`select`** (Status, Priority) — renders a colored `Badge` using
  `option.colorVar`; click opens a shadcn `Popover` containing a `Command`
  list of `options`; selecting one updates the row inline.
- **`date`** (Due Date) — renders the formatted date; click opens a shadcn
  `Calendar` in a `Popover`.
- **`user`** (Assignee) — renders `Avatar` + name; click opens a `Popover`
  + `Command` list built from `users`.
- **`actions`** — renders one icon button per entry in the column's
  `actions` array, each wired to its own `onSelect(row, helpers)`. **Not
  hardcoded to edit/delete** — the action set is declared per content type,
  and the sets genuinely differ:
  - **Tasks**: `[edit, delete]` (the existing `PencilIcon`/`TrashIcon`).
  - **Deliverables**: include a **review** action — this is specific to
    deliverables, not a generic row action. Reuses the existing
    `RateReviewIcon`.
  - **Documents**: anticipated `[download, preview, ...]`.

  Delete is just an action whose `onSelect` calls `helpers.removeRow()` —
  nothing about it is special-cased in `DataTable`.
- **`custom`** — escape hatch: consumer supplies `render`, for whatever a
  future content type needs that doesn't fit the built-in types.

### Status / Priority color mapping

Uses the existing `--color-accent` / `--color-accent-warn` /
`--color-accent-success` tokens from `src/index.css`, referenced via
`var(--color-accent-*)` per AGENTS.md's note that these three are not
exposed as Tailwind utilities (name collision with shadcn's own `--accent`
slot).

| Value (Status)       | Token              |
|-----------------------|--------------------|
| To Do / (other)       | `bg-muted` / `text-muted-foreground` (existing Tailwind utilities, no `var()` needed) |
| In Progress            | `--color-accent` (orange) |
| Done / Complete        | `--color-accent-success` (green) |
| Blocked / At Risk      | `--color-accent-warn` (red) |

| Value (Priority) | Token |
|---|---|
| Low | `bg-muted` / `text-muted-foreground` |
| Medium | `--color-accent` (orange) |
| High | `--color-accent-warn` (red) |

## State & interactivity

`DataTable` is uncontrolled by default and owns all of this state
internally via TanStack Table: sorting state, per-column filter values,
global search text, column visibility, column order, and a working copy of
`data` for inline edits/deletes. An optional `onChange(data)` prop lets a
future wiring layer observe changes without redesigning the component.

- **Sort** — clicking a header cell sorts ascending, click again for
  descending, click again clears. The existing `SwapVerticalIcon` becomes a
  real sort-state indicator (neutral / asc / desc) instead of decorative.
- **Per-column filter** — each filterable header gets a filter affordance:
  a dropdown of `options` for `select` columns, a text input for
  `text`/`date`/`user` columns. This is a separate click target from the
  header's sort click so the two don't conflict.

  *Layout risk to resolve during implementation:* the columns are narrow
  (`w-35`/`w-40`) and the table is already `min-w-250` (1000px) with
  horizontal scroll. Inline filter inputs in every header will crowd the
  headers and push the table wider still. Prefer a **small filter icon per
  header that opens a popover**, rather than an always-visible inline
  input — same interaction, no added width. Confirm visually before
  committing to it.
- **Column show/hide + reorder** — `columnVisibility` and `columnOrder`
  state is wired via TanStack from day one. The trigger is a stub icon
  button next to search (no popover behavior yet) — its detailed UI design
  is a deferred follow-up, so the state model doesn't need reworking when
  that lands.

  Note: no existing `wlicon` export fits this trigger (the current set has
  no columns/tune icon). It needs a new Material Symbols SVG added to
  `src/assets/icons/`, an `npm run iconfont` run, and a wrapper export in
  `icons.tsx` — the pipeline documented in AGENTS.md. Use `view_column`.

### Icons to add

Source: `node_modules/@material-symbols/svg-300/outlined/<name>-fill.svg`,
normalized from `viewBox="0 -960 960 960"` to `viewBox="0 0 960 960"` with a
`<g transform="translate(0,960)">` wrapper (matching every existing icon in
`src/assets/icons/`).

- `view_column` — the column show/hide + reorder trigger (needed now).
- `download`, `preview` — not needed by Tasks, but anticipated for other
  content types' action columns (e.g. Documents). Added now because the
  marginal cost is one SVG plus one `npm run iconfont` run, and it keeps a
  future content type from having to regenerate the font.
- `rate_review` is **already** in the set (`RateReviewIcon`) — the review
  action (deliverables only) reuses it; nothing to add.
- **Global search** — the existing search icon expands into a text input
  on click; filters visible cell values across columns via TanStack's
  global filter.
- **Row edits** — `select`/`date`/`user` cell popovers call an internal
  `updateRow`, which patches `data` in state and fires `onChange`. Row
  actions receive `{ addRow, removeRow, updateRow }` — the latter two bound
  to their row — so a delete action removes its row, a "mark reviewed"
  action patches it, and a "duplicate" action adds a new one, none of it
  requiring the consumer to own table state. Tasks' Edit action is a no-op
  for now (no edit form exists — see Follow-ups); Tasks' "New Task" button
  uses the table-scoped `addRow`.

## File layout

```
src/components/ui/table.tsx          new shadcn primitive
src/components/ui/badge.tsx          new shadcn primitive
src/components/ui/popover.tsx        new shadcn primitive
src/components/ui/command.tsx        new shadcn primitive
src/components/ui/calendar.tsx       new shadcn primitive
src/components/ui/data-table.tsx     the generic DataTable (new)
src/components/TaskList.tsx          shrinks to: task schema + TASKS/USERS data + <DataTable />
docs/TASKS-BACKEND.md                handoff doc (new)
```

## Static export considerations

Per AGENTS.md's static-export section, `renderToStaticMarkup` drops all
React event handlers — sort/filter/inline-edit/delete will not function in
`static-export/` output, same as noted today for "table sort/filter."
`DataTable` is exactly the case AGENTS.md names as *needing* React (real
state, non-trivial rendering logic) rather than a vanilla script, so that
part matches existing project policy.

**But the export script does need a fix, and it will fail silently without
one.** `scripts/export-static.mjs` renders `TaskList` standalone, then
splices it into each page with an exact-string
`pageHtml.replace(rendered['task-list'], ...)` (lines ~109, 184, 218). The
Radix primitives this design adds — `Popover` and `Command` — call React's
`useId()` internally (e.g. for `aria-controls` on a popover trigger).
`useId` output depends on the component's **position in the render tree**,
so the same `TaskList` rendered standalone produces different ids than when
rendered inside `Home`. The two strings no longer match, `.replace()` finds
nothing, and the export silently emits a page with no
`<!-- BEGIN PARTIAL: components/task-list.html -->` markers — no error, no
warning, just a quietly degraded handoff artifact for the Go developer.

Fix: strip or normalize generated ids in `rewriteAssetPaths`, exactly the
way React 19's auto-injected `<link rel="preload">` hints are already
stripped there (`export-static.mjs:65`) and for the same reason — they are
runtime plumbing with no meaning in a static handoff artifact. A regex over
React's `useId` format (`:r0:`, `«r0»` depending on version) applied to both
the standalone and in-page renders keeps them byte-identical.

**Verification step, not an assumption:** run `npm run export:html` after
implementing and confirm `static-export/pages/home.html` and
`project-overview.html` still contain the task-list partial markers. This
is the one failure mode in this design that produces no error message.

## Handoff doc: `docs/TASKS-BACKEND.md`

Lists what the Go backend needs to implement for the prototype's
interactions to become real:
- Persist Status / Priority / Assignee / Due Date edits per task.
- Persist row deletion.
- (Optional) persist per-user column visibility/order preferences.
- Sort/filter/search are currently entirely client-side over
  statically-rendered data; once real data volume exists server-side,
  these likely need to move server-side (query params) rather than
  client-side array operations.
- No edit form/dialog exists yet for the row-level Edit action — flagged
  as a UI still to be designed, not just a backend gap.

## Verification

**Decided: add vitest + @testing-library/react.** The project has no test
infrastructure today (no vitest, no testing-library, no `test` script), so
standing it up is the first task, not a free extra.

What the tests are *for* here is worth being explicit about, because this
codebase ships static HTML/CSS for a Go developer to reimplement against a
real backend — the React app is not the production artifact. So tests
target two things:

1. **The interaction contract.** The component tests double as executable
   documentation of how sort, filter, inline edit, and row actions are
   meant to behave — the behaviour the Go developer is reimplementing. This
   is more durable than prose in `TASKS-BACKEND.md`.
2. **The static export, which *is* the deliverable.** Specifically the
   silent partial-marker failure described above.

Deliberately *not* tested: exact Tailwind class strings or visual styling.
Those are verified by eye in the browser; asserting on them produces brittle
tests that break on every design tweak.

Alongside the test suite:

- `npm run build` (`tsc -b && vite build`) — type correctness of the
  generic schema, which is where most of this design's risk lives.
- `npm run lint` (oxlint).
- `npm run export:html` — then confirm the task-list partial markers are
  still present in both exported pages (see the static-export section
  above; this is the silent failure mode).
- Manual, in the dev server: sort each column type (confirm the date
  column sorts chronologically, not lexically); filter per column; global
  search; open and commit an edit in each of the select/date/user cells;
  delete a row. Check all of it in **both light and dark mode** — the
  badge color tokens and the new Popover/Command/Calendar primitives are
  the most likely place for a dark-mode miss.
- Confirm `TaskList` still renders correctly embedded in both Home and
  Project Overview at multiple viewport widths, including the
  `min-w-0`/`overflow-x-auto` horizontal-scroll behavior from AGENTS.md's
  Tasks-table grid gotcha.

## Follow-ups (not part of this spec)

- Standalone "Tasks" page (Figma frame not yet pulled into code).
- Column show/hide + reorder popover UI design.
- Row-level Edit form/dialog.
