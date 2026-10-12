import { Link } from "@tanstack/react-router"
import { useProjects } from "@/api/queries"
import type { MockProject } from "@/api/mock-types"
import { ArrowRightIcon } from "@/components/icons"
import { Card } from "@/components/ui/card"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { Progress } from "@/components/ui/progress"

// Home shows four cards: a 2×2 grid on desktop, a swipeable row on phones
const QUICK_LINK_COUNT = 4

// Once the row of cards is at least @2xl (42rem) wide — tablet, and desktop
// from ~1160px — each card has room to pair things side by side: days
// remaining moves up next to the status line, and the arrow sits at the end
// of the progress bar. Narrower than that (the phone scroller, desktop's
// 1024–1160px squeeze) everything stacks in DOM order. A container query
// rather than breakpoints, since card width doesn't track the viewport:
// tablet's full-width 2-col grid gives wider cards than small desktop's.
function ProjectQuickLinkCard({ project }: { project: MockProject }) {
  return (
    <Card
      asChild
      className="w-63.5 shrink-0 snap-start md:w-auto @2xl:grid @2xl:grid-cols-[1fr_auto] @2xl:items-center"
    >
      <Link to="/projects/$projectId" params={{ projectId: project.id }}>
        <h2 className="text-base font-medium md:text-22 @2xl:col-span-2">{project.title}</h2>
        <p className="text-sm @2xl:col-start-1 @2xl:row-start-2">
          <span className="font-extrabold">{project.attention.split(" ")[0]} </span>
          {project.attention.split(" ").slice(1).join(" ")}
        </p>
        <Progress
          value={project.progress}
          className="h-3 bg-surface @2xl:col-start-1 @2xl:row-start-3 [&>[data-slot=progress-indicator]]:bg-highlight"
        />
        <div className="flex w-full items-center gap-2.5 @2xl:contents">
          <p className="flex-1 text-13 @2xl:col-span-2 @2xl:col-start-1 @2xl:row-start-2 @2xl:justify-self-end">
            <span className="font-extrabold">{project.daysRemaining}</span> days remaining
          </p>
          <ArrowRightIcon size={24} className="@2xl:col-start-2 @2xl:row-start-3 @2xl:justify-self-end" />
        </div>
      </Link>
    </Card>
  )
}

export function ProjectQuickLinks() {
  const projects = useProjects()

  return (
    <section className="flex w-full flex-col items-start gap-4 md:gap-panel">
      {/* 2rem line box = text-2xl's, so the cards stay level with Upcoming Meetings' */}
      <h2 className="text-lg text-text-primary md:text-xl/8">Project Quick Links</h2>
      {projects.isPending ? (
        <LoadingBlock label="Loading projects" className="h-40" />
      ) : projects.isError ? (
        <LoadError message="Couldn't load your projects." onRetry={() => void projects.refetch()} />
      ) : (
        /*
          Mobile: bleeds past the page's 20px margin so the row can be dragged
          edge-to-edge (negative margin cancels the parent's padding, matching
          px re-adds it as padding so cards still start inset; scroll-px keeps
          snap-start from snapping the first card flush to the viewport edge).
          Desktop: back to a static, non-scrolling 2-col grid.
        */
        <div className="@container -mx-5 flex w-[calc(100%+40px)] snap-x snap-proximity gap-2.5 overflow-x-auto scroll-smooth scroll-px-5 px-5 pb-1 md:mx-0 md:w-full md:grid md:grid-cols-2 md:gap-5 md:overflow-visible md:px-0 md:pb-0">
          {projects.data.slice(0, QUICK_LINK_COUNT).map((project) => (
            <ProjectQuickLinkCard key={project.id} project={project} />
          ))}
        </div>
      )}
    </section>
  )
}
