import { TaskList } from "@/components/TaskList"

// Top-level Tasks page: every task across every project, so the table gains
// a filterable Project column. Just the one block on the standard page
// margins — 20px on mobile (the table bleeds past it edge-to-edge, as on
// Home), 70px from lg.
export function Tasks() {
  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 pt-8 lg:p-page">
        <TaskList showProject showTitleIcon={false} syncWithUrl />
      </main>
    </div>
  )
}
