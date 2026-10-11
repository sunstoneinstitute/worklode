# AGENTS.md

Context for whoever (human or agent) picks this project up next. The app is
a React single-page app that the Go backend serves: one `index.html`, with
the router picking the page from the URL. See `README.md` to build and run
it, `docs/API.md` for the API contract and how Go serves `dist/`, and
`FIGMA.md` for how pages are imported from Figma.

The design for the SPA is
`docs/superpowers/specs/2026-09-24-react-spa-conversion-design.md`, split
into plans under `docs/superpowers/plans/`. Plans 1 (toolchain, routing),
1b (project summary card) and 2a (Home's data from the API) are done.

## Stack

- pnpm 12 (`packageManager` in `package.json`), TypeScript 7 (type
  checking only, `tsc -b`), Vite 8, Vitest + jsdom, Oxlint.
- React 19, TanStack Router (code-based route tree in `src/router.tsx`),
  TanStack Query for server data, MSW for the mock API.
- Tailwind CSS v4 (CSS-based `@theme`, no `tailwind.config.js`), shadcn/ui
  (`radix` base). Font is DM Sans Variable
  (`@fontsource-variable/dm-sans`), matching the Figma file.

## Design tokens (`src/index.css`)

Tailwind v4 config lives entirely in CSS (`@theme inline` + `:root`/`.dark`),
no JS config file. Two rules to keep following:

- **Figma Variables are named 1:1 as CSS custom properties**
  (`--color-text-primary`, `--color-surface`, `--color-highlight`, etc.),
  kept traceable back to the Figma file by name — see `FIGMA.md`. shadcn's
  generic slots (`--background`, `--primary`, `--border`, ...) just point at
  these — don't bypass this and hardcode a shadcn slot directly.
- **No arbitrary pixel values in component `className`s.** Tailwind v4's
  numeric spacing utilities (`gap-13.5`, `w-70`, `min-w-250`, ...) are
  computed dynamically as `calc(var(--spacing) * N)` for *any* N, so
  anything that divides cleanly by the base 4px unit should use that
  instead of `gap-[54px]`-style brackets. For values that don't fit the
  numeric scale (font sizes, corner radii), we added named `@theme` tokens
  instead: `--text-13/15/22` (sizes outside Tailwind's default type scale),
  `--radius-card` / `--radius-panel` (15px / 25px, recurring Figma corner
  radii), `--spacing-page` / `--spacing-panel` (70px / 30px, recurring
  layout constants — the desktop page margin and card/panel padding). Add
  new named tokens the same way rather than reaching for `text-[Npx]` or
  similar.
- A `pl-[12.6%]` and a `w-[calc(100%+40px)]` remain as genuine exceptions —
  the first is a proportional value (not a fixed design-token scale value),
  the second is a computed relationship (2× the mobile page margin for a
  negative-margin bleed trick), not a magic number.

### Dark mode

Real, not a placeholder — both `:root` and `.dark` in `src/index.css` are
filled in from the actual "Worklode Theme" Figma variables table (the user
exported it and shared it directly; FIGMA.md has the story of why this
couldn't be pulled via the Figma API for this file). Don't assume dark mode
is just "invert everything" if you need to add a new token — some things
are intentionally asymmetric:

- `text-light` / `text-dark` are **theme-invariant** — always a light/dark
  value respectively, regardless of overall mode (e.g. white text on the
  always-blue `ProjectSummary` card looks the same whether the app is in
  light or dark mode). Both resolve to the same hex in `:root` and `.dark`.
- `divider-line` flips from a dark navy (light mode) to a *light* gray
  (`#dde1e8`, dark mode) — not a darker shade. This matters because
  `--border`/`--muted` derive from it via `color-mix()`; a light
  divider-line is what actually reads as a subtle line/tint against a dark
  background, and those derived formulas needed no changes to work
  correctly in dark mode as a result.
- `accent` / `accent-warn` / `accent-success` / `button-bg` are identical in
  both modes — brand/status colors, not surface or text colors that need to
  invert for contrast.
- `gradient-start` in dark mode is its own literal value (`#133284`), not
  "= highlight" the way it is in light mode.
- `accent`, `accent-warn`, and `accent-success` are defined as plain CSS
  variables (not exposed via `@theme inline` as Tailwind utilities) because
  Figma's "accent" name collides with shadcn's own `--accent` slot (already
  mapped to `--color-highlight`, used for hover/emphasis states) — exposing
  both under the same utility name would silently let one override the
  other. Reference them via `var(--color-accent-warn)` etc. (see
  `--destructive`, which does exactly this) until something needs a real
  `bg-accent-warn`-style utility, at which point give it a non-colliding
  name.


The dark-mode toggle (`src/hooks/use-dark-mode.ts`) is not persisted yet.
The spec keeps it in localStorage.

## Icons

Figma uses Material Symbols, weight 300, filled (see `FIGMA.md`). The app
uses a subsetted icon font built with `fantasticon` from individual SVGs,
instead of the full `material-symbols` package (~4MB).

- Source SVGs: `src/assets/icons/*.svg`, from `@material-symbols/svg-300`.
  Google's export uses `viewBox="0 -960 960 960"`. Normalize new ones to
  `viewBox="0 0 960 960"` with a `<g transform="translate(0,960)">`
  wrapper, since some tooling mishandles negative-origin viewBoxes.
- Config: `.fantasticonrc.json`. `pnpm iconfont` writes
  `public/assets/iconfont/wlicon.{css,ttf,woff,woff2}`, imported at the top
  of `src/index.css` (it must stay first, see the comment there).
- Usage: `src/components/icons.tsx` exports named components
  (`ScheduleIcon`, `ArrowRightIcon`, ...) that render
  `<i className="wlicon-{name}">`. To add an icon: drop the SVG in
  `src/assets/icons/`, run `pnpm iconfont`, add a wrapper export.
- `svgtofont` (used by sunstone-cms) was tried first and looked broken, but
  that was a test-harness problem, not the tool. It would likely work too.

## Layout conventions

Page-specific layout decisions (the header/sidebar margin exceptions, the
mobile bleed-scroll pattern) are documented in `FIGMA.md` alongside the
Figma frames they come from. General technical patterns worth reusing on
any future page:

- The mobile horizontal-bleed-scroll trick: negative margin cancels the
  parent's padding so a row can be dragged edge-to-edge, matching positive
  padding re-adds it as scroll padding so items still start inset. See
  `ProjectQuickLinks.tsx`.
- Watch for the CSS Grid "1fr won't shrink below content" bug: a plain `1fr`
  track (or a flex child) won't shrink below its content's intrinsic width
  unless it has `min-w-0`. We hit this with the Tasks table (`min-w-[1000px]`)
  forcing the whole page to overflow horizontally at non-1728px widths — fix
  is `min-w-0` on the relevant flex/grid children, or use Tailwind's numeric
  `grid-cols-N` utilities which already wrap tracks in `minmax(0, 1fr)`.


## Routing

`src/router.tsx` declares every route in code. URLs are real paths
(browser history), and `<Link to="...">` is typed, so a link to a missing
route fails the type check.

| Path | Page |
|---|---|
| `/` | `Home` |
| `/tasks` | `Tasks` (every task across projects, `<TaskList showProject />`) |
| `/ideas`, `/ideas/new`, `/ideas/$ideaId/edit`, `/ideas/$ideaId/intake` | `Ideas`, `NewIdea`, `EditIdea`, `IdeaIntake` |
| `/projects` | `Projects` |
| `/projects/$projectId` | `ProjectOverview` |
| `/projects/$projectId/tasks` | `ProjectTasks` |
| `/projects/$projectId/documents` | `ProjectDocuments` (one category at a time via a `ChipNav`) |
| anything else | `NotFound` |

Layouts:

- `src/layouts/RootLayout.tsx` renders what every page shares: `Header`,
  `MobileMenu`, `Toaster`, and on project pages `ScrollLogo`. The root
  route's loader fetches `/api/me` before any page renders. `RootError`
  shows a Retry page if that fails.
- `src/layouts/ProjectLayout.tsx` puts `ProjectToolbar` above every
  project page.

Table pages use `validateSearch: coerceSearch` so their sort, filters,
search and chip category live in the URL (`src/hooks/use-table-url-state.ts`).

## Data layer (`src/api/`, `src/mocks/`)

Every read and write goes through `src/api/`:

- `client.ts`: `apiFetch`, a fetch wrapper that throws `ApiError` on
  non-2xx.
- `queries.ts`: read hooks (`useMe`, `useCrew`, `useProjects`, `useTasks`,
  `useMeetings`). Query keys follow the URL path, so invalidating
  `["tasks"]` refreshes every task list.
- `mutations.ts`: task writes. Edits and deletes are optimistic and roll
  back on error.
- `types.ts`: the data shapes.

MSW answers `/api/*` with dummy data in `pnpm dev` (`src/mocks/browser.ts`)
and in tests (`src/mocks/node.ts`). `src/mocks/handlers.ts` is the reference
implementation of the contract in `docs/API.md`, and `src/mocks/db.ts`
holds the data. `VITE_API_MOCKS=off` turns MSW off and proxies `/api` to
the Go server (see `.env.example`).

Only Home's data is on the API so far: `/api/me`, `/api/crew`,
`/api/projects`, `/api/tasks`, `/api/meetings`. `DocumentList`, `IdeaList`,
`ProjectList`, `ProjectSummary`, `IdeaIntake` and `src/lib/crew.ts` /
`src/lib/ideas.ts` still hold local dummy data, and `RootLayout` passes a
hard-coded `projectName="The Grid"`. Moving them is the remaining plans'
work. When you add an endpoint, add its MSW handler and document it in
`docs/API.md` in the same change.

## DataTable: generic, schema-driven table component

`src/components/ui/data-table/` is a reusable table. Columns, sorting,
filtering, search and row actions come from a schema, not per-page code.
A consumer defines a `DataTableColumn[]` (field key, label, type:
`text`/`select`/`date`/`user`/`users`/`actions`/`custom`, plus `options`
for `select`) and renders `<DataTable columns={...} data={...} />`. See
`data-table/types.ts` for the full props.

- **Rows are controlled or uncontrolled.** Pass `onUpdateRow` /
  `onRemoveRow` / `onAddRow` and the table shows `data` as given and
  reports changes, which the page sends through a mutation (`TaskList`
  does this). Leave them out and the table edits its own copy (the tables
  not yet on the API).
- **Row actions are per content type.** Each `RowAction` is declared by the
  consumer and gets `addRow` / `removeRow` / `updateRow` helpers. A
  "duplicate" action is `addRow` with a copy of the row. In uncontrolled
  mode, call only one helper per action (see the comment in `types.ts`).
- **View state can be page-owned.** `viewState` / `onViewStateChange` let
  the page own sort, filters and search, which is how they reach the URL.
- **Column settings.** The toolbar's "Configure columns" button shows column
  pills (`column-settings.tsx`): drag to reorder (`@dnd-kit`, pointer,
  touch and keyboard), click the eye to hide. The actions column always
  stays last and visible. The layout is not saved yet; the spec saves it
  per user through `/api/me/preferences`.

## Page chrome: header, mobile menu, ScrollLogo

These toggle a class on `<html>`, and `src/index.css` does the rest, so
any element can react with a `:root.<class>` selector:

- `useHeaderScroll` (`src/hooks/use-header-scroll.ts`) sets `header-hidden`
  on scroll down and clears it on scroll up and on navigation.
- `useMobileMenu` (`src/hooks/use-mobile-menu.ts`) sets `mobile-menu-open`.
  The panel's roll-down, the staggered item fade-in and the icon swap are
  CSS keyed off it. It closes on Escape and on navigation.

Project pages add two elements that follow `header-hidden`:

- `ProjectToolbar` is sticky at `top: var(--header-height)`, and at
  `top: 0` once the header hides. Keep `--header-height` in sync with the
  header's `h-13` / `lg:h-19`.
- `ScrollLogo` is one `position: fixed` logo mark that moves between the
  header and the toolbar with a CSS transition. The user rejected two
  cross-fading logos: it must read as one object moving. `Header` hides its
  own mark (`hideLogoMark`) wherever `ScrollLogo` shows. The toolbar-state
  position is derived from the breadcrumb's `TEXT_INSET` in
  `ProjectToolbar.tsx`, so keep the two in sync.

`ProjectSummary` switches between the summary and "What is everyone
working on?" with React state, rendered as `data-view` / `data-selected` /
`data-open` attributes that Tailwind variants style.

**Sticky gotcha:** a `position: sticky` element needs its DOM parent to be
taller than itself. Wrapping it in a container sized to fit it (e.g. a
`<div className="hidden lg:block">`) leaves it nowhere to stick. Put
responsive visibility classes on the sticky element itself.

## Tests

`pnpm test` runs Vitest with jsdom. `src/test/setup.ts` starts MSW with
`onUnhandledRequest: "error"` and resets `db.ts` after each test.
`src/test/render.tsx` has `renderApp(path)` (the real router at a URL) and
`renderWithRouter(ui)` (one component in a throwaway router).

## Known gaps / follow-ups

- See `FIGMA.md` for Figma-side gaps (unbuilt pages, placeholder
  decorative icons on Home and Project Overview).
- `ProjectToolbar`'s nav is icon-only from `lg` (1024px) and shows labels
  from `xl` (1280px), because the labeled nav needs ~896px.
- The breadcrumb ("Projects / Project Name") aligns with the header's
  "Worklode" wordmark (73px, `TEXT_INSET`), not the 70px page margin. The
  user asked for this.
- The toolbar's light "tab" is a `bg-background` rectangle that stops at
  `right-64`, plus `FolderCurve` (a `w-64` inline SVG filled with
  `fill-background`, so it follows dark mode). The curve is a transparent
  cutout showing the gradient behind it. Extending the rectangle under the
  curve fills the cutout and the curve disappears.
