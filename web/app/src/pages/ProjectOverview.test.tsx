import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

// setSystemTime alone, not useFakeTimers: fake timers hang userEvent (see toolbar.test.tsx)
beforeEach(() => vi.setSystemTime(new Date(2026, 10, 1, 12)))
afterEach(() => {
  vi.useRealTimers()
  server.events.removeAllListeners()
})

function card() {
  return screen.getByText("Project Summary").closest<HTMLElement>("[data-project-summary]")!
}

describe("/projects/$projectId", () => {
  it("lists past-due items with how late they are, linking to their URL", async () => {
    await renderApp("/projects/p1")
    await screen.findByText("Project Summary")
    const summary = within(card())
    const view = within(card().querySelector<HTMLElement>('[data-summary-view="summary"]')!)
    const item = view.getByRole("link", { name: /Draft launch copy/ })
    expect(item).toHaveAttribute("href", "/projects/p1/task/1")
    expect(item).toHaveTextContent("2 days late")
    expect(summary.getByRole("link", { name: /Launch plan/ })).toHaveAttribute("href", "/projects/p1/docs/WL-SPEC-3")
    // The task list sits under the card
    expect(await screen.findByRole("row", { name: /Audit icon set/ })).toBeInTheDocument()
  })

  it("marks a member with a past-due task Overbooked", async () => {
    await renderApp("/projects/p1")
    await screen.findByText("Project Summary")
    const stig = card().querySelector<HTMLElement>('[data-crew-row="stig"]')!
    expect(within(stig).getByText("Stig Bakken")).toBeInTheDocument()
    expect(within(stig).getByText("Overbooked")).toBeInTheDocument()
    expect(within(card().querySelector<HTMLElement>('[data-crew-row="ada"]')!).getByText("Open")).toBeInTheDocument()
  })

  it("refetches the summary after a task's due date changes", async () => {
    const user = userEvent.setup()
    let summaryReads = 0
    server.events.on("request:start", ({ request }) => {
      if (request.method === "GET" && new URL(request.url).pathname === "/api/v1/projects/p1/summary") summaryReads++
    })
    await renderApp("/projects/p1")
    const row = await screen.findByRole("row", { name: /Draft launch copy/ })
    await waitFor(() => expect(summaryReads).toBe(1))

    await user.click(within(row).getByRole("button", { name: "Oct 30, 2026" }))
    const day = within(screen.getByRole("grid")).getAllByRole("button").find((b) => b.textContent?.trim() === "15")
    await user.click(day!)

    await waitFor(() => expect(summaryReads).toBe(2))
  })

  it("shows the layout error with Retry when the project fails to load", async () => {
    const user = userEvent.setup()
    server.use(http.get("/api/v1/projects/:id", () => HttpResponse.json({ error: "down" }, { status: 500 }), { once: true }))
    await renderApp("/projects/p1")

    expect(await screen.findByText("Couldn't load this project.")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findByText("Project Summary")).toBeInTheDocument()
  })
})
