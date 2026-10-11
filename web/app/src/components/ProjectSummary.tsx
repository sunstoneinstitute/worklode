import { useEffect, useRef, useState } from "react"
import { ArrowRightIcon, ChevronRightIcon, TasksIcon } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { userHandle } from "@/components/ui/data-table"
import { Progress } from "@/components/ui/progress"
import { CREW } from "@/lib/crew"

type ListItem = { label: string; meta: string }

// Placeholder content taken verbatim from the Figma text nodes (Project
// Overview Desktop / Mobile).
const PAST_DUE: ListItem[] = [
  { label: "Task name goes here", meta: "2 days late" },
  { label: "Task name goes here", meta: "1 day late" },
  { label: "Task name goes here", meta: "1 day late" },
]

const AWAITING_REVIEW: ListItem[] = [
  { label: "Document name", meta: "5 days left" },
  { label: "Another document name", meta: "3 weeks left" },
]

// Crew workload for the "What is everyone working on?" view. Figma's
// placeholder handles (@Stig, @Stig2) are swapped for the real crew roster
// the tables use, so the same people show up everywhere. Each person has
// their own task lists: desktop shows the selected person's Past Due +
// Upcoming in the right-hand column (as in Figma); the mobile accordion
// shows their Past Due + Awaiting Review under their row.
type Workload = {
  userId: string
  availability: "open" | "overbooked"
  done: number
  total: number
  pastDue: ListItem[]
  upcoming: ListItem[]
  awaitingReview: ListItem[]
}

const WORKLOADS: Workload[] = [
  {
    userId: "u2",
    availability: "open",
    done: 5,
    total: 12,
    pastDue: [
      { label: "Task name goes here", meta: "2 days late" },
      { label: "Task name goes here", meta: "1 day late" },
      { label: "Task name goes here", meta: "1 day late" },
    ],
    upcoming: [
      { label: "Task name goes here", meta: "2 days left" },
      { label: "Task name goes here", meta: "3 days left" },
      { label: "Task name goes here", meta: "2 weeks left" },
    ],
    awaitingReview: [{ label: "Document name", meta: "5 days left" }],
  },
  {
    userId: "u1",
    availability: "open",
    done: 5,
    total: 12,
    pastDue: [{ label: "Task name goes here", meta: "1 day late" }],
    upcoming: [
      { label: "Task name goes here", meta: "4 days left" },
      { label: "Task name goes here", meta: "1 week left" },
    ],
    awaitingReview: [{ label: "Another document name", meta: "3 weeks left" }],
  },
  {
    userId: "u3",
    availability: "overbooked",
    done: 5,
    total: 21,
    pastDue: [
      { label: "Task name goes here", meta: "1 day late" },
      { label: "Task name goes here", meta: "4 days late" },
    ],
    upcoming: [
      { label: "Task name goes here", meta: "1 day left" },
      { label: "Task name goes here", meta: "2 days left" },
      { label: "Task name goes here", meta: "5 days left" },
    ],
    awaitingReview: [],
  },
]

const AVAILABILITY = {
  open: { label: "Open", className: "bg-highlight" },
  overbooked: { label: "Overbooked", className: "bg-[var(--color-accent)]" },
}

