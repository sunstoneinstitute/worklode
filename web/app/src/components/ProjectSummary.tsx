import { useEffect, useRef, useState } from "react"
import { useCrew, useProjectSummary } from "@/api/queries"
import type { CrewMember, ProjectSummary as Summary, SummaryItem, Workload } from "@/api/types"
import { ArrowRightIcon, ChevronRightIcon, TasksIcon } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { dueLabel } from "@/lib/due-label"

const AVAILABILITY: Record<string, { label: string; className: string }> = {
  open: { label: "Open", className: "bg-highlight" },
  overbooked: { label: "Overbooked", className: "bg-[var(--color-accent)]" },
}

function displayName(crew: CrewMember[], actor: string) {
  const person = crew.find((member) => member.actor === actor)
  return person?.display_name || actor
}

// The per-person lists, shared by the desktop panel and the mobile accordion row
function WorkloadLists({ workload, compact = false }: { workload: Workload; compact?: boolean }) {
  return (
    <>
      <SummaryList title="Past Due Tasks" items={workload.past_due} compact={compact} />
      <SummaryList title="Upcoming Tasks" items={workload.upcoming} compact={compact} />
      <SummaryList title="Awaiting Review" items={workload.awaiting_review} compact={compact} />
    </>
  )
}

// One summary line per person (name, availability, task count):
// - Desktop: clicking selects that person — their row keeps the darker
//   background (data-selected) and CrewPanels shows their lists. The first
//   person starts selected.
// - Mobile: an accordion — clicking flips data-open on the row, revealing
//   that person's lists underneath.
// One click does both; CSS shows whichever applies at the current width.
function CrewList({
  workloads,
  crew,
  selectedId,
  openIds,
  onRowClick,
}: {
  workloads: Workload[]
  crew: CrewMember[]
  selectedId: string
  openIds: ReadonlySet<string>
  onRowClick: (actor: string) => void
}) {
  return (
    <div className="flex w-full flex-col">
      {workloads.map((workload) => {
        const actor = workload.actor ?? ""
        const availability = AVAILABILITY[workload.availability ?? ""] ?? AVAILABILITY.open
        const open = openIds.has(actor)
        return (
          <div
            key={actor}
            data-crew-row={actor}
            data-selected={actor === selectedId ? "" : undefined}
            data-open={open ? "" : undefined}
            className="group/row border-b border-highlight/30 lg:data-[selected]:bg-black/15"
          >
            <button
              type="button"
              data-crew-row-toggle
              aria-expanded={open}
              onClick={() => onRowClick(actor)}
              className="flex w-full min-w-0 cursor-pointer items-center gap-2.5 px-2.5 py-3 text-left transition-colors hover:bg-black/10"
            >
              <span className="min-w-0 flex-1 truncate text-15">{displayName(crew, actor)}</span>
              <span className="shrink-0 lg:w-22">
                <span className={`rounded-sm px-1.5 py-0.5 text-13 text-text-dark ${availability.className}`}>
                  {availability.label}
                </span>
              </span>
              <span className="flex shrink-0 items-center gap-2 text-13">
                <TasksIcon size={16} />
                <span>
                  <span className="font-semibold">
                    {workload.done ?? 0} / {workload.total ?? 0}
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
              <WorkloadLists workload={workload} compact />
            </div>
          </div>
        )
      })}
    </div>
  )
}

