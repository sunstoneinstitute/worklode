import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { afterEach, describe, expect, it } from "vitest"
import { db } from "@/mocks/db"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

const MINE = "idea_000000000001" // private, by the signed-in actor
const SHARED = "idea_000000000002" // shared, by ada

afterEach(() => {
  server.events.removeAllListeners()
  localStorage.clear()
})

function steps() {
  return within(screen.getByRole("list", { name: "Intake progress" }))
    .getAllByRole("listitem")
    .map((step) => step.textContent)
}

function findingsRequests() {
  const urls: string[] = []
  server.events.on("request:start", ({ request }) => {
    if (/\/(litmus-tests|pre-research|sources)$/.test(new URL(request.url).pathname)) urls.push(request.url)
  })
  return urls
}

describe("/ideas/$ideaId/intake", () => {
  it("shows a private idea's pitch and the Share panel only, and asks for no findings", async () => {
    const urls = findingsRequests()
    await renderApp(`/ideas/${MINE}/intake`)
    expect(await screen.findByRole("heading", { name: "Glacier retreat explainer" })).toBeInTheDocument()
    expect(screen.getByRole("region", { name: "Pitch" })).toBeInTheDocument()
    expect(within(screen.getByRole("region", { name: "Private idea" })).getByRole("button", { name: "Share" })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Authorize AI pre-research" })).not.toBeInTheDocument()
    expect(screen.queryByRole("list", { name: "Intake progress" })).not.toBeInTheDocument()
    expect(urls).toEqual([])
  })

  it("switches to the intake view once shared from the panel", async () => {
    const user = userEvent.setup()
    await renderApp(`/ideas/${MINE}/intake`)
    await user.click(await screen.findByRole("button", { name: "Share" }))
    const dialog = screen.getByRole("dialog")
    expect(dialog).toHaveTextContent("cannot be made private again")
    await user.click(within(dialog).getByRole("button", { name: "Yes" }))

    expect(await screen.findByRole("list", { name: "Intake progress" })).toBeInTheDocument()
    expect(screen.queryByRole("region", { name: "Private idea" })).not.toBeInTheDocument()
  })

  it("authorizes pre-research, and the second step is done once a pre-research row exists", async () => {
    const user = userEvent.setup()
    await renderApp(`/ideas/${SHARED}/intake`)
    await screen.findByRole("list", { name: "Intake progress" })
    await waitFor(() => expect(steps()).toEqual(["Litmus(done)", "AI Pre-Research(to do)", "Decision(to do)"]))

    await user.click(screen.getByRole("button", { name: "Authorize AI pre-research" }))
    await waitFor(() => expect(steps()).toEqual(["Litmus(done)", "AI Pre-Research(done)", "Decision(to do)"]))
    expect(db.ideas.find((i) => i.id === SHARED)?.intake).toEqual({ pre_research_authorized: true, decision: "" })
    expect(screen.getByRole("button", { name: "Accept" })).toBeInTheDocument()
  })

  it("records Accept optimistically", async () => {
    const user = userEvent.setup()
    db.ideas.find((i) => i.id === SHARED)!.intake = { pre_research_authorized: true, decision: "" }
    let release = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    let sent: unknown
    server.use(
      http.patch("/api/v1/ideas/:id/intake", async ({ request }) => {
        sent = await request.json()
        await held
        return HttpResponse.json(db.ideas.find((i) => i.id === SHARED))
      }),
    )
    await renderApp(`/ideas/${SHARED}/intake`)
    await user.click(await screen.findByRole("button", { name: "Accept" }))

    // Before the server answers
    expect(await screen.findByText("Decision recorded: Accepted")).toBeInTheDocument()
    await waitFor(() => expect(sent).toEqual({ pre_research_authorized: true, decision: "accept" }))
    release()
  })

  it("rolls the decision back when the server refuses it", async () => {
    const user = userEvent.setup()
    server.use(http.patch("/api/v1/ideas/:id/intake", () => HttpResponse.json({ error: "decision refused" }, { status: 400 })))
    await renderApp(`/ideas/${SHARED}/intake`)
    await user.click(await screen.findByRole("button", { name: "Park" }))

    expect(await screen.findByText("decision refused")).toBeInTheDocument()
    await waitFor(() => expect(steps()).toContain("Decision(to do)"))
    expect(screen.queryByText("Decision recorded: Parked")).not.toBeInTheDocument()
  })

  it("adds a source with the sources category", async () => {
    const user = userEvent.setup()
    await renderApp(`/ideas/${SHARED}/intake`)
    await user.click(await screen.findByRole("button", { name: "Sources" }))
    expect(await screen.findByText("Station registry")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Add Source" }))
    expect(await screen.findByText("Untitled source")).toBeInTheDocument()
    expect(db.documents.at(-1)).toMatchObject({ name: "Untitled source", idea: SHARED, category: "sources" })
  })

  it("says so when the idea has no litmus test", async () => {
    const user = userEvent.setup()
    db.litmusTests = {}
    await renderApp(`/ideas/${SHARED}/intake`)
    await user.click(await screen.findByRole("button", { name: "Litmus" }))
    expect(await screen.findByText("No litmus test yet.")).toBeInTheDocument()
  })

  it("shows the newest litmus test first", async () => {
    const user = userEvent.setup()
    await renderApp(`/ideas/${SHARED}/intake`)
    await user.click(await screen.findByRole("button", { name: "Litmus" }))
    expect(await screen.findByText("Strong fit once narrowed to one region.")).toBeInTheDocument()
    expect(screen.getByRole("combobox", { name: "Litmus test" })).toHaveTextContent("Latest (Oct 12, 2026)")
  })

  it("edits the pitch in a side panel for its author only", async () => {
    const user = userEvent.setup()
    await renderApp(`/ideas/${MINE}/intake`)
    await user.click(await screen.findByRole("button", { name: "Edit pitch" }))
    const panel = await screen.findByRole("dialog", { name: "Edit idea" })
    const body = within(panel).getByLabelText("Idea")
    await user.type(body, "A shorter pitch.")
    await user.click(within(panel).getByRole("button", { name: "Save" }))

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(await within(screen.getByRole("region", { name: "Pitch" })).findByText("A shorter pitch.")).toBeInTheDocument()
  })

  it("has no Edit pitch on another actor's idea", async () => {
    await renderApp(`/ideas/${SHARED}/intake`)
    await screen.findByRole("region", { name: "Pitch" })
    expect(screen.queryByRole("button", { name: "Edit pitch" })).not.toBeInTheDocument()
  })

  it("renders Not Found for an idea the caller cannot see", async () => {
    await renderApp("/ideas/idea_000000000003/intake")
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeInTheDocument()
  })

  it("is where an idea's title links from the Ideas list", async () => {
    await renderApp("/ideas")
    const link = await screen.findByRole("link", { name: "Map every weather station" })
    expect(link).toHaveAttribute("href", `/ideas/${SHARED}/intake`)
  })
})
