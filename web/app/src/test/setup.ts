import "@testing-library/jest-dom/vitest"
import { configure } from "@testing-library/react"
import { afterAll, afterEach, beforeAll } from "vitest"
import { resetDb } from "@/mocks/db"
import { server } from "@/mocks/node"

// A shared CI runner can take over a second to load a lock and mount BlockNote;
// the 1000 ms default made the editor waits flaky (WL-1448)
configure({ asyncUtilTimeout: 5000 })

// Every test talks to the mock API; an unmocked request is a test bug
beforeAll(() => server.listen({ onUnhandledRequest: "error" }))
afterEach(() => {
  server.resetHandlers()
  resetDb()
})
afterAll(() => server.close())

// Mock ResizeObserver for cmdk and other libraries
globalThis.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
} as any

// Mock Element.prototype.scrollIntoView for cmdk
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {}
}

// Radix Select uses pointer capture, which jsdom doesn't implement
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
}

// jsdom has no matchMedia; default to "no match" (tests can stub their own)
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
}

// jsdom's scrollTo only logs "not implemented"; the router's scroll
// restoration calls it on every navigation
window.scrollTo = (() => {}) as typeof window.scrollTo
