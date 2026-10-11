import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { renderWithQuery } from "@/test/render"
import { UpcomingMeetings } from "./UpcomingMeetings"

describe("UpcomingMeetings", () => {
  it("lists meetings from the API", async () => {
    await renderWithQuery(<UpcomingMeetings />)
    expect(await screen.findAllByText("The Grid Startup Meeting")).toHaveLength(4)
    expect(screen.getAllByText("Today from 9am – 1:45pm")).toHaveLength(4)
  })

  it("respects limit", async () => {
    await renderWithQuery(<UpcomingMeetings limit={2} />)
    expect(await screen.findAllByText("The Grid Startup Meeting")).toHaveLength(2)
  })

  it("removes the loading placeholder once the meetings arrive", async () => {
    await renderWithQuery(<UpcomingMeetings />)
    await screen.findAllByText("The Grid Startup Meeting")
    expect(screen.queryByRole("status", { name: "Loading meetings" })).not.toBeInTheDocument()
  })

  it("shows an error with Retry, and Retry recovers", async () => {
    const user = userEvent.setup()
    server.use(http.get("/api/meetings", () => HttpResponse.json({ message: "down" }, { status: 500 }), { once: true }))
    await renderWithQuery(<UpcomingMeetings />)

    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load meetings.")
    await user.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findAllByText("The Grid Startup Meeting")).toHaveLength(4)
  })
})
