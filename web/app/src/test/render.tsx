import type { ReactElement } from "react"
import { act, render } from "@testing-library/react"
import { QueryClientProvider } from "@tanstack/react-query"
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router"
import { createQueryClient } from "@/api/query-client"
import { createAppRouter } from "@/router"

/**
 * Renders one component inside a throwaway router, at `path`. Every path
 * renders the same component, so a test can click something that
 * navigates and then check `router.state.location`.
 */
export async function renderWithRouter(ui: ReactElement, { path = "/" }: { path?: string } = {}) {
  const rootRoute = createRootRoute({ component: Outlet })
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => ui })
  const splatRoute = createRoute({ getParentRoute: () => rootRoute, path: "$", component: () => ui })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, splatRoute]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  const queryClient = createQueryClient({ test: true })
  const result = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  await act(async () => {})
  return { ...result, router, queryClient }
}

/** Renders the real app, with its real routes and layouts, at `path`. */
export async function renderApp(path: string) {
  const queryClient = createQueryClient({ test: true })
  const router = createAppRouter({ history: createMemoryHistory({ initialEntries: [path] }), queryClient })
  await router.load()
  const result = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  await act(async () => {})
  return { ...result, router, queryClient }
}

/** Renders a component that uses queries but no router */
export async function renderWithQuery(ui: ReactElement) {
  const queryClient = createQueryClient({ test: true })
  const result = render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>)
  await act(async () => {})
  return { ...result, queryClient }
}
