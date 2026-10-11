import { IdeaList } from "@/components/IdeaList"

// Top-level Ideas page — same single-table layout as the Tasks page.
export function Ideas() {
  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 pt-8 lg:p-page">
        <IdeaList />
      </main>
    </div>
  )
}
