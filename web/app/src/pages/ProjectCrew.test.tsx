import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import type { components } from "@/api/schema"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

describe("/projects/$projectId/crew", () => {
  it("shows the lead with a Lead badge and no Remove", async () => {
    await renderApp("/projects/p1/crew")
    const lead = await screen.findByRole("row", { name: /Stig Bakken/ })
    expect(within(lead).getByText("Lead")).toBeInTheDocument()
    expect(within(lead).queryByRole("button", { name: "Remove" })).not.toBeInTheDocument()
    const member = screen.getByRole("row", { name: /Ada Lovelace/ })
    expect(within(member).getByText("member")).toBeInTheDocument()
    expect(within(member).getByRole("button", { name: "Remove" })).toBeInTheDocument()
  })

  it("adds a member and shows them after the refetch", async () => {
    const user = userEvent.setup()
    let posted: components["schemas"]["AddCrewMemberInput"] | undefined
    server.events.on("request:start", async ({ request }) => {
      if (request.method === "POST") posted = await request.clone().json()
    })
    await renderApp("/projects/p1/crew")
    await screen.findByRole("row", { name: /Ada Lovelace/ })

    await user.click(screen.getByRole("combobox", { name: "Person" }))
    // Only people not already on the Crew are offered
    expect(screen.queryByRole("option", { name: "Ada Lovelace" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("option", { name: "Bob" }))
    await user.type(screen.getByRole("textbox", { name: "Role" }), "editor")
    await user.click(screen.getByRole("button", { name: "Add" }))

    const bob = await screen.findByRole("row", { name: /Bob/ })
    expect(within(bob).getByText("editor")).toBeInTheDocument()
    expect(posted).toEqual({ actor: "bob", role: "editor", lead: false })
    server.events.removeAllListeners()
  })

  it("shows the server's message when a removal is refused", async () => {
    const user = userEvent.setup()
    const message = "actor ada still owns open work on project p1: task WL-7 (Fix it): invalid input"
    server.use(
      http.delete("/api/v1/projects/:id/participants/:actor", () =>
        HttpResponse.json({ error: message }, { status: 422 }),
      ),
    )
    await renderApp("/projects/p1/crew")
    const ada = await screen.findByRole("row", { name: /Ada Lovelace/ })
    await user.click(within(ada).getByRole("button", { name: "Remove" }))
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Yes" }))

    expect(await screen.findByRole("alert")).toHaveTextContent(message)
    expect(screen.getByRole("row", { name: /Ada Lovelace/ })).toBeInTheDocument()
  })

  it("says No Crew yet for an empty roster", async () => {
    await renderApp("/projects/p2/crew")
    expect(await screen.findByText("No Crew yet")).toBeInTheDocument()
  })
})
