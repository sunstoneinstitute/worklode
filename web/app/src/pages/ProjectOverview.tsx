import { useParams } from "@tanstack/react-router"
import { ProjectSummary } from "@/components/ProjectSummary"
import { TaskList } from "@/components/TaskList"
import { UpcomingMeetings } from "@/components/UpcomingMeetings"

const GRADIENT = "bg-gradient-to-br from-gradient-start to-gradient-end"

export function ProjectOverview() {
  const { projectId } = useParams({ from: "/projects/$projectId" })

  return (
    // lg:flex-1 stretches the grid to at least the rest of the viewport,
    // so the sidebar gradient always reaches the bottom of the window
    <div className="flex flex-col gap-10 px-5 pb-5 lg:grid lg:flex-1 lg:grid-cols-12 lg:items-stretch lg:gap-0 lg:p-0">
      <main className="flex min-w-0 flex-col gap-8 lg:col-span-8 lg:p-page">
        <ProjectSummary />

        {/*
          Mobile only: a gradient band with a reduced (2-item) meetings
          list, matching Home Mobile's hero-band pattern (bleeds past the
          page's 20px margin the same way ProjectQuickLinks/ProjectSummary
          do). Desktop shows the full 3-item list in the sidebar instead.
          -mt-8 cancels main's own gap-8 above this one item specifically,
          so it sits flush against ProjectSummary — the gap-8 below it
          (before TaskList) is untouched.
        */}
        <div className={`-mx-5 -mt-8 w-[calc(100%+40px)] px-5 py-panel ${GRADIENT} lg:hidden`}>
          <UpcomingMeetings limit={2} />
        </div>

        <TaskList projectId={projectId} />
      </main>

      {/*
        Desktop sidebar bleeds flush to the viewport's right edge like
        Home's (see FIGMA.md) — but unlike Home it does NOT start flush
        below the header/toolbar. Figma positions this panel's background
        level with the main column's own content, after the page's 70px
        top margin, hence lg:mt-page here instead of lg:items-stretch
        starting it at the row's very top.
      */}
      <aside
        className={`hidden min-w-0 lg:col-span-4 lg:mt-page lg:block lg:rounded-tl-panel ${GRADIENT}`}
      >
        <div className="px-5 py-panel 2xl:px-panel 2xl:pr-page">
          <UpcomingMeetings />
        </div>
      </aside>
    </div>
  )
}