// One summary line per person (handle, availability, task count):
// - Desktop: clicking selects that person — their row keeps the darker
//   background (data-selected) and CrewPanels shows their lists. The first
//   person starts selected.
// - Mobile: an accordion — clicking flips data-open on the row, revealing
//   that person's own Past Due Tasks and Awaiting Review underneath.
// One click does both; CSS shows whichever applies at the current width.
function CrewList({
  selectedId,
  openIds,
  onRowClick,
}: {
  selectedId: string
  openIds: ReadonlySet<string>
  onRowClick: (userId: string) => void
}) {
  return (
    <div className="flex w-full flex-col">
      {WORKLOADS.map((workload) => {
        const person = CREW.find((member) => member.id === workload.userId)
        const availability = AVAILABILITY[workload.availability]
        const open = openIds.has(workload.userId)
        return (
          <div
            key={workload.userId}
            data-crew-row={workload.userId}
            data-selected={workload.userId === selectedId ? "" : undefined}
            data-open={open ? "" : undefined}
            className="group/row border-b border-highlight/30 lg:data-[selected]:bg-black/15"
          >
            <button
              type="button"
              data-crew-row-toggle
              aria-expanded={open}
              onClick={() => onRowClick(workload.userId)}
              className="flex w-full min-w-0 cursor-pointer items-center gap-2.5 px-2.5 py-3 text-left transition-colors hover:bg-black/10"
            >
              <span className="min-w-0 flex-1 truncate text-15">
                {person ? userHandle(person) : ""}
              </span>
              <span className="shrink-0 lg:w-22">
                <span
                  className={`rounded-sm px-1.5 py-0.5 text-13 text-text-dark ${availability.className}`}
                >
                  {availability.label}
                </span>
              </span>
              <span className="flex shrink-0 items-center gap-2 text-13">
                <TasksIcon size={16} />
                <span>
                  <span className="font-semibold">
                    {workload.done} / {workload.total}
                  </span>
                  <span className="hidden sm:inline"> tasks complete</span>
                </span>
              </span>
              <ArrowRightIcon size={24} className="hidden shrink-0 lg:block" />
              <ChevronRightIcon
                size={24}
                className="shrink-0 transition-transform group-data-[open]/row:rotate-90 lg:hidden"
              />
            </button>
            <div className="hidden flex-col gap-6 px-2.5 pt-2 pb-5 group-data-[open]/row:flex lg:hidden!">
              <SummaryList title="Past Due Tasks" items={workload.pastDue} compact />
              <SummaryList title="Awaiting Review" items={workload.awaitingReview} compact />
            </div>
          </div>
        )
      })}
    </div>
  )
}

// Desktop right-hand column of the crew view: one panel per person, only
// the selected person's showing.
function CrewPanels({ selectedId }: { selectedId: string }) {
  return (
    <div className="hidden lg:block lg:flex-1">
      {WORKLOADS.map((workload) => (
        <div
          key={workload.userId}
          data-crew-panel={workload.userId}
          data-selected={workload.userId === selectedId ? "" : undefined}
          className="hidden flex-col gap-6 data-[selected]:flex"
        >
          <SummaryList title="Past Due Tasks" items={workload.pastDue} />
          <SummaryList title="Upcoming Tasks" items={workload.upcoming} />
        </div>
      ))}
    </div>
  )
}

const TOGGLE_CLASSES =
  "shrink-0 rounded-full border-text-light bg-transparent px-3 text-xs text-text-light hover:bg-text-light hover:text-primary focus-visible:border-text-light focus-visible:ring-text-light/50"

function SummaryList({
  title,
  items,
  compact = false,
}: {
  title: string
  items: ListItem[]
  /** Smaller heading, for lists nested inside a crew accordion row */
  compact?: boolean
}) {
  return (
    <div className="flex flex-col items-start gap-2.5">
      <h3 className={`font-semibold text-text-light ${compact ? "text-base" : "text-xl"}`}>
        {title}
      </h3>
      <div className="flex w-full flex-col">
        {items.length === 0 ? <p className="py-2 text-13 text-text-light/70">Nothing here</p> : null}
        {items.map((item, index) => (
          <div
            key={index}
            className="flex w-full min-w-0 items-center gap-2.5 border-b border-highlight/30 py-2"
          >
            <p className="min-w-0 flex-1 text-13 text-text-light">{item.label}</p>
            <p className="shrink-0 text-13 text-text-light">
              <span className="font-semibold">{item.meta.split(" ")[0]} </span>
              {item.meta.split(" ").slice(1).join(" ")}
            </p>
            <ArrowRightIcon size={24} className="shrink-0 text-text-light" />
          </div>
        ))}
      </div>
    </div>
  )
}