// Desktop right-hand column of the crew view: one panel per person, only
// the selected person's showing.
function CrewPanels({ workloads, selectedId }: { workloads: Workload[]; selectedId: string }) {
  return (
    <div className="hidden lg:block lg:flex-1">
      {workloads.map((workload) => (
        <div
          key={workload.actor}
          data-crew-panel={workload.actor}
          data-selected={workload.actor === selectedId ? "" : undefined}
          className="hidden flex-col gap-6 data-[selected]:flex"
        >
          <WorkloadLists workload={workload} />
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
  items?: SummaryItem[] | null
  /** Smaller heading, for lists nested inside a crew accordion row */
  compact?: boolean
}) {
  const list = items ?? []
  return (
    <div className="flex flex-col items-start gap-2.5">
      <h3 className={`font-semibold text-text-light ${compact ? "text-base" : "text-xl"}`}>{title}</h3>
      <div className="flex w-full flex-col">
        {list.length === 0 ? <p className="py-2 text-13 text-text-light/70">Nothing here</p> : null}
        {list.map((item) => {
          const [amount, ...rest] = dueLabel(item.due ?? "").split(" ")
          return (
            <a
              key={item.id}
              href={item.url}
              className="flex w-full min-w-0 items-center gap-2.5 border-b border-highlight/30 py-2 hover:bg-black/10"
            >
              <p className="min-w-0 flex-1 text-13 text-text-light">{item.label}</p>
              <p className="shrink-0 text-13 text-text-light">
                <span className="font-semibold">{amount} </span>
                {rest.join(" ")}
              </p>
              <ArrowRightIcon size={24} className="shrink-0 text-text-light" />
            </a>
          )
        })}
      </div>
    </div>
  )
}

function SummaryCard({ name, summary, crew }: { name: string; summary: Summary; crew: CrewMember[] }) {
  // Two views in one card: the project summary and "What is everyone
  // working on?". Both are always in the markup; data-view="crew" on the
  // root lets the group-data variants below show one and hide the other.
  const workloads = summary.workloads ?? []
  const [view, setView] = useState<"summary" | "crew">("summary")
  const [selectedId, setSelectedId] = useState(workloads[0]?.actor ?? "")
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

  const onRowClick = (actor: string) => {
    setSelectedId(actor)
    setOpenIds((current) => {
      const next = new Set(current)
      if (next.has(actor)) next.delete(actor)
      else next.add(actor)
      return next
    })
  }

  return (
    <div
      data-project-summary
      data-view={view === "crew" ? "crew" : undefined}
      className="group/summary -mx-5 w-[calc(100%+40px)] bg-primary px-5 py-panel text-text-light lg:mx-0 lg:w-full lg:rounded-panel lg:px-panel lg:pb-12.5"
    >
      <div data-summary-view="summary" className="flex flex-col gap-2.5 group-data-[view=crew]/summary:hidden">
        <p className="pb-2.5 text-2xl font-extrabold">{name}</p>

        {/* The toggle sits right under the name, where the crew view has its
            own button, so it doesn't move when the view switches. */}
        <Button
          data-project-summary-toggle
          ref={summaryToggle}
          onClick={switchView}
          variant="outline"
          size="sm"
          className={`self-start ${TOGGLE_CLASSES}`}
        >
          <span className="lg:hidden">Crew Overview</span>
          <span className="hidden lg:inline">What is the crew working on?</span>
        </Button>

        {/* The design's progress bar and days remaining are left out: the
            backbone records neither for a project. Their column stays empty
            on desktop so the lists keep the right half. */}
        <div className="flex flex-col gap-10 pt-2 lg:flex-row lg:items-start lg:gap-12.5">
          <div className="hidden lg:block lg:flex-1" />

          <div className="flex flex-col gap-10 lg:flex-1 lg:gap-6">
            <SummaryList title="Past Due Tasks" items={summary.past_due} />
            <SummaryList title="Awaiting Review" items={summary.awaiting_review} />
          </div>
        </div>
      </div>

      <div data-summary-view="crew" className="hidden flex-col gap-2.5 group-data-[view=crew]/summary:flex">
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
            {workloads.length === 0 ? (
              <p className="py-2 text-13 text-text-light/70">No Crew yet</p>
            ) : (
              <CrewList
                workloads={workloads}
                crew={crew}
                selectedId={selectedId}
                openIds={openIds}
                onRowClick={onRowClick}
              />
            )}
          </div>

          {/* Desktop only — on mobile each person's lists live inside their
              accordion row instead. */}
          <CrewPanels workloads={workloads} selectedId={selectedId} />
        </div>
      </div>
    </div>
  )
}

/**
 * The Project Overview card (WL-REQ-1804): what is past due and awaiting
 * review, plus a second view of each Crew member's workload. Names come
 * from the global roster, since a workload's actor need not be in the
 * project's own Crew.
 */
export function ProjectSummary({ projectId, name = "Project Summary" }: { projectId: string; name?: string }) {
  const summary = useProjectSummary(projectId)
  const crew = useCrew()

  if (summary.isPending || crew.isPending) {
    return <LoadingBlock label="Loading project summary" className="h-80" />
  }
  if (summary.isError || crew.isError) {
    return (
      <LoadError
        message="Couldn't load the project summary."
        onRetry={() => {
          if (summary.isError) void summary.refetch()
          if (crew.isError) void crew.refetch()
        }}
      />
    )
  }

  return <SummaryCard name={name} summary={summary.data} crew={crew.data} />
}
