import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { QueryClientProvider } from "@tanstack/react-query"
import { RouterProvider } from "@tanstack/react-router"
import "./index.css"
import { createQueryClient } from "./api/query-client"
import { createAppRouter } from "./router"

/**
 * In development, MSW answers /api calls with dummy data unless
 * VITE_API_MOCKS=off, in which case Vite proxies them to the Go server
 * (see vite.config.ts). The dynamic import keeps MSW out of production.
 */
async function startMockApi() {
  if (!import.meta.env.DEV || import.meta.env.VITE_API_MOCKS === "off") return
  const { worker } = await import("./mocks/browser")
  await worker.start({ onUnhandledRequest: "bypass" })
}

await startMockApi()

const queryClient = createQueryClient()
const router = createAppRouter({ queryClient })

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