export function ProjectSummary({
  name = "Project Summary",
  daysRemaining = 20,
  progress = 55,
}: {
  name?: string
  daysRemaining?: number
  progress?: number
}) {
  // Two views in one card: the project summary and "What is everyone
  // working on?". Both are always in the markup; data-view="crew" on the
  // root lets the group-data variants below show one and hide the other.
  const [view, setView] = useState<"summary" | "crew">("summary")
  const [selectedId, setSelectedId] = useState(WORKLOADS[0].userId)
  const [openIds, setOpenIds] = useState<ReadonlySet<string>>(new Set())
  const summaryToggle = useRef<HTMLButtonElement>(null)
  const crewToggle = useRef<HTMLButtonElement>(null)
  // Set by a click, so switching views moves focus but the first render doesn't
  const moveFocus = useRef(false)

  useEffect(() => {
    if (!moveFocus.current) return
    moveFocus.current = false
    // Keep keyboard focus on the equivalent button in the view now showing
    ;(view === "crew" ? crewToggle : summaryToggle).current?.focus()
  }, [view])

  const switchView = () => {
    moveFocus.current = true
    setView((current) => (current === "crew" ? "summary" : "crew"))
  }

  const onRowClick = (userId: string) => {
    setSelectedId(userId)
    setOpenIds((current) => {
      const next = new Set(current)
      if (next.has(userId)) next.delete(userId)
      else next.add(userId)
      return next
    })
  }

  return (
    <div
      data-project-summary
      data-view={view === "crew" ? "crew" : undefined}
      className="group/summary -mx-5 w-[calc(100%+40px)] bg-primary px-5 py-panel text-text-light lg:mx-0 lg:w-full lg:rounded-panel lg:px-panel lg:pb-12.5"
    >
      <div
        data-summary-view="summary"
        className="flex flex-col gap-2.5 group-data-[view=crew]/summary:hidden"
      >
        <p className="pb-2.5 text-2xl font-extrabold">{name}</p>

        <div className="flex flex-col gap-10 lg:flex-row lg:items-stretch lg:gap-12.5">
          {/* Progress + CTA: button sits beside the bar on mobile, above it on
              desktop. Desktop: justify-between (with the row stretched to
              match the right column's height) pushes the button to the top —
              aligned with "Past Due Tasks" below — and the progress bar to
              the bottom, rather than the two sitting stacked together. */}
          <div className="flex items-start gap-7.5 lg:flex-1 lg:flex-col lg:items-start lg:justify-between lg:gap-12.5">
            <Button
              data-project-summary-toggle
              ref={summaryToggle}
              onClick={switchView}
              variant="outline"
              size="sm"
              className={`order-2 self-center lg:order-1 lg:self-start ${TOGGLE_CLASSES}`}
            >
              <span className="lg:hidden">Crew Overview</span>
              <span className="hidden lg:inline">What is the crew working on?</span>
            </Button>
            <div className="order-1 flex flex-1 flex-col gap-2.5 pt-2 lg:order-2 lg:w-full lg:flex-none">
              <Progress
                value={progress}
                className="h-4.25 bg-surface [&>[data-slot=progress-indicator]]:bg-highlight"
              />
              <p className="text-13">{daysRemaining} days remaining</p>
            </div>
          </div>

          {/* Past Due Tasks / Awaiting Review stack in a single right-hand
              column (matching Figma), not side by side. */}
          <div className="flex flex-col gap-10 lg:flex-1 lg:gap-6">
            <SummaryList title="Past Due Tasks" items={PAST_DUE} />
            <SummaryList title="Awaiting Review" items={AWAITING_REVIEW} />
          </div>
        </div>
      </div>

      <div
        data-summary-view="crew"
        className="hidden flex-col gap-2.5 group-data-[view=crew]/summary:flex"
      >
        <p className="pb-2.5 text-2xl font-extrabold">What is everyone working on?</p>

        <div className="flex flex-col gap-10 lg:flex-row lg:items-start lg:gap-12.5">
          <div className="flex min-w-0 flex-col items-start gap-7.5 lg:flex-1 lg:gap-12.5">
            <Button
              data-project-summary-toggle
              ref={crewToggle}
              onClick={switchView}
              variant="outline"
              size="sm"
              className={TOGGLE_CLASSES}
            >
              Back to project summary
            </Button>
            <CrewList selectedId={selectedId} openIds={openIds} onRowClick={onRowClick} />
          </div>

          {/* Desktop only — on mobile each person's lists live inside their
              accordion row instead. */}
          <CrewPanels selectedId={selectedId} />
        </div>
      </div>
    </div>
  )
}
