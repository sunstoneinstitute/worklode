import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { afterEach, describe, expect, it } from "vitest"
import type { components } from "@/api/schema"
import { db } from "@/mocks/db"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

afterEach(() => server.events.removeAllListeners())

function projectCell(taskTitle: string) {
  const row = screen.getByRole("row", { name: new RegExp(taskTitle) })
  return row.querySelector('[data-column-id="project"]') as HTMLElement
}

describe("TaskList", () => {
  it("shows the raw project id until projects load, then Deleted project for one not in the list", async () => {
    db.tasks.push({ id: "GONE-1", project: "p3", title: "Orphaned task", state: "ready", priority: "low", assignee: "", due: "" })
    let release!: () => void
    const loaded = new Promise<void>((resolve) => (release = resolve))
    server.use(
      http.get("/api/v1/projects", async () => {
        await loaded
        return HttpResponse.json({ projects: db.projects })
      }),
    )
    await renderApp("/tasks")
    await screen.findByRole("row", { name: /Orphaned task/ })
    expect(projectCell("Orphaned task")).toHaveTextContent("p3")

    release()
    await waitFor(() => expect(projectCell("Orphaned task")).toHaveTextContent("Deleted project"))
    expect(projectCell("Draft launch copy")).toHaveTextContent("Worklode")
  })

  it("links a task's title to its canonical URL", async () => {
    await renderApp("/tasks")
    expect(await screen.findByRole("link", { name: "Draft launch copy" })).toHaveAttribute("href", "/WL-1")
  })

  it("editing a due date patches {due}", async () => {
    const user = userEvent.setup()
    let patched: components["schemas"]["EditTaskInput"] | undefined
    server.events.on("request:start", async ({ request }) => {
      if (request.method === "PATCH") patched = await request.clone().json()
    })
    await renderApp("/tasks")
    const row = await screen.findByRole("row", { name: /Draft launch copy/ })
    await user.click(within(row).getByRole("button", { name: "Oct 30, 2026" }))
    const day = within(screen.getByRole("grid")).getAllByRole("button").find((b) => b.textContent?.trim() === "15")
    await user.click(day!)

    await waitFor(() => expect(patched).toEqual({ due: "2026-10-15" }))
  })

  it("rolls a refused state change back and shows the server's message", async () => {
    const user = userEvent.setup()
    const message = "task WL-1 cannot move from in_progress to merged"
    server.use(http.patch("/api/v1/tasks/:id", () => HttpResponse.json({ error: message }, { status: 400 })))
    await renderApp("/tasks")
    const row = await screen.findByRole("row", { name: /Draft launch copy/ })
    await user.click(within(row).getByRole("button", { name: "In progress" }))
    await user.click(screen.getByRole("option", { name: "Merged" }))

    expect(await screen.findByText(message)).toBeInTheDocument()
    const state = screen.getByRole("row", { name: /Draft launch copy/ }).querySelector('[data-column-id="state"]')
    expect(state).toHaveTextContent("In progress")
  })
})
