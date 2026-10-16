import { QueryClient } from "@tanstack/react-query"
import type { AnyRoute } from "@tanstack/react-router"
import { describe, expect, it } from "vitest"
import routes from "@/routes.json"
import { createAppRouter } from "@/router"

// Every leaf route's full path, the list the Go server keeps in spaRoutes.
// An index route's full path ends in "/", which the server's path does not.
function leafPaths(route: AnyRoute): string[] {
  const children = (route.children ?? []) as AnyRoute[]
  if (children.length === 0) return [route.fullPath.replace(/(.)\/$/, "$1")]
  return children.flatMap(leafPaths)
}

describe("route tree", () => {
  it("matches routes.json (WL-REQ-1305)", () => {
    // Full paths are computed when a router is built from the tree
    const router = createAppRouter({ queryClient: new QueryClient() })
    const paths = leafPaths(router.routeTree)
    expect(paths.sort()).toEqual([...routes].sort())
  })
})
