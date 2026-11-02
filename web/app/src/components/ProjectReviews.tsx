import { Link } from "@tanstack/react-router"
import { useProjectReviews } from "@/api/queries"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

function age(openedAt?: string) {
  const days = openedAt ? Math.max(0, Math.floor((Date.now() - Date.parse(openedAt)) / 86_400_000)) : 0
  return `${days}d`
}

/** A project's open reviews, each linking to its read-only page (WL-REQ-1775) */
export function ProjectReviews({ projectId }: { projectId: string }) {
  const reviews = useProjectReviews(projectId)
  return (
    <section aria-label="Open reviews" className="mt-8 flex flex-col gap-2">
      <h2 className="font-medium">Open reviews</h2>
      {reviews.isError ? (
        <LoadError message="Couldn't load reviews." onRetry={reviews.refetch} />
      ) : reviews.data === undefined ? (
        <LoadingBlock label="Loading reviews" className="h-16" />
      ) : reviews.data.length === 0 ? (
        <p className="text-sm">No open reviews</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Subject</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Reviewers</TableHead>
              <TableHead>Age</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {reviews.data.map((r) => (
              <TableRow key={r.id}>
                <TableCell>
                  <Link to="/projects/$projectId/reviews/$reviewId" params={{ projectId, reviewId: String(r.id) }} className="underline-offset-2 hover:text-highlight hover:underline">
                    {r.task || `${r.subject} ${r.doc || r.rule}`}
                  </Link>
                </TableCell>
                <TableCell>{r.state}</TableCell>
                <TableCell>{(r.reviewers ?? []).join(", ")}</TableCell>
                <TableCell>{age(r.opened_at)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  )
}
