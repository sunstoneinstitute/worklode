import * as React from "react"
import { cn } from "cn"
import { Slot } from "radix-ui"

// The blue rounded card shell (Figma's project quick-link card). Layout
// concerns that depend on where the card sits — fixed widths, scroll-snap,
// shrink behavior — belong in the consumer's className, not here. Pass
// asChild to render as a link or other element instead of a <div>.
function Card({
  className,
  asChild = false,
  ...props
}: React.ComponentProps<"div"> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : "div"

  return (
    <Comp
      data-slot="card"
      className={cn(
        "flex flex-col gap-2.5 rounded-card bg-primary p-5 text-text-light md:rounded-panel md:p-panel",
        className
      )}
      {...props}
    />
  )
}

export { Card }
