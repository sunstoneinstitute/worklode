import { useNavigate } from "@tanstack/react-router"
import { NewIdeaFlow } from "@/components/IdeaForm"
import { addIdea } from "@/lib/ideas"

// The New Idea form as its own route, rather than the slide-in panel the
// Ideas table opens. Submitting or closing goes back to Ideas; the new idea
// is in the shared store, so the table shows it.
export function NewIdea() {
  const navigate = useNavigate()
  const backToIdeas = () => void navigate({ to: "/ideas" })

  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 pt-8 lg:p-page">
        <NewIdeaFlow
          onClose={backToIdeas}
          onSubmit={(idea) => {
            addIdea(idea)
            backToIdeas()
          }}
        />
      </main>
    </div>
  )
}
