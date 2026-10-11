# Generic DataTable Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the bespoke hardcoded Tasks table with a generic, schema-driven `DataTable` component that other content types (deliverables, documents) can reuse by declaring a column schema.

**Architecture:** TanStack Table v8 supplies the headless engine (sorting, filtering, column visibility/order). shadcn/ui primitives supply the rendering (`Table`, `Badge`, `Popover`, `Command`, `Calendar`). A thin adapter layer translates our declarative `DataTableColumn[]` schema into TanStack `ColumnDef[]`, so consumers never touch TanStack's API. `TaskList` becomes a thin wrapper that declares a schema plus placeholder data.

**Tech Stack:** React 19, TypeScript, Vite 8, Tailwind CSS v4 (CSS-based `@theme`), shadcn/ui (radix-nova style, `radix-ui` unified package), `@tanstack/react-table` v8, vitest + @testing-library/react.

**Spec:** `docs/superpowers/specs/2026-09-21-data-table-design.md`

## Global Constraints

These apply to every task. They come from `AGENTS.md` and the user's global `CLAUDE.md`.

- **ES modules only** (`import`/`export`), destructure imports where possible (`import { foo } from 'bar'`).
- **No arbitrary pixel values in `className`s.** Tailwind v4 computes numeric spacing utilities dynamically (`gap-2.5`, `w-40`, `min-w-250`) for any N that divides by the 4px base. For values off the numeric scale, use the named `@theme` tokens (`--text-13/15/22`, `--radius-card`, `--radius-panel`, `--spacing-page`, `--spacing-panel`) or add a new named token. Never `text-[14px]`-style brackets.
- **`accent`, `accent-warn`, `accent-success` are NOT Tailwind utilities.** They collide with shadcn's own `--accent` slot, so they are plain CSS variables. Reference them as `var(--color-accent-warn)` etc. in inline styles. `muted` / `muted-foreground` ARE real utilities (`bg-muted`, `text-muted-foreground`) — use those for neutral states.
- **Dark mode is real, not a placeholder.** Every visual addition must be checked in both modes. Do not assume dark mode is "invert everything."
- **Do not hardcode shadcn generic slots.** Figma variables are the source (`--color-text-primary`, `--color-surface`, …); shadcn slots point at them.
- **The static export must keep working.** `npm run export:html` splices standalone-rendered partials into pages by exact string match. See Task 10 — this is the one failure mode in this plan that produces no error message.
- **Default to no code comments.** Only comment a non-obvious *why*.

---

## File Structure

**Created:**

| File | Responsibility |
|---|---|
| `src/test/setup.ts` | vitest + jest-dom global setup |
| `src/components/ui/table.tsx` | shadcn primitive (generated) |
| `src/components/ui/badge.tsx` | shadcn primitive (generated) |
| `src/components/ui/popover.tsx` | shadcn primitive (generated) |
| `src/components/ui/command.tsx` | shadcn primitive (generated) |
| `src/components/ui/calendar.tsx` | shadcn primitive (generated) |
| `src/components/ui/data-table/types.ts` | Schema types only. No React. |
| `src/components/ui/data-table/cells.tsx` | The five cell renderers |
| `src/components/ui/data-table/build-columns.tsx` | Schema → TanStack `ColumnDef[]` adapter |
| `src/components/ui/data-table/data-table.tsx` | The component: table state, header, body |
| `src/components/ui/data-table/toolbar.tsx` | Title, search, per-column filters, customize stub |
| `src/components/ui/data-table/index.ts` | Public exports |
| `src/assets/icons/view-column.svg` | Column customize trigger icon |
| `src/assets/icons/download.svg` | Anticipated action icon |
| `src/assets/icons/preview.svg` | Anticipated action icon |
| `docs/TASKS-BACKEND.md` | Go developer handoff doc |

**Modified:**

| File | Change |
|---|---|
| `package.json` | test deps + `test` script |
| `vite.config.ts` | vitest `test` block |
| `src/components/icons.tsx` | three new icon exports |
| `src/components/TaskList.tsx` | rewritten as a thin `DataTable` wrapper |
| `scripts/export-static.mjs` | strip React `useId` output in `rewriteAssetPaths` |

---

## Task 1: Test infrastructure

Nothing else in this plan can be verified without this. Ends with a passing smoke test.

**Files:**
- Modify: `package.json`
- Modify: `vite.config.ts`
- Create: `src/test/setup.ts`
- Test: `src/test/setup.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `npm test` runs vitest in jsdom with `@testing-library/jest-dom` matchers available globally. Every later task depends on this.

- [ ] **Step 1: Install test dependencies**

```bash
npm install -D vitest@^3 jsdom @testing-library/react @testing-library/dom @testing-library/user-event @testing-library/jest-dom
```

- [ ] **Step 2: Create the setup file**

Create `src/test/setup.ts`:

```ts
import "@testing-library/jest-dom/vitest"
```

- [ ] **Step 3: Add the vitest config block**

Modify `vite.config.ts`. Add the triple-slash reference on line 1 (TypeScript needs it to accept the `test` key) and the `test` block:

```ts
/// <reference types="vitest/config" />
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
  },
})
```

- [ ] **Step 4: Add the test script**

Modify `package.json`, in `"scripts"`, after `"lint"`:

```json
"test": "vitest run",
"test:watch": "vitest",
```

- [ ] **Step 5: Write the smoke test**

Create `src/test/setup.test.ts`:

```ts
import { describe, expect, it } from "vitest"

describe("test infrastructure", () => {
  it("runs in a DOM environment", () => {
    const el = document.createElement("div")
    el.textContent = "ok"
    document.body.appendChild(el)

    expect(document.body).toContainElement(el)
    expect(el).toHaveTextContent("ok")
  })
})
```

The `toContainElement` / `toHaveTextContent` matchers come from jest-dom — if the setup file is wired wrong, this test fails with "not a function", which is exactly what we want it to prove.

- [ ] **Step 6: Run the test**

Run: `npm test`
Expected: PASS, 1 test.

- [ ] **Step 7: Verify typecheck still passes**

Run: `npm run build`
Expected: no TypeScript errors. (If `vite.config.ts` errors on the `test` key, the triple-slash reference in Step 3 is missing or misplaced — it must be the first line.)

- [ ] **Step 8: Commit**

```bash
git add package.json package-lock.json vite.config.ts src/test/
git commit -m "Add vitest + testing-library test infrastructure"
```

---

## Task 2: Dependencies, shadcn primitives, and icons

**Files:**
- Modify: `package.json` (via installers)
- Create: `src/components/ui/{table,badge,popover,command,calendar}.tsx` (generated)
- Create: `src/assets/icons/{view-column,download,preview}.svg`
- Modify: `src/components/icons.tsx`
- Test: `src/components/icons.test.tsx`

**Interfaces:**
- Consumes: Task 1's test runner.
- Produces:
  - `@tanstack/react-table` available for import.
  - shadcn primitives at `@/components/ui/{table,badge,popover,command,calendar}`.
  - `ViewColumnIcon`, `DownloadIcon`, `PreviewIcon` exported from `@/components/icons`, each rendering `<i class="wlicon-{name}">`.

- [ ] **Step 1: Install TanStack Table**

```bash
npm install @tanstack/react-table
```

- [ ] **Step 2: Add the shadcn primitives**

```bash
npx shadcn@latest add table badge popover command calendar
```

This transitively installs `react-day-picker` (for `calendar`) and `cmdk` (for `command`). Accept any overwrite prompt only for files that do not already exist — do NOT let it overwrite `avatar.tsx`, `button.tsx`, or `progress.tsx`.

- [ ] **Step 3: Add the three icon SVGs, normalized**

Every existing icon in `src/assets/icons/` was normalized from Material Symbols' negative-origin viewBox to a standard one. Run this to do the same for all three at once:

```bash
for name in view_column download preview; do
  out="src/assets/icons/$(echo "$name" | tr '_' '-').svg"
  sed -e 's/viewBox="0 -960 960 960"/viewBox="0 0 960 960"/' \
      -e 's|<path|<g transform="translate(0,960)"><path|' \
      -e 's|</svg>|</g></svg>|' \
      "node_modules/@material-symbols/svg-300/outlined/${name}-fill.svg" > "$out"
  echo "wrote $out"
done
```

- [ ] **Step 4: Verify the normalization matches the existing convention**

Run: `head -c 200 src/assets/icons/view-column.svg && echo && head -c 200 src/assets/icons/edit.svg`
Expected: both start with `<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 960 960"><g transform="translate(0,960)"><path`. If `view-column.svg` still shows `0 -960`, the sed did not match — fix before continuing, because the glyph will render upside down or empty.

- [ ] **Step 5: Regenerate the icon font**

```bash
npm run iconfont
```

- [ ] **Step 6: Write the failing test for the new icon exports**

Create `src/components/icons.test.tsx`:

```tsx
import { render } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { DownloadIcon, PreviewIcon, ViewColumnIcon } from "@/components/icons"

describe("new action icons", () => {
  it.each([
    [ViewColumnIcon, "wlicon-view-column"],
    [DownloadIcon, "wlicon-download"],
    [PreviewIcon, "wlicon-preview"],
  ])("renders the right glyph class", (IconComponent, expectedClass) => {
    const { container } = render(<IconComponent />)
    const icon = container.querySelector("i")

    expect(icon).toHaveClass(expectedClass)
    expect(icon).toHaveAttribute("aria-hidden", "true")
  })

  it("applies a custom size as a font-size", () => {
    const { container } = render(<ViewColumnIcon size={20} />)

    expect(container.querySelector("i")).toHaveStyle({ fontSize: "20px" })
  })
})
```

- [ ] **Step 7: Run it to confirm it fails**

