import { Link, useNavigate } from "@tanstack/react-router"
import { IdeaForm } from "@/components/IdeaForm"
import { getIdeas, updateIdea } from "@/lib/ideas"

/**
 * The Edit Idea form as its own page — where an idea's pencil goes (its
 * title opens the same form in the slide-in panel instead). Saving or
 * closing goes back to Ideas; the change is in the shared store, so the
 * table shows it.
 */
export function EditIdea({ ideaId }: { ideaId: string }) {
  const idea = getIdeas().find((candidate) => candidate.id === ideaId)
  const navigate = useNavigate()
  const backToIdeas = () => void navigate({ to: "/ideas" })

  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 pt-8 text-text-primary lg:p-page">
        {idea ? (
          <IdeaForm
            idea={idea}
            onClose={backToIdeas}
            onSubmit={(next) => {
              updateIdea(ideaId, next)
              backToIdeas()
            }}
          />
        ) : (
          <div className="flex flex-col gap-4">
            <h1 className="text-2xl md:text-3xl">Idea not found</h1>
            <Link to="/ideas" className="self-start text-sm underline underline-offset-2 hover:text-highlight">
              Back to Ideas
            </Link>
          </div>
        )}
      </main>
    </div>
  )
}
