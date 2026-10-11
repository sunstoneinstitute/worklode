import type { ReactNode } from "react"
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet"
import { useMediaQuery } from "@/hooks/use-media-query"

/**
 * The app's slide-in panel (New/Edit Idea, Edit Pitch): slides the whole way
 * in from the right on desktop and up from the bottom on mobile — the
 * shadcn Sheet default only nudges 2.5rem while fading. 2000px wide, at
 * most 90vw. Styled like ConfirmDialog: background-colored, no border, a
 * faint primary-tinted shadow, rounded on its inner edge, over the Sheet's
 * overlay-bg backdrop. Close it from inside (children render their own X).
 */
export function SidePanel({
  open,
  onOpenChange,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  children: ReactNode
}) {
  const desktop = useMediaQuery("(min-width: 1024px)")

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side={desktop ? "right" : "bottom"}
        showCloseButton={false}
        aria-describedby={undefined}
        className="overflow-y-auto border-none bg-background p-5 text-text-primary shadow-lg shadow-primary/10 outline-none duration-300 data-[side=bottom]:h-11/12 data-[side=bottom]:rounded-t-panel data-[side=bottom]:data-closed:slide-out-to-bottom-full data-[side=bottom]:data-open:slide-in-from-bottom-full data-[side=right]:w-500 data-[side=right]:max-w-9/10 data-[side=right]:rounded-l-panel data-[side=right]:data-closed:slide-out-to-right-full data-[side=right]:data-open:slide-in-from-right-full data-[side=right]:sm:max-w-9/10 lg:p-page"
      >
        {children}
      </SheetContent>
    </Sheet>
  )
}

/** The panel's accessible title, styled as the page-level heading */
export function SidePanelTitle({ children }: { children: ReactNode }) {
  return (
    <SheetTitle className="text-2xl font-normal text-text-primary md:text-3xl">{children}</SheetTitle>
  )
}