Run: `npm test -- icons`
Expected: FAIL — the three icon components are not exported yet.

- [ ] **Step 8: Add the icon exports**

Modify `src/components/icons.tsx`. Append alongside the existing exports, following their exact pattern:

```tsx
export const ViewColumnIcon = (props: Omit<IconProps, "name">) => <Icon name="view-column" {...props} />
export const DownloadIcon = (props: Omit<IconProps, "name">) => <Icon name="download" {...props} />
export const PreviewIcon = (props: Omit<IconProps, "name">) => <Icon name="preview" {...props} />
```

- [ ] **Step 9: Run the test again**

Run: `npm test -- icons`
Expected: PASS, 4 tests.

- [ ] **Step 10: Verify the build**

Run: `npm run build && npm run lint`
Expected: both clean.

- [ ] **Step 11: Commit**

```bash
git add package.json package-lock.json src/components/ui/ src/assets/icons/ public/assets/iconfont/ src/components/icons.tsx src/components/icons.test.tsx
git commit -m "Add TanStack Table, shadcn table primitives, and three action icons"
```

---

## Task 3: Schema types

Pure types, no behaviour. Small, but every later task imports from here, so it lands first and alone.

**Files:**
- Create: `src/components/ui/data-table/types.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: all names below, imported by Tasks 4–9.

- [ ] **Step 1: Write the types**

Create `src/components/ui/data-table/types.ts`:

```ts
import type { ReactNode } from "react"

export type ColumnType = "text" | "select" | "date" | "user" | "actions" | "custom"

export interface SelectOption {
  value: string
  label: string
  /** A CSS custom property name, e.g. "--color-accent-warn". Omit for a neutral badge. */
  colorVar?: string
}

export interface DataTableUser {
  id: string
  name: string
  avatarUrl?: string
}

/** Available to anything that can mutate the table, including the toolbar. */
export interface TableHelpers<T> {
  addRow: (row: T) => void
}

/**
 * Row actions get the table-scoped helpers plus two bound to their own row.
 * `addRow` is what makes a "duplicate" action possible — the consumer passes
 * the whole new row, because only they can mint an id.
 */
export interface RowActionHelpers<T> extends TableHelpers<T> {
  removeRow: () => void
  updateRow: (patch: Partial<T>) => void
}

export interface RowAction<T> {
  id: string
  /** Used as both aria-label and title. */
  label: string
  icon: ReactNode
  onSelect: (row: T, helpers: RowActionHelpers<T>) => void
  variant?: "default" | "destructive"
}

export interface DataTableColumn<T> {
  /**
   * For data-backed types this keys into T. For "actions" and "custom"
   * there is no backing field, so it is just a unique column id.
   */
  key: string
  label: string
  type: ColumnType
  /** Tailwind width utility, e.g. "w-40". */
  width?: string
  /** Required for type "select". */
  options?: SelectOption[]
  /** Required for type "actions". */
  actions?: RowAction<T>[]
  /** Defaults to true, except "actions" which is always false. */
  sortable?: boolean
  /** Defaults to true, except "actions" which is always false. */
  filterable?: boolean
  /** Required for type "custom"; an optional override for any other type. */
  render?: (row: T) => ReactNode
}

export interface DataTableProps<T> {
  columns: DataTableColumn<T>[]
  data: T[]
  getRowId: (row: T) => string
  /** Roster for "user"-type columns. */
  users?: DataTableUser[]
  title?: string
  titleIcon?: ReactNode
  /**
   * Content-type-specific toolbar buttons, e.g. "New Task". Takes a render
   * prop when the button needs to mutate the table — which "New" does.
   */
  toolbarActions?: ReactNode | ((helpers: TableHelpers<T>) => ReactNode)
  /** Fires after any inline edit or row-action mutation. */
  onChange?: (data: T[]) => void
}
```

- [ ] **Step 2: Verify it typechecks**

Run: `npm run build`
Expected: clean. (A types-only file has no test of its own; Tasks 4–9 exercise it.)

- [ ] **Step 3: Commit**

```bash
git add src/components/ui/data-table/types.ts
git commit -m "Add DataTable schema types"
```

---

## Task 4: Text and Select cells

**Files:**
- Create: `src/components/ui/data-table/cells.tsx`
- Test: `src/components/ui/data-table/cells.test.tsx`

**Interfaces:**
- Consumes: `SelectOption` from `./types`; `Badge` from `@/components/ui/badge`; `Popover`, `Command` from shadcn.
- Produces:
  - `TextCell({ value }: { value: unknown })`
  - `SelectCell({ value, options, onChange }: { value: string; options: SelectOption[]; onChange: (next: string) => void })`
  - `badgeStyle(option: SelectOption | undefined)` — returns `{ className, style }` for a badge.

- [ ] **Step 1: Write the failing tests**

Create `src/components/ui/data-table/cells.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { SelectCell, TextCell } from "./cells"
import type { SelectOption } from "./types"

const STATUS_OPTIONS: SelectOption[] = [
  { value: "todo", label: "To Do" },
  { value: "in-progress", label: "In Progress", colorVar: "--color-accent" },
  { value: "done", label: "Done", colorVar: "--color-accent-success" },
]

describe("TextCell", () => {
  it("renders the value", () => {
    render(<TextCell value="Sample task name" />)

    expect(screen.getByText("Sample task name")).toBeInTheDocument()
  })

  it("renders nothing for a nullish value", () => {
    const { container } = render(<TextCell value={undefined} />)

    expect(container).toHaveTextContent("")
  })
})

describe("SelectCell", () => {
  it("renders the label for the current value, not the raw value", () => {
    render(<SelectCell value="in-progress" options={STATUS_OPTIONS} onChange={vi.fn()} />)

    expect(screen.getByRole("button")).toHaveTextContent("In Progress")
    expect(screen.queryByText("in-progress")).not.toBeInTheDocument()
  })

  it("tints the badge using the option's colorVar", () => {
    render(<SelectCell value="done" options={STATUS_OPTIONS} onChange={vi.fn()} />)

    expect(screen.getByRole("button")).toHaveStyle({
      color: "var(--color-accent-success)",
    })
  })

  it("opens a list of every option when clicked", async () => {
    const user = userEvent.setup()
    render(<SelectCell value="todo" options={STATUS_OPTIONS} onChange={vi.fn()} />)

    await user.click(screen.getByRole("button"))

    for (const option of STATUS_OPTIONS) {
      expect(screen.getByRole("option", { name: option.label })).toBeInTheDocument()
    }
  })

  it("calls onChange with the chosen value", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<SelectCell value="todo" options={STATUS_OPTIONS} onChange={onChange} />)

    await user.click(screen.getByRole("button"))
    await user.click(screen.getByRole("option", { name: "Done" }))

    expect(onChange).toHaveBeenCalledWith("done")
  })
})
```

- [ ] **Step 2: Run to confirm failure**

Run: `npm test -- cells`
Expected: FAIL — `./cells` does not exist.

- [ ] **Step 3: Implement the cells**

Create `src/components/ui/data-table/cells.tsx`:

```tsx
import { useState } from "react"
import { Badge } from "@/components/ui/badge"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import type { SelectOption } from "./types"

export function TextCell({ value }: { value: unknown }) {
  if (value === null || value === undefined) return null
  return <>{String(value)}</>
}

export function badgeStyle(option: SelectOption | undefined) {
  if (!option?.colorVar) {
    return { className: "bg-muted text-muted-foreground", style: undefined }
  }
  return {
    className: "",
    style: {
      backgroundColor: `color-mix(in srgb, var(${option.colorVar}) 15%, transparent)`,
      color: `var(${option.colorVar})`,
    },
  }
}

