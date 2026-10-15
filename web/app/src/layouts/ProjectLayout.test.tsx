import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { act, render } from "@testing-library/react"
import { QueryClientProvider } from "@tanstack/react-query"
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router"
import { createQueryClient } from "@/api/query-client"
import { ProjectLayout } from "./ProjectLayout"

// The layout under a throwaway router: a root, the project route, and one
// child page. The app's own route tree has no child yet.
async function renderLayout(path: string) {
  const rootRoute = createRootRoute()
  const projectRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "projects/$projectId",
    component: ProjectLayout,
  })
  const childRoute = createRoute({ getParentRoute: () => projectRoute, path: "/", component: () => <p>child page</p> })
  const router = createRouter({
    routeTree: rootRoute.addChildren([projectRoute.addChildren([childRoute])]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={createQueryClient({ test: true })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  await act(async () => {})
}

describe("ProjectLayout", () => {
  it("shows the Not Found page for a project the server does not know", async () => {
    await renderLayout("/projects/p9")
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeInTheDocument()
  })

  it("shows the toolbar with the project's name", async () => {
    await renderLayout("/projects/p1")
    expect(await screen.findByText((_, el) => el?.tagName === "P" && el.textContent === "Projects / Worklode")).toBeInTheDocument()
    expect(screen.getByText("child page")).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "Tasks" })).toHaveAttribute("href", "/projects/p1/tasks")
  })

  it("shows an error with Retry on a 500, and the toolbar once the retry succeeds", async () => {
    server.use(http.get("/api/v1/projects/p1", () => HttpResponse.json({ error: "boom" }, { status: 500 })))
    await renderLayout("/projects/p1")
    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load this project.")

    server.resetHandlers()
    await userEvent.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findByText((_, el) => el?.tagName === "P" && el.textContent === "Projects / Worklode")).toBeInTheDocument()
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })
})
