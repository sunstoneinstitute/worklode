import { useMe } from "@/api/queries"
import { ProjectQuickLinks } from "@/components/ProjectQuickLinks"
import { TaskList } from "@/components/TaskList"
import { UpcomingMeetings } from "@/components/UpcomingMeetings"
import { userHandle } from "@/components/ui/data-table/cells"

const GRADIENT = "bg-gradient-to-br from-gradient-start to-gradient-end"

export function Home() {
  const greeting = `Welcome, ${userHandle(useMe())}!`
  return (
    <div className="min-h-screen bg-background pt-13 lg:flex lg:flex-col lg:pt-19">
      {/* Mobile hero: welcome + upcoming meetings over the gradient band */}
      <div className={`px-5 pb-8 pt-6 ${GRADIENT} lg:hidden`}>
        <h1 className="text-xl font-semibold text-text-light">{greeting}</h1>
        <UpcomingMeetings className="mt-6" />
      </div>

      {/*
        Desktop: two grid rows shared by both columns via subgrid — row 1 is
        the welcome line, row 2 everything else. The sidebar's content starts
        in row 2, so "Upcoming Meetings" and its cards line up with
        "Project Quick Links" and its cards however tall the welcome row is.
        flex-1 + an auto/1fr row split stretches the grid to at least the
        viewport's height below the header, so the sidebar gradient always
        reaches the bottom, and the extra height lands in row 2, not the
        welcome row.
      */}
      <div className="flex flex-col gap-10 p-5 lg:grid lg:flex-1 lg:grid-cols-12 lg:grid-rows-[auto_1fr] lg:gap-0 lg:p-0">
        <main className="flex min-w-0 flex-col gap-8 lg:col-span-8 lg:row-span-2 lg:grid lg:grid-rows-subgrid lg:gap-0 lg:px-page lg:pt-14 lg:pb-page">
          <h1 className="hidden pb-2 text-2xl font-semibold text-text-primary lg:block">
            {greeting}
          </h1>
          <div className="flex min-w-0 flex-col gap-12 lg:gap-18">
            <ProjectQuickLinks />
            <TaskList />
          </div>
        </main>

        {/*
          Desktop sidebar is an exception to the page's 70px margin: the panel
          itself bleeds flush to the viewport's right edge and starts right
          below the header (no outer padding on the grid row), while its own
          inner content keeps the standard 70px margin via pr-page — from 2xl
          only. Below that the sidebar column is too narrow for the meeting
          cards, so it drops to a 20px inset on both sides.
        */}
        <aside className={`hidden min-w-0 lg:col-span-4 lg:row-span-2 lg:grid lg:grid-rows-subgrid ${GRADIENT}`}>
          <div className="row-start-2 px-5 pb-panel 2xl:px-panel 2xl:pr-page">
            <UpcomingMeetings />
          </div>
        </aside>
      </div>
    </div>
  )
}
