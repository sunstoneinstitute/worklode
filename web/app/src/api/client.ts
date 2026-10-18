import createClient, { type Middleware } from "openapi-fetch"
import { clearDrafts } from "@/hooks/use-draft"
import type { paths } from "./schema"

/** WL-REQ-1770: a session-authorized write carries the page header and a JSON body. */
const writeHeaders: Middleware = {
  onRequest({ request }) {
    if (request.method !== "GET" && request.method !== "HEAD") {
      request.headers.set("X-Requested-With", "lode-web")
      if (!request.headers.has("Content-Type")) request.headers.set("Content-Type", "application/json")
    }
    return request
  },
}

/** WL-REQ-1813: drafts can hold unpublished work, so they end with the session. */
const dropDraftsOn401: Middleware = {
  onResponse({ response }) {
    if (response.status === 401) clearDrafts()
    return response
  },
}

export const api = createClient<paths>({
  baseUrl: window.location.origin,
  credentials: "same-origin",
  // createClient captures its `fetch` option once, at module load, which in
  // tests runs before MSW's server.listen() has patched the global fetch.
  // This indirection reads globalThis.fetch at call time instead.
  fetch: (...args: Parameters<typeof fetch>) => globalThis.fetch(...args),
})
api.use(writeHeaders, dropDraftsOn401)
