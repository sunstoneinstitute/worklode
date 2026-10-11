# SPA conversion, plan 1b: project summary card as React state

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port `public/scripts/project-summary.js`, the last vanilla script, to React state inside `ProjectSummary`.

**Architecture:** Split off from plan 1 (`2026-09-24-spa-1-toolchain-routing.md`, was its Task 7) to save time, since Home doesn't use the card. Run it after plan 1 has landed. The card keeps rendering the same data attributes, so no CSS changes.

**Tech Stack:** React 19, Vitest, Testing Library, pnpm.

**Spec:** `docs/superpowers/specs/2026-09-24-react-spa-conversion-design.md`, section 6 ("Vanilla scripts ported to React", the `project-summary.js` row).

## Global Constraints

- Package manager is pnpm (`pnpm@12.6.0`); TypeScript `^7.0.2`, type checking only.
- Every existing screen must look the same: do not change Tailwind classes, `src/index.css`, or the data attributes CSS keys off (`data-project-summary`, `data-summary-view`, `data-view`, `data-crew-row`, `data-crew-panel`, `data-selected`, `data-open`).
- No arbitrary pixel values in `className`s.
- Code style: ES modules, destructured imports, match surrounding comment density.
- Every commit message ends with a blank line and `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
- Work happens on branch `react-spa-conversion`.

## Review Focus

1. **Keyboard focus after switching views:** focus must land on the other view's toggle button, not get lost on a now-hidden button. Tested in Task 1.
2. **First render must not steal focus:** opening Project Overview must not move focus anywhere. Covered by the `moveFocus` ref; add an assertion in Task 1 Step 1's first test that `document.body` still has focus after render.

---

### Task 1: Project summary card as React state

Port `public/scripts/project-summary.js`, the last script. The card renders the same `data-view`, `data-selected`, `data-open` and `aria-expanded` values, so every `group-data-*` variant keeps working.

**Files:**
- Create: `src/components/ProjectSummary.test.tsx`
- Modify: `src/components/ProjectSummary.tsx`, `index.html`
- Delete: `public/scripts/project-summary.js` and the now-empty `public/scripts/`

**Interfaces:**
- Produces: `ProjectSummary` keeps its current props (`name`, `daysRemaining`, `progress`).

- [ ] **Step 1: Write the failing test**

Create `src/components/ProjectSummary.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { ProjectSummary } from "./ProjectSummary"

function setup() {
  const user = userEvent.setup()
  const { container } = render(<ProjectSummary />)
  const card = container.querySelector<HTMLElement>("[data-project-summary]")!
  const rows = () => [...container.querySelectorAll<HTMLElement>("[data-crew-row]")]
  const panels = () => [...container.querySelectorAll<HTMLElement>("[data-crew-panel]")]
  const toggleOf = (row: HTMLElement) => row.querySelector<HTMLElement>("[data-crew-row-toggle]")!
  return { user, card, rows, panels, toggleOf }
}

