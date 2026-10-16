import { screen } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import { afterEach, describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

afterEach(() => server.events.removeAllListeners())

describe("task pages", () => {
  it("/tasks lists every project's tasks with a Project column", async () => {
    await renderApp("/tasks")
    expect(await screen.findByRole("row", { name: /Load the glacier data/ })).toBeInTheDocument()
    expect(screen.getByRole("columnheader", { name: /Project/ })).toBeInTheDocument()
  })

  it("/projects/p1/tasks requests ?project=p1 and hides the Project column", async () => {
    const urls: string[] = []
    server.events.on("request:start", ({ request }) => {
      urls.push(request.url)
    })
    await renderApp("/projects/p1/tasks")
    await screen.findByRole("row", { name: /Draft launch copy/ })
    expect(urls.map((u) => new URL(u)).some((u) => u.pathname === "/api/v1/tasks" && u.search === "?project=p1")).toBe(true)
    expect(screen.queryByRole("row", { name: /Load the glacier data/ })).not.toBeInTheDocument()
    expect(screen.queryByRole("columnheader", { name: /Project/ })).not.toBeInTheDocument()
  })

  it("/projects/p9/tasks shows the layout's error with Retry when the project fails to load", async () => {
    server.use(http.get("/api/v1/projects/:id", () => HttpResponse.json({ error: "boom" }, { status: 500 })))
    await renderApp("/projects/p9/tasks")
    expect(await screen.findByText("Couldn't load this project.")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument()
  })
})
