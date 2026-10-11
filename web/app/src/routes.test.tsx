import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it, vi } from "vitest"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

describe("routes", () => {
  it.each([
    ["/", "Welcome, @Claudicia!"],
    ["/tasks", "Tasks"],
    ["/ideas", "Ideas"],
    ["/ideas/new", "New Idea"],
    ["/ideas/i2/edit", "Edit Idea"],
    ["/ideas/i1/intake", "Where the glaciers went"],
    ["/projects", "Projects"],
    ["/projects/the-grid/tasks", "Tasks"],
    ["/projects/the-grid/documents", "Documents"],
    ["/no-such-page", "Page not found"],
  ])("%s renders its page", async (path, heading) => {
    await renderApp(path)
    expect((await screen.findAllByRole("heading", { name: heading })).length).toBeGreaterThan(0)
  })

  it("/projects/the-grid renders Project Overview", async () => {
    await renderApp("/projects/the-grid")
    expect(screen.getByText("Project Summary")).toBeInTheDocument()
  })

  it("the all-projects Tasks page has a Project column; a project's Tasks page doesn't", async () => {
    const { unmount } = await renderApp("/tasks")
    expect(await screen.findByRole("columnheader", { name: /^Project/ })).toBeInTheDocument()
    unmount()

    await renderApp("/projects/the-grid/tasks")
    expect(screen.queryByRole("columnheader", { name: /^Project/ })).not.toBeInTheDocument()
  })

  it("links to a project's pages with its id", async () => {
    await renderApp("/projects/melting-glaciers/tasks")
    // The toolbar and the mobile menu's folder card each have one
    const links = screen.getAllByRole("link", { name: "Documents" })
    expect(links.length).toBeGreaterThan(0)
    for (const link of links) {
      expect(link).toHaveAttribute("href", "/projects/melting-glaciers/documents")
    }
  })

  it.each(["/", "/tasks", "/ideas/i1/intake", "/projects/the-grid", "/projects/the-grid/documents", "/nope"])(
    "%s has exactly one header and one mobile menu",
    async (path) => {
      await renderApp(path)
      expect(screen.getAllByRole("banner")).toHaveLength(1)
      expect(document.querySelectorAll("[data-mobile-menu]")).toHaveLength(1)
    },
  )

  it("a project's pages get the project toolbar and the travelling logo; other pages don't", async () => {
    const { unmount } = await renderApp("/projects/the-grid/tasks")
    expect(document.querySelectorAll("[data-secondary-toolbar]")).toHaveLength(1)
    expect(document.querySelectorAll("[data-scroll-logo]")).toHaveLength(1)
    unmount()

    await renderApp("/tasks")
    expect(document.querySelector("[data-secondary-toolbar]")).toBeNull()
    expect(document.querySelector("[data-scroll-logo]")).toBeNull()
  })

  it("shows an error with Retry when the root loader (/api/me) fails, and Retry recovers", async () => {
    const user = userEvent.setup()
    // React logs the caught error to console.error even though the
    // errorComponent handles it — expected for this test, so silence it.
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {})
    server.use(http.get("/api/me", () => HttpResponse.json({ message: "down" }, { status: 500 }), { once: true }))
    await renderApp("/")

    expect(await screen.findByRole("alert")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Retry" }))
    expect((await screen.findAllByRole("heading", { name: "Welcome, @Claudicia!" })).length).toBeGreaterThan(0)

    consoleError.mockRestore()
  })
})
