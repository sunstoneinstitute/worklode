import type { ReactNode } from "react"
import { ArrowRightIcon, ImportantIcon } from "@/components/icons"

/**
 * A task that needs your attention, laid out like an Upcoming Meetings row:
 * a round icon overlapping the start of a card — here the "important"
 * symbol (Material's filled error glyph) in the meeting icon's blue
 * (icons-alt) — with the
 * task and an optional detail line, on a card colored like a meeting's.
 * Made for the blue gradient panels, like the meetings list. With an href the whole row is a link
 * and gets the meeting row's arrow.
 */
export function TaskHighlight({
  title,
  detail,
  href,
  children,
}: {
  title: string
  detail?: string
  href?: string
  /** Actions for the task (buttons), under the text */
  children?: ReactNode
}) {
  const content = (
    <>
      {/* The glyph draws its circle a little inside its box: -ml pulls the
          visible edge onto the content edge, -my keeps it from making the
          row taller than the card, -mr overlaps the card (like meetings) */}
      <ImportantIcon
        size={null}
        className="relative z-10 -my-2 -mr-8 -ml-1.5 shrink-0 text-6xl text-icons-alt"
      />
      {/* The meeting card's colors (translucent background, primary text),
          without its border */}
      <span className="flex min-w-0 flex-1 items-center gap-5 rounded-panel bg-background-translucent py-5 pr-5 pl-10 text-text-primary">
        <span className="min-w-0 flex-1">
          <span className="block text-15 font-medium md:text-base">{title}</span>
          {detail ? <span className="block text-13">{detail}</span> : null}
          {children ? <span className="mt-4 flex flex-col items-start gap-3">{children}</span> : null}
        </span>
        {href ? <ArrowRightIcon size={24} className="shrink-0" /> : null}
      </span>
    </>
  )

  return href ? (
    <a href={href} className="flex min-w-0 items-center gap-4">
      {content}
    </a>
  ) : (
    // Level with the title (not centered) once actions make the card tall
    <div role="status" className={`flex min-w-0 gap-4 ${children ? "items-start [&>i]:mt-3" : "items-center"}`}>
      {content}
    </div>
  )
}