export function SelectCell({
  value,
  options,
  onChange,
}: {
  value: string
  options: SelectOption[]
  onChange: (next: string) => void
}) {
  const [open, setOpen] = useState(false)
  const selected = options.find((option) => option.value === value)
  const { className, style } = badgeStyle(selected)

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" className="cursor-pointer" style={style}>
          <Badge className={className} style={style}>
            {selected?.label ?? value}
          </Badge>
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-44 p-0" align="start">
        <Command>
          <CommandList>
            <CommandEmpty>No options.</CommandEmpty>
            <CommandGroup>
              {options.map((option) => (
                <CommandItem
                  key={option.value}
                  value={option.value}
                  onSelect={() => {
                    onChange(option.value)
                    setOpen(false)
                  }}
                >
                  {option.label}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
```

- [ ] **Step 4: Run the tests**

Run: `npm test -- cells`
Expected: PASS, 6 tests.

If the `getByRole("option")` queries fail, check what role `cmdk`'s `CommandItem` renders in the installed version — adjust the query to match the real accessible role rather than changing the component to satisfy the test.

- [ ] **Step 5: Commit**

```bash
git add src/components/ui/data-table/cells.tsx src/components/ui/data-table/cells.test.tsx
git commit -m "Add TextCell and SelectCell with tinted status badges"
```

---

## Task 5: Date and User cells

**Files:**
- Modify: `src/components/ui/data-table/cells.tsx`
- Modify: `src/components/ui/data-table/cells.test.tsx`

**Interfaces:**
- Consumes: `DataTableUser` from `./types`; `Calendar`, `Avatar` primitives.
- Produces:
  - `formatIsoDate(iso: string): string` — `"2026-09-15"` → `"Sept 15, 2026"`
  - `DateCell({ value, onChange }: { value: string; onChange: (nextIso: string) => void })`
  - `UserCell({ value, users, onChange }: { value: string; users: DataTableUser[]; onChange: (nextUserId: string) => void })`

- [ ] **Step 1: Write the failing tests**

Append to `src/components/ui/data-table/cells.test.tsx`. Add these imports to the existing import block: `DateCell`, `UserCell`, `formatIsoDate` from `./cells`, and `DataTableUser` from `./types`.

```tsx
const USERS: DataTableUser[] = [
  { id: "u1", name: "Claudicia" },
  { id: "u2", name: "Marcus" },
]

describe("formatIsoDate", () => {
  it("formats an ISO date for display", () => {
    expect(formatIsoDate("2026-09-15")).toBe("Sept 15, 2026")
  })

  it("does not shift the day across timezones", () => {
    expect(formatIsoDate("2026-01-01")).toContain("1, 2026")
  })

  it("returns an empty string for an empty value", () => {
    expect(formatIsoDate("")).toBe("")
  })
})

describe("DateCell", () => {
  it("renders the formatted date, not the raw ISO string", () => {
    render(<DateCell value="2026-09-15" onChange={vi.fn()} />)

    expect(screen.getByRole("button")).toHaveTextContent("Sept 15, 2026")
  })

  it("opens a calendar when clicked", async () => {
    const user = userEvent.setup()
    render(<DateCell value="2026-09-15" onChange={vi.fn()} />)

    await user.click(screen.getByRole("button"))

    expect(screen.getByRole("grid")).toBeInTheDocument()
  })

  it("calls onChange with an ISO date when a day is picked", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<DateCell value="2026-09-15" onChange={onChange} />)

    await user.click(screen.getByRole("button"))
    await user.click(screen.getByRole("gridcell", { name: "22" }))

    expect(onChange).toHaveBeenCalledWith("2026-09-22")
  })
})

describe("UserCell", () => {
  it("renders the user's name, not their id", () => {
    render(<UserCell value="u1" users={USERS} onChange={vi.fn()} />)

    expect(screen.getByRole("button")).toHaveTextContent("Claudicia")
    expect(screen.queryByText("u1")).not.toBeInTheDocument()
  })

  it("renders unassigned when the value matches no one", () => {
    render(<UserCell value="" users={USERS} onChange={vi.fn()} />)

    expect(screen.getByRole("button")).toHaveTextContent("Unassigned")
  })

  it("calls onChange with the chosen user id", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<UserCell value="u1" users={USERS} onChange={onChange} />)

    await user.click(screen.getByRole("button"))
    await user.click(screen.getByRole("option", { name: /Marcus/ }))

    expect(onChange).toHaveBeenCalledWith("u2")
  })
})
```

- [ ] **Step 2: Run to confirm failure**

Run: `npm test -- cells`
Expected: FAIL — `DateCell`, `UserCell`, `formatIsoDate` are not exported.

- [ ] **Step 3: Implement the cells**

Append to `src/components/ui/data-table/cells.tsx`. Add to the existing imports: `Avatar`, `AvatarFallback`, `AvatarImage` from `@/components/ui/avatar`; `Calendar` from `@/components/ui/calendar`; `CommandInput` from `@/components/ui/command`; `DataTableUser` from `./types`.

```tsx
const DATE_FORMAT = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  year: "numeric",
  timeZone: "UTC",
})

/** ISO (YYYY-MM-DD) is parsed as UTC; formatting in UTC too keeps the day from shifting. */
export function formatIsoDate(iso: string) {
  if (!iso) return ""
  return DATE_FORMAT.format(new Date(`${iso}T00:00:00Z`))
}

function toIsoDate(date: Date) {
  return [
    date.getFullYear(),
    String(date.getMonth() + 1).padStart(2, "0"),
    String(date.getDate()).padStart(2, "0"),
  ].join("-")
}

export function DateCell({
  value,
  onChange,
}: {
  value: string
  onChange: (nextIso: string) => void
}) {
  const [open, setOpen] = useState(false)

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" className="cursor-pointer text-left">
          {formatIsoDate(value)}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-auto p-0" align="start">
        <Calendar
          mode="single"
          selected={value ? new Date(`${value}T00:00:00Z`) : undefined}
          onSelect={(date) => {
            if (date) onChange(toIsoDate(date))
            setOpen(false)
          }}
        />
      </PopoverContent>
    </Popover>
  )
}

