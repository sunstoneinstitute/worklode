import { useParams } from "@tanstack/react-router"
import { HttpError, useReview, useReviewGuide } from "@/api/queries"
import type { Review as ReviewData, ReviewGuide } from "@/api/queries"
import { ThreadItem } from "@/editor/CommentPanel"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { NotFound } from "@/pages/NotFound"

/**
 * A review, read-only (WL-REQ-1775): its subject, reviewers, each round's
 * verdict, the guide and every thread. Reviews are written through `lode
 * review`, so the page has no form. An `obsolete` thread is listed struck
 * through, never hidden (WL-REQ-1317).
 */
export function Review() {
  const { reviewId } = useParams({ from: "/projects/$projectId/reviews/$reviewId" })
  const review = useReview(reviewId)
  if (review.error instanceof HttpError && review.error.status === 404) return <NotFound />
  if (review.isError) return <main className="p-5 pt-8 lg:p-page"><LoadError message="Couldn't load this review." onRetry={() => void review.refetch()} /></main>
  if (!review.data) return <main className="p-5 pt-8 lg:p-page"><LoadingBlock label="Loading review" className="h-64" /></main>
  return <ReviewBody review={review.data} id={reviewId} />
}

function ReviewBody({ review, id }: { review: ReviewData; id: string }) {
  const rounds = review.rounds ?? []
  const threads = review.threads ?? []
  const focused = rounds.at(-1)?.focused ?? false
  const guide = useReviewGuide(id, focused)
  return (
    <main className="flex flex-col gap-6 p-5 pt-8 text-sm lg:p-page">
      <header className="flex flex-col gap-1">
        <h1 className="text-22 font-medium">Review {review.id}: {review.task || `${review.subject} ${review.doc || review.rule || ""}`}</h1>
        <p>
          {review.state}, opened by {review.opened_by} {review.opened_at?.slice(0, 10)}. Reviewers: {(review.reviewers ?? []).join(", ") || "none"}.
        </p>
        {review.reason ? <p>{review.reason}</p> : null}
      </header>
      <section aria-label="Rounds" className="flex flex-col gap-2">
        <h2 className="font-medium">Rounds</h2>
        {rounds.length === 0 ? <p>No rounds yet.</p> : null}
        <ul className="flex flex-col gap-1">
          {rounds.map((r) => (
            <li key={r.round}>
              Round {r.round}: {r.verdict} by {r.actor}{r.focused ? " (focused)" : ""}
              {r.overall_note ? `. ${r.overall_note}` : ""}
            </li>
          ))}
        </ul>
      </section>
      {guide.data ? <Guide guide={guide.data} /> : null}
      <section aria-label="Threads" className="flex flex-col gap-3">
        <h2 className="font-medium">Threads</h2>
        {threads.length === 0 ? <p>No threads.</p> : null}
        <ul className="flex flex-col gap-3">
          {threads.map((t) => (
            <ThreadItem key={t.id} thread={t} readOnly />
          ))}
        </ul>
      </section>
    </main>
  )
}

function Guide({ guide }: { guide: ReviewGuide }) {
  return (
    <section aria-label="Guide" className="flex flex-col gap-2">
      <h2 className="font-medium">Guide</h2>
      {guide.stale ? <p>This guide is from an earlier round.</p> : null}
      <p>{guide.overview}</p>
      <ul className="flex flex-col gap-2">
        {(guide.files ?? []).map((f) => (
          <li key={f.path} className="flex flex-col gap-1">
            <span className="font-medium">{f.path}{f.scrutiny ? " (scrutinize)" : ""}</span>
            {f.orientation ? <span>{f.orientation}</span> : null}
            {(f.skims ?? []).map((s) => (
              <span key={`${s.from}-${s.to}`} className="text-text-primary/50">
                Lines {s.from}-{s.to}: skimmed by {s.decided_by}
              </span>
            ))}
          </li>
        ))}
      </ul>
    </section>
  )
}
