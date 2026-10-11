import { renderHook } from "@testing-library/react"
import { afterEach, describe, expect, it } from "vitest"
import { useHeaderScroll } from "./use-header-scroll"

function scrollTo(y: number) {
  Object.defineProperty(window, "scrollY", { value: y, configurable: true })
  window.dispatchEvent(new Event("scroll"))
}

const hidden = () => document.documentElement.classList.contains("header-hidden")

describe("useHeaderScroll", () => {
  afterEach(() => {
    Object.defineProperty(window, "scrollY", { value: 0, configurable: true })
    document.documentElement.classList.remove("header-hidden")
  })

  it("stays visible for small scrolls near the top", () => {
    renderHook(() => useHeaderScroll("/"))
    scrollTo(20)
    expect(hidden()).toBe(false)
  })

  it("hides when scrolling down past 24px", () => {
    renderHook(() => useHeaderScroll("/"))
    scrollTo(100)
    expect(hidden()).toBe(true)
  })

  it("shows again on any scroll up", () => {
    renderHook(() => useHeaderScroll("/"))
    scrollTo(300)
    scrollTo(250)
    expect(hidden()).toBe(false)
  })

  it("shows again when the path changes, even without a scroll", () => {
    const { rerender } = renderHook(({ path }) => useHeaderScroll(path), {
      initialProps: { path: "/tasks" },
    })
    scrollTo(300)
    expect(hidden()).toBe(true)
    rerender({ path: "/ideas" })
    expect(hidden()).toBe(false)
  })

  it("stops listening once unmounted", () => {
    const { unmount } = renderHook(() => useHeaderScroll("/"))
    unmount()
    scrollTo(300)
    expect(hidden()).toBe(false)
  })
})
