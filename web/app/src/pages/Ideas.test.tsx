import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { afterEach, describe, expect, it } from "vitest"
import type { components } from "@/api/schema"
import { api } from "@/api/client"
import { db } from "@/mocks/db"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

const DRAFT = "worklode:v1:draft:actor-1:idea"

afterEach(() => {
  server.events.removeAllListeners()
  localStorage.clear()
})

function row(name: string) {
  return screen.getByRole("row", { name: new RegExp(name) })
}

describe("/ideas", () => {
  it("shows my private idea with Share and Edit, and another actor's shared idea with neither", async () => {
    await renderApp("/ideas")
    const mine = await screen.findByRole("row", { name: /Glacier retreat explainer/ })
    expect(mine.querySelector('[data-column-id="status"]')).toHaveTextContent("Private")
    expect(within(mine).getByRole("button", { name: "Share idea" })).toBeInTheDocument()
    expect(within(mine).getByRole("button", { name: "Edit idea" })).toBeInTheDocument()

    const theirs = row("Map every weather station")
    expect(theirs.querySelector('[data-column-id="status"]')).toHaveTextContent("Shared · Intake")
    expect(within(theirs).queryByRole("button", { name: "Share idea" })).not.toBeInTheDocument()
    expect(within(theirs).queryByRole("button", { name: "Edit idea" })).not.toBeInTheDocument()
    expect(screen.queryByText("Bob's secret")).not.toBeInTheDocument()
  })

  it("asks before sharing, posts the share and shows the row in intake", async () => {
    const user = userEvent.setup()
    let shared: string | undefined
    server.events.on("request:start", ({ request }) => {
      if (request.method === "POST" && request.url.endsWith("/share")) shared = request.url
    })
    await renderApp("/ideas")
    await user.click(within(await screen.findByRole("row", { name: /Glacier retreat/ })).getByRole("button", { name: "Share idea" }))
    const dialog = screen.getByRole("dialog")
    expect(dialog).toHaveTextContent("cannot be made private again")
    await user.click(within(dialog).getByRole("button", { name: "Yes" }))

    await waitFor(() => expect(row("Glacier retreat").querySelector('[data-column-id="status"]')).toHaveTextContent("Intake"))
    expect(shared).toMatch(/\/api\/v1\/ideas\/idea_000000000001\/share$/)
    expect(within(row("Glacier retreat")).queryByRole("button", { name: "Share idea" })).not.toBeInTheDocument()
  })

  it("shows the server's 409 as a toast and keeps the row private", async () => {
    const user = userEvent.setup()
    db.intakeConfigured = false
    await renderApp("/ideas")
    await user.click(within(await screen.findByRole("row", { name: /Glacier retreat/ })).getByRole("button", { name: "Share idea" }))
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Yes" }))

    expect(await screen.findByText("intake project not configured")).toBeInTheDocument()
    expect(row("Glacier retreat").querySelector('[data-column-id="status"]')).toHaveTextContent("Private")
  })

  it("removes a deleted row", async () => {
    const user = userEvent.setup()
    await renderApp("/ideas")
    await user.click(within(await screen.findByRole("row", { name: /Glacier retreat/ })).getByRole("button", { name: "Delete idea" }))
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Yes" }))
    await waitFor(() => expect(screen.queryByRole("row", { name: /Glacier retreat/ })).not.toBeInTheDocument())
    expect(db.ideas.some((i) => i.id === "idea_000000000001")).toBe(false)
  })
})

