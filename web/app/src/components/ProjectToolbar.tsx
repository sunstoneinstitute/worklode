import { Link } from "@tanstack/react-router"
import { FolderCurve } from "@/components/FolderCurve"
import { PROJECT_NAV_ITEMS, PROJECT_NAV_SETTINGS } from "@/lib/project-nav"

// 20px (Header's own px-5) + 43px (Header's lg icon width, w-10.75) + 10px
// (Header's icon-to-text gap-2.5) — the exact left edge of "Worklode" in
// Header, so this breadcrumb's text lines up with it (the user asked for
// this alignment specifically, not the page's general 70px margin).
const TEXT_INSET = "73px"

export function ProjectToolbar({
  projectId,
  projectName = "Project Name",
}: {
  projectId: string
  projectName?: string
}) {
  return (
    <div
      data-secondary-toolbar
      className="z-40 hidden h-14.5 w-full items-center gap-10 overflow-hidden bg-gradient-to-r from-gradient-start to-gradient-end pr-5 lg:flex"
    >
      {/*
        One continuous light "tab": a solid rectangle (bg-background, same
        color as FolderCurve's fill) covers the breadcrumb area,
        stopping exactly where the curve image begins (right-64, matching
        the image's own w-64) — not stretched/skewed to cover more area,
        since the flat majority of that shape is just a solid color anyway.
        Important: the rectangle must NOT extend under the image itself —
        the curve's actual "curve" is a transparent cutout in the image that
        reveals the gradient behind it; a rectangle spanning the full width
        would fill that cutout in with solid color too and hide the curve
        entirely. flex-1 + the image's own object-cover/object-right means
        this whole tab still resizes with the available space automatically.
      */}
      <div className="relative flex h-[calc(100%-12px)] min-w-0 flex-1 items-center self-end">
        <div className="absolute inset-y-0 right-64 left-0 bg-background" />
        <FolderCurve className="absolute inset-y-0 right-0 h-full w-64" />
        <p
          className="relative z-10 truncate text-15 font-medium text-text-primary"
          style={{ paddingLeft: TEXT_INSET }}
        >
          <Link to="/projects" className="underline decoration-from-font underline-offset-2">
            Projects
          </Link>
          {" / "}
          {projectName}
        </p>
      </div>

      {/*
        Icon-only from lg (1024px), labels return at xl (1280px). The
        labeled version needs ~900px on its own — combined with the
        breadcrumb, that doesn't actually fit until well past lg (see
        AGENTS.md) — so lg gets the icon-only tier instead of overlapping.
      */}
      <nav className="hidden shrink-0 items-center gap-6 lg:flex xl:gap-16.25">
        {PROJECT_NAV_ITEMS.map((item) => {
          const { label, Icon } = item
          const className = "flex items-center gap-2 text-xs leading-none font-medium whitespace-nowrap text-text-light"
          const body = (
            <>
              <Icon size={24} />
              <span className="hidden xl:inline">{label}</span>
            </>
          )
          // A ported page gets a typed <Link>; the rest are plain links the
          // templ cockpit serves (WL-REQ-1801)
          return item.spa ? (
            <Link key={label} to={item.to} params={{ projectId }} aria-label={label} className={className}>
              {body}
            </Link>
          ) : (
            <a key={label} href={`/projects/${projectId}${item.path}`} aria-label={label} className={className}>
              {body}
            </a>
          )
        })}
        <button type="button" aria-label={PROJECT_NAV_SETTINGS.label} className="text-text-light">
          <PROJECT_NAV_SETTINGS.Icon size={24} />
        </button>
      </nav>
    </div>
  )
}
