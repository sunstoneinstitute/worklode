# SPA conversion, plan 1 of 4: toolchain, routing, script ports

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the prototype to pnpm and TypeScript 7, replace the hand-rolled hash router with TanStack Router (real URLs, shared layouts, sort/filter/search in the URL), port two of the three vanilla scripts to React (the project summary card's moved to plan 1b), and remove the static HTML export.

**Architecture:** One code-based TanStack Router route tree in `src/router.tsx`. A root layout renders the header and mobile menu once for every page; a project layout adds the project toolbar under `/projects/$projectId`. The vanilla scripts in `public/scripts/` become React state and hooks that set the same CSS classes and data attributes, so `src/index.css` does not change. DataTable gains optional controlled sort/filter/search state, which full-page tables keep in the URL.

**Tech Stack:** pnpm 12.6.0, TypeScript 7.0.x (type checking only), Vite 8, Vitest 5, React 19, TanStack Router 1.170.x, TanStack Table 8, Testing Library.

**Spec:** `docs/superpowers/specs/2026-09-24-react-spa-conversion-design.md`. This plan covers spec sections 1 (Tooling), 2 (Routing), 4's "Sort, filters, search, chip category (URL)" subsection, and 6 (Removals and ports). Plans 2–4 cover the data layer, forms, persisted state and docs; they are written after this one lands.

**The series:**
1. This plan: toolchain, routing, script ports.
   1b. The project summary card port (`2026-09-24-spa-1b-project-summary.md`), split off to save time.
2. Data layer: `src/api/`, MSW, TanStack Query, controlled DataTable rows, project crew.
3. Schema-built forms: `forms/*.json`, `SchemaForm`, the unsaved-changes dialog.
4. Persisted state and docs: theme, column layout on the server, drafts, `docs/API.md`, `AGENTS.md`, `README.md`.

## Global Constraints

- Package manager is pnpm, pinned as `"packageManager": "pnpm@12.6.0"`. Never run `npm install` again in this repo.
- TypeScript is `typescript@^7.0.2`, type checking only (`noEmit` stays on).
- Router is `@tanstack/react-router@^1.170.39`, code-based route tree, browser history. No `#/` URLs anywhere.
- Every existing screen must look the same: do not change Tailwind classes, `src/index.css`, or the data attributes CSS keys off (`data-header`, `data-secondary-toolbar`, `data-scroll-logo`, `data-mobile-menu`, `data-mobile-menu-panel`, `data-mobile-menu-item`, `data-menu-icon-open`, `data-menu-icon-close`, `data-project-summary`, `data-summary-view`, `data-view`, `data-crew-row`, `data-crew-panel`, `data-selected`, `data-open`).
- The classes set on `<html>` keep their names: `header-hidden`, `mobile-menu-open`.
- No arbitrary pixel values in `className`s (AGENTS.md "Design tokens"); use the numeric spacing scale or named tokens.
- Code style: ES modules, destructured imports, match surrounding comment density.
- Every commit message ends with a blank line and `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
- Work happens on branch `react-spa-conversion`.
- Project names stay hard-coded ("The Grid") in this plan. Plan 2 loads them from the API; an unknown `$projectId` shows The Grid's chrome until then.
- `AGENTS.md` and `README.md` are rewritten in plan 4; do not edit them here.

## Review Focus

The inputs most likely to bite a real person that the spec implies but no feature test would naturally exercise. Each has its test in the named task.

1. **A URL typed or pasted by hand with a numeric or boolean value** (`/tasks?q=123`, `?q=true`): TanStack's default search parser turns these into a number or boolean. They must still reach the table as the string `"123"`, not be dropped or crash. Test in Task 8 (`viewStateFromSearch` coerces) and Task 9 (`/tasks?q=123` opens search showing 123).
2. **Typing in a table's search box**: each keystroke rewrites the URL. It must not add one Back step per keystroke, and must not drop characters while the router catches up. Test in Task 9.
3. **A filter param for a column the current view doesn't have** (switching Documents from Meeting Notes, which has a Participants column, to Spec, which doesn't): ignored, no crash, rows still show. Test in Task 8 and Task 9.
4. **Navigating while the header is hidden**: the new page must start with the header showing, even if the scroll position doesn't change. Test in Task 5.
5. **Refreshing on a deep URL** (`/ideas/i1/edit`, `/projects/the-grid/documents`): the dev server and `vite preview` must serve the app, not a 404. Checked with `curl` in Task 10.

---

### Task 1: pnpm and TypeScript 7

Toolchain only. No app code changes. This was dry-run on a copy of the repo on 2026-09-24: all 17 test files (178 tests), the type check, lint and build pass under pnpm 12.6.0 and TypeScript 7.0.2 with no config changes.

**Files:**
- Modify: `package.json`
- Create: `pnpm-lock.yaml` (generated), `pnpm-workspace.yaml`
- Delete: `package-lock.json`

**Interfaces:**
- Produces: the `pnpm typecheck` script (`tsc -b`), used by every later task.

- [ ] **Step 1: Pin pnpm and enable it through corepack**

Add this line to `package.json`, right after `"type": "module",`:

```json
  "packageManager": "pnpm@12.6.0",
```

Run:

```bash
corepack enable pnpm
pnpm -v
```

Expected: `12.6.0`. (Node here is v22 from nvm, so `corepack enable` needs no sudo.)

- [ ] **Step 2: Convert the npm lockfile and remove npm's install**

```bash
pnpm import
git rm -q package-lock.json
rm -rf node_modules
```

Expected: `pnpm import` prints `Done in …s using pnpm v12.6.0` and creates `pnpm-lock.yaml`.

- [ ] **Step 3: Allow the one install script that needs to run**

pnpm 12 refuses to run dependencies' install scripts unless they are listed. Two packages have one: `ttf2woff2` (builds the WOFF2 converter `fantasticon` uses for `pnpm iconfont`) and `es5-ext` (only prints a message; not needed).

Create `pnpm-workspace.yaml` (overwrite it if `pnpm import` created one):

```yaml
# Dependencies allowed to run install scripts (pnpm blocks all others).
allowBuilds:
  # Only prints a message on install
  es5-ext: false
  # Builds the WOFF2 converter used by `pnpm iconfont`
  ttf2woff2: true
```

- [ ] **Step 4: Install**

```bash
pnpm install
```

Expected: ends with `.../node_modules/ttf2woff2 install: Done` and `Done in …s using pnpm v12.6.0`. No `ERR_PNPM_IGNORED_BUILDS`.

- [ ] **Step 5: Upgrade TypeScript and add the typecheck script**

```bash
pnpm add -D typescript@^7.0.2
pnpm exec tsc -v
```

Expected: `Version 7.0.2` (or a later 7.0.x).

In `package.json` `scripts`, add after `"build"`:

```json
    "typecheck": "tsc -b",
```

- [ ] **Step 6: Verify everything still passes**

```bash
pnpm typecheck && pnpm test && pnpm lint && pnpm build
```

Expected: typecheck prints nothing and exits 0; `Test Files  17 passed (17)`, `Tests  178 passed (178)`; lint shows the two existing `only-export-components` warnings in `cells.tsx` and no errors; build ends with `✓ built in …ms`.

- [ ] **Step 7: Commit**

```bash
git add package.json pnpm-lock.yaml pnpm-workspace.yaml
git commit -m "Switch to pnpm 12 and TypeScript 7

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Remove the static HTML export

The export rendered components with `renderToStaticMarkup` for Go templates. Go now serves the React app, and the export would break as soon as components use router `Link`s (they need a router around them). Remove it before the router arrives.

**Files:**
- Delete: `scripts/export-static.mjs`, `scripts/strip-generated-ids.mjs`, `scripts/strip-generated-ids.test.js` (the whole `scripts/` folder)
- Modify: `package.json` (remove `export:html`), `vite.config.ts:19-24` (test include), `.gitignore` (remove `static-export`)

**Interfaces:** none.

- [ ] **Step 1: Delete the export code and its local output**

```bash
git rm -q -r scripts
rm -rf static-export
```

- [ ] **Step 2: Remove the script and the test glob**

In `package.json` `scripts`, delete the line:

```json
    "export:html": "vite build && node scripts/export-static.mjs"
```

(and the trailing comma on the line before it, `"iconfont": …`).

In `vite.config.ts`, change the test `include` to:

```ts
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
```

In `.gitignore`, delete the line `static-export`.

- [ ] **Step 3: Verify**

```bash
pnpm typecheck && pnpm test && pnpm build
```

Expected: `Test Files  16 passed (16)` (the strip-generated-ids test file is gone); build succeeds.

- [ ] **Step 4: Commit**

```bash
git add -A package.json vite.config.ts .gitignore scripts
git commit -m "Remove the static HTML export

Go serves the React app now, so the renderToStaticMarkup export for Go
templates is no longer needed.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: TanStack Router with typed links

Replace `App.tsx`'s hash router with TanStack Router and every `#/…` link with a typed `Link` (or `useNavigate`). Router and links change together: in browser-history mode, a leftover `#/ideas` link would only change the hash and go nowhere.

Pages still render their own Header and MobileMenu in this task; Task 4 moves them into layouts.

**Files:**
- Create: `src/router.tsx`, `src/pages/NotFound.tsx`, `src/test/render.tsx`, `src/routes.test.tsx`
- Delete: `src/App.tsx`
- Modify: `src/main.tsx`, `src/test/setup.ts`, `src/lib/project-nav.ts`, `src/components/Header.tsx`, `src/components/MobileMenu.tsx`, `src/components/ProjectToolbar.tsx`, `src/components/ProjectQuickLinks.tsx`, `src/components/ProjectList.tsx`, `src/components/IdeaList.tsx`, `src/components/IdeaForm.tsx` (comment only), `src/lib/ideas.ts` (comment only), `src/pages/NewIdea.tsx`, `src/pages/EditIdea.tsx`, `src/pages/IdeaIntake.tsx`, `src/pages/ProjectOverview.tsx`, `src/pages/ProjectTasks.tsx`, `src/pages/ProjectDocuments.tsx`
- Test: `src/routes.test.tsx`, `src/components/IdeaList.test.tsx`, `src/components/ProjectList.test.tsx`, `src/pages/EditIdea.test.tsx`, `src/pages/IdeaIntake.test.tsx`

**Interfaces:**
- Produces:
  - `createAppRouter(history?: RouterHistory)` in `src/router.tsx`: builds the app's router. `main.tsx` calls it with no argument (browser history); tests pass `createMemoryHistory(...)`.
  - `routeTree` in `src/router.tsx`.
  - Route ids later tasks use: `"/projects/$projectId"` (the project layout route; its params are `{ projectId: string }`), `"/ideas/$ideaId/edit"`, `"/ideas/$ideaId/intake"`.
  - `renderWithRouter(ui: ReactElement, options?: { path?: string }): Promise<RenderResult & { router }>` in `src/test/render.tsx`: renders one component inside a throwaway router at `path`; any navigation lands on the same component, so tests assert on `router.state.location`.
  - `renderApp(path: string): Promise<RenderResult & { router }>` in `src/test/render.tsx`: renders the real app at `path`.
  - `PROJECT_NAV_ITEMS` in `src/lib/project-nav.ts`: `readonly { label, Icon, to: "/projects/$projectId" | "/projects/$projectId/tasks" | "/projects/$projectId/documents" | null }[]`; `PROJECT_NAV_SETTINGS`: `{ label, Icon, to: null }`. `to: null` means "no page yet": render it as a non-link.
  - `ProjectToolbar` props: `{ projectId: string; projectName?: string }`. `MobileMenu` props: `{ variant?: "default" | "project"; projectName?: string; projectId?: string }`.

- [ ] **Step 1: Install the router**

```bash
pnpm add @tanstack/react-router@^1.170.39
```

- [ ] **Step 2: Stub `window.scrollTo` in the test setup**

The router's scroll restoration calls `window.scrollTo`, which jsdom only implements as a "not implemented" console error. Add to the end of `src/test/setup.ts`:

```ts
// jsdom's scrollTo only logs "not implemented"; the router's scroll
// restoration calls it on every navigation
window.scrollTo = (() => {}) as typeof window.scrollTo
```

- [ ] **Step 3: Write the test helpers**

Create `src/test/render.tsx`:

```tsx
import type { ReactElement } from "react"
import { act, render } from "@testing-library/react"
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router"
import { createAppRouter } from "@/router"

/**
 * Renders one component inside a throwaway router, at `path`. Every path
 * renders the same component, so a test can click something that
 * navigates and then check `router.state.location`.
 */
export async function renderWithRouter(ui: ReactElement, { path = "/" }: { path?: string } = {}) {
  const rootRoute = createRootRoute({ component: Outlet })
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => ui })
  const splatRoute = createRoute({ getParentRoute: () => rootRoute, path: "$", component: () => ui })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, splatRoute]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  const result = render(<RouterProvider router={router} />)
  await act(async () => {})
  return { ...result, router }
}

/** Renders the real app, with its real routes and layouts, at `path`. */
export async function renderApp(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }))
  await router.load()
  const result = render(<RouterProvider router={router} />)
  await act(async () => {})
  return { ...result, router }
}
```

- [ ] **Step 4: Write the failing route test**

Create `src/routes.test.tsx`:

```tsx
import { screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { renderApp } from "@/test/render"

describe("routes", () => {
  it.each([
    ["/", "Welcome, @Claudicia!"],
    ["/tasks", "Tasks"],
    ["/ideas", "Ideas"],
    ["/ideas/new", "New Idea"],
    ["/ideas/i2/edit", "Edit Idea"],
    ["/ideas/i1/intake", "Where the glaciers went"],
    ["/projects", "Projects"],
    ["/projects/the-grid/tasks", "Tasks"],
    ["/projects/the-grid/documents", "Documents"],
    ["/no-such-page", "Page not found"],
  ])("%s renders its page", async (path, heading) => {
    await renderApp(path)
    expect(screen.getAllByRole("heading", { name: heading }).length).toBeGreaterThan(0)
  })

  it("/projects/the-grid renders Project Overview", async () => {
    await renderApp("/projects/the-grid")
    expect(screen.getByText("Project Summary")).toBeInTheDocument()
  })

  it("the all-projects Tasks page has a Project column; a project's Tasks page doesn't", async () => {
    const { unmount } = await renderApp("/tasks")
    expect(screen.getByRole("columnheader", { name: /^Project/ })).toBeInTheDocument()
    unmount()

    await renderApp("/projects/the-grid/tasks")
    expect(screen.queryByRole("columnheader", { name: /^Project/ })).not.toBeInTheDocument()
  })

  it("links to a project's pages with its id", async () => {
    await renderApp("/projects/melting-glaciers/tasks")
    // The toolbar and the mobile menu's folder card each have one
    const links = screen.getAllByRole("link", { name: "Documents" })
    expect(links.length).toBeGreaterThan(0)
    for (const link of links) {
      expect(link).toHaveAttribute("href", "/projects/melting-glaciers/documents")
    }
  })
})
```

- [ ] **Step 5: Run it to verify it fails**

Run: `pnpm test src/routes.test.tsx`
Expected: FAIL, `Failed to resolve import "@/router"`.

- [ ] **Step 6: Create the Not Found page**

Create `src/pages/NotFound.tsx`:

```tsx
import { Link } from "@tanstack/react-router"
import { Header } from "@/components/Header"
import { MobileMenu } from "@/components/MobileMenu"

// Any path the route tree doesn't know
export function NotFound() {
  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <Header />
      <MobileMenu />
      <main className="flex flex-col gap-4 p-5 pt-8 text-text-primary lg:p-page">
        <h1 className="text-2xl md:text-3xl">Page not found</h1>
        <Link to="/" className="self-start text-sm underline underline-offset-2 hover:text-highlight">
          Back to Home
        </Link>
      </main>
    </div>
  )
}
```

- [ ] **Step 7: Create the route tree**

Create `src/router.tsx`:

```tsx
import {
  Outlet,
  type RouterHistory,
  createRootRoute,
  createRoute,
  createRouter,
  useParams,
} from "@tanstack/react-router"
import { EditIdea } from "@/pages/EditIdea"
import { Home } from "@/pages/Home"
import { IdeaIntake } from "@/pages/IdeaIntake"
import { Ideas } from "@/pages/Ideas"
import { NewIdea } from "@/pages/NewIdea"
import { NotFound } from "@/pages/NotFound"
import { ProjectDocuments } from "@/pages/ProjectDocuments"
import { ProjectOverview } from "@/pages/ProjectOverview"
import { ProjectTasks } from "@/pages/ProjectTasks"
import { Projects } from "@/pages/Projects"
import { Tasks } from "@/pages/Tasks"

// Route components that read path params and hand them to the page
function EditIdeaRoute() {
  const { ideaId } = useParams({ from: "/ideas/$ideaId/edit" })
  return <EditIdea ideaId={ideaId} />
}

function IdeaIntakeRoute() {
  const { ideaId } = useParams({ from: "/ideas/$ideaId/intake" })
  return <IdeaIntake ideaId={ideaId} />
}

// Project pages read the id from their parent, the project route
function ProjectOverviewRoute() {
  const { projectId } = useParams({ from: "/projects/$projectId" })
  return <ProjectOverview projectId={projectId} />
}

function ProjectTasksRoute() {
  const { projectId } = useParams({ from: "/projects/$projectId" })
  return <ProjectTasks projectId={projectId} />
}

function ProjectDocumentsRoute() {
  const { projectId } = useParams({ from: "/projects/$projectId" })
  return <ProjectDocuments projectId={projectId} />
}

const rootRoute = createRootRoute({ component: Outlet, notFoundComponent: NotFound })

const homeRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: Home })
const tasksRoute = createRoute({ getParentRoute: () => rootRoute, path: "tasks", component: Tasks })
const ideasRoute = createRoute({ getParentRoute: () => rootRoute, path: "ideas", component: Ideas })
const newIdeaRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "ideas/new",
  component: NewIdea,
})
const editIdeaRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "ideas/$ideaId/edit",
  component: EditIdeaRoute,
})
const ideaIntakeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "ideas/$ideaId/intake",
  component: IdeaIntakeRoute,
})
const projectsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "projects",
  component: Projects,
})

// Parent of a project's pages. Just an Outlet for now; the project layout
// (toolbar and all) arrives with the shared layouts.
const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "projects/$projectId",
  component: Outlet,
})
const projectOverviewRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: "/",
  component: ProjectOverviewRoute,
})
const projectTasksRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: "tasks",
  component: ProjectTasksRoute,
})
const projectDocumentsRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: "documents",
  component: ProjectDocumentsRoute,
})

export const routeTree = rootRoute.addChildren([
  homeRoute,
  tasksRoute,
  ideasRoute,
  newIdeaRoute,
  editIdeaRoute,
  ideaIntakeRoute,
  projectsRoute,
  projectRoute.addChildren([projectOverviewRoute, projectTasksRoute, projectDocumentsRoute]),
])

/**
 * The app's router. Browser history by default; tests pass a memory
 * history. Scroll restoration starts each new page at the top and puts
 * Back/Forward where you were.
 */
export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history, scrollRestoration: true })
}

// Makes every Link, useNavigate and useParams type-check against these routes
declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
```

- [ ] **Step 8: Mount the router and delete the hash router**

Replace `src/main.tsx` with:

```tsx
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { RouterProvider } from "@tanstack/react-router"
import "./index.css"
import { createAppRouter } from "./router"

const router = createAppRouter()

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
)
```

```bash
git rm -q src/App.tsx
```

- [ ] **Step 9: Give project nav items route targets**

Replace the `PROJECT_NAV_ITEMS` / `PROJECT_NAV_SETTINGS` block in `src/lib/project-nav.ts` (keep the imports and the top comment, but change its last line from `"#" hrefs are pages that don't exist yet.` to `` `to: null` marks a page that doesn't exist yet: shown, but not a link. ``):

```ts
export const PROJECT_NAV_ITEMS = [
  { label: "Overview", Icon: DashboardIcon, to: "/projects/$projectId" },
  { label: "Tasks", Icon: TasksIcon, to: "/projects/$projectId/tasks" },
  { label: "Documents", Icon: DescriptionIcon, to: "/projects/$projectId/documents" },
  { label: "Activity", Icon: HistoryIcon, to: null },
] as const

export const PROJECT_NAV_SETTINGS = { label: "Settings", Icon: SettingsIcon, to: null } as const
```

- [ ] **Step 10: Header links**

In `src/components/Header.tsx`, add `import { Link } from "@tanstack/react-router"` and change `NAV_ITEMS` to:

```tsx
const NAV_ITEMS = [
  { label: "Ideas", to: "/ideas" },
  { label: "Projects", to: "/projects" },
  { label: "Tasks", to: "/tasks" },
] as const
```

Change the logo `<a href="/" className="flex items-center gap-2.5">` … `</a>` to `<Link to="/" className="flex items-center gap-2.5">` … `</Link>`, and the nav map to:

```tsx
        {NAV_ITEMS.map((item) => (
          // Primary is dark navy in dark mode — highlight reads there instead
          <Link key={item.label} to={item.to} className="hover:text-primary dark:hover:text-highlight">
            {item.label}
          </Link>
        ))}
```

- [ ] **Step 11: Mobile menu links**

In `src/components/MobileMenu.tsx`, add `import { Link } from "@tanstack/react-router"`. Add `projectId?: string` to `MobileMenuProps`. Change `ProjectFolderCard` to take and use it:

```tsx
function ProjectFolderCard({ projectName, projectId }: { projectName: string; projectId: string }) {
```

and replace its `.map(({ label, Icon, href }) => ( <a …> ))` with:

```tsx
          {[...PROJECT_NAV_ITEMS, PROJECT_NAV_SETTINGS].map(({ label, Icon, to }) =>
            to ? (
              <Link
                key={label}
                to={to}
                params={{ projectId }}
                className="flex items-center gap-2 text-sm font-medium text-text-primary"
              >
                <Icon size={24} />
                {label}
              </Link>
            ) : (
              // No page yet: shown, but not a link
              <span key={label} className="flex items-center gap-2 text-sm font-medium text-text-primary">
                <Icon size={24} />
                {label}
              </span>
            ),
          )}
```

Change the component signature to `export function MobileMenu({ variant = "default", projectName = "The Grid", projectId = "the-grid" }: MobileMenuProps)`, pass `projectId={projectId}` to `ProjectFolderCard`, and change the three `<a href="#/ideas" …>`, `<a href="#/projects" …>`, `<a href="#/tasks" …>` elements to `<Link to="/ideas" …>`, `<Link to="/projects" …>`, `<Link to="/tasks" …>` (same attributes, `</a>` → `</Link>`). Leave the Logout `<a href="#">` as it is: logout doesn't exist yet (plan 4).

- [ ] **Step 12: Project toolbar links**

In `src/components/ProjectToolbar.tsx`, add `import { Link } from "@tanstack/react-router"`. Change the signature to:

```tsx
export function ProjectToolbar({
  projectId,
  projectName = "Project Name",
}: {
  projectId: string
  projectName?: string
}) {
```

Change the breadcrumb `<a href="#/projects" …>Projects</a>` to `<Link to="/projects" className="underline decoration-from-font underline-offset-2">Projects</Link>`, and the nav map to:

```tsx
        {PROJECT_NAV_ITEMS.map(({ label, Icon, to }) => {
          const className =
            "flex items-center gap-2 text-xs leading-none font-medium whitespace-nowrap text-text-light"
          const content = (
            <>
              <Icon size={24} />
              <span className="hidden xl:inline">{label}</span>
            </>
          )
          return to ? (
            <Link key={label} to={to} params={{ projectId }} aria-label={label} className={className}>
              {content}
            </Link>
          ) : (
            // No page yet: shown, but not a link
            <span key={label} role="link" aria-disabled="true" aria-label={label} className={className}>
              {content}
            </span>
          )
        })}
```

- [ ] **Step 13: Project links from Home and Projects**

In `src/components/ProjectQuickLinks.tsx`: add `import { Link } from "@tanstack/react-router"`; add `id: string` as the first field of `ProjectQuickLink`; give the data ids (`id: "the-grid"` on The Grid, `id: "melting-glaciers"` on the three Melting Glaciers cards); change `ProjectQuickLinkCard({ name, … })` to `ProjectQuickLinkCard({ id, name, … })`; and replace

```tsx
      {/* Every card opens the one Project Overview template for now */}
      <a href="#/project-overview">
```

with

```tsx
      <Link to="/projects/$projectId" params={{ projectId: id }}>
```

and its closing `</a>` with `</Link>`.

In `src/components/ProjectList.tsx`: add `import { Link } from "@tanstack/react-router"`; change the project ids to slugs, matching the ids the task list already uses for The Grid and Melting Glaciers:

```ts
  { id: "the-grid", title: "The Grid", … },
  { id: "melting-glaciers", title: "Melting Glaciers", … },
  { id: "night-shift", title: "Night shift at the reservoir", … },
  { id: "urban-heat-islands", title: "Urban heat islands", … },
  { id: "grid-outages", title: "Grid outages, block by block", … },
```

(keep every other field as it is); change `blankProject`'s id to `` `project-${nextProjectId++}` ``; and replace the title `render` and its comment with:

```tsx
    // The title opens that project's overview. Still a text column
    // underneath, so sorting and search keep working off the title itself.
    render: (project) => (
      <Link
        to="/projects/$projectId"
        params={{ projectId: project.id }}
        className="underline-offset-2 transition-colors hover:text-highlight hover:underline"
      >
        {project.title}
      </Link>
    ),
```

- [ ] **Step 14: Idea navigation**

In `src/components/IdeaList.tsx`, add `import { useNavigate } from "@tanstack/react-router"`. Change `ideaColumns` to take the two navigations:

```tsx
function ideaColumns(
  onEdit: (idea: Idea, save: (patch: Partial<Idea>) => void) => void,
  open: { intake: (ideaId: string) => void; edit: (ideaId: string) => void },
  { type, storyType }: { type: boolean; storyType: boolean },
): DataTableColumn<Idea>[] {
```

In the title column's `onClick`, replace `window.location.hash = \`#/ideas/${idea.id}/intake\`` with `open.intake(idea.id)`. Replace the edit action's `onSelect` with:

```tsx
        onSelect: (idea) => {
          if (idea.status === "intake") open.intake(idea.id)
          else open.edit(idea.id)
        },
```

In `IdeaList`, add `const navigate = useNavigate()` before `columnsByCategory`, and change the memo to:

```tsx
  const columnsByCategory = useMemo(() => {
    const onEdit = (idea: Idea, save: (patch: Partial<Idea>) => void) => setEditing({ idea, save })
    const open = {
      intake: (ideaId: string) => void navigate({ to: "/ideas/$ideaId/intake", params: { ideaId } }),
      edit: (ideaId: string) => void navigate({ to: "/ideas/$ideaId/edit", params: { ideaId } }),
    }
    const plain = ideaColumns(onEdit, open, { type: false, storyType: false })
    return {
      all: ideaColumns(onEdit, open, { type: true, storyType: true }),
      general: plain,
      "story-pitch": ideaColumns(onEdit, open, { type: false, storyType: true }),
    }
  }, [navigate])
```

Update the comment above it from "Stable across renders (setEditing never changes)" to "Stable across renders (setEditing and navigate never change)". In the `IdeaList` doc comment, change `#/ideas/new` to `/ideas/new`. Do the same in the `IdeaForm` doc comment (`src/components/IdeaForm.tsx:75`) and the top comment of `src/lib/ideas.ts`.

In `src/pages/NewIdea.tsx`, add `import { useNavigate } from "@tanstack/react-router"` and replace `backToIdeas` with:

```tsx
  const navigate = useNavigate()
  const backToIdeas = () => void navigate({ to: "/ideas" })
```

In `src/pages/EditIdea.tsx`: add `import { Link, useNavigate } from "@tanstack/react-router"`; replace `backToIdeas` the same way; change the not-found `<a href="#/ideas" …>Back to Ideas</a>` to `<Link to="/ideas" …>Back to Ideas</Link>` (same className); delete the `editIdeaId` function and its comment at the bottom of the file.

In `src/pages/IdeaIntake.tsx`: add `import { Link } from "@tanstack/react-router"`; change the not-found `<a href="#/ideas" …>` to `<Link to="/ideas" …>` (same className, `</a>` → `</Link>`); delete the `intakeIdeaId` function and its comment at the bottom of the file.

- [ ] **Step 15: Project pages take their id**

In each of `src/pages/ProjectOverview.tsx`, `src/pages/ProjectTasks.tsx`, `src/pages/ProjectDocuments.tsx`, change the signature to `({ projectId }: { projectId: string })`, pass `projectId={projectId}` to their `<MobileMenu variant="project" … />`, and change `<ProjectToolbar />` to `<ProjectToolbar projectId={projectId} />`.

- [ ] **Step 16: Update the existing tests that need a router**

Components with a `Link` or `useNavigate` need a router around them in tests.

`src/components/ProjectList.test.tsx`: add `import { renderWithRouter } from "@/test/render"`; replace every `render(<ProjectList />)` with `await renderWithRouter(<ProjectList />, { path: "/projects" })` and make each `it` callback `async` if it isn't; change the expected href `"#/project-overview"` to `"/projects/the-grid"`. Remove `render` from the Testing Library import if nothing else uses it.

`src/components/IdeaList.test.tsx`: add `import { renderWithRouter } from "@/test/render"`; replace every `render(<IdeaList />)` with `await renderWithRouter(<IdeaList />, { path: "/ideas" })` (make callbacks `async`). Replace the two navigation tests with:

```tsx
  it("opens the full-page form from the pencil", async () => {
    const user = userEvent.setup()
    const { router } = await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const row = screen.getByRole("row", { name: /Reusable map legend/ })
    await user.click(within(row).getByRole("button", { name: "Edit idea" }))

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(router.state.location.pathname).toBe("/ideas/i2/edit")
  })

  it("sends Edit on an idea in intake to its Intake view instead of the form", async () => {
    const user = userEvent.setup()
    const { router } = await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const row = screen.getByRole("row", { name: /Where the glaciers went/ })
    await user.click(within(row).getByRole("button", { name: "Edit idea" }))

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(router.state.location.pathname).toBe("/ideas/i1/intake")
  })
```

`src/pages/EditIdea.test.tsx`: replace the whole file with:

```tsx
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it } from "vitest"
import { getIdeas, resetIdeas } from "@/lib/ideas"
import { renderWithRouter } from "@/test/render"
import { EditIdea } from "./EditIdea"

describe("EditIdea", () => {
  beforeEach(() => resetIdeas())

  it("edits the idea as a full page, saves it to the store, and returns to Ideas", async () => {
    const user = userEvent.setup()
    const { router } = await renderWithRouter(<EditIdea ideaId="i2" />, { path: "/ideas/i2/edit" })

    const title = screen.getByRole("textbox", { name: "Title" })
    expect(title).toHaveValue("Reusable map legend")
    await user.clear(title)
    await user.type(title, "Map legend kit")
    await user.click(screen.getByRole("button", { name: "Save" }))

    expect(getIdeas().find((idea) => idea.id === "i2")?.title).toBe("Map legend kit")
    expect(router.state.location.pathname).toBe("/ideas")
  })
})
```

`src/pages/IdeaIntake.test.tsx`: delete the `"reads the idea id from an intake hash, and nothing else"` test and `intakeIdeaId` from the import; add `import { renderWithRouter } from "@/test/render"`; replace every `render(<IdeaIntake ideaId="i1" />)` with `await renderWithRouter(<IdeaIntake ideaId="i1" />, { path: "/ideas/i1/intake" })` (and any other idea id the same way, with its own path); make callbacks `async`.

- [ ] **Step 17: Check nothing still uses hash URLs**

Run: `grep -rn '#/' src`
Expected: only `src/components/TaskHighlight.test.tsx` (it passes `"#/ideas"` as an arbitrary `href` value to a generic component; leave it).

- [ ] **Step 18: Run everything**

```bash
pnpm typecheck && pnpm test && pnpm lint
```

Expected: typecheck clean; all test files pass, including `src/routes.test.tsx`; no lint errors.

- [ ] **Step 19: Commit**

```bash
git add -A src package.json pnpm-lock.yaml
git commit -m "Route with TanStack Router; typed links instead of #/ URLs

Real paths (/ideas/i1/edit, /projects/the-grid/tasks), a Not Found page,
and scroll restoration. Project pages take their id from the URL.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Shared layouts

Render the header and mobile menu once, in a root layout, and the project toolbar once, in a project layout. Pages keep only their own content.

**Files:**
- Create: `src/layouts/RootLayout.tsx`, `src/layouts/ProjectLayout.tsx`
- Modify: `src/router.tsx`, every file in `src/pages/` except the tests
- Test: `src/routes.test.tsx`

**Interfaces:**
- Consumes: route id `"/projects/$projectId"` (Task 3).
- Produces: `RootLayout` (root route component) and `ProjectLayout` (project route component). Tasks 5 and 6 add hooks inside `RootLayout`. Project page components take no props after this task.

- [ ] **Step 1: Write the failing layout tests**

Add to `src/routes.test.tsx`, inside the `describe`:

```tsx
  it.each(["/", "/tasks", "/ideas/i1/intake", "/projects/the-grid", "/projects/the-grid/documents", "/nope"])(
    "%s has exactly one header and one mobile menu",
    async (path) => {
      await renderApp(path)
      expect(screen.getAllByRole("banner")).toHaveLength(1)
      expect(document.querySelectorAll("[data-mobile-menu]")).toHaveLength(1)
    },
  )

  it("a project's pages get the project toolbar and the travelling logo; other pages don't", async () => {
    const { unmount } = await renderApp("/projects/the-grid/tasks")
    expect(document.querySelectorAll("[data-secondary-toolbar]")).toHaveLength(1)
    expect(document.querySelectorAll("[data-scroll-logo]")).toHaveLength(1)
    unmount()

    await renderApp("/tasks")
    expect(document.querySelector("[data-secondary-toolbar]")).toBeNull()
    expect(document.querySelector("[data-scroll-logo]")).toBeNull()
  })
```

- [ ] **Step 2: Run them before the move**

Run: `pnpm test src/routes.test.tsx`
Expected: all pass. These are regression guards: today every page renders its own chrome, once. After Step 5 the chrome comes from the layouts, and a failure means the move dropped or doubled something.

- [ ] **Step 3: Create the root layout**

Create `src/layouts/RootLayout.tsx`:

```tsx
import { Outlet, useMatch } from "@tanstack/react-router"
import { Header } from "@/components/Header"
import { MobileMenu } from "@/components/MobileMenu"
import { ScrollLogo } from "@/components/ScrollLogo"

/**
 * What every page shares: the fixed header and the mobile menu. On a
 * project's pages (anything under /projects/$projectId), the header's own
 * logo mark hides in favor of the single travelling ScrollLogo (see
 * ScrollLogo.tsx), and the mobile menu gains the project folder card.
 */
export function RootLayout() {
  const projectMatch = useMatch({ from: "/projects/$projectId", shouldThrow: false })
  const projectId = projectMatch?.params.projectId

  return (
    <>
      <Header hideLogoMark={projectId !== undefined} />
      {projectId !== undefined ? (
        <>
          <ScrollLogo />
          <MobileMenu variant="project" projectName="The Grid" projectId={projectId} />
        </>
      ) : (
        <MobileMenu />
      )}
      <Outlet />
    </>
  )
}
```

- [ ] **Step 4: Create the project layout**

Create `src/layouts/ProjectLayout.tsx`:

```tsx
import { Outlet, useParams } from "@tanstack/react-router"
import { ProjectToolbar } from "@/components/ProjectToolbar"

/**
 * A project's pages share the project toolbar under the header. The
 * toolbar is sticky, so it must sit directly in this tall wrapper — a
 * wrapper sized to the toolbar alone gives it nowhere to stick (see
 * AGENTS.md). lg:flex-col lets Project Overview's grid stretch to the
 * bottom of the window, as its own wrapper used to.
 */
export function ProjectLayout() {
  const { projectId } = useParams({ from: "/projects/$projectId" })

  return (
    <div className="min-h-screen bg-background pt-13 lg:flex lg:flex-col lg:pt-19">
      <ProjectToolbar projectId={projectId} />
      <Outlet />
    </div>
  )
}
```

- [ ] **Step 5: Use the layouts and strip the chrome from pages**

In `src/router.tsx`:
- import `RootLayout` and `ProjectLayout` from `@/layouts/…`;
- `createRootRoute({ component: RootLayout, notFoundComponent: NotFound })`;
- `projectRoute`'s `component: ProjectLayout`, and replace its comment with `// Parent of a project's pages: the project toolbar, then the page`;
- delete `ProjectOverviewRoute`, `ProjectTasksRoute`, `ProjectDocumentsRoute` and use `component: ProjectOverview`, `component: ProjectTasks`, `component: ProjectDocuments` directly.

In the pages:
- `Home.tsx`, `Tasks.tsx`, `Ideas.tsx`, `Projects.tsx`, `NewIdea.tsx`, `EditIdea.tsx`, `IdeaIntake.tsx` (both the not-found and the main return), `NotFound.tsx`: delete `<Header … />` and `<MobileMenu … />` and their imports. Keep each page's outer `<div className="min-h-screen bg-background …">` wrapper.
- `ProjectOverview.tsx`: remove the `projectId` prop, the imports and elements for `Header`, `ScrollLogo`, `MobileMenu`, `ProjectToolbar`, and their comments, and the outer `<div className="min-h-screen …">` wrapper. The component returns the grid `<div className="flex flex-col gap-10 px-5 pb-5 lg:grid lg:flex-1 …">` directly.
- `ProjectTasks.tsx` and `ProjectDocuments.tsx`: same removals; each returns its `<main className="p-5 pt-8 lg:p-page">` directly. Update each top comment's "same project chrome as…" wording to "Under the project layout (toolbar and all):".

- [ ] **Step 6: Run everything**

```bash
pnpm typecheck && pnpm test && pnpm lint
```

Expected: all pass, including the layout tests from Step 1.

If only the `/nope` case fails with no header, the root route's `notFoundComponent` is rendering outside `RootLayout`. Fix it by removing `notFoundComponent` from `createRootRoute` and adding a catch-all child route instead, last in `routeTree`:

```tsx
const notFoundRoute = createRoute({ getParentRoute: () => rootRoute, path: "$", component: NotFound })
```

- [ ] **Step 7: Look at it**

Run `pnpm dev`, open `http://localhost:5173/projects/the-grid` at a desktop width, scroll down and up: the header hides, the toolbar moves to the top, and the logo mark travels between them, as before. Open `/` at a phone width and check the menu button still toggles the menu (the script still drives it until Task 6).

- [ ] **Step 8: Commit**

```bash
git add -A src
git commit -m "Shared root and project layouts

Header and mobile menu render once in the root layout; the project
toolbar once in the project layout. Pages keep only their content.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Header scroll as a React hook

Port `public/scripts/header-scroll.js` to a hook. Same threshold, same `header-hidden` class on `<html>`, so the CSS for the header, the sticky toolbar and ScrollLogo is untouched. New: the class is cleared on navigation.

**Files:**
- Create: `src/hooks/use-header-scroll.ts`, `src/hooks/use-header-scroll.test.ts`
- Modify: `src/layouts/RootLayout.tsx`, `index.html`, `src/components/ScrollLogo.tsx` (comment)
- Delete: `public/scripts/header-scroll.js`

**Interfaces:**
- Produces: `useHeaderScroll(resetKey: string): void`.

- [ ] **Step 1: Write the failing test**

Create `src/hooks/use-header-scroll.test.ts`:

```ts
import { renderHook } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { useHeaderScroll } from "./use-header-scroll"

function scrollTo(y: number) {
  Object.defineProperty(window, "scrollY", { value: y, configurable: true })
  window.dispatchEvent(new Event("scroll"))
}

const hidden = () => document.documentElement.classList.contains("header-hidden")

describe("useHeaderScroll", () => {
  afterEach(() => {
    Object.defineProperty(window, "scrollY", { value: 0, configurable: true })
    document.documentElement.classList.remove("header-hidden")
  })

  it("stays visible for small scrolls near the top", () => {
    renderHook(() => useHeaderScroll("/"))
    scrollTo(20)
    expect(hidden()).toBe(false)
  })

  it("hides when scrolling down past 24px", () => {
    renderHook(() => useHeaderScroll("/"))
    scrollTo(100)
    expect(hidden()).toBe(true)
  })

  it("shows again on any scroll up", () => {
    renderHook(() => useHeaderScroll("/"))
    scrollTo(300)
    scrollTo(250)
    expect(hidden()).toBe(false)
  })

  it("shows again when the path changes, even without a scroll", () => {
    const { rerender } = renderHook(({ path }) => useHeaderScroll(path), {
      initialProps: { path: "/tasks" },
    })
    scrollTo(300)
    expect(hidden()).toBe(true)
    rerender({ path: "/ideas" })
    expect(hidden()).toBe(false)
  })

  it("stops listening once unmounted", () => {
    const { unmount } = renderHook(() => useHeaderScroll("/"))
    unmount()
    scrollTo(300)
    expect(hidden()).toBe(false)
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm test src/hooks/use-header-scroll.test.ts`
Expected: FAIL, `Failed to resolve import "./use-header-scroll"`.

- [ ] **Step 3: Write the hook**

Create `src/hooks/use-header-scroll.ts`:

```ts
import { useEffect } from "react"

// Scrolling down past this hides the header; any scroll up shows it
const HIDE_AFTER_PX = 24

/**
 * Hides the header on scroll down and shows it on scroll up, by toggling
 * `header-hidden` on <html> — not on the header itself, so other elements
 * (the sticky project toolbar, ScrollLogo) can react to it with
 * `:root.header-hidden` in index.css. `resetKey` (the current path)
 * clears it on navigation, so a new page never starts with the header
 * hidden.
 */
export function useHeaderScroll(resetKey: string) {
  useEffect(() => {
    const root = document.documentElement
    let lastY = window.scrollY

    const onScroll = () => {
      const y = window.scrollY
      root.classList.toggle("header-hidden", y >= HIDE_AFTER_PX && y > lastY)
      lastY = y
    }

    window.addEventListener("scroll", onScroll, { passive: true })
    return () => window.removeEventListener("scroll", onScroll)
  }, [])

  useEffect(() => {
    document.documentElement.classList.remove("header-hidden")
  }, [resetKey])
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `pnpm test src/hooks/use-header-scroll.test.ts`
Expected: 5 passed.

- [ ] **Step 5: Use it and delete the script**

In `src/layouts/RootLayout.tsx`, change the router import to `import { Outlet, useMatch, useRouterState } from "@tanstack/react-router"`, add `import { useHeaderScroll } from "@/hooks/use-header-scroll"`, and add at the top of `RootLayout`:

```tsx
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  useHeaderScroll(pathname)
```

In `index.html`, delete `<script src="/scripts/header-scroll.js"></script>`.

```bash
git rm -q public/scripts/header-scroll.js
```

In `src/components/ScrollLogo.tsx`, change "toggled by the same header-hidden class the header-hide script already sets" to "toggled by the header-hidden class useHeaderScroll sets".

In `src/index.css`, the comment at line 275 says "The script toggles `header-hidden`". Change "The script" to "useHeaderScroll (src/hooks/use-header-scroll.ts)". Comment only; no rule changes.

- [ ] **Step 6: Run everything and look**

```bash
pnpm typecheck && pnpm test && pnpm lint
```

Then `pnpm dev`, open `/projects/the-grid` on desktop, scroll down and up: same behavior as before. Scroll down so the header hides, click the toolbar's Tasks link: the new page starts with the header showing.

- [ ] **Step 7: Commit**

```bash
git add -A src index.html public
git commit -m "Header show/hide on scroll as a React hook

Same threshold and header-hidden class; also clears on navigation.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Mobile menu as React state

Port `public/scripts/mobile-menu.js`. Same `mobile-menu-open` class on `<html>`, so the roll-down, the staggered items and the icon swap in `index.css` are untouched. New: the menu closes on navigation, and the toggle button reports its state to screen readers.

**Files:**
- Create: `src/hooks/use-mobile-menu.ts`, `src/layouts/RootLayout.test.tsx`
- Modify: `src/layouts/RootLayout.tsx`, `src/components/Header.tsx`, `src/components/MobileMenu.tsx`, `index.html`, `src/index.css` (comment)
- Delete: `public/scripts/mobile-menu.js`

**Interfaces:**
- Consumes: `renderApp` (Task 3), `RootLayout` (Task 4).
- Produces:
  - `useMobileMenu(pathname: string): { open: boolean; toggle: () => void; close: () => void }`.
  - `Header` props: `{ hideLogoMark?: boolean; menuOpen: boolean; onToggleMenu: () => void }`.
  - `MobileMenu` props gain `onNavigate?: () => void`, called when any menu link is clicked.

- [ ] **Step 1: Write the failing test**

Create `src/layouts/RootLayout.test.tsx`:

```tsx
import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it } from "vitest"
import { renderApp } from "@/test/render"

const menuOpen = () => document.documentElement.classList.contains("mobile-menu-open")
const menu = () => within(document.querySelector<HTMLElement>("[data-mobile-menu]")!)

describe("mobile menu", () => {
  afterEach(() => document.documentElement.classList.remove("mobile-menu-open"))

  it("opens and closes from the header button", async () => {
    const user = userEvent.setup()
    await renderApp("/")

    const toggle = screen.getByRole("button", { name: "Open menu" })
    expect(toggle).toHaveAttribute("aria-expanded", "false")
    await user.click(toggle)
    expect(menuOpen()).toBe(true)
    expect(screen.getByRole("button", { name: "Close menu" })).toHaveAttribute("aria-expanded", "true")

    await user.click(screen.getByRole("button", { name: "Close menu" }))
    expect(menuOpen()).toBe(false)
  })

  it("closes on Escape", async () => {
    const user = userEvent.setup()
    await renderApp("/")
    await user.click(screen.getByRole("button", { name: "Open menu" }))
    await user.keyboard("{Escape}")
    expect(menuOpen()).toBe(false)
  })

  it("closes when a menu link navigates", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/")
    await user.click(screen.getByRole("button", { name: "Open menu" }))
    await user.click(menu().getByRole("link", { name: "Tasks" }))
    expect(router.state.location.pathname).toBe("/tasks")
    expect(menuOpen()).toBe(false)
  })

  it("closes when the link is for the page already showing", async () => {
    const user = userEvent.setup()
    await renderApp("/tasks")
    await user.click(screen.getByRole("button", { name: "Open menu" }))
    await user.click(menu().getByRole("link", { name: "Tasks" }))
    expect(menuOpen()).toBe(false)
  })

  it("closes from a project folder card link", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/projects/the-grid")
    await user.click(screen.getByRole("button", { name: "Open menu" }))
    await user.click(menu().getByRole("link", { name: "Documents" }))
    expect(router.state.location.pathname).toBe("/projects/the-grid/documents")
    expect(menuOpen()).toBe(false)
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm test src/layouts/RootLayout.test.tsx`
Expected: FAIL. The button has no `aria-expanded`, and without the script (not loaded in tests) clicking changes nothing.

- [ ] **Step 3: Write the hook**

Create `src/hooks/use-mobile-menu.ts`:

```ts
import { useEffect, useState } from "react"

/**
 * Open/closed state for the mobile menu. While open, `mobile-menu-open` is
 * on <html>; index.css keys the panel's roll-down, the staggered items and
 * the hamburger/close icon swap off it. Closes on Escape, and by itself
 * whenever `pathname` changes: the menu remembers which page it was opened
 * on and counts as open only there.
 */
export function useMobileMenu(pathname: string) {
  const [openOn, setOpenOn] = useState<string | null>(null)
  const open = openOn === pathname

  useEffect(() => {
    const root = document.documentElement
    root.classList.toggle("mobile-menu-open", open)
    return () => root.classList.remove("mobile-menu-open")
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpenOn(null)
    }
    document.addEventListener("keydown", onKeyDown)
    return () => document.removeEventListener("keydown", onKeyDown)
  }, [open])

  return {
    open,
    toggle: () => setOpenOn(open ? null : pathname),
    close: () => setOpenOn(null),
  }
}
```

- [ ] **Step 4: Wire the header button**

In `src/components/Header.tsx`, change the signature to:

```tsx
export function Header({
  hideLogoMark = false,
  menuOpen,
  onToggleMenu,
}: {
  hideLogoMark?: boolean
  menuOpen: boolean
  onToggleMenu: () => void
}) {
```

Change the menu button's comment to `{/* Toggles the mobile menu; the icon swaps between hamburger and close via CSS keyed off mobile-menu-open on <html> — see [data-menu-icon-open]/[data-menu-icon-close] in index.css. */}`, and its opening tag to:

```tsx
        <button
          type="button"
          data-mobile-menu-toggle
          onClick={onToggleMenu}
          aria-expanded={menuOpen}
          aria-label={menuOpen ? "Close menu" : "Open menu"}
          className="relative flex size-6 cursor-pointer items-center justify-center text-text-primary lg:hidden"
        >
```

- [ ] **Step 5: Close the menu from its links**

In `src/components/MobileMenu.tsx`, add `onNavigate?: () => void` to `MobileMenuProps` and to the destructured props. Add `onClick={onNavigate}` to the Ideas, Projects and Tasks `Link`s and the Logout `<a>`. Give `ProjectFolderCard` an `onNavigate?: () => void` prop, pass it through, and add `onClick={onNavigate}` to its `Link`.

- [ ] **Step 6: Hold the state in the root layout**

In `src/layouts/RootLayout.tsx`, add `import { useMobileMenu } from "@/hooks/use-mobile-menu"`. After `useHeaderScroll(pathname)` add:

```tsx
  const menu = useMobileMenu(pathname)
```

Change `<Header hideLogoMark={projectId !== undefined} />` to:

```tsx
      <Header hideLogoMark={projectId !== undefined} menuOpen={menu.open} onToggleMenu={menu.toggle} />
```

and add `onNavigate={menu.close}` to both `<MobileMenu … />` elements.

- [ ] **Step 7: Delete the script**

In `index.html`, delete `<script src="/scripts/mobile-menu.js"></script>`.

```bash
git rm -q public/scripts/mobile-menu.js
```

In `src/index.css`, the comment at line 353 says "public/scripts/mobile-menu.js toggles `mobile-menu-open`". Change it to "useMobileMenu (src/hooks/use-mobile-menu.ts) toggles `mobile-menu-open`". Comment only.

- [ ] **Step 8: Run everything and look**

```bash
pnpm typecheck && pnpm test && pnpm lint
```

Expected: all pass, including the 5 new tests. Then `pnpm dev` at a phone width: the menu rolls down with staggered items, the icon swaps, a link closes it, Escape closes it.

- [ ] **Step 9: Commit**

```bash
git add -A src index.html public
git commit -m "Mobile menu as React state

Same mobile-menu-open class and animation; closes on navigation and
Escape, and the toggle reports aria-expanded.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Moved to plan 1b

The project summary card port moved to `docs/superpowers/plans/2026-09-24-spa-1b-project-summary.md` to save time: Home doesn't use it. Until then `public/scripts/project-summary.js` and its `<script>` tag in `index.html` stay, and keep working (the script uses event delegation, so it doesn't care that React renders the card). Skip to Task 8.

---

### Task 8: DataTable view state that a page can control

Let a page own a table's sort, column filters and search, and convert that state to and from URL search parameters. This task is the table and the conversion only; Task 9 connects them to routes.

**URL format** (spec section 4):

| State | Search param | Example |
|---|---|---|
| Sort (first sort only) | `sort=<columnKey>.<asc\|desc>` | `sort=due.asc` |
| Global search | `q=<text>` | `q=launch` |
| Select, user and users filters | `<columnKey>=<value>,<value>` | `status=blocked,todo` |
| Date range filter | `<columnKey>=<from>..<to>`, either side optional | `due=2026-09-01..2026-09-30`, `due=..2026-09-30` |
| Text filter | `<columnKey>=<text>` | `source=archive` |

`sort`, `q` and `category` are reserved and never used as column keys. Invalid values are dropped: unknown columns, unknown select options, malformed dates, a sort direction other than `asc`/`desc`.

**Files:**
- Create: `src/components/ui/data-table/view-state.ts`, `src/components/ui/data-table/url-state.ts`, `src/components/ui/data-table/url-state.test.ts`
- Modify: `src/components/ui/data-table/types.ts`, `src/components/ui/data-table/data-table.tsx`, `src/components/ui/data-table/toolbar.tsx:31`, `src/components/ui/data-table/index.ts`
- Test: `src/components/ui/data-table/data-table.test.tsx`

**Interfaces:**
- Produces:
  - `interface TableViewState { sorting: SortingState; columnFilters: ColumnFiltersState; globalFilter: string }` (exported from `@/components/ui/data-table`).
  - `DataTableProps<T>` gains `viewState?: TableViewState` and `onViewStateChange?: (next: TableViewState) => void`.
  - `EMPTY_VIEW_STATE: TableViewState` in `view-state.ts`.
  - In `url-state.ts`: `coerceSearch(raw: Record<string, unknown>): Record<string, string>`, `viewStateFromSearch<T>(search: Record<string, unknown>, columns: DataTableColumn<T>[]): TableViewState`, `searchFromViewState<T>(state: TableViewState, columns: DataTableColumn<T>[]): Record<string, string>`, `tableSearchKeys<T>(columns: DataTableColumn<T>[]): string[]`.

- [ ] **Step 1: Write the failing conversion tests**

Create `src/components/ui/data-table/url-state.test.ts`:

```ts
import { describe, expect, it } from "vitest"
import type { DataTableColumn } from "./types"
import { coerceSearch, searchFromViewState, tableSearchKeys, viewStateFromSearch } from "./url-state"

type Row = { id: string; name: string; status: string; owner: string; due: string }

const COLUMNS: DataTableColumn<Row>[] = [
  { key: "name", label: "Name", type: "text", filterable: false },
  {
    key: "status",
    label: "Status",
    type: "select",
    options: [
      { value: "todo", label: "To Do" },
      { value: "blocked", label: "Blocked" },
    ],
  },
  { key: "owner", label: "Owner", type: "user" },
  { key: "due", label: "Due", type: "date" },
  { key: "actions", label: "", type: "actions", actions: [] },
]

describe("coerceSearch", () => {
  it("keeps strings, turns numbers and booleans into strings, drops everything else", () => {
    expect(coerceSearch({ a: "x", b: 123, c: true, d: { x: 1 }, e: ["y"], f: null })).toEqual({
      a: "x",
      b: "123",
      c: "true",
    })
  })
})

describe("viewStateFromSearch", () => {
  it("is empty for an empty search", () => {
    expect(viewStateFromSearch({}, COLUMNS)).toEqual({ sorting: [], columnFilters: [], globalFilter: "" })
  })

  it("reads sort, search and filters of every kind", () => {
    const state = viewStateFromSearch(
      { sort: "due.desc", q: "launch", status: "blocked,todo", owner: "u1,u2", due: "2026-09-01..2026-09-30" },
      COLUMNS,
    )
    expect(state).toEqual({
      sorting: [{ id: "due", desc: true }],
      globalFilter: "launch",
      columnFilters: [
        { id: "status", value: ["blocked", "todo"] },
        { id: "owner", value: ["u1", "u2"] },
        { id: "due", value: { from: "2026-09-01", to: "2026-09-30" } },
      ],
    })
  })

  it("accepts a date range open at either end", () => {
    expect(viewStateFromSearch({ due: "..2026-09-30" }, COLUMNS).columnFilters).toEqual([
      { id: "due", value: { to: "2026-09-30" } },
    ])
    expect(viewStateFromSearch({ due: "2026-09-01.." }, COLUMNS).columnFilters).toEqual([
      { id: "due", value: { from: "2026-09-01" } },
    ])
  })

  it("coerces a number or boolean typed into the URL into search text", () => {
    expect(viewStateFromSearch({ q: 123 }, COLUMNS).globalFilter).toBe("123")
    expect(viewStateFromSearch({ q: true }, COLUMNS).globalFilter).toBe("true")
  })

  it("drops invalid values instead of failing", () => {
    const state = viewStateFromSearch(
      {
        sort: "nope.asc",
        status: "blocked,bogus",
        due: "yesterday..tomorrow",
        name: "filtered", // name isn't filterable
        actions: "x", // nor is actions
        participants: "u1", // not a column here
      },
      COLUMNS,
    )
    expect(state).toEqual({ sorting: [], globalFilter: "", columnFilters: [{ id: "status", value: ["blocked"] }] })
    expect(viewStateFromSearch({ sort: "due.sideways" }, COLUMNS).sorting).toEqual([])
    expect(viewStateFromSearch({ sort: "due" }, COLUMNS).sorting).toEqual([])
    expect(viewStateFromSearch({ status: "bogus" }, COLUMNS).columnFilters).toEqual([])
  })
})

describe("searchFromViewState", () => {
  it("writes only what is set", () => {
    expect(searchFromViewState({ sorting: [], columnFilters: [], globalFilter: "" }, COLUMNS)).toEqual({})
  })

  it("writes sort, search and filters", () => {
    expect(
      searchFromViewState(
        {
          sorting: [{ id: "name", desc: false }],
          globalFilter: "launch",
          columnFilters: [
            { id: "status", value: ["blocked"] },
            { id: "due", value: { to: "2026-09-30" } },
          ],
        },
        COLUMNS,
      ),
    ).toEqual({ sort: "name.asc", q: "launch", status: "blocked", due: "..2026-09-30" })
  })

  it("round-trips", () => {
    const search = { sort: "due.desc", q: "x", status: "todo", owner: "u2", due: "2026-09-01.." }
    expect(searchFromViewState(viewStateFromSearch(search, COLUMNS), COLUMNS)).toEqual(search)
  })
})

describe("tableSearchKeys", () => {
  it("lists sort, q and every filterable column", () => {
    expect(tableSearchKeys(COLUMNS)).toEqual(["sort", "q", "status", "owner", "due"])
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm test src/components/ui/data-table/url-state.test.ts`
Expected: FAIL, `Failed to resolve import "./url-state"`.

- [ ] **Step 3: Add the view state type**

In `src/components/ui/data-table/types.ts`, add at the top:

```ts
import type { ColumnFiltersState, SortingState } from "@tanstack/react-table"
```

(merge with the existing `import type { ReactNode } from "react"` as a separate line), add before `DataTableColumn`:

```ts
/** The parts of a table's state a page can own — for example, keep in the URL. */
export interface TableViewState {
  sorting: SortingState
  columnFilters: ColumnFiltersState
  globalFilter: string
}
```

and add to the end of `DataTableProps<T>`:

```ts
  /**
   * Sort, column filters and search, owned by the page. Omit both to let
   * the table keep its own. When set, the table shows exactly this and
   * reports every change through onViewStateChange.
   */
  viewState?: TableViewState
  onViewStateChange?: (next: TableViewState) => void
```

Create `src/components/ui/data-table/view-state.ts`:

```ts
import type { Updater } from "@tanstack/react-table"
import type { TableViewState } from "./types"

export const EMPTY_VIEW_STATE: TableViewState = { sorting: [], columnFilters: [], globalFilter: "" }

/** TanStack hands state changes over as a value or a function of the old value */
export function resolveUpdater<V>(updater: Updater<V>, current: V): V {
  return typeof updater === "function" ? (updater as (old: V) => V)(current) : updater
}
```

- [ ] **Step 4: Write the conversion**

Create `src/components/ui/data-table/url-state.ts`:

```ts
import type { ColumnFiltersState, SortingState } from "@tanstack/react-table"
import type { DataTableColumn, TableViewState } from "./types"

// Converts a table's sort, filters and search to and from URL search
// params, so a page can keep them in the URL. See the format table in
// docs/superpowers/plans/2026-09-24-spa-1-toolchain-routing.md (Task 8).

const SORT_KEY = "sort"
const QUERY_KEY = "q"
/** Never used for a column: sort, search, and the chip category pages use */
const RESERVED = new Set([SORT_KEY, QUERY_KEY, "category"])
const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/

type DateRange = { from?: string; to?: string }

function isFilterable<T>(column: DataTableColumn<T>) {
  return column.type !== "actions" && column.filterable !== false && !RESERVED.has(column.key)
}

function isSortable<T>(column: DataTableColumn<T>) {
  return column.type !== "actions" && column.sortable !== false
}

/**
 * TanStack Router parses `?q=123` as the number 123 and `?q=true` as a
 * boolean. Tables only deal in text, so turn those back into strings, and
 * drop anything that isn't a plain value.
 */
export function coerceSearch(raw: Record<string, unknown>): Record<string, string> {
  const search: Record<string, string> = {}
  for (const [key, value] of Object.entries(raw)) {
    if (typeof value === "string") search[key] = value
    else if (typeof value === "number" || typeof value === "boolean") search[key] = String(value)
  }
  return search
}

function filterFromText<T>(text: string, column: DataTableColumn<T>): unknown {
  switch (column.type) {
    case "select": {
      const allowed = new Set((column.options ?? []).map((option) => option.value))
      const values = text.split(",").filter((value) => allowed.has(value))
      return values.length > 0 ? values : undefined
    }
    case "user":
    case "users": {
      const values = text.split(",").filter(Boolean)
      return values.length > 0 ? values : undefined
    }
    case "date": {
      const [from = "", to = ""] = text.split("..")
      const range: DateRange = {}
      if (ISO_DATE.test(from)) range.from = from
      if (ISO_DATE.test(to)) range.to = to
      return range.from || range.to ? range : undefined
    }
    default:
      return text
  }
}

function filterToText<T>(value: unknown, column: DataTableColumn<T>): string {
  if (Array.isArray(value)) return value.join(",")
  if (column.type === "date" && value && typeof value === "object") {
    const { from, to } = value as DateRange
    return from || to ? `${from ?? ""}..${to ?? ""}` : ""
  }
  return typeof value === "string" ? value : ""
}

/** The table state a URL describes. Anything invalid is left out. */
export function viewStateFromSearch<T>(
  raw: Record<string, unknown>,
  columns: DataTableColumn<T>[],
): TableViewState {
  const search = coerceSearch(raw)

  const sorting: SortingState = []
  const sort = search[SORT_KEY]
  if (sort) {
    const dot = sort.lastIndexOf(".")
    const id = sort.slice(0, dot)
    const direction = sort.slice(dot + 1)
    const column = columns.find((candidate) => candidate.key === id)
    if (dot > 0 && column && isSortable(column) && (direction === "asc" || direction === "desc")) {
      sorting.push({ id, desc: direction === "desc" })
    }
  }

  const columnFilters: ColumnFiltersState = []
  for (const column of columns) {
    const text = search[column.key]
    if (!text || !isFilterable(column)) continue
    const value = filterFromText(text, column)
    if (value !== undefined) columnFilters.push({ id: column.key, value })
  }

  return { sorting, columnFilters, globalFilter: search[QUERY_KEY] ?? "" }
}

/** The search params for a table state; empty parts are left out. */
export function searchFromViewState<T>(
  state: TableViewState,
  columns: DataTableColumn<T>[],
): Record<string, string> {
  const search: Record<string, string> = {}
  // Only the first sort is kept (shift-click multi-sort isn't in the URL)
  const [sort] = state.sorting
  if (sort) search[SORT_KEY] = `${sort.id}.${sort.desc ? "desc" : "asc"}`
  if (state.globalFilter) search[QUERY_KEY] = state.globalFilter
  for (const filter of state.columnFilters) {
    const column = columns.find((candidate) => candidate.key === filter.id)
    if (!column || !isFilterable(column)) continue
    const text = filterToText(filter.value, column)
    if (text) search[column.key] = text
  }
  return search
}

/** Every search key a table with these columns may write */
export function tableSearchKeys<T>(columns: DataTableColumn<T>[]): string[] {
  return [SORT_KEY, QUERY_KEY, ...columns.filter(isFilterable).map((column) => column.key)]
}
```

- [ ] **Step 5: Run the conversion tests**

Run: `pnpm test src/components/ui/data-table/url-state.test.ts`
Expected: all pass.

- [ ] **Step 6: Write the failing DataTable tests**

In `src/components/ui/data-table/data-table.test.tsx`, add at the end of the file:

```tsx
describe("controlled view state", () => {
  const firstColumn = () =>
    screen
      .getAllByRole("row")
      .slice(1)
      .map((row) => within(row).getAllByRole("cell")[0].textContent)

  it("shows the sort it's given", () => {
    renderTable({
      viewState: { sorting: [{ id: "name", desc: false }], columnFilters: [], globalFilter: "" },
      onViewStateChange: () => {},
    })
    expect(firstColumn()).toEqual(["Audit icons", "Ship export", "Write copy"])
  })

  it("reports a header click instead of sorting by itself", async () => {
    const user = userEvent.setup()
    const onViewStateChange = vi.fn()
    renderTable({
      viewState: { sorting: [], columnFilters: [], globalFilter: "" },
      onViewStateChange,
    })

    await user.click(screen.getByRole("columnheader", { name: /Task Name/ }))

    expect(onViewStateChange).toHaveBeenCalledWith({
      sorting: [{ id: "name", desc: false }],
      columnFilters: [],
      globalFilter: "",
    })
    // Unchanged until the page passes the new state back in
    expect(firstColumn()).toEqual(["Write copy", "Audit icons", "Ship export"])
  })

  it("shows the filters and search it's given, with the search box open", () => {
    renderTable({
      viewState: { sorting: [], columnFilters: [{ id: "status", value: ["todo"] }], globalFilter: "Ship" },
      onViewStateChange: () => {},
    })
    expect(firstColumn()).toEqual(["Ship export"])
    expect(screen.getByRole("searchbox")).toHaveValue("Ship")
  })
})
```

- [ ] **Step 7: Run to verify they fail**

Run: `pnpm test src/components/ui/data-table/data-table.test.tsx`
Expected: the three new tests fail (the props are ignored); existing tests pass.

- [ ] **Step 8: Make DataTable controllable**

In `src/components/ui/data-table/data-table.tsx`:

Change the TanStack import to drop `ColumnFiltersState` and `SortingState` (no longer used directly), keeping `VisibilityState`, and add:

```tsx
import { EMPTY_VIEW_STATE, resolveUpdater } from "./view-state"
import type { DataTableColumn, DataTableProps, TableViewState } from "./types"
```

(replacing the existing `import type { DataTableColumn, DataTableProps } from "./types"`).

Add `viewState: controlledViewState` and `onViewStateChange` to the destructured props. Replace the three `useState` lines for `sorting`, `columnFilters`, `globalFilter` with:

```tsx
  // Sort, filters and search: the page's, when it passes them in; otherwise our own
  const [ownViewState, setOwnViewState] = useState<TableViewState>(EMPTY_VIEW_STATE)
  const viewState = controlledViewState ?? ownViewState
  const setViewState = (next: TableViewState) => {
    if (controlledViewState === undefined) setOwnViewState(next)
    onViewStateChange?.(next)
  }
```

In `useReactTable`, change the `state` and the three handlers to:

```tsx
    state: { ...viewState, columnVisibility, columnOrder },
    onSortingChange: (updater) =>
      setViewState({ ...viewState, sorting: resolveUpdater(updater, viewState.sorting) }),
    onColumnFiltersChange: (updater) =>
      setViewState({ ...viewState, columnFilters: resolveUpdater(updater, viewState.columnFilters) }),
    onGlobalFilterChange: (updater) =>
      setViewState({ ...viewState, globalFilter: resolveUpdater(updater, viewState.globalFilter) }),
```

In `src/components/ui/data-table/toolbar.tsx`, line 31, open the search box when the table starts with a search:

```tsx
  // Open from the start when there's already a search (from the URL, say)
  const [searchOpen, setSearchOpen] = useState(() => Boolean(table.getState().globalFilter))
```

In `src/components/ui/data-table/index.ts`, make sure `TableViewState` is exported with the other types. It re-exports from `./types`; if it lists names explicitly, add `TableViewState` to that list.

- [ ] **Step 9: Run the table tests**

Run: `pnpm test src/components/ui/data-table`
Expected: all pass, old and new.

- [ ] **Step 10: Commit**

```bash
git add src/components/ui/data-table
git commit -m "DataTable: optional page-owned sort, filters and search

Plus conversion to and from URL search params.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Keep table state and chip categories in the URL

Connect the full-page tables to the URL: `/tasks`, `/projects/$projectId/tasks`, `/ideas`, `/projects`, `/projects/$projectId/documents`. The tables embedded in Home, Project Overview and the intake page's Sources tab keep their own state.

Table state changes **replace** the current history entry (typing in search must not add a Back step per keystroke). Chip changes **push** a new entry (Back returns to the previous category).

**Files:**
- Create: `src/hooks/use-table-url-state.ts`, `src/url-state.test.tsx`
- Modify: `src/router.tsx`, `src/components/TaskList.tsx`, `src/components/ProjectList.tsx`, `src/components/IdeaList.tsx`, `src/components/DocumentList.tsx`, `src/pages/Tasks.tsx`, `src/pages/ProjectTasks.tsx`
- Test: `src/components/TaskList.test.tsx`, `src/components/DocumentList.test.tsx` (render inside a router)

**Interfaces:**
- Consumes: `coerceSearch`, `viewStateFromSearch`, `searchFromViewState`, `tableSearchKeys`, `TableViewState`, `viewState`/`onViewStateChange` props (Task 8); `renderApp`, `renderWithRouter` (Task 3).
- Produces:
  - `useTableUrlState<T>(columns: DataTableColumn<T>[], options?: { enabled?: boolean }): Pick<DataTableProps<T>, "viewState" | "onViewStateChange">`. Returns `{}` when disabled.
  - `useCategoryParam<V extends string>(allowed: readonly V[], fallback: V): [V, (next: V) => void]`.
  - `TaskList` gains prop `syncWithUrl?: boolean` (default `false`).

- [ ] **Step 1: Write the failing integration tests**

Create `src/url-state.test.tsx`:

```tsx
import { act, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { renderApp } from "@/test/render"

const firstColumn = () =>
  screen
    .getAllByRole("row")
    .slice(1)
    .map((row) => within(row).getAllByRole("cell")[0].textContent)

describe("table state in the URL", () => {
  it("applies filters from the URL", async () => {
    await renderApp("/tasks?status=blocked")
    expect(firstColumn()).toEqual(["Ship static export"])
  })

  it("writes a sort into the URL, replacing the history entry", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/tasks")
    const entries = router.history.length

    await user.click(screen.getByRole("columnheader", { name: /Task Name/ }))

    expect(router.state.location.search).toEqual({ sort: "name.asc" })
    expect(router.history.length).toBe(entries)
  })

  it("keeps every typed character of a search, in one history entry", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/tasks")
    const entries = router.history.length

    await user.click(screen.getByRole("button", { name: /search/i }))
    await user.type(screen.getByRole("searchbox"), "launch")

    expect(screen.getByRole("searchbox")).toHaveValue("launch")
    expect(router.state.location.search).toEqual({ q: "launch" })
    expect(router.history.length).toBe(entries)
    expect(firstColumn()).toEqual(["Draft launch copy"])
  })

  it("opens search showing a number typed into the URL", async () => {
    await renderApp("/tasks?q=123")
    expect(screen.getByRole("searchbox")).toHaveValue("123")
  })

  it("restores the filtered view on Back", async () => {
    const { router } = await renderApp("/tasks?status=blocked")
    await act(() => router.navigate({ to: "/ideas" }))
    await act(async () => router.history.back())
    await screen.findByRole("columnheader", { name: /Task Name/ })
    expect(firstColumn()).toEqual(["Ship static export"])
  })

  it("leaves the URL alone for the task list embedded in Home", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/")
    await user.click(screen.getByRole("columnheader", { name: /Task Name/ }))
    expect(router.state.location.search).toEqual({})
  })
})

describe("chip category in the URL", () => {
  it("reads the category, falling back to the default for an unknown one", async () => {
    const { unmount } = await renderApp("/ideas?category=story-pitch")
    expect(screen.getByRole("button", { name: "Story Pitches" })).toHaveAttribute("aria-pressed", "true")
    unmount()

    await renderApp("/ideas?category=bogus")
    expect(screen.getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true")
  })

  it("writes the category, and leaves the default out of the URL", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/ideas")

    await user.click(screen.getByRole("button", { name: "General Ideas" }))
    expect(router.state.location.search).toEqual({ category: "general" })

    await user.click(screen.getByRole("button", { name: "All" }))
    expect(router.state.location.search).toEqual({})
  })

  it("keeps table params when the category changes", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/ideas?sort=title.asc")
    await user.click(screen.getByRole("button", { name: "General Ideas" }))
    expect(router.state.location.search).toEqual({ sort: "title.asc", category: "general" })
  })

  it("ignores a filter for a column the category doesn't have", async () => {
    await renderApp("/projects/the-grid/documents?category=spec&participants=u1")
    expect(screen.getByRole("button", { name: "Spec" })).toHaveAttribute("aria-pressed", "true")
    expect(screen.getAllByRole("row").length).toBeGreaterThan(1)
  })
})
```

- [ ] **Step 2: Run to verify they fail**

Run: `pnpm test src/url-state.test.tsx`
Expected: FAIL. Filters from the URL aren't applied, sorting leaves the URL empty, and the chips ignore `category`. The "embedded in Home" and "ignores a filter" tests may pass already; that's fine.

- [ ] **Step 3: Write the hooks**

Create `src/hooks/use-table-url-state.ts`:

```ts
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import type { DataTableColumn, DataTableProps, TableViewState } from "@/components/ui/data-table"
import {
  searchFromViewState,
  tableSearchKeys,
  viewStateFromSearch,
} from "@/components/ui/data-table/url-state"

/** The current search params minus `drop`, keeping only string values */
function keptSearch(prev: Record<string, unknown>, drop: string[]): Record<string, string> {
  const kept: Record<string, string> = {}
  for (const [key, value] of Object.entries(prev)) {
    if (!drop.includes(key) && typeof value === "string") kept[key] = value
  }
  return kept
}

/**
 * Keeps a DataTable's sort, filters and search in the URL, so a refresh, a
 * copied link or Back restores them. Pass `enabled: false` for a table that
 * shouldn't touch the URL (the task list embedded in Home, say).
 *
 * Changes replace the current history entry: typing in search shouldn't
 * add a Back step per keystroke. The table works from a local copy so
 * typing never waits on the router; the copy follows the URL whenever the
 * URL changes for another reason (Back, Forward, a pasted link).
 */
export function useTableUrlState<T>(
  columns: DataTableColumn<T>[],
  { enabled = true }: { enabled?: boolean } = {},
): Pick<DataTableProps<T>, "viewState" | "onViewStateChange"> {
  const search = useSearch({ strict: false }) as Record<string, unknown>
  const navigate = useNavigate()
  const fromUrl = useMemo(() => viewStateFromSearch(search, columns), [search, columns])
  const urlKey = JSON.stringify(fromUrl)
  const [local, setLocal] = useState(fromUrl)
  // Keys of URL states this hook wrote and hasn't seen arrive yet
  const written = useRef<string[]>([])

  useEffect(() => {
    const index = written.current.indexOf(urlKey)
    if (index !== -1) {
      // Our own write arriving: the local copy is already ahead of it
      written.current = written.current.slice(index + 1)
      return
    }
    setLocal(JSON.parse(urlKey) as TableViewState)
  }, [urlKey])

  const onViewStateChange = useCallback(
    (next: TableViewState) => {
      setLocal(next)
      const nextSearch = searchFromViewState(next, columns)
      written.current.push(JSON.stringify(viewStateFromSearch(nextSearch, columns)))
      const keys = tableSearchKeys(columns)
      void navigate({
        to: ".",
        search: (prev: Record<string, unknown>) => ({ ...keptSearch(prev, keys), ...nextSearch }),
        replace: true,
      })
    },
    [columns, navigate],
  )

  return enabled ? { viewState: local, onViewStateChange } : {}
}

/**
 * A chip category kept in the URL as `?category=`. An unknown value reads
 * as `fallback`, and choosing `fallback` removes the param, so plain links
 * stay clean. Choosing pushes a history entry, so Back returns to the
 * previous category.
 */
export function useCategoryParam<V extends string>(
  allowed: readonly V[],
  fallback: V,
): [V, (next: V) => void] {
  const search = useSearch({ strict: false }) as Record<string, unknown>
  const navigate = useNavigate()
  const raw = search.category
  const value = allowed.includes(raw as V) ? (raw as V) : fallback

  const setValue = useCallback(
    (next: V) =>
      void navigate({
        to: ".",
        search: (prev: Record<string, unknown>) => {
          const kept = keptSearch(prev, ["category"])
          return next === fallback ? kept : { ...kept, category: next }
        },
      }),
    [navigate, fallback],
  )

  return [value, setValue]
}
```

- [ ] **Step 4: Validate search on the table routes**

In `src/router.tsx`, add `import { coerceSearch } from "@/components/ui/data-table/url-state"` and add `validateSearch: coerceSearch,` to five routes: `tasksRoute`, `ideasRoute`, `projectsRoute`, `projectTasksRoute`, `projectDocumentsRoute`. This types their search as `Record<string, string>` and turns `?q=123` into the string `"123"`.

- [ ] **Step 5: Connect the tables**

`src/components/TaskList.tsx`: add `import { useTableUrlState } from "@/hooks/use-table-url-state"`. Add `syncWithUrl = false` to the props (type `syncWithUrl?: boolean`), and document it in the comment above the component: "`syncWithUrl` keeps sort, filters and search in the URL — for the full-page Tasks tables, not the ones embedded in Home and Project Overview." In the body:

```tsx
  const columns = showProject ? COLUMNS_WITH_PROJECT : COLUMNS
  const urlState = useTableUrlState(columns, { enabled: syncWithUrl })
```

and pass `columns={columns}` and `{...urlState}` to `DataTable`.

`src/pages/Tasks.tsx`: `<TaskList showProject showTitleIcon={false} syncWithUrl />`.
`src/pages/ProjectTasks.tsx`: `<TaskList showTitleIcon={false} syncWithUrl />`.

`src/components/ProjectList.tsx`: add the import, `const urlState = useTableUrlState(COLUMNS)` at the top of `ProjectList`, and `{...urlState}` on `DataTable`.

`src/components/IdeaList.tsx`: add `import { useCategoryParam, useTableUrlState } from "@/hooks/use-table-url-state"`. Replace `const [category, setCategory] = useState<Category>("all")` with:

```tsx
  const [category, setCategory] = useCategoryParam(
    CATEGORIES.map((option) => option.value),
    "all",
  )
```

After `columnsByCategory`, add `const urlState = useTableUrlState(columnsByCategory[category])`, and pass `{...urlState}` to `DataTable`. The rest (`showCategoryOf`, the `ChipNav`) keeps calling `setCategory`.

`src/components/DocumentList.tsx`: same pattern:

```tsx
  const [category, setCategory] = useCategoryParam(
    CATEGORIES.map((option) => option.value),
    "spec",
  )
  const urlState = useTableUrlState(COLUMNS_BY_CATEGORY[category])
```

and `{...urlState}` on `DataTable`. Remove `useState` from the React import if nothing else uses it (in both files).

Note: `CATEGORIES.map(...)` creates a new array each render; `useCategoryParam` only reads it with `includes`, so that's fine.

- [ ] **Step 6: Give the remaining list tests a router**

`TaskList` and `DocumentList` now call router hooks. In `src/components/TaskList.test.tsx` and `src/components/DocumentList.test.tsx`: add `import { renderWithRouter } from "@/test/render"`; replace each `render(<TaskList … />)` with `await renderWithRouter(<TaskList … />)` and each `render(<DocumentList />)` with `await renderWithRouter(<DocumentList />, { path: "/projects/the-grid/documents" })`; make callbacks `async`. Any test that renders, unmounts and renders again keeps doing so with the helper.

- [ ] **Step 7: Run everything**

```bash
pnpm typecheck && pnpm test && pnpm lint
```

Expected: all pass, including `src/url-state.test.tsx`.

- [ ] **Step 8: Look at it**

`pnpm dev`, then:
- `/tasks`: sort by Due Date, filter Status to Blocked, search "ship". The URL shows `?sort=due.asc&status=blocked&q=ship` (in some order). Refresh: same view, search box open.
- `/ideas`: click Story Pitches, then General Ideas, then Back: Story Pitches again.
- `/`: sorting the task list doesn't change the URL.

- [ ] **Step 9: Commit**

```bash
git add -A src
git commit -m "Keep table sort, filters, search and chip category in the URL

Full-page tables only; embedded tables keep their own state. Table
changes replace the history entry, chip changes push one.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Verify the whole branch

**Files:** none changed, unless a check fails.

- [ ] **Step 1: All checks**

```bash
pnpm typecheck && pnpm lint && pnpm test && pnpm build
```

Expected: typecheck clean; lint shows only the two existing `cells.tsx` warnings; all tests pass; build succeeds.

- [ ] **Step 2: No leftovers**

```bash
grep -rn '#/' src | grep -v TaskHighlight.test
grep -rn 'export-static\|window.location.hash\|intakeIdeaId\|editIdeaId\|header-scroll.js\|mobile-menu.js' src index.html vite.config.ts package.json
ls public/scripts
```

Expected: both greps print nothing; `ls public/scripts` prints only `project-summary.js` (plan 1b removes it).

- [ ] **Step 3: Deep URLs load on refresh (Review Focus 5)**

```bash
pnpm dev --port 5173 &
sleep 3
for path in /ideas/i1/edit /projects/the-grid/documents /no-such-page; do
  curl -s "http://localhost:5173$path" | grep -c 'id="root"'
done
kill %1

pnpm build && (pnpm preview --port 4173 &) && sleep 2
for path in /ideas/i1/edit /projects/the-grid/documents; do
  curl -s "http://localhost:4173$path" | grep -c 'id="root"'
done
kill %1
```

Expected: every line prints `1`. Vite serves `index.html` for unknown paths in both dev and preview, and the router picks the page (the Go server does the same in production, per spec section 2).

- [ ] **Step 4: Click through every route**

`pnpm dev`, then at a desktop width and again at a phone width (about 400px):
- Home, Tasks, Ideas, New Idea (from Ideas → New Idea, and at `/ideas/new`), Edit Idea (pencil on a draft idea), Idea Intake (title of "Where the glaciers went"), Projects, a project's Overview / Tasks / Documents from the toolbar and from the mobile menu's folder card, and a made-up URL (Not Found).
- Toggle dark mode on a couple of pages (still resets on refresh; plan 4 fixes that).
- If anything looks different, compare against `main` side by side: `git worktree add ../worklode-main main && cd ../worklode-main && npm install && npm run dev -- --port 5174` (`main` still uses npm). Every screen must look the same.

- [ ] **Step 5: Record the result**

If everything passed, there is nothing to commit. If a check failed, fix it in the task it belongs to (with a test), run Step 1 again, and commit the fix with a message naming what broke.
