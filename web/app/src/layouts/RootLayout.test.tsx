import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it } from "vitest"
import { renderApp } from "@/test/render"

const menuOpen = () => document.documentElement.classList.contains("mobile-menu-open")
const menu = () => within(document.querySelector<HTMLElement>("[data-mobile-menu]")!)

describe("mobile menu", () => {
  afterEach(() => document.documentElement.classList.remove("mobile-menu-open"))

  it("opens and closes from the header button", async () => {
    const user = userEvent.setup()
    await renderApp("/")

    const toggle = screen.getByRole("button", { name: "Open menu" })
    expect(toggle).toHaveAttribute("aria-expanded", "false")
    await user.click(toggle)
    expect(menuOpen()).toBe(true)
    expect(screen.getByRole("button", { name: "Close menu" })).toHaveAttribute("aria-expanded", "true")

    await user.click(screen.getByRole("button", { name: "Close menu" }))
    expect(menuOpen()).toBe(false)
  })

  it("closes on Escape", async () => {
    const user = userEvent.setup()
    await renderApp("/")
    await user.click(screen.getByRole("button", { name: "Open menu" }))
    await user.keyboard("{Escape}")
    expect(menuOpen()).toBe(false)
  })

  it("closes when a menu link navigates", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/")
    await user.click(screen.getByRole("button", { name: "Open menu" }))
    await user.click(menu().getByRole("link", { name: "Tasks" }))
    expect(router.state.location.pathname).toBe("/tasks")
    expect(menuOpen()).toBe(false)
  })

  it("closes when the link is for the page already showing", async () => {
    const user = userEvent.setup()
    await renderApp("/tasks")
    await user.click(screen.getByRole("button", { name: "Open menu" }))
    await user.click(menu().getByRole("link", { name: "Tasks" }))
    expect(menuOpen()).toBe(false)
  })

  it("closes from a project folder card link", async () => {
    const user = userEvent.setup()
    const { router } = await renderApp("/projects/the-grid")
    await user.click(screen.getByRole("button", { name: "Open menu" }))
    await user.click(menu().getByRole("link", { name: "Documents" }))
    expect(router.state.location.pathname).toBe("/projects/the-grid/documents")
    expect(menuOpen()).toBe(false)
  })
})
