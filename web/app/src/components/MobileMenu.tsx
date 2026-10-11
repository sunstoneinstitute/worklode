import type { CSSProperties } from "react"
import { Link } from "@tanstack/react-router"
import { FolderCurve } from "@/components/FolderCurve"
import { PROJECT_NAV_ITEMS, PROJECT_NAV_SETTINGS } from "@/lib/project-nav"

// Matches Figma's Mobile Components > Menu Mobile:
// - "Open" (used on Home): plain links only.
// - "Open Projects" (used on Project Overview): same links, plus a "project
//   folder" card reusing the exact same nav items as the desktop
//   ProjectToolbar (@/lib/project-nav).
type MobileMenuProps = {
  variant?: "default" | "project"
  projectName?: string
  projectId?: string
  onNavigate?: () => void
}

// Waits for the panel's roll-down to mostly finish before items start
// fading in on open (see [data-mobile-menu-item] in index.css), then
// staggers each item after that by this step — kept short so the whole
// thing still feels snappy rather than sluggish. Set as a CSS custom
// property, not a plain transitionDelay: index.css only applies it while
// opening — closing uses a fixed, fast, unstaggered delay instead (see the
// user's request for the close animation), which a plain transitionDelay
// couldn't express since it'd apply to both directions equally.
const STAGGER_BASE_MS = 180
const STAGGER_STEP_MS = 60

function itemDelay(index: number) {
  return { "--item-delay": `${STAGGER_BASE_MS + index * STAGGER_STEP_MS}ms` } as CSSProperties
}

function ProjectFolderCard({
  projectName,
  projectId,
  onNavigate,
}: {
  projectName: string
  projectId: string
  onNavigate?: () => void
}) {
  return (
    <div className="flex w-86.5 flex-col overflow-hidden rounded-panel">
      {/* Small version of the desktop toolbar's folder-curve treatment: a
          tinted strip with the same FolderCurve anchored to its right edge
          (not stretched — see ProjectToolbar.tsx for why). Cropped to the
          strip's full width (not a narrow sliver) so enough of the curve's
          horizontal extent shows to still read as a smooth curve rather
          than an abrupt diagonal — a too-narrow crop is what made this look
          skewed originally. No separate solid-color backing needed here:
          the curve's own transparent cutout correctly reveals this
          container's own bg-primary/10 tint (matching Figma), unlike the
          desktop toolbar's fill color, which isn't tinted. */}
      <div className="relative -mb-px h-9.5 w-full shrink-0 overflow-hidden bg-primary/10">
        <FolderCurve className="absolute inset-y-0 right-0 h-full w-full" />
      </div>
      <div className="flex flex-col gap-2.5 rounded-tr-[20px] bg-background px-6 pb-8">
        <div className="flex flex-col items-center pb-2.5 text-center">
          <p className="text-xs font-medium tracking-[0.6px] text-text-primary">
            ACTIVE PROJECT
          </p>
          <p className="text-base font-semibold text-text-primary">{projectName}</p>
        </div>
        <div className="grid grid-cols-2 gap-x-12.5 gap-y-2.5">
          {[...PROJECT_NAV_ITEMS, PROJECT_NAV_SETTINGS].map(({ label, Icon, to }) =>
            to ? (
              <Link
                key={label}
                to={to}
                params={{ projectId }}
                onClick={onNavigate}
                className="flex items-center gap-2 text-sm font-medium text-text-primary"
              >
                <Icon size={24} />
                {label}
              </Link>
            ) : (
              // No page yet: shown, but not a link
              <span
                key={label}
                role="link"
                aria-disabled="true"
                className="flex items-center gap-2 text-sm font-medium text-text-primary"
              >
                <Icon size={24} />
                {label}
              </span>
            ),
          )}
        </div>
      </div>
    </div>
  )
}

export function MobileMenu({
  variant = "default",
  projectName = "The Grid",
  projectId = "the-grid",
  onNavigate,
}: MobileMenuProps) {
  const isProject = variant === "project"
  const linkClassName = `text-xl font-medium text-text-light ${isProject ? "opacity-80" : ""}`

  return (
    <div data-mobile-menu className="fixed inset-x-0 top-13 bottom-0 z-40 lg:hidden">
      <div
        data-mobile-menu-panel
        className={`flex h-full flex-col items-center overflow-y-auto bg-primary px-5 pt-12.5 pb-10 ${
          isProject ? "gap-12.5" : "gap-22.75"
        }`}
      >
        <Link
          to="/ideas"
          data-mobile-menu-item
          style={itemDelay(0)}
          className={linkClassName}
          onClick={onNavigate}
        >
          Ideas
        </Link>
        <Link
          to="/projects"
          data-mobile-menu-item
          style={itemDelay(1)}
          className={linkClassName}
          onClick={onNavigate}
        >
          Projects
        </Link>
        {isProject && (
          <div data-mobile-menu-item style={itemDelay(2)}>
            <ProjectFolderCard projectName={projectName} projectId={projectId} onNavigate={onNavigate} />
          </div>
        )}
        <Link
          to="/tasks"
          data-mobile-menu-item
          style={itemDelay(isProject ? 3 : 2)}
          className={linkClassName}
          onClick={onNavigate}
        >
          Tasks
        </Link>
        <a
          href="#"
          data-mobile-menu-item
          style={itemDelay(isProject ? 4 : 3)}
          className={linkClassName}
          onClick={onNavigate}
        >
          Logout
        </a>
      </div>
    </div>
  )
}
