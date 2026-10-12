import type { CSSProperties } from "react"
import { Link } from "@tanstack/react-router"

// Matches Figma's Mobile Components > Menu Mobile ("Open"), cut to the
// pages the server serves.

// Waits for the panel's roll-down to mostly finish before items start
// fading in on open (see [data-mobile-menu-item] in index.css), then
// staggers each item after that by this step. Set as a CSS custom
// property: index.css only applies it while opening, and closing uses a
// fixed, fast, unstaggered delay instead.
const STAGGER_BASE_MS = 180
const STAGGER_STEP_MS = 60

function itemDelay(index: number) {
  return { "--item-delay": `${STAGGER_BASE_MS + index * STAGGER_STEP_MS}ms` } as CSSProperties
}

export function MobileMenu({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <div data-mobile-menu className="fixed inset-x-0 top-13 bottom-0 z-40 lg:hidden">
      <div
        data-mobile-menu-panel
        className="flex h-full flex-col items-center gap-22.75 overflow-y-auto bg-primary px-5 pt-12.5 pb-10"
      >
        <Link
          to="/projects"
          data-mobile-menu-item
          style={itemDelay(0)}
          className="text-xl font-medium text-text-light"
          onClick={onNavigate}
        >
          Projects
        </Link>
      </div>
    </div>
  )
}
