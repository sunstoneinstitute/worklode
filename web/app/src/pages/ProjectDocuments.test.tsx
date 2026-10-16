import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { afterEach, describe, expect, it } from "vitest"
import type { components } from "@/api/schema"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

afterEach(() => server.events.removeAllListeners())

function statusCell(name: string) {
  return screen.getByRole("row", { name: new RegExp(name) }).querySelector('[data-column-id="status"]')
}

describe("/projects/$projectId/documents", () => {
  it("lists the project's specs on the Specs tab, not its plans, each ref linking to /<ref>", async () => {
    await renderApp("/projects/p1/documents")
    expect(screen.getByRole("tab", { name: "Specs" })).toHaveAttribute("aria-selected", "true")
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual(["Specs", "Plans", "Rules", "Files & links"])
    expect(await screen.findByRole("link", { name: "WL-SPEC-3" })).toHaveAttribute("href", "/WL-SPEC-3")
    expect(screen.getByRole("link", { name: "WL-SPEC-4" })).toHaveAttribute("href", "/WL-SPEC-4")
    expect(screen.queryByText("WL-PLAN-5")).not.toBeInTheDocument()
    expect(screen.queryByText("DP-SPEC-1")).not.toBeInTheDocument()
  })

  it("shows the rules table with its headings on ?tab=rules", async () => {
    await renderApp("/projects/p1/documents?tab=rules")
    expect(await screen.findByRole("link", { name: "WL-REQ-10" })).toHaveAttribute("href", "/WL-REQ-10")
    expect(screen.getByText("Launch copy is reviewed")).toBeInTheDocument()
    expect(screen.getByText("One icon font")).toBeInTheDocument()
    expect(screen.queryByText("DP-REQ-1")).not.toBeInTheDocument()
  })

  it("says No plans yet for a project without plans", async () => {
    await renderApp("/projects/p2/documents?tab=plans")
    expect(await screen.findByText("No plans yet")).toBeInTheDocument()
  })

  it("lists the register on Files & links with category labels", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/projects/p1/documents")
    await user.click(screen.getByRole("tab", { name: "Files & links" }))
    const brief = await screen.findByRole("row", { name: /Launch brief/ })
    expect(router.state.location.search).toMatchObject({ tab: "files" })
    expect(within(brief).getByRole("link", { name: "Launch brief" })).toHaveAttribute("href", "https://docs.example.com/brief")
    expect(brief.querySelector('[data-column-id="category"]')).toHaveTextContent("Spec")
    const notes = screen.getByRole("row", { name: /Kickoff notes/ })
    expect(notes.querySelector('[data-column-id="category"]')).toHaveTextContent("Meeting Notes")
    expect(screen.queryByText("Glacier dataset")).not.toBeInTheDocument()
  })

  it("patches an inline status edit and shows it at once", async () => {
    const user = userEvent.setup()
    let patched: components["schemas"]["PatchProjectDocumentInput"] | undefined
    let release!: () => void
    const held = new Promise<void>((resolve) => (release = resolve))
    server.use(
      http.patch("/api/v1/documents/:id", async ({ request }) => {
        patched = (await request.json()) as typeof patched
        await held
        return HttpResponse.json({})
      }, { once: true }),
    )
    await renderApp("/projects/p1/documents?tab=files")
    const row = await screen.findByRole("row", { name: /Launch brief/ })
    await user.click(within(row).getByRole("button", { name: "Draft" }))
    await user.click(screen.getByRole("option", { name: "Final" }))

    await waitFor(() => expect(patched).toEqual({ status: "final" }))
    expect(statusCell("Launch brief")).toHaveTextContent("Final")
    release()
  })

  it("rolls a failed patch back with a toast", async () => {
    const user = userEvent.setup()
    server.use(http.patch("/api/v1/documents/:id", () => HttpResponse.json({ error: "down" }, { status: 500 })))
    await renderApp("/projects/p1/documents?tab=files")
    const row = await screen.findByRole("row", { name: /Launch brief/ })
    await user.click(within(row).getByRole("button", { name: "Draft" }))
    await user.click(screen.getByRole("option", { name: "Final" }))

    expect(await screen.findByText("Couldn't save the document")).toBeInTheDocument()
    await waitFor(() => expect(statusCell("Launch brief")).toHaveTextContent("Draft"))
  })

  it("removes a deleted row", async () => {
    const user = userEvent.setup()
    await renderApp("/projects/p1/documents?tab=files")
    const row = await screen.findByRole("row", { name: /Kickoff notes/ })
    await user.click(within(row).getByRole("button", { name: "Delete document" }))
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Yes" }))

    await waitFor(() => expect(screen.queryByRole("row", { name: /Kickoff notes/ })).not.toBeInTheDocument())
    expect(screen.getByRole("row", { name: /Launch brief/ })).toBeInTheDocument()
  })

  it("adds a document from the toolbar form", async () => {
    const user = userEvent.setup()
    await renderApp("/projects/p1/documents?tab=files")
    await screen.findByRole("row", { name: /Launch brief/ })
    await user.type(screen.getByRole("textbox", { name: "New document name" }), "Field photos")
    await user.click(screen.getByRole("combobox", { name: "Category" }))
    await user.click(screen.getByRole("option", { name: "Sources" }))
    await user.click(screen.getByRole("button", { name: "Add document" }))

    const row = await screen.findByRole("row", { name: /Field photos/ })
    expect(row.querySelector('[data-column-id="category"]')).toHaveTextContent("Sources")
  })

  it("shows the layout error with Retry when the project fails to load", async () => {
    const user = userEvent.setup()
    server.use(http.get("/api/v1/projects/:id", () => HttpResponse.json({ error: "down" }, { status: 500 }), { once: true }))
    await renderApp("/projects/p1/documents")

    expect(await screen.findByText("Couldn't load this project.")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findByRole("link", { name: "WL-SPEC-3" })).toBeInTheDocument()
  })
})
