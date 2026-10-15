import { QueryClient } from "@tanstack/react-query"
import type { AnyRoute } from "@tanstack/react-router"
import { describe, expect, it } from "vitest"
import routes from "@/routes.json"
import { createAppRouter } from "@/router"

// Every leaf route's full path, the list the Go server keeps in spaRoutes
function leafPaths(route: AnyRoute): string[] {
  const children = (route.children ?? []) as AnyRoute[]
  if (children.length === 0) return [route.fullPath]
  return children.flatMap(leafPaths)
}

describe("route tree", () => {
  it("matches routes.json (WL-REQ-1305)", () => {
    // Full paths are computed when a router is built from the tree
    const router = createAppRouter({ queryClient: new QueryClient() })
    // The project layout has no page of its own until its children are ported
    const paths = leafPaths(router.routeTree).filter((path) => path !== "/projects/$projectId")
    expect(paths.sort()).toEqual([...routes].sort())
  })
})
