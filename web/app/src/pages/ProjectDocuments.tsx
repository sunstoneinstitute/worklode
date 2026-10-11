import { DocumentList } from "@/components/DocumentList"

// A project's Documents page. Under the project layout (toolbar and all):
// the Documents table full width on the standard page margins.
export function ProjectDocuments() {
  return (
    <main className="p-5 pt-8 lg:p-page">
      <DocumentList />
    </main>
  )
}
