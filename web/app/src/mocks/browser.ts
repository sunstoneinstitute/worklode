import { setupWorker } from "msw/browser"
import { handlers } from "./handlers"

/** MSW for `pnpm dev`: a service worker answers /api calls in the browser */
export const worker = setupWorker(...handlers)
