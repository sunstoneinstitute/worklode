import { useMeetings } from "@/api/queries"
import { ArrowRightIcon, ScheduleIcon } from "@/components/icons"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { formatMeetingTime } from "@/lib/meeting-time"

// Phones and the desktop sidebar show three meetings (as in Figma); tablet
// lays them out two-across, so it shows a fourth to fill out the 2×2 grid.
const TABLET_ONLY_FROM = 3

// The Figma design uses a bespoke illustrated clock icon here, which the
// user is still designing (out of scope for now) — this is a plain
// placeholder in its place, not a faithful reproduction. The filled
// "schedule" glyph is itself already circular, so it's rendered directly
// (no separate circle wrapper behind it) at a larger size. The glyph draws
// its circle ~8px inside its own box, hence -ml-2 to put the visible edge
// on the page margin, and -my-2 so it doesn't make the row taller than the
// (shorter, on mobile) card beside it and push the rows apart.
// 60px (and a proportionally smaller pull/overlap) on phones and in the
// narrow 1024–1536px sidebar; 80px on tablet and from 2xl.
function SchedulePlaceholder() {
  return (
    <ScheduleIcon
      size={null}
      className="relative z-10 -my-2 -ml-1.5 -mr-8 shrink-0 text-6xl text-icons-alt md:-ml-2 md:-mr-10 md:text-80 lg:-ml-1.5 lg:-mr-8 lg:text-6xl 2xl:-ml-2 2xl:-mr-10 2xl:text-80"
    />
  )
}

export function UpcomingMeetings({
  className,
  limit = Infinity,
}: {
  className?: string
  limit?: number
}) {
  const meetings = useMeetings()

  return (
    <section className={className}>
      {/* Same type as "Project Quick Links" (incl. its 2rem line box) so the
          two headings and their card rows line up on desktop Home. Centered
          whenever it's in the right-hand sidebar (lg+); left-aligned in the
          phone/tablet band. */}
      <h2 className="text-lg text-text-light md:text-xl/8 lg:text-center">
        Upcoming Meetings
      </h2>
      {meetings.isPending ? (
        <LoadingBlock label="Loading meetings" className="mt-4 h-64 md:mt-panel" />
      ) : meetings.isError ? (
        <div className="mt-4 md:mt-panel">
          <LoadError message="Couldn't load meetings." onRetry={() => void meetings.refetch()} />
        </div>
      ) : (
        <div className="mt-4 flex w-full flex-col gap-2 md:mt-panel md:grid md:grid-cols-2 md:gap-3.75 lg:flex">
          {meetings.data.slice(0, limit).map((meeting, index) => (
            <div
              key={meeting.id}
              className={`${index >= TABLET_ONLY_FROM ? "hidden md:flex lg:hidden" : "flex"} min-w-0 items-center gap-4 2xl:pl-[12.6%]`}
            >
              <SchedulePlaceholder />
              <div className="flex min-w-0 flex-1 items-center gap-5 rounded-2xl border border-primary/60 bg-background-translucent py-4 pl-6 pr-5 md:rounded-panel md:py-panel md:pl-10 md:pr-panel lg:py-5 lg:pl-8 lg:pr-5 2xl:py-panel 2xl:pl-10 2xl:pr-panel">
                <div className="min-w-0 flex-1">
                  <p className="text-15 font-medium text-text-primary md:text-base">
                    {meeting.title}
                  </p>
                  <p className="text-13 text-text-primary">
                    {formatMeetingTime(meeting.start, meeting.end)}
                  </p>
                </div>
                <ArrowRightIcon size={24} className="shrink-0 text-text-primary" />
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
