import { Outlet, useRouterState } from "@tanstack/react-router"
import { Header } from "@/components/Header"
import { MobileMenu } from "@/components/MobileMenu"
import { Toaster } from "@/components/ui/sonner"
import { useHeaderScroll } from "@/hooks/use-header-scroll"
import { useMobileMenu } from "@/hooks/use-mobile-menu"

/** What every page shares: the fixed header and the mobile menu. */
export function RootLayout() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  useHeaderScroll(pathname)
  const menu = useMobileMenu(pathname)

  return (
    <>
      <Header menuOpen={menu.open} onToggleMenu={menu.toggle} />
      <MobileMenu onNavigate={menu.close} />
      <Outlet />
      <Toaster />
    </>
  )
}