describe("ProjectSummary", () => {
  it("starts on the summary view, without moving focus", () => {
    const { card } = setup()
    expect(card).not.toHaveAttribute("data-view")
    expect(document.body).toHaveFocus()
  })

  it("switches to the crew view and back, moving focus to the other view's button", async () => {
    const { user, card } = setup()

    await user.click(screen.getByRole("button", { name: /What is the crew working on\?/ }))
    expect(card).toHaveAttribute("data-view", "crew")
    expect(screen.getByRole("button", { name: "Back to project summary" })).toHaveFocus()

    await user.click(screen.getByRole("button", { name: "Back to project summary" }))
    expect(card).not.toHaveAttribute("data-view")
    expect(screen.getByRole("button", { name: /What is the crew working on\?/ })).toHaveFocus()
  })

  it("starts with the first person selected and no row open", () => {
    const { rows, panels, toggleOf } = setup()
    expect(rows()[0]).toHaveAttribute("data-selected")
    expect(panels()[0]).toHaveAttribute("data-selected")
    expect(rows()[1]).not.toHaveAttribute("data-selected")
    for (const row of rows()) {
      expect(row).not.toHaveAttribute("data-open")
      expect(toggleOf(row)).toHaveAttribute("aria-expanded", "false")
    }
  })

  it("clicking a row selects that person and opens their row", async () => {
    const { user, rows, panels, toggleOf } = setup()
    const second = rows()[1]

    await user.click(toggleOf(second))

    expect(second).toHaveAttribute("data-selected")
    expect(second).toHaveAttribute("data-open")
    expect(toggleOf(second)).toHaveAttribute("aria-expanded", "true")
    expect(panels()[1]).toHaveAttribute("data-selected")
    expect(rows()[0]).not.toHaveAttribute("data-selected")
    expect(panels()[0]).not.toHaveAttribute("data-selected")
  })

  it("clicking an open row again closes it but keeps it selected", async () => {
    const { user, rows, toggleOf } = setup()
    const second = rows()[1]

    await user.click(toggleOf(second))
    await user.click(toggleOf(second))

    expect(second).not.toHaveAttribute("data-open")
    expect(toggleOf(second)).toHaveAttribute("aria-expanded", "false")
    expect(second).toHaveAttribute("data-selected")
  })

  it("rows open independently", async () => {
    const { user, rows, toggleOf } = setup()
    await user.click(toggleOf(rows()[0]))
    await user.click(toggleOf(rows()[1]))
    expect(rows()[0]).toHaveAttribute("data-open")
    expect(rows()[1]).toHaveAttribute("data-open")
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm test src/components/ProjectSummary.test.tsx`
Expected: FAIL in the switching and clicking tests (nothing responds to clicks without the script); "starts on the summary view" and "starts with the first person selected" pass.

- [ ] **Step 3: Move the state into the component**

In `src/components/ProjectSummary.tsx`:

Add `import { useEffect, useRef, useState } from "react"` at the top.

Replace `CrewList`'s comment and signature with:

```tsx
// One summary line per person (handle, availability, task count):
// - Desktop: clicking selects that person — their row keeps the darker
//   background (data-selected) and CrewPanels shows their lists. The first
//   person starts selected.
// - Mobile: an accordion — clicking flips data-open on the row, revealing
//   that person's own Past Due Tasks and Awaiting Review underneath.
// One click does both; CSS shows whichever applies at the current width.
function CrewList({
  selectedId,
  openIds,
  onRowClick,
}: {
  selectedId: string
  openIds: ReadonlySet<string>
  onRowClick: (userId: string) => void
}) {
```

Inside its map, drop the `index` parameter and compute `const open = openIds.has(workload.userId)`. On the row `<div>`, replace `data-selected={index === 0 ? "" : undefined}` with `data-selected={workload.userId === selectedId ? "" : undefined}` and add `data-open={open ? "" : undefined}`. On the row's `<button data-crew-row-toggle>`, replace `aria-expanded="false"` with `aria-expanded={open}` and add `onClick={() => onRowClick(workload.userId)}`.

Replace `CrewPanels`'s comment and signature with:

```tsx
// Desktop right-hand column of the crew view: one panel per person, only
// the selected person's showing.
function CrewPanels({ selectedId }: { selectedId: string }) {
```

and in its map drop `index` and use `data-selected={workload.userId === selectedId ? "" : undefined}`.

In `ProjectSummary`, replace the comment above `return` with the state, and wire the buttons:

```tsx
  // Two views in one card: the project summary and "What is everyone
  // working on?". Both are always in the markup; data-view="crew" on the
  // root lets the group-data variants below show one and hide the other.
  const [view, setView] = useState<"summary" | "crew">("summary")
  const [selectedId, setSelectedId] = useState(WORKLOADS[0].userId)
  const [openIds, setOpenIds] = useState<ReadonlySet<string>>(new Set())
  const summaryToggle = useRef<HTMLButtonElement>(null)
  const crewToggle = useRef<HTMLButtonElement>(null)
  // Set by a click, so switching views moves focus but the first render doesn't
  const moveFocus = useRef(false)

  useEffect(() => {
    if (!moveFocus.current) return
    moveFocus.current = false
    // Keep keyboard focus on the equivalent button in the view now showing
    ;(view === "crew" ? crewToggle : summaryToggle).current?.focus()
  }, [view])

  const switchView = () => {
    moveFocus.current = true
    setView((current) => (current === "crew" ? "summary" : "crew"))
  }

  const onRowClick = (userId: string) => {
    setSelectedId(userId)
    setOpenIds((current) => {
      const next = new Set(current)
      if (next.has(userId)) next.delete(userId)
      else next.add(userId)
      return next
    })
  }
```

On the root `<div data-project-summary …>` add `data-view={view === "crew" ? "crew" : undefined}`.

On the summary view's toggle `<Button data-project-summary-toggle …>` add `ref={summaryToggle}` and `onClick={switchView}`. On the crew view's `<Button data-project-summary-toggle …>Back to project summary</Button>` add `ref={crewToggle}` and `onClick={switchView}`.

Change `<CrewList />` to `<CrewList selectedId={selectedId} openIds={openIds} onRowClick={onRowClick} />` and `<CrewPanels />` to `<CrewPanels selectedId={selectedId} />`.

- [ ] **Step 4: Run it to verify it passes**

Run: `pnpm test src/components/ProjectSummary.test.tsx`
Expected: 6 passed. If `ref` on `Button` fails to type-check, check `src/components/ui/button.tsx` takes `React.ComponentProps<"button">` (React 19 passes `ref` as a normal prop); it does in the shadcn template used here.

- [ ] **Step 5: Delete the last script**

In `index.html`, delete `<script src="/scripts/project-summary.js"></script>`. The body is now just `<div id="root"></div>` and the `main.tsx` module script.

```bash
git rm -q public/scripts/project-summary.js
```

Run: `ls public`
Expected: `assets  favicon.svg` (no `scripts`).

- [ ] **Step 6: Run everything and look**

```bash
pnpm typecheck && pnpm test && pnpm lint
```

Then `pnpm dev`, `/projects/the-grid`: on desktop, "What is the crew working on?" switches views and clicking people switches the right-hand panel; on a phone width, rows open as an accordion.

- [ ] **Step 7: Commit**

```bash
git add -A src index.html public
git commit -m "Project summary card as React state

Same data-view / data-selected / data-open attributes; the last vanilla
script is gone.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```
