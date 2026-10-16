import { Link } from "@tanstack/react-router"
import { useMe } from "@/api/queries"
import worklodeMark from "@/assets/brand/worklode-mark.svg"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { initials } from "@/lib/initials"
import { ColorModeToggle } from "@/components/ColorModeToggle"
import { CloseIcon, MenuIcon, NotificationsIcon } from "@/components/icons"

const NAV_ITEMS = [
  { label: "Projects", to: "/projects" },
  { label: "Tasks", to: "/tasks" },
] as const

export function Header({ menuOpen, onToggleMenu }: { menuOpen: boolean; onToggleMenu: () => void }) {
  const me = useMe()
  return (
    <header
      data-header
      className="fixed inset-x-0 top-0 z-50 flex h-13 w-full items-center gap-10 border-b border-highlight/25 bg-surface px-5 lg:h-19"
    >
      {/*
        The topbar is an exception to the page's 70px desktop margin: it
        keeps the same 20px padding at every breakpoint. That's deliberate —
        it lets the mark sit further left while the "Worklode" wordmark's
        left edge still lines up with the 70px content column below.
      */}
      <Link to="/projects" className="flex items-center gap-2.5">
        <img src={worklodeMark} alt="" className="h-7 w-8.25 lg:h-9 lg:w-10.75" />
        <span className="text-15 font-medium text-text-primary lg:text-lg">
          Worklode
        </span>
      </Link>

      <nav className="hidden items-center gap-13.5 text-15 font-medium text-text-primary lg:flex">
        {NAV_ITEMS.map((item) => (
          // Primary is dark navy in dark mode — highlight reads there instead
          <Link key={item.label} to={item.to} className="hover:text-primary dark:hover:text-highlight">
            {item.label}
          </Link>
        ))}
      </nav>

      <div className="ml-auto flex items-center gap-5">
        <button
          type="button"
          aria-label="Notifications"
          className="hidden flex size-6 cursor-pointer items-center justify-center text-text-primary"
        >
          <NotificationsIcon size={22} />
        </button>
        <ColorModeToggle />
        {/* Stays visible when the mobile menu opens — Figma's "Open"/"Open
            Projects" variants drop it, but the user asked to keep it. */}
        <Avatar>
          <AvatarFallback className="bg-primary text-xs text-primary-foreground">
            {initials(me.id ?? "")}
          </AvatarFallback>
        </Avatar>
        {/* Toggles the mobile menu; the icon swaps between hamburger and
            close via CSS keyed off mobile-menu-open on <html> — see
            [data-menu-icon-open]/[data-menu-icon-close] in index.css. */}
        <button
          type="button"
          data-mobile-menu-toggle
          onClick={onToggleMenu}
          aria-expanded={menuOpen}
          aria-label={menuOpen ? "Close menu" : "Open menu"}
          className="relative flex size-6 cursor-pointer items-center justify-center text-text-primary lg:hidden"
        >
          <span
            data-menu-icon-open
            className="absolute inset-0 flex items-center justify-center"
          >
            <MenuIcon size={22} />
          </span>
          <span
            data-menu-icon-close
            className="absolute inset-0 flex items-center justify-center"
          >
            <CloseIcon size={22} />
          </span>
        </button>
      </div>
    </header>
  )
}
