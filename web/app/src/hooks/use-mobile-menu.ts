import { useEffect, useState } from "react"

/**
 * Open/closed state for the mobile menu. While open, `mobile-menu-open` is
 * on <html>; index.css keys the panel's roll-down, the staggered items and
 * the hamburger/close icon swap off it. Closes on Escape, and by itself
 * whenever `pathname` changes: the menu remembers which page it was opened
 * on and counts as open only there.
 */
export function useMobileMenu(pathname: string) {
  const [openOn, setOpenOn] = useState<string | null>(null)
  const open = openOn === pathname

  useEffect(() => {
    const root = document.documentElement
    root.classList.toggle("mobile-menu-open", open)
    return () => root.classList.remove("mobile-menu-open")
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpenOn(null)
    }
    document.addEventListener("keydown", onKeyDown)
    return () => document.removeEventListener("keydown", onKeyDown)
  }, [open])

  return {
    open,
    toggle: () => setOpenOn(open ? null : pathname),
    close: () => setOpenOn(null),
  }
}
