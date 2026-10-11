import { Outlet, useMatch, useRouterState } from "@tanstack/react-router"
import { Header } from "@/components/Header"
import { MobileMenu } from "@/components/MobileMenu"
import { ScrollLogo } from "@/components/ScrollLogo"
import { Toaster } from "@/components/ui/sonner"
import { useHeaderScroll } from "@/hooks/use-header-scroll"
import { useMobileMenu } from "@/hooks/use-mobile-menu"

/**
 * What every page shares: the fixed header and the mobile menu. On a
 * project's pages (anything under /projects/$projectId), the header's own
 * logo mark hides in favor of the single travelling ScrollLogo (see
 * ScrollLogo.tsx), and the mobile menu gains the project folder card.
 */
export function RootLayout() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  useHeaderScroll(pathname)
  const menu = useMobileMenu(pathname)

  const projectMatch = useMatch({ from: "/projects/$projectId", shouldThrow: false })
  const projectId = projectMatch?.params.projectId

  return (
    <>
      <Header hideLogoMark={projectId !== undefined} menuOpen={menu.open} onToggleMenu={menu.toggle} />
      {projectId !== undefined ? (
        <>
          <ScrollLogo />
          <MobileMenu
            variant="project"
            projectName="The Grid"
            projectId={projectId}
            onNavigate={menu.close}
          />
        </>
      ) : (
        <MobileMenu onNavigate={menu.close} />
      )}
      <Outlet />
      <Toaster />
    </>
  )
}
