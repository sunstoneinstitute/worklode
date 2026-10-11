import { act, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { renderApp } from "@/test/render"

const firstColumn = () =>
  screen
    .getAllByRole("row")
    .slice(1)
    .map((row) => within(row).getAllByRole("cell")[0].textContent)

describe("table state in the URL", () => {
  it("applies filters from the URL", async () => {
    await renderApp("/tasks?status=blocked")
    await screen.findByRole("columnheader", { name: /Task Name/ })
    expect(firstColumn()).toEqual(["Ship static export"])
  })

  it("writes a sort into the URL, replacing the history entry", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/tasks")
    const entries = router.history.length

    await user.click(await screen.findByRole("columnheader", { name: /Task Name/ }))

    expect(router.state.location.search).toEqual({ sort: "name.asc" })
    expect(router.history.length).toBe(entries)
  })

  it("keeps every typed character of a search, in one history entry", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/tasks")
    const entries = router.history.length

    await user.click(await screen.findByRole("button", { name: /search/i }))
    await user.type(screen.getByRole("searchbox"), "launch")

    expect(screen.getByRole("searchbox")).toHaveValue("launch")
    expect(router.state.location.search).toEqual({ q: "launch" })
    expect(router.history.length).toBe(entries)
    expect(firstColumn()).toEqual(["Draft launch copy"])
  })

  it("opens search showing a number typed into the URL", async () => {
    await renderApp("/tasks?q=123")
    expect(await screen.findByRole("searchbox")).toHaveValue("123")
  })

  it("restores the filtered view on Back", async () => {
    const { router } = await renderApp("/tasks?status=blocked")
    await act(() => router.navigate({ to: "/ideas" }))
    await act(async () => router.history.back())
    await screen.findByRole("columnheader", { name: /Task Name/ })
    expect(firstColumn()).toEqual(["Ship static export"])
  })

  it("leaves the URL alone for the task list embedded in Home", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/")
    await user.click(await screen.findByRole("columnheader", { name: /Task Name/ }))
    expect(router.state.location.search).toEqual({})
  })
})

describe("chip category in the URL", () => {
  it("reads the category, falling back to the default for an unknown one", async () => {
    const { unmount } = await renderApp("/ideas?category=story-pitch")
    expect(screen.getByRole("button", { name: "Story Pitches" })).toHaveAttribute("aria-pressed", "true")
    unmount()

    await renderApp("/ideas?category=bogus")
    expect(screen.getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true")
  })

  it("writes the category, and leaves the default out of the URL", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/ideas")

    await user.click(screen.getByRole("button", { name: "General Ideas" }))
    expect(router.state.location.search).toEqual({ category: "general" })

    await user.click(screen.getByRole("button", { name: "All" }))
    expect(router.state.location.search).toEqual({})
  })

  it("keeps table params when the category changes", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/ideas?sort=title.asc")
    await user.click(screen.getByRole("button", { name: "General Ideas" }))
    expect(router.state.location.search).toEqual({ sort: "title.asc", category: "general" })
  })

  it("ignores a filter for a column the category doesn't have", async () => {
    await renderApp("/projects/the-grid/documents?category=spec&participants=u1")
    expect(screen.getByRole("button", { name: "Spec" })).toHaveAttribute("aria-pressed", "true")
    expect(screen.getAllByRole("row").length).toBeGreaterThan(1)
  })
})

describe("scroll position", () => {
  it("does not scroll the window on sort, search or chip changes", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/tasks")
    await screen.findByRole("columnheader", { name: /Task Name/ })
    // The initial render's own navigation (and the table's own load) may
    // legitimately scroll; only interactions after this point should be silent.
    const scrollSpy = vi.spyOn(window, "scrollTo")
    scrollSpy.mockClear()

    await user.click(screen.getByRole("columnheader", { name: /Task Name/ }))
    await user.click(screen.getByRole("button", { name: /search/i }))
    await user.type(screen.getByRole("searchbox"), "a")
    expect(scrollSpy).not.toHaveBeenCalled()

    await act(() => router.navigate({ to: "/ideas" }))
    scrollSpy.mockClear()
    await user.click(screen.getByRole("button", { name: "General Ideas" }))
    expect(scrollSpy).not.toHaveBeenCalled()

    scrollSpy.mockRestore()
  })
})
