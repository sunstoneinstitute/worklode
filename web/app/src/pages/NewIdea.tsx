import { useNavigate } from "@tanstack/react-router"
import { IdeaForm } from "@/components/IdeaForm"

/** A new, private idea (WL-REQ-1806). Saving and closing both go back to Ideas. */
export function NewIdea() {
  const navigate = useNavigate()
  const backToIdeas = () => void navigate({ to: "/ideas" })
  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 pt-8 lg:p-page">
        <IdeaForm onDone={backToIdeas} onClose={backToIdeas} />
      </main>
    </div>
  )
}
