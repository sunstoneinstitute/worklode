import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { toast } from "sonner"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { queryKeys } from "@/api/queries"
import type { Task, TaskPatch } from "@/api/types"
import { Toaster } from "@/components/ui/sonner"
import { celebrate } from "@/lib/burst"
import { db } from "@/mocks/db"
import { server } from "@/mocks/node"
import { renderWithRouter } from "@/test/render"
import { TaskList } from "./TaskList"

vi.mock("@/lib/burst", () => ({ celebrate: vi.fn() }))

function rowNames() {
  const [, ...bodyRows] = screen.getAllByRole("row")
  return bodyRows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

async function renderList(props: Parameters<typeof TaskList>[0] = {}) {
  const result = await renderWithRouter(
    <>
      <TaskList {...props} />
      <Toaster />
    </>,
  )
  await screen.findByRole("columnheader", { name: /Task Name/ })
  return result
}

function cellOf(taskName: string, columnId: string) {
  const row = screen.getByRole("row", { name: new RegExp(taskName) })
  return row.querySelector(`[data-column-id="${columnId}"]`) as HTMLElement
}

async function setSelect(taskName: string, columnId: string, label: string) {
  const user = userEvent.setup()
  await user.click(within(cellOf(taskName, columnId)).getByRole("button"))
  await user.click(screen.getByRole("option", { name: label }))
}

describe("TaskList", () => {
  // sonner's toast queue is a module-level store, outliving any one test's
  // <Toaster/> — clear it so a toast an earlier test raised (and never
  // dismissed) doesn't linger into the next test's assertions.
  beforeEach(() => toast.dismiss())

  it("renders the Tasks heading and a New Task button", async () => {
    await renderList()
    expect(screen.getByRole("heading", { name: "Tasks" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "New Task" })).toBeInTheDocument()
  })

  it("renders every expected column header", async () => {
    await renderList()
    for (const label of ["Task Name", "Assignee", "Due Date", "Status", "Priority"]) {
      expect(screen.getByRole("columnheader", { name: new RegExp(label) })).toBeInTheDocument()
    }
  })

  it("has no per-column filter on Task Name, but keeps it on the other filterable columns", async () => {
    await renderList()

    const nameHeader = screen.getByRole("columnheader", { name: /Task Name/ })
    expect(within(nameHeader).queryByRole("button", { name: /filter/i })).not.toBeInTheDocument()

    for (const label of ["Assignee", "Due Date", "Status", "Priority"]) {
      const header = screen.getByRole("columnheader", { name: new RegExp(label) })
      expect(within(header).getByRole("button", { name: /filter/i })).toBeInTheDocument()
    }
  })

  it("loads its rows from the API", async () => {
    await renderList()
    expect(rowNames()).toEqual(["Draft launch copy", "Audit icon set", "Ship static export", "Review crew roster"])
  })

  it("shows only one project's tasks when given a projectId", async () => {
    await renderList({ projectId: "melting-glaciers" })
    expect(rowNames()).toEqual(["Audit icon set", "Review crew roster"])
  })

  it("resolves assignees to crew names from the API", async () => {
    await renderList()
    expect(screen.getByText("@MarcusChen")).toBeInTheDocument()
  })

  it("sorts by due date chronologically", async () => {
    const user = userEvent.setup()
    await renderList()
    await user.click(screen.getByRole("columnheader", { name: /Due Date/ }))
    expect(rowNames()).toEqual(["Draft launch copy", "Ship static export", "Audit icon set", "Review crew roster"])
  })

  it("saves an inline edit to the server", async () => {
    await renderList()
    await setSelect("Audit icon set", "priority", "High")
    expect(within(cellOf("Audit icon set", "priority")).getByText("High")).toBeInTheDocument()
    await vi.waitFor(() => expect(db.tasks.find((task) => task.id === "t2")?.priority).toBe("high"))
  })

  it("puts the old value back and says so when the server rejects an edit", async () => {
    server.use(http.patch("/api/tasks/:id", () => HttpResponse.json({ message: "nope" }, { status: 500 })))
    await renderList()

    await setSelect("Audit icon set", "status", "Blocked")

    expect(await screen.findByText("Couldn't save the task")).toBeInTheDocument()
    await vi.waitFor(() =>
      expect(within(cellOf("Audit icon set", "status")).getByText("To Do")).toBeInTheDocument(),
    )
  })

  it("puts the old value back and says so on a 422 (invalid status) rejection", async () => {
    server.use(
      http.patch("/api/tasks/:id", () =>
        HttpResponse.json({ message: "Invalid task", errors: { status: "bad status" } }, { status: 422 }),
      ),
    )
    await renderList()

    await setSelect("Audit icon set", "status", "Blocked")

    expect(await screen.findByText("Couldn't save the task")).toBeInTheDocument()
    await vi.waitFor(() =>
      expect(within(cellOf("Audit icon set", "status")).getByText("To Do")).toBeInTheDocument(),
    )
  })

  it("updates every cached task list, so other tables show the edit", async () => {
    const { queryClient } = await renderList()
    queryClient.setQueryData(queryKeys.tasks.list("melting-glaciers"), db.tasks.filter((task) => task.project === "melting-glaciers"))

    await setSelect("Audit icon set", "priority", "High")

    const other = queryClient.getQueryData<Task[]>(queryKeys.tasks.list("melting-glaciers"))
    expect(other?.find((task) => task.id === "t2")?.priority).toBe("high")
  })

  it("doesn't let a fast edit's settle refetch stomp a slower edit still in flight", async () => {
    // B's PATCH (task t1) is gated open, never resolving until releaseB() is
    // called; A's PATCH (task t2) is ordinary and settles right away — the
    // server hasn't seen B's change while A's settles.
    let releaseB!: () => void
    const bGate = new Promise<void>((resolve) => {
      releaseB = resolve
    })
    server.use(
      http.patch("/api/tasks/:id", async ({ params, request }) => {
        const patch = (await request.json()) as TaskPatch
        if (params.id === "t1") await bGate
        const task = db.tasks.find((candidate) => candidate.id === params.id)
        Object.assign(task!, patch)
        return HttpResponse.json(task)
      }),
    )
    const { queryClient } = await renderList()
    const cachedTask = (id: string) =>
      queryClient.getQueryData<Task[]>(queryKeys.tasks.list())?.find((task) => task.id === id)

    // B: starts a slow edit (in-progress → blocked)
    await setSelect("Draft launch copy", "status", "Blocked")
    await vi.waitFor(() => expect(cachedTask("t1")?.status).toBe("blocked"))

    // A: a fast edit elsewhere (medium → high), fully settles — including
    // its own refetch — while B is still in flight
    await setSelect("Audit icon set", "priority", "High")
    await vi.waitFor(() => expect(db.tasks.find((task) => task.id === "t2")?.priority).toBe("high"))
    await vi.waitFor(() => expect(queryClient.isFetching({ queryKey: queryKeys.tasks.all })).toBe(0))

    // A's settle must not have refetched over B's still-pending optimistic
    // value: the server hasn't seen B's change yet (still "in-progress"), so
    // a premature refetch would flip the cache back before B's own PATCH
    // eventually lands.
    expect(cachedTask("t1")?.status).toBe("blocked")

    releaseB()
    await vi.waitFor(() => expect(db.tasks.find((task) => task.id === "t1")?.status).toBe("blocked"))
    await vi.waitFor(() => expect(cachedTask("t1")?.status).toBe("blocked"))
  })

  it("keeps another row's open picker open when a task elsewhere in the cache changes", async () => {
    const user = userEvent.setup()
    const { queryClient } = await renderList()

    const cellBefore = cellOf("Ship static export", "status")
    const triggerBefore = within(cellBefore).getByRole("button")
    await user.click(triggerBefore)
    expect(screen.getByRole("option", { name: "Blocked" })).toBeInTheDocument()

    // A different row's cached data changes (e.g. another mutation settling)
    // — a value genuinely different from what's cached, so this doesn't get
    // silently dropped by TanStack Query's structural sharing.
    queryClient.setQueryData(queryKeys.tasks.list(), (tasks: Task[] | undefined) =>
      tasks?.map((task) => (task.id === "t1" ? { ...task, priority: "low" } : task)),
    )
    // Confirms the change actually rendered before checking row B below
    await waitFor(() =>
      expect(within(cellOf("Draft launch copy", "priority")).getByText("Low")).toBeInTheDocument(),
    )

    // Row B's picker must still be open, on the very same trigger button DOM
    // node — not remounted (which would silently close it and reset it).
    expect(screen.getByRole("option", { name: "Blocked" })).toBeInTheDocument()
    expect(within(cellOf("Ship static export", "status")).getByRole("button")).toBe(triggerBefore)
  })

  it("deletes a row on the server", async () => {
    const user = userEvent.setup()
    await renderList()
    const row = screen.getByRole("row", { name: /Ship static export/ })
    await user.click(within(row).getByRole("button", { name: "Delete task" }))
    await user.click(screen.getByRole("button", { name: "Yes" }))

    await vi.waitFor(() => expect(rowNames()).not.toContain("Ship static export"))
    expect(db.tasks.map((task) => task.id)).not.toContain("t3")
  })

  it("adds a task through the server when New Task is clicked", async () => {
    const user = userEvent.setup()
    await renderList({ projectId: "the-grid" })
    await user.click(screen.getByRole("button", { name: "New Task" }))

    await vi.waitFor(() => expect(rowNames().at(-1)).toBe("Untitled task"))
    expect(db.tasks.at(-1)).toMatchObject({ name: "Untitled task", project: "the-grid", status: "todo", priority: "medium" })
  })

  it("shows an error with Retry when the tasks don't load", async () => {
    const user = userEvent.setup()
    server.use(http.get("/api/tasks", () => HttpResponse.json({ message: "down" }, { status: 500 }), { once: true }))
    await renderWithRouter(<TaskList />)

    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load tasks.")
    await user.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findByRole("columnheader", { name: /Task Name/ })).toBeInTheDocument()
  })

  describe("Project column (top-level Tasks page)", () => {
    it("is left off by default", async () => {
      await renderList()
      expect(screen.queryByRole("columnheader", { name: /Project/ })).not.toBeInTheDocument()
    })

    it("shows each task's project right after Task Name when enabled", async () => {
      await renderList({ showProject: true })
      const headers = screen.getAllByRole("columnheader").map((header) => header.textContent)
      expect(headers.slice(0, 2)).toEqual(["Task Name", "Project"])
      expect(screen.getAllByText("The Grid")).toHaveLength(2)
      expect(screen.getAllByText("Melting Glaciers")).toHaveLength(2)
    })

    it("filters tasks by project, like assignees", async () => {
      const user = userEvent.setup()
      await renderList({ showProject: true })
      const header = screen.getByRole("columnheader", { name: /Project/ })
      await user.click(within(header).getByRole("button", { name: /filter/i }))
      await user.click(screen.getByRole("option", { name: "Melting Glaciers" }))
      expect(rowNames()).toEqual(["Audit icon set", "Review crew roster"])
    })
  })

  describe("done-status celebration", () => {
    beforeEach(() => vi.mocked(celebrate).mockClear())

    it("celebrates when a task is changed to Done", async () => {
      await renderList()
      await setSelect("Draft launch copy", "status", "Done")
      expect(celebrate).toHaveBeenCalledTimes(1)
    })

    it("doesn't burst for other status changes", async () => {
      await renderList()
      await setSelect("Draft launch copy", "status", "Blocked")
      expect(celebrate).not.toHaveBeenCalled()
    })

    it("doesn't burst when a task that's already done is edited", async () => {
      await renderList()
      await setSelect("Review crew roster", "priority", "High")
      await setSelect("Review crew roster", "status", "Done")
      expect(celebrate).not.toHaveBeenCalled()
    })
  })
})
