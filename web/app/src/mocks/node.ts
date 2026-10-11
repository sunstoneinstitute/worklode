import { setupServer } from "msw/node"
import { handlers } from "./handlers"

/** MSW for tests: answers fetch calls in Node with the same handlers */
export const server = setupServer(...handlers)
