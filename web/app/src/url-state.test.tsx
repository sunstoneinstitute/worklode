import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { renderApp } from "@/test/render"

const firstColumn = () =>
  screen
    .getAllByRole("row")
    .slice(1)
    .map((row) => within(row).getAllByRole("cell")[0].textContent)

describe("table state in the URL", () => {
  it("applies a sort from the URL", async () => {
    await renderApp("/projects?sort=name.desc")
    await screen.findByRole("columnheader", { name: /Name/ })
    expect(firstColumn()).toEqual(["Worklode", "Data platform"])
  })

  it("writes a sort into the URL, replacing the history entry", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/projects")
    const entries = router.history.length

    await user.click(await screen.findByRole("columnheader", { name: /Name/ }))

    expect(router.state.location.search).toEqual({ sort: "name.asc" })
    expect(router.history.length).toBe(entries)
  })

  it("keeps every typed character of a search, in one history entry", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/projects")
    const entries = router.history.length

    await user.click(await screen.findByRole("button", { name: /search/i }))
    await user.type(screen.getByRole("searchbox"), "data")

    expect(screen.getByRole("searchbox")).toHaveValue("data")
    expect(router.state.location.search).toEqual({ q: "data" })
    expect(router.history.length).toBe(entries)
    expect(firstColumn()).toEqual(["Data platform"])
  })
})