export function UserCell({
  value,
  users,
  onChange,
}: {
  value: string
  users: DataTableUser[]
  onChange: (nextUserId: string) => void
}) {
  const [open, setOpen] = useState(false)
  const selected = users.find((user) => user.id === value)

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" className="flex cursor-pointer items-center gap-2 text-left">
          {selected ? (
            <>
              <Avatar className="size-6">
                {selected.avatarUrl ? <AvatarImage src={selected.avatarUrl} alt="" /> : null}
                <AvatarFallback>{selected.name.slice(0, 1)}</AvatarFallback>
              </Avatar>
              {selected.name}
            </>
          ) : (
            "Unassigned"
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-56 p-0" align="start">
        <Command>
          <CommandInput placeholder="Find someone..." />
          <CommandList>
            <CommandEmpty>No one found.</CommandEmpty>
            <CommandGroup>
              {users.map((user) => (
                <CommandItem
                  key={user.id}
                  value={user.name}
                  onSelect={() => {
                    onChange(user.id)
                    setOpen(false)
                  }}
                >
                  {user.name}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
```

- [ ] **Step 4: Run the tests**

Run: `npm test -- cells`
Expected: PASS, 15 tests total in this file.

Note on the `formatIsoDate` test: `Intl` renders September as `"Sep"` in some ICU versions and `"Sept"` in others. If the assertion fails on that alone, change the test's expected string to whatever the runtime actually produces — the behaviour under test is "formatted, not raw ISO", not the specific abbreviation.

- [ ] **Step 5: Commit**

```bash
git add src/components/ui/data-table/cells.tsx src/components/ui/data-table/cells.test.tsx
git commit -m "Add DateCell with calendar picker and UserCell with roster picker"
```

---

## Task 6: Actions cell

**Files:**
- Modify: `src/components/ui/data-table/cells.tsx`
- Modify: `src/components/ui/data-table/cells.test.tsx`

**Interfaces:**
- Consumes: `RowAction`, `RowActionHelpers` from `./types`.
- Produces: `ActionsCell<T>({ row, actions, helpers }: { row: T; actions: RowAction<T>[]; helpers: RowActionHelpers<T> })`

- [ ] **Step 1: Write the failing tests**

Append to `src/components/ui/data-table/cells.test.tsx`. Add `ActionsCell` to the `./cells` import and `RowAction` to the `./types` import.

```tsx
describe("ActionsCell", () => {
  type Row = { id: string; name: string }
  const row: Row = { id: "r1", name: "Sample" }

  function makeHelpers() {
    return { addRow: vi.fn(), removeRow: vi.fn(), updateRow: vi.fn() }
  }

  it("renders one button per declared action, with its label", () => {
    const actions: RowAction<Row>[] = [
      { id: "edit", label: "Edit task", icon: <span>E</span>, onSelect: vi.fn() },
      { id: "delete", label: "Delete task", icon: <span>D</span>, onSelect: vi.fn() },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={makeHelpers()} />)

    expect(screen.getByRole("button", { name: "Edit task" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Delete task" })).toBeInTheDocument()
  })

  it("renders a content type's own action set, not a hardcoded edit/delete pair", () => {
    const actions: RowAction<Row>[] = [
      { id: "download", label: "Download", icon: <span>v</span>, onSelect: vi.fn() },
      { id: "review", label: "Review", icon: <span>r</span>, onSelect: vi.fn() },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={makeHelpers()} />)

    expect(screen.getByRole("button", { name: "Download" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Review" })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /edit/i })).not.toBeInTheDocument()
  })

  it("passes the row and the helpers to onSelect", async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    const helpers = makeHelpers()
    const actions: RowAction<Row>[] = [
      { id: "edit", label: "Edit task", icon: <span>E</span>, onSelect },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={helpers} />)

    await user.click(screen.getByRole("button", { name: "Edit task" }))

    expect(onSelect).toHaveBeenCalledWith(row, helpers)
  })

  it("lets a delete action remove its own row through the helpers", async () => {
    const user = userEvent.setup()
    const helpers = makeHelpers()
    const actions: RowAction<Row>[] = [
      {
        id: "delete",
        label: "Delete task",
        icon: <span>D</span>,
        variant: "destructive",
        onSelect: (_row, h) => h.removeRow(),
      },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={helpers} />)

    await user.click(screen.getByRole("button", { name: "Delete task" }))

    expect(helpers.removeRow).toHaveBeenCalledTimes(1)
  })

  it("lets a duplicate action add a new row through the helpers", async () => {
    const user = userEvent.setup()
    const helpers = makeHelpers()
    const actions: RowAction<Row>[] = [
      {
        id: "duplicate",
        label: "Duplicate task",
        icon: <span>+</span>,
        onSelect: (original, h) => h.addRow({ ...original, id: "r2" }),
      },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={helpers} />)

    await user.click(screen.getByRole("button", { name: "Duplicate task" }))

    expect(helpers.addRow).toHaveBeenCalledWith({ id: "r2", name: "Sample" })
  })
})
```

- [ ] **Step 2: Run to confirm failure**

Run: `npm test -- cells`
Expected: FAIL — `ActionsCell` is not exported.

- [ ] **Step 3: Implement it**

Append to `src/components/ui/data-table/cells.tsx` (add `RowAction`, `RowActionHelpers` to the `./types` import):

```tsx
export function ActionsCell<T>({
  row,
  actions,
  helpers,
}: {
  row: T
  actions: RowAction<T>[]
  helpers: RowActionHelpers<T>
}) {
  return (
    <div className="flex items-center justify-end gap-2.5">
      {actions.map((action) => (
        <button
          key={action.id}
          type="button"
          aria-label={action.label}
          title={action.label}
          onClick={() => action.onSelect(row, helpers)}
          className={
            action.variant === "destructive"
              ? "cursor-pointer text-[var(--color-accent-warn)]"
              : "cursor-pointer text-text-primary"
          }
        >
          {action.icon}
        </button>
      ))}
    </div>
  )
}
```

- [ ] **Step 4: Run the tests**

Run: `npm test -- cells`
Expected: PASS, 20 tests total in this file.

- [ ] **Step 5: Commit**

```bash
git add src/components/ui/data-table/cells.tsx src/components/ui/data-table/cells.test.tsx
git commit -m "Add ActionsCell with per-content-type row actions"
```

---

## Task 7: Schema-to-ColumnDef adapter

The adapter is where the date-sorting regression from the spec is prevented, so it gets its own task and its own tests.

**Files:**
- Create: `src/components/ui/data-table/build-columns.tsx`
- Test: `src/components/ui/data-table/build-columns.test.tsx`

**Interfaces:**
- Consumes: `./types`, `./cells`.
- Produces:

```ts
interface BuildColumnsContext<T> {
  users: DataTableUser[]
  getRowId: (row: T) => string
  updateRow: (rowId: string, patch: Partial<T>) => void
  removeRow: (rowId: string) => void
  addRow: (row: T) => void
}
function buildColumns<T>(columns: DataTableColumn<T>[], ctx: BuildColumnsContext<T>): ColumnDef<T>[]
```

- [ ] **Step 1: Write the failing tests**

Create `src/components/ui/data-table/build-columns.test.tsx`:

```tsx
import type { ReactNode } from "react"
import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { buildColumns } from "./build-columns"
import type { DataTableColumn } from "./types"

type Row = { id: string; name: string; due: string; status: string }

const ctx = {
  users: [],
  getRowId: (row: Row) => row.id,
  updateRow: vi.fn(),
  removeRow: vi.fn(),
  addRow: vi.fn(),
}

describe("buildColumns", () => {
  it("maps each schema column to a column def, preserving order", () => {
    const schema: DataTableColumn<Row>[] = [
      { key: "name", label: "Task Name", type: "text" },
      { key: "due", label: "Due Date", type: "date" },
    ]

    const defs = buildColumns(schema, ctx)

    expect(defs.map((def) => def.id)).toEqual(["name", "due"])
    expect(defs.map((def) => def.header)).toEqual(["Task Name", "Due Date"])
  })

  it("disables sorting and filtering on an actions column", () => {
    const schema: DataTableColumn<Row>[] = [
      { key: "actions", label: "", type: "actions", actions: [] },
    ]

    const [def] = buildColumns(schema, ctx)

    expect(def.enableSorting).toBe(false)
    expect(def.enableColumnFilter).toBe(false)
  })

  it("respects an explicit sortable: false on a data column", () => {
    const schema: DataTableColumn<Row>[] = [
      { key: "name", label: "Task Name", type: "text", sortable: false },
    ]

    expect(buildColumns(schema, ctx)[0].enableSorting).toBe(false)
  })

  it("sorts data columns by default", () => {
    const schema: DataTableColumn<Row>[] = [{ key: "name", label: "Name", type: "text" }]

    expect(buildColumns(schema, ctx)[0].enableSorting).toBe(true)
  })

  it("uses a custom column's own render function", () => {
    const schema: DataTableColumn<Row>[] = [
      {
        key: "badge",
        label: "Badge",
        type: "custom",
        render: (row) => <span>custom:{row.name}</span>,
      },
    ]

    const [def] = buildColumns(schema, ctx)
    const cell = def.cell as (ctx: { row: { original: Row } }) => ReactNode
    render(<>{cell({ row: { original: { id: "r1", name: "Ada", due: "", status: "" } } })}</>)

    expect(screen.getByText("custom:Ada")).toBeInTheDocument()
  })

  it("lets render override a built-in type", () => {
    const schema: DataTableColumn<Row>[] = [
      { key: "status", label: "Status", type: "select", options: [], render: () => <span>overridden</span> },
    ]

    const [def] = buildColumns(schema, ctx)
    const cell = def.cell as (ctx: { row: { original: Row } }) => ReactNode
    render(<>{cell({ row: { original: { id: "r1", name: "Ada", due: "", status: "todo" } } })}</>)

    expect(screen.getByText("overridden")).toBeInTheDocument()
  })
})

describe("date column sorting", () => {
  // The bug this guards: "Oct 2, 2026" sorts BEFORE "Sept 15, 2026"
  // lexicographically. ISO values must compare chronologically.
  function sortDates(values: string[]) {
    const schema: DataTableColumn<Row>[] = [{ key: "due", label: "Due", type: "date" }]
    const [def] = buildColumns(schema, ctx)
    const sortingFn = def.sortingFn as (a: unknown, b: unknown, id: string) => number

    const rows = values.map((due) => ({ getValue: () => due }))
    return [...rows].sort((a, b) => sortingFn(a, b, "due")).map((row) => row.getValue())
  }

  it("orders September before October", () => {
    expect(sortDates(["2026-10-02", "2026-09-15"])).toEqual(["2026-09-15", "2026-10-02"])
  })

  it("orders across year boundaries", () => {
    expect(sortDates(["2027-01-05", "2026-12-30"])).toEqual(["2026-12-30", "2027-01-05"])
  })

  it("puts empty dates last", () => {
    expect(sortDates(["", "2026-09-15"])).toEqual(["2026-09-15", ""])
  })
})
```

- [ ] **Step 2: Run to confirm failure**

Run: `npm test -- build-columns`
Expected: FAIL — `./build-columns` does not exist.

- [ ] **Step 3: Implement the adapter**

Create `src/components/ui/data-table/build-columns.tsx`:

```tsx
import type { ColumnDef, Row } from "@tanstack/react-table"
import { ActionsCell, DateCell, SelectCell, TextCell, UserCell, formatIsoDate } from "./cells"
import type { DataTableColumn, DataTableUser } from "./types"

export interface BuildColumnsContext<T> {
  users: DataTableUser[]
  getRowId: (row: T) => string
  updateRow: (rowId: string, patch: Partial<T>) => void
  removeRow: (rowId: string) => void
  addRow: (row: T) => void
}

function compareIsoDates<T>(a: Row<T>, b: Row<T>, columnId: string) {
  const left = a.getValue<string>(columnId)
  const right = b.getValue<string>(columnId)
  if (!left) return right ? 1 : 0
  if (!right) return -1
  return left < right ? -1 : left > right ? 1 : 0
}

export function buildColumns<T>(
  columns: DataTableColumn<T>[],
  ctx: BuildColumnsContext<T>,
): ColumnDef<T>[] {
  return columns.map((column) => {
    const isActions = column.type === "actions"
    const base = {
      id: column.key,
      header: column.label,
      accessorFn: (row: T) => (row as Record<string, unknown>)[column.key],
      enableSorting: isActions ? false : column.sortable !== false,
      enableColumnFilter: isActions ? false : column.filterable !== false,
      meta: { column },
    } satisfies Partial<ColumnDef<T>> & Record<string, unknown>

    if (column.render) {
      return { ...base, cell: ({ row }) => column.render?.(row.original) } as ColumnDef<T>
    }

    switch (column.type) {
      case "select":
        return {
          ...base,
          cell: ({ row }) => (
            <SelectCell
              value={row.getValue<string>(column.key) ?? ""}
              options={column.options ?? []}
              onChange={(next) =>
                ctx.updateRow(ctx.getRowId(row.original), {
                  [column.key]: next,
                } as Partial<T>)
              }
            />
          ),
        } as ColumnDef<T>

      case "date":
        return {
          ...base,
          sortingFn: compareIsoDates,
          // Filter against the displayed text, which is what the user sees and types.
          filterFn: (row, columnId, filterValue: string) =>
            formatIsoDate(row.getValue<string>(columnId) ?? "")
              .toLowerCase()
              .includes(filterValue.toLowerCase()),
          cell: ({ row }) => (
            <DateCell
              value={row.getValue<string>(column.key) ?? ""}
              onChange={(next) =>
                ctx.updateRow(ctx.getRowId(row.original), {
                  [column.key]: next,
                } as Partial<T>)
              }
            />
          ),
        } as ColumnDef<T>

      case "user":
        return {
          ...base,
          // Sort and filter by the person's name, never their opaque id.
          sortingFn: (a, b, columnId) => {
            const name = (id: string) => ctx.users.find((u) => u.id === id)?.name ?? ""
            return name(a.getValue(columnId)).localeCompare(name(b.getValue(columnId)))
          },
          filterFn: (row, columnId, filterValue: string) => {
            const id = row.getValue<string>(columnId)
            const name = ctx.users.find((u) => u.id === id)?.name ?? ""
            return name.toLowerCase().includes(filterValue.toLowerCase())
          },
          cell: ({ row }) => (
            <UserCell
              value={row.getValue<string>(column.key) ?? ""}
              users={ctx.users}
              onChange={(next) =>
                ctx.updateRow(ctx.getRowId(row.original), {
                  [column.key]: next,
                } as Partial<T>)
              }
            />
          ),
        } as ColumnDef<T>

      case "actions":
        return {
          ...base,
          accessorFn: undefined,
          cell: ({ row }) => {
            const rowId = ctx.getRowId(row.original)
            return (
              <ActionsCell
                row={row.original}
                actions={column.actions ?? []}
                helpers={{
                  addRow: ctx.addRow,
                  removeRow: () => ctx.removeRow(rowId),
                  updateRow: (patch) => ctx.updateRow(rowId, patch),
                }}
              />
            )
          },
        } as ColumnDef<T>

      default:
        return {
          ...base,
          cell: ({ row }) => <TextCell value={row.getValue(column.key)} />,
        } as ColumnDef<T>
    }
  })
}
```

- [ ] **Step 4: Run the tests**

Run: `npm test -- build-columns`
Expected: PASS, 9 tests (6 in the `buildColumns` describe block, 3 in `date column sorting`).

- [ ] **Step 5: Verify typecheck**

Run: `npm run build`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add src/components/ui/data-table/build-columns.tsx src/components/ui/data-table/build-columns.test.tsx
git commit -m "Add schema-to-ColumnDef adapter with chronological date sorting"
```

---

## Task 8: The DataTable component

**Files:**
- Create: `src/components/ui/data-table/data-table.tsx`
- Create: `src/components/ui/data-table/index.ts`
- Test: `src/components/ui/data-table/data-table.test.tsx`

**Interfaces:**
- Consumes: `./types`, `./build-columns`, `@/components/ui/table`, `SwapVerticalIcon` from `@/components/icons`.
- Produces: `DataTable<T>(props: DataTableProps<T>)`, re-exported with all types from `./index`.

- [ ] **Step 1: Write the failing tests**

Create `src/components/ui/data-table/data-table.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { DataTable } from "./data-table"
import type { DataTableColumn, DataTableProps } from "./types"

type Task = { id: string; name: string; due: string; status: string; assignee: string }

const USERS = [
  { id: "u1", name: "Claudicia" },
  { id: "u2", name: "Marcus" },
]

const TASKS: Task[] = [
  { id: "t1", name: "Write copy", due: "2026-10-02", status: "todo", assignee: "u1" },
  { id: "t2", name: "Audit icons", due: "2026-09-15", status: "done", assignee: "u2" },
  { id: "t3", name: "Ship export", due: "2026-11-20", status: "todo", assignee: "u1" },
]

const COLUMNS: DataTableColumn<Task>[] = [
  { key: "name", label: "Task Name", type: "text" },
  { key: "assignee", label: "Assignee", type: "user" },
  { key: "due", label: "Due Date", type: "date" },
  {
    key: "status",
    label: "Status",
    type: "select",
    options: [
      { value: "todo", label: "To Do" },
      { value: "done", label: "Done", colorVar: "--color-accent-success" },
    ],
  },
]

function renderTable(overrides: Partial<DataTableProps<Task>> = {}) {
  return render(
    <DataTable
      columns={COLUMNS}
      data={TASKS}
      getRowId={(task) => task.id}
      users={USERS}
      {...overrides}
    />,
  )
}

function rowNames() {
  const [, ...bodyRows] = screen.getAllByRole("row")
  return bodyRows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("DataTable rendering", () => {
  it("renders a header per schema column", () => {
    renderTable()

    for (const column of COLUMNS) {
      expect(screen.getByRole("columnheader", { name: new RegExp(column.label) })).toBeInTheDocument()
    }
  })

  it("renders a row per data item", () => {
    renderTable()

    expect(rowNames()).toEqual(["Write copy", "Audit icons", "Ship export"])
  })

  it("resolves user ids to names", () => {
    renderTable()

    expect(screen.getAllByText("Claudicia")).toHaveLength(2)
  })
})

describe("sorting", () => {
  it("sorts ascending on first header click", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("columnheader", { name: /Task Name/ }))

    expect(rowNames()).toEqual(["Audit icons", "Ship export", "Write copy"])
  })

  it("sorts descending on second click", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Task Name/ })
    await user.click(header)
    await user.click(header)

    expect(rowNames()).toEqual(["Write copy", "Ship export", "Audit icons"])
  })

  it("clears sorting on third click", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Task Name/ })
    await user.click(header)
    await user.click(header)
    await user.click(header)

    expect(rowNames()).toEqual(["Write copy", "Audit icons", "Ship export"])
  })

  it("sorts dates chronologically, not lexicographically", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("columnheader", { name: /Due Date/ }))

    // Sept before Oct before Nov — the string sort would put Nov first.
    expect(rowNames()).toEqual(["Audit icons", "Write copy", "Ship export"])
  })

  it("does not sort an actions column", async () => {
    const user = userEvent.setup()
    renderTable({
      columns: [
        ...COLUMNS,
        { key: "actions", label: "Actions", type: "actions", actions: [] },
      ],
    })

    await user.click(screen.getByRole("columnheader", { name: /Actions/ }))

    expect(rowNames()).toEqual(["Write copy", "Audit icons", "Ship export"])
  })
})

describe("inline editing", () => {
  it("updates the row and fires onChange when a select value changes", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderTable({ onChange })

    const [firstRow] = screen.getAllByRole("row").slice(1)
    await user.click(within(firstRow).getByRole("button", { name: "To Do" }))
    await user.click(screen.getByRole("option", { name: "Done" }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange.mock.calls[0][0]).toContainEqual(
      expect.objectContaining({ id: "t1", status: "done" }),
    )
  })

  it("leaves other rows untouched", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderTable({ onChange })

    const [firstRow] = screen.getAllByRole("row").slice(1)
    await user.click(within(firstRow).getByRole("button", { name: "To Do" }))
    await user.click(screen.getByRole("option", { name: "Done" }))

    expect(onChange.mock.calls[0][0]).toContainEqual(
      expect.objectContaining({ id: "t2", status: "done" }),
    )
    expect(onChange.mock.calls[0][0]).toContainEqual(
      expect.objectContaining({ id: "t3", status: "todo" }),
    )
  })
})

describe("row actions", () => {
  it("removes the row a delete action was invoked on", async () => {
    const user = userEvent.setup()
    renderTable({
      columns: [
        ...COLUMNS,
        {
          key: "actions",
          label: "Actions",
          type: "actions",
          actions: [
            {
              id: "delete",
              label: "Delete",
              icon: <span>x</span>,
              onSelect: (_row, { removeRow }) => removeRow(),
            },
          ],
        },
      ],
    })

    const [firstRow] = screen.getAllByRole("row").slice(1)
    await user.click(within(firstRow).getByRole("button", { name: "Delete" }))

    expect(rowNames()).toEqual(["Audit icons", "Ship export"])
  })
})
```

- [ ] **Step 2: Run to confirm failure**

Run: `npm test -- data-table`
Expected: FAIL — `./data-table` does not exist.

- [ ] **Step 3: Implement the component**

Create `src/components/ui/data-table/data-table.tsx`:

```tsx
import { useCallback, useMemo, useState } from "react"
import {
  type ColumnFiltersState,
  type SortingState,
  type VisibilityState,
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table"
import { SwapVerticalIcon } from "@/components/icons"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { buildColumns } from "./build-columns"
import type { DataTableProps } from "./types"

export function DataTable<T>({
  columns,
  data,
  getRowId,
  users = [],
  onChange,
}: DataTableProps<T>) {
  const [rows, setRows] = useState(data)
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const [globalFilter, setGlobalFilter] = useState("")
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})
  const [columnOrder, setColumnOrder] = useState<string[]>([])

  const commit = useCallback(
    (next: T[]) => {
      setRows(next)
      onChange?.(next)
    },
    [onChange],
  )

  const updateRow = useCallback(
    (rowId: string, patch: Partial<T>) =>
      commit(rows.map((row) => (getRowId(row) === rowId ? { ...row, ...patch } : row))),
    [commit, rows, getRowId],
  )

  const removeRow = useCallback(
    (rowId: string) => commit(rows.filter((row) => getRowId(row) !== rowId)),
    [commit, rows, getRowId],
  )

  const addRow = useCallback((row: T) => commit([...rows, row]), [commit, rows])

  const columnDefs = useMemo(
    () => buildColumns(columns, { users, getRowId, updateRow, removeRow, addRow }),
    [columns, users, getRowId, updateRow, removeRow, addRow],
  )

  const table = useReactTable({
    data: rows,
    columns: columnDefs,
    state: { sorting, columnFilters, globalFilter, columnVisibility, columnOrder },
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    onGlobalFilterChange: setGlobalFilter,
    onColumnVisibilityChange: setColumnVisibility,
    onColumnOrderChange: setColumnOrder,
    getRowId: (row) => getRowId(row),
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
  })

  return (
    <div className="w-full overflow-x-auto rounded-lg">
      <Table className="w-full min-w-250">
        <TableHeader>
          {table.getHeaderGroups().map((headerGroup) => (
            <TableRow key={headerGroup.id} className="border-b border-text-primary">
              {headerGroup.headers.map((header) => {
                const canSort = header.column.getCanSort()
                return (
                  <TableHead
                    key={header.id}
                    onClick={canSort ? header.column.getToggleSortingHandler() : undefined}
                    aria-sort={
                      header.column.getIsSorted() === "asc"
                        ? "ascending"
                        : header.column.getIsSorted() === "desc"
                          ? "descending"
                          : "none"
                    }
                    className={canSort ? "cursor-pointer select-none" : undefined}
                  >
                    <span className="flex items-center gap-2.5 text-sm font-semibold text-text-primary">
                      {flexRender(header.column.columnDef.header, header.getContext())}
                      {canSort ? <SwapVerticalIcon size={20} /> : null}
                    </span>
                  </TableHead>
                )
              })}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {table.getRowModel().rows.map((row) => (
            <TableRow key={row.id} className="border-b border-divider-line/40">
              {row.getVisibleCells().map((cell) => (
                <TableCell key={cell.id} className="px-4 py-3.5 text-sm text-text-primary">
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
```

The `columnVisibility` / `columnOrder` / `globalFilter` state is wired now but has no UI yet — Task 9 adds the toolbar that drives it. Leaving the state here means Task 9 does not have to restructure this component.

- [ ] **Step 4: Run the tests**

Run: `npm test -- data-table`
Expected: PASS, 11 tests.

If the "clears sorting on third click" test fails, TanStack's `enableSortingRemoval` defaults to `true`; confirm nothing is overriding it.

- [ ] **Step 5: Add the public export barrel**

Create `src/components/ui/data-table/index.ts`:

```ts
export { DataTable } from "./data-table"
export type {
  ColumnType,
  DataTableColumn,
  DataTableProps,
  DataTableUser,
  RowAction,
  RowActionHelpers,
  SelectOption,
  TableHelpers,
} from "./types"
```

- [ ] **Step 6: Verify build and lint**

Run: `npm run build && npm run lint && npm test`
Expected: all clean.

- [ ] **Step 7: Commit**

```bash
git add src/components/ui/data-table/
git commit -m "Add DataTable component with schema-driven rendering and sorting"
```

---

## Task 9: Toolbar — title, search, per-column filters, customize stub

**Files:**
- Create: `src/components/ui/data-table/toolbar.tsx`
- Modify: `src/components/ui/data-table/data-table.tsx`
- Test: `src/components/ui/data-table/toolbar.test.tsx`

**Interfaces:**
- Consumes: `Table` instance type from `@tanstack/react-table`, `SearchIcon`/`ViewColumnIcon` from `@/components/icons`.
- Produces: `DataTableToolbar<T>({ table, title, titleIcon, toolbarActions, addRow })`, rendered by `DataTable` above the table. It resolves `toolbarActions` whether it is a plain node or a render prop taking `TableHelpers<T>`. Per-column filter triggers render inside each header (in `data-table.tsx`).

Per the spec's layout note: filters are **icon-triggered popovers**, not always-visible inline inputs. The columns are `w-35`/`w-40` inside an already-1000px-wide table; inline inputs would crowd the headers and push the table wider.

- [ ] **Step 1: Write the failing tests**

Create `src/components/ui/data-table/toolbar.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { DataTable } from "./data-table"
import type { DataTableColumn } from "./types"

type Task = { id: string; name: string; status: string }

const TASKS: Task[] = [
  { id: "t1", name: "Write copy", status: "todo" },
  { id: "t2", name: "Audit icons", status: "done" },
  { id: "t3", name: "Ship export", status: "todo" },
]

const COLUMNS: DataTableColumn<Task>[] = [
  { key: "name", label: "Task Name", type: "text" },
  {
    key: "status",
    label: "Status",
    type: "select",
    options: [
      { value: "todo", label: "To Do" },
      { value: "done", label: "Done" },
    ],
  },
]

function renderTable() {
  return render(
    <DataTable
      columns={COLUMNS}
      data={TASKS}
      getRowId={(task) => task.id}
      title="Tasks"
      toolbarActions={<button type="button">New Task</button>}
    />,
  )
}

function rowNames() {
  const [, ...bodyRows] = screen.getAllByRole("row")
  return bodyRows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("toolbar", () => {
  it("renders the title and the consumer's own toolbar actions", () => {
    renderTable()

    expect(screen.getByRole("heading", { name: "Tasks" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "New Task" })).toBeInTheDocument()
  })

  it("filters rows as you type in the search box", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("button", { name: /search/i }))
    await user.type(screen.getByRole("searchbox"), "icons")

    expect(rowNames()).toEqual(["Audit icons"])
  })

  it("shows every row again when the search is cleared", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("button", { name: /search/i }))
    await user.type(screen.getByRole("searchbox"), "icons")
    await user.clear(screen.getByRole("searchbox"))

    expect(rowNames()).toHaveLength(3)
  })

  it("renders a column-customize trigger", () => {
    renderTable()

    expect(screen.getByRole("button", { name: /columns/i })).toBeInTheDocument()
  })
})

describe("per-column filters", () => {
  it("narrows rows to a chosen select value", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Status/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(screen.getByRole("option", { name: "To Do" }))

    expect(rowNames()).toEqual(["Write copy", "Ship export"])
  })

  it("narrows rows by typed text on a text column", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Task Name/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.type(screen.getByRole("textbox"), "ship")

    expect(rowNames()).toEqual(["Ship export"])
  })

  it("does not offer a filter on an actions column", () => {
    render(
      <DataTable
        columns={[...COLUMNS, { key: "actions", label: "Actions", type: "actions", actions: [] }]}
        data={TASKS}
        getRowId={(task) => task.id}
      />,
    )

    const header = screen.getByRole("columnheader", { name: /Actions/ })

    expect(within(header).queryByRole("button", { name: /filter/i })).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run to confirm failure**

Run: `npm test -- toolbar`
Expected: FAIL — no toolbar is rendered.

- [ ] **Step 3: Implement the toolbar**

Create `src/components/ui/data-table/toolbar.tsx`:

```tsx
import { type ReactNode, useState } from "react"
import type { Table } from "@tanstack/react-table"
import { SearchIcon, ViewColumnIcon } from "@/components/icons"

export function DataTableToolbar<T>({
  table,
  title,
  titleIcon,
  toolbarActions,
  addRow,
}: {
  table: Table<T>
  title?: string
  titleIcon?: ReactNode
  toolbarActions?: ReactNode | ((helpers: TableHelpers<T>) => ReactNode)
  addRow: (row: T) => void
}) {
  const [searchOpen, setSearchOpen] = useState(false)
  const actions =
    typeof toolbarActions === "function" ? toolbarActions({ addRow }) : toolbarActions

  return (
    <div className="flex w-full items-center gap-4">
      <div className="flex flex-1 items-center gap-5">
        {title ? (
          <div className="flex items-center gap-2.5">
            {titleIcon}
            <h2 className="text-xl text-text-primary md:text-22">{title}</h2>
          </div>
        ) : null}

        {searchOpen ? (
          <input
            type="search"
            autoFocus
            aria-label="Search"
            placeholder="Search..."
            value={table.getState().globalFilter ?? ""}
            onChange={(event) => table.setGlobalFilter(event.target.value)}
            onBlur={(event) => {
              if (!event.target.value) setSearchOpen(false)
            }}
            className="w-50 border-b border-divider-line bg-transparent py-1 text-sm text-text-primary outline-none"
          />
        ) : (
          <button
            type="button"
            aria-label="Search"
            onClick={() => setSearchOpen(true)}
            className="flex cursor-pointer items-center text-text-primary"
          >
            <SearchIcon size={24} />
          </button>
        )}

        <button
          type="button"
          aria-label="Configure columns"
          title="Configure columns"
          className="flex cursor-pointer items-center text-text-primary"
        >
          <ViewColumnIcon size={24} />
        </button>
      </div>

      {actions}
    </div>
  )
}
```

Add `TableHelpers` to the `./types` import in this file.

The columns button is deliberately inert — its popover (show/hide plus reorder) is a deferred follow-up. The state it will drive (`columnVisibility`, `columnOrder`) is already wired in `data-table.tsx`.

- [ ] **Step 4: Add the header filter control**

Create the filter trigger in the same file, `src/components/ui/data-table/toolbar.tsx`:

```tsx
import type { Column } from "@tanstack/react-table"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import type { DataTableColumn } from "./types"

export function ColumnFilter<T>({
  column,
  schema,
}: {
  column: Column<T, unknown>
  schema: DataTableColumn<T>
}) {
  const [open, setOpen] = useState(false)
  const active = column.getFilterValue() != null && column.getFilterValue() !== ""

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`Filter ${schema.label}`}
          onClick={(event) => event.stopPropagation()}
          className={active ? "cursor-pointer text-[var(--color-accent)]" : "cursor-pointer text-text-primary"}
        >
          <SearchIcon size={16} />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-48 p-0" align="start" onClick={(e) => e.stopPropagation()}>
        {schema.type === "select" ? (
          <Command>
            <CommandList>
              <CommandEmpty>No options.</CommandEmpty>
              <CommandGroup>
                <CommandItem
                  value="__all__"
                  onSelect={() => {
                    column.setFilterValue(undefined)
                    setOpen(false)
                  }}
                >
                  All
                </CommandItem>
                {(schema.options ?? []).map((option) => (
                  <CommandItem
                    key={option.value}
                    value={option.value}
                    onSelect={() => {
                      column.setFilterValue(option.value)
                      setOpen(false)
                    }}
                  >
                    {option.label}
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        ) : (
          <input
            type="text"
            autoFocus
            aria-label={`Filter ${schema.label}`}
            value={(column.getFilterValue() as string) ?? ""}
            onChange={(event) => column.setFilterValue(event.target.value)}
            className="w-full bg-transparent p-2 text-sm text-text-primary outline-none"
          />
        )}
      </PopoverContent>
    </Popover>
  )
}
```

- [ ] **Step 5: Wire both into DataTable**

Modify `src/components/ui/data-table/data-table.tsx`:

Add the imports:

```tsx
import { ColumnFilter, DataTableToolbar } from "./toolbar"
```

Destructure the three new props in the signature — `title`, `titleIcon`, `toolbarActions`.

Wrap the existing return in a section and add the toolbar, replacing the outermost `<div className="w-full overflow-x-auto rounded-lg">` opening with:

```tsx
<section className="flex w-full flex-col items-start gap-4">
  <DataTableToolbar
    table={table}
    title={title}
    titleIcon={titleIcon}
    toolbarActions={toolbarActions}
    addRow={addRow}
  />
  <div className="w-full overflow-x-auto rounded-lg">
```

and closing the `</div>` with `</section>` after it.

Inside the header `<span>`, after the sort icon, add the filter trigger:

```tsx
{header.column.getCanFilter() ? (
  <ColumnFilter
    column={header.column}
    schema={(header.column.columnDef.meta as { column: DataTableColumn<T> }).column}
  />
) : null}
```

Add `DataTableColumn` to the `./types` import.

- [ ] **Step 6: Run the tests**

Run: `npm test -- toolbar`
Expected: PASS, 7 tests.

The filter trigger calls `event.stopPropagation()` because it sits inside a header cell whose click handler toggles sorting — without it, opening a filter would also re-sort the column. If a test shows rows reordering when a filter opens, that call was dropped.

- [ ] **Step 7: Run the whole suite**

Run: `npm test && npm run build && npm run lint`
Expected: all clean. Task 8's tests must still pass — the header markup changed.

- [ ] **Step 8: Commit**

```bash
git add src/components/ui/data-table/
git commit -m "Add DataTable toolbar with search, per-column filters, and column stub"
```

---

## Task 10: Rewrite TaskList on top of DataTable

**Files:**
- Modify: `src/components/TaskList.tsx` (full rewrite)
- Test: `src/components/TaskList.test.tsx`

**Interfaces:**
- Consumes: `DataTable` and its types from `@/components/ui/data-table`.
- Produces: `TaskList()` — same export name and same usage in `Home.tsx` and `ProjectOverview.tsx`, so neither page changes.

The placeholder rows are Figma wireframe filler (the design repeats one sample row four times). They must be varied here, because sorting and filtering are invisible to demo when every row is identical.

- [ ] **Step 1: Write the failing tests**

Create `src/components/TaskList.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { TaskList } from "./TaskList"

function rowNames() {
  const [, ...bodyRows] = screen.getAllByRole("row")
  return bodyRows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("TaskList", () => {
  it("renders the Tasks heading and a New Task button", () => {
    render(<TaskList />)

    expect(screen.getByRole("heading", { name: "Tasks" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "New Task" })).toBeInTheDocument()
  })

  it("renders every expected column header", () => {
    render(<TaskList />)

    for (const label of ["Task Name", "Assignee", "Due Date", "Status", "Priority"]) {
      expect(screen.getByRole("columnheader", { name: new RegExp(label) })).toBeInTheDocument()
    }
  })

  it("renders rows with distinct names so sorting is demonstrable", () => {
    render(<TaskList />)
    const names = rowNames()

    expect(names.length).toBeGreaterThan(1)
    expect(new Set(names).size).toBe(names.length)
  })

  it("resolves assignees to crew names rather than raw ids", () => {
    render(<TaskList />)

    expect(screen.queryByText(/^u\d+$/)).not.toBeInTheDocument()
  })

  it("sorts by due date chronologically", async () => {
    const user = userEvent.setup()
    render(<TaskList />)

    await user.click(screen.getByRole("columnheader", { name: /Due Date/ }))
    const sorted = rowNames()

    await user.click(screen.getByRole("columnheader", { name: /Due Date/ }))
    expect(rowNames()).toEqual([...sorted].reverse())
  })

  it("deletes a row when its delete action is used", async () => {
    const user = userEvent.setup()
    render(<TaskList />)
    const before = rowNames()

    const [firstRow] = screen.getAllByRole("row").slice(1)
    await user.click(within(firstRow).getByRole("button", { name: "Delete task" }))

    expect(rowNames()).toHaveLength(before.length - 1)
    expect(rowNames()).not.toContain(before[0])
  })

  it("adds a row when New Task is clicked", async () => {
    const user = userEvent.setup()
    render(<TaskList />)
    const before = rowNames()

    await user.click(screen.getByRole("button", { name: "New Task" }))

    expect(rowNames()).toHaveLength(before.length + 1)
    expect(rowNames().at(-1)).toBe("Untitled task")
  })
})
```

- [ ] **Step 2: Run to confirm failure**

Run: `npm test -- TaskList`
Expected: FAIL — the current `TaskList` has identical rows and no working delete.

- [ ] **Step 3: Rewrite TaskList**

Replace the entire contents of `src/components/TaskList.tsx`:

```tsx
import { Button } from "@/components/ui/button"
import { DataTable, type DataTableColumn, type DataTableUser } from "@/components/ui/data-table"
import { PencilIcon, TasksIcon, TrashIcon } from "@/components/icons"

type Task = {
  id: string
  name: string
  assignee: string
  due: string
  status: string
  priority: string
}

const CREW: DataTableUser[] = [
  { id: "u1", name: "Claudicia" },
  { id: "u2", name: "Marcus" },
  { id: "u3", name: "Priya" },
  { id: "u4", name: "Tobias" },
]

const TASKS: Task[] = [
  { id: "t1", name: "Draft launch copy", assignee: "u1", due: "2026-09-15", status: "in-progress", priority: "high" },
  { id: "t2", name: "Audit icon set", assignee: "u2", due: "2026-10-02", status: "todo", priority: "medium" },
  { id: "t3", name: "Ship static export", assignee: "u3", due: "2026-09-28", status: "blocked", priority: "high" },
  { id: "t4", name: "Review crew roster", assignee: "u4", due: "2026-11-20", status: "done", priority: "low" },
]

const STATUS_OPTIONS = [
  { value: "todo", label: "To Do" },
  { value: "in-progress", label: "In Progress", colorVar: "--color-accent" },
  { value: "done", label: "Done", colorVar: "--color-accent-success" },
  { value: "blocked", label: "Blocked", colorVar: "--color-accent-warn" },
]

const PRIORITY_OPTIONS = [
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium", colorVar: "--color-accent" },
  { value: "high", label: "High", colorVar: "--color-accent-warn" },
]

const COLUMNS: DataTableColumn<Task>[] = [
  { key: "name", label: "Task Name", type: "text", width: "w-70" },
  { key: "assignee", label: "Assignee", type: "user", width: "w-50" },
  { key: "due", label: "Due Date", type: "date", width: "w-40" },
  { key: "status", label: "Status", type: "select", width: "w-40", options: STATUS_OPTIONS },
  { key: "priority", label: "Priority", type: "select", width: "w-35", options: PRIORITY_OPTIONS },
  {
    key: "actions",
    label: "",
    type: "actions",
    actions: [
      {
        id: "edit",
        label: "Edit task",
        icon: <PencilIcon size={20} />,
        onSelect: () => {},
      },
      {
        id: "delete",
        label: "Delete task",
        icon: <TrashIcon size={20} />,
        variant: "destructive",
        onSelect: (_task, { removeRow }) => removeRow(),
      },
    ],
  },
]

export function TaskList() {
  return (
    <DataTable
      columns={COLUMNS}
      data={TASKS}
      getRowId={(task) => task.id}
      users={CREW}
      title="Tasks"
      titleIcon={<TasksIcon size={24} className="text-text-primary" />}
      toolbarActions={({ addRow }) => (
        <Button
          variant="outline"
          size="sm"
          onClick={() => addRow(blankTask())}
          className="rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-muted"
        >
          New Task
        </Button>
      )}
    />
  )
}
```

Add the row factory above the component:

```tsx
let nextTaskId = TASKS.length + 1

function blankTask(): Task {
  return {
    id: `t${nextTaskId++}`,
    name: "Untitled task",
    assignee: "",
    due: "",
    status: "todo",
    priority: "medium",
  }
}
```

The Edit action is intentionally a no-op: no edit form exists yet, and inventing one is out of scope (see the spec's Follow-ups).

- [ ] **Step 4: Run the tests**

Run: `npm test -- TaskList`
Expected: PASS, 7 tests.

- [ ] **Step 5: Apply column widths in the table body**

The `width` values in the schema are not yet used. Modify `src/components/ui/data-table/data-table.tsx` — on the `<TableHead>`, add the width class from the column meta:

```tsx
className={[
  (header.column.columnDef.meta as { column: DataTableColumn<T> }).column.width,
  canSort ? "cursor-pointer select-none" : "",
].filter(Boolean).join(" ")}
```

- [ ] **Step 6: Run the full suite**

Run: `npm test && npm run build && npm run lint`
Expected: all clean.

- [ ] **Step 7: Verify in the browser**

Run: `npm run dev`

Check all of the following, on **both** `#/` (Home) and `#/project-overview`:
- Rows show distinct sample data; status and priority render as tinted badges.
- Clicking a header sorts; clicking twice reverses; three times restores original order.
- Due Date sorts Sept → Oct → Nov (chronological, not alphabetical).
- Clicking a status badge opens the option list and changes the value.
- Clicking a due date opens a calendar and changes the date.
- Clicking an assignee opens the crew list and changes the person.
- The delete icon removes its row; the edit icon does nothing (expected).
- "New Task" appends an "Untitled task" row, whose status/date/assignee cells are then editable like any other.
- Search narrows rows; a per-column filter narrows rows.
- **Toggle dark mode and repeat the visual checks** — badge tints, popover surfaces, and the calendar must all be legible.
- Resize the window from wide to narrow: the table scrolls horizontally inside its wrapper; the **page itself** must not gain a horizontal scrollbar (the `min-w-0` gotcha from AGENTS.md).

- [ ] **Step 8: Commit**

```bash
git add src/components/TaskList.tsx src/components/TaskList.test.tsx src/components/ui/data-table/data-table.tsx
git commit -m "Rewrite TaskList as a DataTable schema with varied sample data"
```

---

## Task 11: Fix the static export's partial matching

This is the failure mode that produces no error message. `export-static.mjs` renders `TaskList` standalone, then splices it into each page by exact string match. Radix's `Popover` and `Command` call React's `useId()`, whose output depends on position in the render tree — so the standalone render and the in-page render now differ, `.replace()` silently matches nothing, and the exported pages lose their partial markers.

**Files:**
- Create: `scripts/strip-generated-ids.mjs`
- Modify: `scripts/export-static.mjs`
- Test: `scripts/strip-generated-ids.test.js`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `stripGeneratedIds(html: string): string`, exported from `scripts/strip-generated-ids.mjs`, imported by `export-static.mjs` and called inside `rewriteAssetPaths`.

The function lives in its own module rather than in `export-static.mjs` for a specific reason: `export-static.mjs` performs its whole export at import time (it calls `createServer`, renders, and writes files at the top level). A test importing from it would run the entire export as a side effect.

- [ ] **Step 1: Write the failing test**

Create `scripts/strip-generated-ids.test.js`:

```js
import { describe, expect, it } from "vitest"
import { stripGeneratedIds } from "./strip-generated-ids.mjs"

describe("stripGeneratedIds", () => {
  it("removes React useId values in colon format", () => {
    const html = '<button aria-controls=":r3:" id=":r2:">Status</button>'

    expect(stripGeneratedIds(html)).toBe("<button>Status</button>")
  })

  it("removes React useId values in guillemet format", () => {
    const html = '<button aria-controls="«r3»" id="«r2»">Status</button>'

    expect(stripGeneratedIds(html)).toBe("<button>Status</button>")
  })

  it("makes two renders of the same markup match despite different ids", () => {
    const standalone = '<button id=":r0:" aria-controls=":r1:">Status</button>'
    const inPage = '<button id=":r7:" aria-controls=":r8:">Status</button>'

    expect(stripGeneratedIds(standalone)).toBe(stripGeneratedIds(inPage))
  })

  it("leaves author-written ids alone", () => {
    const html = '<div id="main" aria-controls="sidebar">x</div>'

    expect(stripGeneratedIds(html)).toBe(html)
  })

  it("leaves markup with no ids untouched", () => {
    const html = "<td>Sample task name</td>"

    expect(stripGeneratedIds(html)).toBe(html)
  })
})
```

- [ ] **Step 2: Allow vitest to pick up files in `scripts/`**

Modify the `test` block in `vite.config.ts` to include both locations:

```ts
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.{ts,tsx}', 'scripts/**/*.{test,spec}.{js,mjs}'],
  },
```

- [ ] **Step 3: Run to confirm failure**

Run: `npm test -- strip-generated-ids`
Expected: FAIL — the module does not exist.

- [ ] **Step 4: Implement the module**

Create `scripts/strip-generated-ids.mjs`:

```js
// React's useId output depends on a component's position in the render tree, so
// the same component rendered standalone and rendered inside a page produces
// different ids. The partial splicing in export-static.mjs is an exact string
// match, so those ids must go or it silently stops matching. Radix's Popover and
// Command (used by DataTable's editable cells) call useId.
export function stripGeneratedIds(html) {
  return html.replace(
    /\s(?:id|aria-controls|aria-labelledby|aria-describedby|for)="(?::r[0-9a-z]+:|«r[0-9a-z]+»)"/g,
    '',
  )
}
```

- [ ] **Step 5: Wire it into the export script**

Modify `scripts/export-static.mjs`. Add the import alongside the existing imports at the top:

```js
import { stripGeneratedIds } from './strip-generated-ids.mjs'
```

Then wrap the existing `rewriteAssetPaths` chain (currently at lines 61-68) so everything it returns passes through the strip. Both the standalone partial renders and the page renders go through this function, which is why fixing it in one place is sufficient:

```js
function rewriteAssetPaths(html) {
  return stripGeneratedIds(
    html
      .replaceAll('/src/assets/brand/worklode-mark.svg', `../assets/${markFile}`)
      .replaceAll('/src/assets/brand/folder-curve.svg', '../assets/folder-curve.svg')
      .replace(/<link rel="preload"[^>]*>/g, ''), // React 19's auto image-preload hints hoist
    // inconsistently between a standalone partial render and the full-page render
    // (deduped once at the top of whichever tree it's rendered in), which breaks
    // exact-string partial matching below — drop them, they're a browser
    // optimization hint with no effect on the markup Go will template from.
  )
}
```

- [ ] **Step 6: Run the tests**

Run: `npm test -- strip-generated-ids`
Expected: PASS, 5 tests.

- [ ] **Step 7: Count the partial markers BEFORE, as a baseline**

Before trusting the fix, know what the correct number is. Count the `pageHtml.replace(` calls that splice a partial into each page in `scripts/export-static.mjs` — that is how many `BEGIN PARTIAL` markers each page should contain.

- [ ] **Step 8: Run the real export and verify the markers survive**

```bash
npm run export:html
grep -c "BEGIN PARTIAL: components/task-list.html" static-export/pages/home.html static-export/pages/project-overview.html
```

Expected: `1` for each file. **If either prints `0`, the fix is incomplete — stop and investigate.** A `0` here is exactly the silent failure this task exists to prevent.

- [ ] **Step 9: Confirm the other partials did not regress**

```bash
grep -c "BEGIN PARTIAL" static-export/pages/home.html static-export/pages/project-overview.html
```

Expected: the baseline counts from Step 7. If any dropped, `stripGeneratedIds` is too aggressive and is removing an id the markup genuinely depends on.

- [ ] **Step 10: Commit**

```bash
git add scripts/strip-generated-ids.mjs scripts/strip-generated-ids.test.js scripts/export-static.mjs vite.config.ts
git commit -m "Strip React useId output so static export partial matching survives Radix"
```

---

## Task 12: Backend handoff doc

**Files:**
- Create: `docs/TASKS-BACKEND.md`

**Interfaces:**
- Consumes: the finished behaviour of Tasks 1–11.
- Produces: documentation only.

- [ ] **Step 1: Write the doc**

Create `docs/TASKS-BACKEND.md`:

```markdown
# Tasks table: what the backend needs to provide

The Tasks table in this prototype is a working React component
(`src/components/ui/data-table/`) driven by a column schema. Everything below
works **in the browser against in-memory state only** — nothing is persisted,
and there is no API. This document lists what has to exist server-side for the
prototype's behaviour to become real.

The static export (`npm run export:html`) drops all React event handlers, so
the exported HTML shows the table's *appearance* but none of its interactivity.
Use the React component as the behavioural reference, not the exported HTML.

## Data shape

Each row currently looks like:

| Field | Type in prototype | Notes |
|---|---|---|
| `id` | string | Stable row identity. Every mutation below targets a row by this. |
| `name` | string | Free text. |
| `assignee` | string | A **user id**, resolved against a crew roster for display. Not a display name. |
| `due` | string | **ISO date** (`YYYY-MM-DD`). Formatted for display in the UI. Stored ISO so it sorts chronologically. |
| `status` | string | One of an enumerated set. |
| `priority` | string | One of an enumerated set. |

Two of these are load-bearing and easy to get wrong:

- **`due` must be a real date, not a display string.** The prototype originally
  held `"Sept 15, 2026"`, which sorts alphabetically — putting October before
  September. If the API returns preformatted dates, sorting breaks in the same
  way.
- **`assignee` must be an id, not a name.** The UI resolves it against the crew
  roster to render an avatar and to mark the current selection in the picker.

## Mutations that need endpoints

Each of these is a single-field edit made inline in the table:

1. **Change status** — set `status` on a task to one of the allowed values.
2. **Change priority** — set `priority` on a task to one of the allowed values.
3. **Change assignee** — set `assignee` to a user id, chosen from the project's
   crew.
4. **Change due date** — set `due` to an ISO date, chosen from a calendar.
5. **Delete a task** — remove the row.
6. **Create a task** — the "New Task" button appends a blank row. In the
   prototype this mints a local id and fills defaults; server-side it needs
   to create the record and return its real id.

The allowed values for status and priority are hardcoded in the prototype
(`src/components/TaskList.tsx`). If they are configurable server-side, the
table accepts them as data — the column schema takes an `options` array.

## Reads

- **The crew roster** for the assignee picker: `{ id, name, avatarUrl? }` per
  person, scoped to the project.

## Sort, filter, and search

All three are currently **client-side** over the full row set held in memory:

- Sort by any column, ascending/descending/off.
- Filter per column (a value picker for enumerated columns, text for the rest).
- Global text search across columns.

This is fine at prototype scale. Once real data volume exists, these should
move server-side as query parameters rather than shipping every row to the
browser to be filtered there.

## Not built yet

- **An edit form.** The row-level Edit (pencil) action is deliberately a no-op —
  there is no form or dialog designed for it yet.
- **Column show/hide and reordering.** The table's state model supports both,
  and there is a placeholder toolbar button for it, but the UI is not designed.
  If column preferences should persist per user, that needs storage.
- **The standalone Tasks page.** A Figma frame exists for it; only the embedded
  table is built.

## Reusing this table for other content types

`DataTable` is generic. Another content type supplies its own column schema and
its own row actions — the action set is per type, not fixed:

- Tasks: edit, delete.
- Deliverables: include a **review** action.
- Documents: download, preview.

Each action can add, remove, or patch rows through the helpers it is handed,
so "duplicate" is an ordinary action rather than a special case.

So "what actions does this row have" is a per-content-type question, and the
backend should expect a different set of operations per type rather than a
universal edit/delete pair.
```

- [ ] **Step 2: Verify the claims against the code**

Read back through the doc and confirm each field, action, and file path matches what Tasks 1–11 actually built. Anything that drifted during implementation gets corrected here, not left as written.

- [ ] **Step 3: Commit**

```bash
git add docs/TASKS-BACKEND.md
git commit -m "Document backend requirements for the Tasks table handoff"
```

---

## Final verification

- [ ] **Run everything**

```bash
npm test && npm run build && npm run lint && npm run export:html
```

All four must pass.

- [ ] **Confirm the export markers one more time**

```bash
grep -c "BEGIN PARTIAL: components/task-list.html" static-export/pages/home.html static-export/pages/project-overview.html
```

Expected: `1` for each.

- [ ] **Update AGENTS.md**

Add a short section covering: the `DataTable` schema pattern and where it lives, that row actions are per content type, and the `stripGeneratedIds` requirement in the export script (alongside the two gotchas already documented there). Keep it in the voice of the surrounding document.

- [ ] **Commit the AGENTS.md update**

```bash
git add AGENTS.md
git commit -m "Document the DataTable pattern in AGENTS.md"
```