describe("/ideas/new", () => {
  it("shows the story fields only for a story pitch, and no status field", async () => {
    const user = userEvent.setup()
    await renderApp("/ideas/new")
    expect(screen.getByLabelText("Title")).toBeInTheDocument()
    expect(screen.queryByLabelText("Story type")).not.toBeInTheDocument()
    expect(screen.queryByLabelText("Litmus test result ID")).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/status/i)).not.toBeInTheDocument()

    await user.click(screen.getByLabelText("Type"))
    await user.click(screen.getByRole("option", { name: "Story Pitch" }))
    expect(screen.getByLabelText("Story type")).toBeInTheDocument()
    expect(screen.getByLabelText("Litmus test result ID")).toBeInTheDocument()
  })

  it("posts a new idea and goes back to Ideas", async () => {
    const user = userEvent.setup()
    let posted: components["schemas"]["CreateIdeaInput"] | undefined
    server.events.on("request:start", async ({ request }) => {
      if (request.method === "POST" && request.url.endsWith("/api/v1/ideas")) posted = await request.clone().json()
    })
    const { router } = await renderApp("/ideas/new")
    await user.type(screen.getByLabelText("Title"), "Ocean heat")
    await user.click(screen.getByRole("button", { name: "Submit" }))

    await waitFor(() => expect(router.state.location.pathname).toBe("/ideas"))
    expect(posted).toMatchObject({ title: "Ocean heat", type: "general" })
    expect(await screen.findByRole("row", { name: /Ocean heat/ })).toBeInTheDocument()
    expect(localStorage.getItem(DRAFT)).toBeNull()
  })

  it("shows each 422 field message under its field, and an unshown field's at the top", async () => {
    const user = userEvent.setup()
    server.use(
      http.post("/api/v1/ideas", () =>
        HttpResponse.json(
          { error: "invalid idea", fields: { title: "Give your idea a title.", author: "Who are you?" } },
          { status: 422 },
        ),
      ),
    )
    const { router } = await renderApp("/ideas/new")
    await user.type(screen.getByLabelText("Title"), "x")
    await user.click(screen.getByRole("button", { name: "Submit" }))

    expect(await screen.findByText("Give your idea a title.")).toBeInTheDocument()
    expect(screen.getByLabelText("Title")).toHaveAccessibleDescription("Give your idea a title.")
    expect(screen.getByText("Who are you?")).toBeInTheDocument()
    expect(router.state.location.pathname).toBe("/ideas/new")
  })

  it("keeps the values and shows a toast on any other error", async () => {
    const user = userEvent.setup()
    server.use(http.post("/api/v1/ideas", () => HttpResponse.json({ error: "down" }, { status: 500 })))
    await renderApp("/ideas/new")
    await user.type(screen.getByLabelText("Title"), "Ocean heat")
    await user.click(screen.getByRole("button", { name: "Submit" }))

    expect(await screen.findByText("Couldn't save the idea: down")).toBeInTheDocument()
    expect(screen.getByLabelText("Title")).toHaveValue("Ocean heat")
  })

  it("restores a draft after a remount, and Discard clears it", async () => {
    const user = userEvent.setup()
    const first = await renderApp("/ideas/new")
    await user.type(screen.getByLabelText("Title"), "Half a thought")
    await waitFor(() => expect(localStorage.getItem(DRAFT)).toContain("Half a thought"))
    first.unmount()

    await renderApp("/ideas/new")
    expect(screen.getByLabelText("Title")).toHaveValue("Half a thought")
    await user.click(screen.getByRole("button", { name: "Discard" }))
    expect(screen.getByLabelText("Title")).toHaveValue("")
    expect(localStorage.getItem(DRAFT)).toBeNull()
  })

  it("asks before leaving with unsaved changes, and Discard leaves", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/ideas/new")
    await user.type(screen.getByLabelText("Title"), "Unsent")
    await user.click(screen.getByRole("button", { name: "Cancel" }))

    const dialog = screen.getByRole("dialog")
    expect(dialog).toHaveTextContent("You have unsaved changes.")
    expect(within(dialog).getByRole("button", { name: "Keep editing" })).toHaveFocus()
    await user.click(within(dialog).getByRole("button", { name: "Discard" }))
    await waitFor(() => expect(router.state.location.pathname).toBe("/ideas"))
    expect(localStorage.getItem(DRAFT)).toBeNull()
  })

  it("deletes every draft when a request answers 401", async () => {
    localStorage.setItem(DRAFT, JSON.stringify({ title: "a" }))
    localStorage.setItem(`${DRAFT}:idea_000000000001`, JSON.stringify({ title: "b" }))
    localStorage.setItem("worklode.theme", "dark")
    server.use(http.get("/api/v1/ideas", () => HttpResponse.json({ error: "no session" }, { status: 401 })))
    await api.GET("/api/v1/ideas")

    expect(localStorage.getItem(DRAFT)).toBeNull()
    expect(localStorage.getItem(`${DRAFT}:idea_000000000001`)).toBeNull()
    expect(localStorage.getItem("worklode.theme")).toBe("dark")
  })
})

describe("/ideas/$ideaId/edit", () => {
  it("opens with the idea's values and patches it", async () => {
    const user = userEvent.setup()
    let patched: components["schemas"]["PatchIdeaInput"] | undefined
    server.events.on("request:start", async ({ request }) => {
      if (request.method === "PATCH") patched = await request.clone().json()
    })
    const { router } = await renderApp("/ideas/idea_000000000001/edit")
    const title = await screen.findByLabelText("Title")
    expect(title).toHaveValue("Glacier retreat explainer")
    expect(screen.getByLabelText("Story type")).toHaveTextContent("Data Story")
    await user.clear(title)
    await user.type(title, "Glaciers, retreating")
    await user.click(screen.getByRole("button", { name: "Save" }))

    await waitFor(() => expect(router.state.location.pathname).toBe("/ideas"))
    expect(patched).toMatchObject({ title: "Glaciers, retreating", type: "story-pitch", story_type: "data-story" })
  })

  it("renders Not Found for an idea the caller cannot see", async () => {
    await renderApp("/ideas/idea_000000000003/edit")
    expect(await screen.findByText("Page not found")).toBeInTheDocument()
  })
})
