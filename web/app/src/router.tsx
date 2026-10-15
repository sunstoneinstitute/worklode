import type { QueryClient } from "@tanstack/react-query"
import {
  type ErrorComponentProps,
  type RouterHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  useRouter,
} from "@tanstack/react-router"
import { LoginRedirect, meQueryOptions } from "@/api/queries"
import { coerceSearch } from "@/components/ui/data-table/url-state"
import { LoadError } from "@/components/ui/load-state"
import { ProjectLayout } from "@/layouts/ProjectLayout"
import { RootLayout } from "@/layouts/RootLayout"
import { NotFound } from "@/pages/NotFound"
import { ProjectCrew } from "@/pages/ProjectCrew"
import { Projects } from "@/pages/Projects"

// Shown instead of TanStack Router's default "Something went wrong!" page
// when the root loader (fetching whoami, below) fails. Retrying re-runs
// every active route's loader. Blank while a login redirect is under way.
function RootError({ error }: ErrorComponentProps) {
  const router = useRouter()
  if (error instanceof LoginRedirect) return null
  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 lg:p-page">
        <LoadError message="Couldn't load the app." onRetry={() => router.invalidate()} />
      </main>
    </div>
  )
}

const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: RootLayout,
  notFoundComponent: NotFound,
  errorComponent: RootError,
  // The header needs the signed-in actor on every page
  loader: ({ context }) => context.queryClient.ensureQueryData(meQueryOptions),
})

const projectsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "projects",
  component: Projects,
  validateSearch: coerceSearch,
})

// Pages under a project hang off this route as they are ported
const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "projects/$projectId",
  component: ProjectLayout,
})

const projectCrewRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: "crew",
  component: ProjectCrew,
})

export const routeTree = rootRoute.addChildren([projectsRoute, projectRoute.addChildren([projectCrewRoute])])

/**
 * The app's router. Browser history by default; tests pass a memory
 * history. Loaders get the query client through the router context.
 */
export function createAppRouter({
  history,
  queryClient,
}: {
  history?: RouterHistory
  queryClient: QueryClient
}) {
  return createRouter({ routeTree, history, context: { queryClient }, scrollRestoration: true })
}

// Makes every Link, useNavigate and useParams type-check against these routes
declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
