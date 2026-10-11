import { ProjectList } from "@/components/ProjectList"

// Top-level Projects page — same single-table layout as Tasks and Ideas.
export function Projects() {
  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 pt-8 lg:p-page">
        <ProjectList />
      </main>
    </div>
  )
}
