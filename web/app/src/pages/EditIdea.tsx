import { useNavigate, useParams } from "@tanstack/react-router"
import { HttpError, useIdea } from "@/api/queries"
import { IdeaForm } from "@/components/IdeaForm"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { NotFound } from "@/pages/NotFound"

/** Edits one of the caller's ideas, read fresh from its own route (WL-REQ-1810) */
export function EditIdea() {
  const { ideaId } = useParams({ from: "/ideas/$ideaId/edit" })
  const idea = useIdea(ideaId)
  const navigate = useNavigate()
  const backToIdeas = () => void navigate({ to: "/ideas" })

  if (idea.isError && idea.error instanceof HttpError && idea.error.status === 404) return <NotFound />
  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 pt-8 lg:p-page">
        {idea.isPending ? (
          <LoadingBlock label="Loading idea" className="h-80" />
        ) : idea.isError ? (
          <LoadError message="Couldn't load the idea." onRetry={() => void idea.refetch()} />
        ) : (
          <IdeaForm key={ideaId} idea={idea.data} onDone={backToIdeas} onClose={backToIdeas} />
        )}
      </main>
    </div>
  )
}
