import { useEffect } from "react"

// Scrolling down past this hides the header; any scroll up shows it
const HIDE_AFTER_PX = 24

/**
 * Hides the header on scroll down and shows it on scroll up, by toggling
 * `header-hidden` on <html> — not on the header itself, so other elements
 * (the sticky project toolbar, ScrollLogo) can react to it with
 * `:root.header-hidden` in index.css. `resetKey` (the current path)
 * clears it on navigation, so a new page never starts with the header
 * hidden.
 */
export function useHeaderScroll(resetKey: string) {
  useEffect(() => {
    const root = document.documentElement
    let lastY = window.scrollY

    const onScroll = () => {
      const y = window.scrollY
      root.classList.toggle("header-hidden", y >= HIDE_AFTER_PX && y > lastY)
      lastY = y
    }

    window.addEventListener("scroll", onScroll, { passive: true })
    return () => window.removeEventListener("scroll", onScroll)
  }, [])

  useEffect(() => {
    document.documentElement.classList.remove("header-hidden")
  }, [resetKey])
}
