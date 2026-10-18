import { IdeaList } from "@/components/IdeaList"

/** The caller's private ideas and every shared one (WL-REQ-1806) */
export function Ideas() {
  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 pt-8 lg:p-page">
        <IdeaList />
      </main>
    </div>
  )
}
