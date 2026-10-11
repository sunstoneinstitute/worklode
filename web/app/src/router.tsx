import type { QueryClient } from "@tanstack/react-query"
import {
  type RouterHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  useParams,
  useRouter,
} from "@tanstack/react-router"
import { meQueryOptions } from "@/api/queries"
import { coerceSearch } from "@/components/ui/data-table/url-state"
import { LoadError } from "@/components/ui/load-state"
import { ProjectLayout } from "@/layouts/ProjectLayout"
import { RootLayout } from "@/layouts/RootLayout"
import { EditIdea } from "@/pages/EditIdea"
import { Home } from "@/pages/Home"
import { IdeaIntake } from "@/pages/IdeaIntake"
import { Ideas } from "@/pages/Ideas"
import { NewIdea } from "@/pages/NewIdea"
import { NotFound } from "@/pages/NotFound"
import { ProjectDocuments } from "@/pages/ProjectDocuments"
import { ProjectOverview } from "@/pages/ProjectOverview"
import { ProjectTasks } from "@/pages/ProjectTasks"
import { Projects } from "@/pages/Projects"
import { Tasks } from "@/pages/Tasks"

// Route components that read path params and hand them to the page
function EditIdeaRoute() {
  const { ideaId } = useParams({ from: "/ideas/$ideaId/edit" })
  return <EditIdea ideaId={ideaId} />
}

function IdeaIntakeRoute() {
  const { ideaId } = useParams({ from: "/ideas/$ideaId/intake" })
  return <IdeaIntake ideaId={ideaId} />
}

// Shown instead of TanStack Router's default "Something went wrong!" page
// when the root loader (fetching /api/me, below) fails — retrying re-runs
// every active route's loader.
function RootError() {
  const router = useRouter()
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
  // The header and Home's welcome need the signed-in user on every page
  loader: ({ context }) => context.queryClient.ensureQueryData(meQueryOptions),
})

const homeRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: Home })
const tasksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "tasks",
  component: Tasks,
  validateSearch: coerceSearch,
})
const ideasRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "ideas",
  component: Ideas,
  validateSearch: coerceSearch,
})
const newIdeaRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "ideas/new",
  component: NewIdea,
})
const editIdeaRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "ideas/$ideaId/edit",
  component: EditIdeaRoute,
})
const ideaIntakeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "ideas/$ideaId/intake",
  component: IdeaIntakeRoute,
})
const projectsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "projects",
  component: Projects,
  validateSearch: coerceSearch,
})

// Parent of a project's pages: the project toolbar, then the page
const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "projects/$projectId",
  component: ProjectLayout,
})
const projectOverviewRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: "/",
  component: ProjectOverview,
})
const projectTasksRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: "tasks",
  component: ProjectTasks,
  validateSearch: coerceSearch,
})
const projectDocumentsRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: "documents",
  component: ProjectDocuments,
  validateSearch: coerceSearch,
})

export const routeTree = rootRoute.addChildren([
  homeRoute,
  tasksRoute,
  ideasRoute,
  newIdeaRoute,
  editIdeaRoute,
  ideaIntakeRoute,
  projectsRoute,
  projectRoute.addChildren([projectOverviewRoute, projectTasksRoute, projectDocumentsRoute]),
])

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
