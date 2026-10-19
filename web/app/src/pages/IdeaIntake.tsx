import { useCallback, useRef, useState } from "react"
import { useParams } from "@tanstack/react-router"
import { format, parseISO } from "date-fns"
import { useCreateIdeaSource, useDeleteDocument, useSaveIntake, useShareIdea, useUpdateDocument } from "@/api/mutations"
import { HttpError, useCrew, useIdea, useIdeaSources, useLitmusTests, useMe, usePreResearch } from "@/api/queries"
import type { Idea, ProjectDocument } from "@/api/types"
import { SOURCE_COLUMNS } from "@/components/DocumentList"
import { IdeaForm, type IdeaFormHandle } from "@/components/IdeaForm"
import { SHARE_IDEA_CONFIRM } from "@/components/IdeaList"
import { TaskHighlight } from "@/components/TaskHighlight"
import { CheckIcon, PencilIcon } from "@/components/icons"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ChipNav } from "@/components/ui/chip-nav"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { DataTable, formatIsoDate } from "@/components/ui/data-table"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { SidePanel, SidePanelTitle } from "@/components/ui/side-panel"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useMediaQuery } from "@/hooks/use-media-query"
import { STORY_TYPE_OPTIONS } from "@/lib/content-types"
import { NotFound } from "@/pages/NotFound"

type Intake = { pre_research_authorized: boolean; decision: string }
type LitmusTest = NonNullable<ReturnType<typeof useLitmusTests>["data"]>[number]

const GRADIENT = "bg-gradient-to-br from-gradient-start to-gradient-end"

// Same filled button style as ConfirmDialog's Yes
const FILLED_BUTTON =
  "h-8 whitespace-nowrap cursor-pointer rounded-full border border-button-bg bg-button-bg px-4 text-sm text-button-text transition-colors hover:border-primary hover:bg-primary dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
// The quieter choices: outlined, filling with the outline's color on hover
const QUIET_BUTTON =
  "h-8 whitespace-nowrap cursor-pointer rounded-full border border-text-primary px-4 text-sm transition-colors hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
const OUTLINED_BUTTON =
  "rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"

const DECISION_LABELS: Record<string, string> = {
  accept: "Accepted",
  narrow: "Narrowed",
  park: "Parked",
  stop: "Stopped",
}

/**
 * The decision buttons (WL-REQ-1807): "Authorize AI pre-research", then
 * "Accept", each with Narrow, Park and Stop as quieter alternatives. They
 * record the decision and nothing else.
 */
function IntakeActions({ intake, onChange }: { intake: Intake; onChange: (next: Intake) => void }) {
  const decide = (decision: string) => onChange({ ...intake, decision })
  return (
    <>
      {intake.pre_research_authorized ? (
        <button type="button" className={FILLED_BUTTON} onClick={() => decide("accept")}>
          Accept
        </button>
      ) : (
        <button type="button" className={FILLED_BUTTON} onClick={() => onChange({ ...intake, pre_research_authorized: true })}>
          Authorize AI pre-research
        </button>
      )}
      <span className="flex flex-wrap gap-2.5">
        <button type="button" className={QUIET_BUTTON} onClick={() => decide("narrow")}>
          Narrow
        </button>
        <button type="button" className={QUIET_BUTTON} onClick={() => decide("park")}>
          Park
        </button>
        {/* Dark text on the soft red: red text on this card is under 3:1 */}
        <button
          type="button"
          className="h-8 cursor-pointer rounded-full bg-destructive/20 px-4 text-sm transition-colors hover:bg-destructive hover:text-text-dark"
          onClick={() => decide("stop")}
        >
          Stop
        </button>
      </span>
    </>
  )
}

/** Where intake stands, from `intake` and which findings exist (WL-PLAN-157 D2) */
function intakeSteps(intake: Intake, hasLitmus: boolean, hasPreResearch: boolean) {
  const steps = [
    { label: "Litmus", done: hasLitmus },
    { label: "AI Pre-Research", done: intake.pre_research_authorized && hasPreResearch },
    { label: "Decision", done: intake.decision !== "" },
  ]
  const status = intake.decision
    ? `Decision recorded: ${DECISION_LABELS[intake.decision] ?? intake.decision}`
    : intake.pre_research_authorized
      ? hasPreResearch
        ? "AI pre-research done. Waiting on your decision."
        : "AI pre-research authorized. Waiting on its findings."
      : hasLitmus
        ? "Litmus test complete. Waiting on authorization for AI pre-research."
        : "No litmus test yet. Waiting on authorization for AI pre-research."
  return { steps, status }
}

/** Status only: the three steps and a line saying where things stand */
function IntakeSummary({ steps, status }: ReturnType<typeof intakeSteps>) {
  // A band across the top of the gradient sidebar, darkened over it so
  // white text keeps its contrast (6.0:1 at the light end)
  return (
    <section className="relative isolate flex flex-col items-start gap-4 py-panel pr-5 pl-14.5 text-left text-text-light 2xl:pr-page 2xl:pl-17">
      <div aria-hidden="true" className="absolute inset-0 -z-10 bg-icons-alt/90 mix-blend-darken dark:mix-blend-normal" />
      <h2 className="text-lg md:text-xl/8">Intake summary</h2>
      <ol aria-label="Intake progress" className="flex w-full max-w-md gap-0.5 overflow-hidden rounded-full text-xs leading-tight">
        {steps.map((step) => (
          <li
            key={step.label}
            className={`flex min-w-0 flex-1 items-center justify-center gap-1 px-1.5 py-2 text-center ${
              step.done ? "bg-highlight text-text-dark" : "bg-text-light/10"
            }`}
          >
            {step.done ? <CheckIcon size={14} className="shrink-0" /> : null}
            {step.label}
            <span className="sr-only">{step.done ? "(done)" : "(to do)"}</span>
          </li>
        ))}
      </ol>
      <p className="max-w-sm text-sm">{status}</p>
    </section>
  )
}

/** The pitch in full: title, author, submitted date, body; Edit for its author */
function PitchCard({ idea, author, onEdit }: { idea: Idea; author: string; onEdit?: () => void }) {
  return (
    <section aria-label="Pitch" className="relative flex flex-col gap-3 rounded-card bg-surface p-panel pr-16">
      {onEdit ? (
        <button
          type="button"
          aria-label="Edit pitch"
          onClick={onEdit}
          className="absolute top-5 right-5 flex cursor-pointer items-center transition-colors hover:text-highlight"
        >
          <PencilIcon size={22} />
        </button>
      ) : null}
      <p className="text-sm">
        {author}
        {idea.submitted ? ` · Submitted ${formatIsoDate(idea.submitted)}` : null}
      </p>
      <p className="text-base whitespace-pre-line">{idea.body || "No pitch text yet."}</p>
    </section>
  )
}

/** Shown instead of everything intake-side while the idea is private (WL-REQ-1807) */
function PrivateIdeaPanel({ idea, canShare }: { idea: Idea; canShare: boolean }) {
  const share = useShareIdea()
  const [confirming, setConfirming] = useState(false)
  return (
    <section aria-label="Private idea" className="flex flex-wrap items-center gap-4 rounded-card bg-surface p-panel text-base">
      <p className="flex-1">This idea is private: only you can see it. Share it to send it to intake.</p>
      {canShare ? (
        <>
          <Button variant="outline" size="sm" disabled={share.isPending} onClick={() => setConfirming(true)} className={OUTLINED_BUTTON}>
            Share
          </Button>
          <ConfirmDialog
            open={confirming}
            message={SHARE_IDEA_CONFIRM}
            onConfirm={() => share.mutate(idea.id ?? "")}
            onOpenChange={setConfirming}
          />
        </>
      ) : null}
    </section>
  )
}

/** "Latest (Oct 12, 2026)" for the newest test, "Oct 9, 2026" for the rest */
function litmusTestLabel(date: string, index: number) {
  const day = date ? format(parseISO(date), "MMM d, yyyy") : "Undated"
  return index === 0 ? `Latest (${day})` : day
}

function LitmusTab({ tests }: { tests: ReturnType<typeof useLitmusTests> }) {
  const [testId, setTestId] = useState<string>()

  if (tests.isPending) return <LoadingBlock label="Loading litmus tests" className="min-h-100" />
  if (tests.isError) return <LoadError message="Couldn't load the litmus tests." onRetry={() => void tests.refetch()} />
  if (tests.data.length === 0) return <p className="rounded-card bg-surface p-panel text-base">No litmus test yet.</p>

  // The newest test until another is picked
  const test: LitmusTest = tests.data.find((candidate) => candidate.id === testId) ?? tests.data[0]
  return (
    <Tabs defaultValue="summary" className="gap-5">
      <div className="flex flex-wrap items-center justify-between gap-5">
        <TabsList variant="line" className="border-b border-divider-line/25">
          <TabsTrigger value="summary" className="cursor-pointer px-6 md:flex-none">
            Summary
          </TabsTrigger>
          <TabsTrigger value="recommendations" className="cursor-pointer px-6 md:flex-none">
            Recommendations
          </TabsTrigger>
        </TabsList>
        <Select value={test.id} onValueChange={setTestId}>
          <SelectTrigger aria-label="Litmus test" className="w-56">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {tests.data.map((candidate, index) => (
              <SelectItem key={candidate.id} value={candidate.id ?? ""}>
                {litmusTestLabel(candidate.date ?? "", index)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <TabsContent value="summary" className="min-h-100 rounded-card bg-surface p-panel text-base">
        <p>{test.summary || "No summary."}</p>
      </TabsContent>
      <TabsContent value="recommendations" className="min-h-100 rounded-card bg-surface p-panel text-base">
        {test.recommendations?.length ? (
          <ol className="list-decimal space-y-2 pl-5">
            {test.recommendations.map((recommendation) => (
              <li key={recommendation}>{recommendation}</li>
            ))}
          </ol>
        ) : (
          <p>No recommendations.</p>
        )}
      </TabsContent>
    </Tabs>
  )
}

function PreResearchTab({ research }: { research: ReturnType<typeof usePreResearch> }) {
  return (
    <div className="flex flex-col gap-5">
      <h2 className="text-2xl">AI Pre-Research</h2>
      {research.isPending ? (
        <LoadingBlock label="Loading pre-research" className="h-40" />
      ) : research.isError ? (
        <LoadError message="Couldn't load the pre-research." onRetry={() => void research.refetch()} />
      ) : research.data.length === 0 ? (
        <p className="rounded-card bg-surface p-panel text-base">No pre-research yet.</p>
      ) : (
        <ul className="list-disc space-y-2 rounded-card bg-surface p-panel pl-10">
          {research.data.map((finding) => (
            <li key={finding}>{finding}</li>
          ))}
        </ul>
      )}
    </div>
  )
}

// Module-level so DataTable gets a stable function (see TaskList)
function getSourceId(source: ProjectDocument) {
  return source.id ?? ""
}

/** The idea's sources: the Documents page's Sources columns and actions */
function SourcesTab({ ideaId }: { ideaId: string }) {
  const sources = useIdeaSources(ideaId)
  const createSource = useCreateIdeaSource(ideaId)
  const updateMutate = useUpdateDocument().mutate
  const deleteMutate = useDeleteDocument().mutate
  const onUpdateRow = useCallback(
    (id: string, patch: Partial<ProjectDocument>) => updateMutate({ id, patch }),
    [updateMutate],
  )
  const onRemoveRow = useCallback((id: string) => deleteMutate(id), [deleteMutate])

  if (sources.isPending) return <LoadingBlock label="Loading sources" className="h-80" />
  if (sources.isError) return <LoadError message="Couldn't load the sources." onRetry={() => void sources.refetch()} />

  return (
    <DataTable
      columns={SOURCE_COLUMNS}
      data={sources.data}
      getRowId={getSourceId}
      onUpdateRow={onUpdateRow}
      onRemoveRow={onRemoveRow}
      title="Sources"
      toolbarActions={
        <Button
          variant="outline"
          size="sm"
          disabled={createSource.isPending}
          onClick={() => createSource.mutate({ name: "Untitled source", type: "document", status: "draft" })}
          className={OUTLINED_BUTTON}
        >
          Add Source
        </Button>
      }
    />
  )
}

const SECTIONS = [
  { value: "pitch", label: "Pitch" },
  { value: "litmus", label: "Litmus" },
  { value: "pre-research", label: "AI Pre-Research" },
  { value: "sources", label: "Sources" },
] as const

type Section = (typeof SECTIONS)[number]["value"]

/**
 * One idea's Intake page (WL-REQ-1807). A private idea shows its pitch and
 * the Share panel only, and none of the findings routes is called. A shared
 * one adds the intake summary, the decision buttons, and its litmus tests,
 * pre-research and sources.
 */
export function IdeaIntake() {
  const { ideaId } = useParams({ from: "/ideas/$ideaId/intake" })
  const me = useMe().id ?? ""
  const ideaQuery = useIdea(ideaId)
  const shared = ideaQuery.data?.status === "intake"
  const litmus = useLitmusTests(ideaId, shared)
  const research = usePreResearch(ideaId, shared)
  const crew = useCrew()
  const saveIntake = useSaveIntake()
  const [section, setSection] = useState<Section>("pitch")
  const [editingPitch, setEditingPitch] = useState(false)
  const pitchFormRef = useRef<IdeaFormHandle>(null)
  const desktop = useMediaQuery("(min-width: 1024px)")

  if (ideaQuery.isError && ideaQuery.error instanceof HttpError && ideaQuery.error.status === 404) return <NotFound />
  if (ideaQuery.isPending || ideaQuery.isError) {
    return (
      <div className="min-h-screen bg-background pt-13 lg:pt-19">
        <main className="p-5 pt-8 lg:p-page">
          {ideaQuery.isPending ? (
            <LoadingBlock label="Loading idea" className="h-80" />
          ) : (
            <LoadError message="Couldn't load the idea." onRetry={() => void ideaQuery.refetch()} />
          )}
        </main>
      </div>
    )
  }

  const idea = ideaQuery.data
  const mine = idea.author === me
  const authorName = crew.data?.find((c) => c.actor === idea.author)?.display_name || idea.author || ""
  const storyType = STORY_TYPE_OPTIONS.find((option) => option.value === idea.story_type)
  const pitch = <PitchCard idea={idea} author={authorName} onEdit={mine ? () => setEditingPitch(true) : undefined} />
  const intake: Intake = {
    pre_research_authorized: idea.intake?.pre_research_authorized ?? false,
    decision: idea.intake?.decision ?? "",
  }
  const summary = intakeSteps(intake, (litmus.data?.length ?? 0) > 0, (research.data?.length ?? 0) > 0)
  // AI Pre-Research only once it is authorized
  const sections = SECTIONS.filter((option) => option.value !== "pre-research" || intake.pre_research_authorized)

  // The summary band, then the decision (while one is waiting): the
  // gradient sidebar on desktop, a band under the title on mobile
  const sidebar = (
    <>
      <IntakeSummary {...summary} />
      {intake.decision ? null : (
        <div className="px-5 py-panel 2xl:px-panel 2xl:pr-page">
          <TaskHighlight
            title="You have a decision to make on this pitch"
            detail={
              intake.pre_research_authorized
                ? "Your acceptance is needed to start the project."
                : "Your decision is required to authorize pre-research for this idea."
            }
          >
            <IntakeActions intake={intake} onChange={(next) => saveIntake.mutate({ id: ideaId, patch: { intake: next } })} />
          </TaskHighlight>
        </div>
      )}
    </>
  )

  return (
    <div className="min-h-screen bg-background pt-13 lg:flex lg:flex-col lg:pt-19">
      <div className="flex flex-col px-5 pb-5 lg:grid lg:flex-1 lg:grid-cols-12 lg:items-stretch lg:p-0">
        <main className="flex min-w-0 flex-col gap-8 pt-8 text-text-primary lg:col-span-8 lg:p-page">
          <div className="flex flex-col items-start gap-2">
            <h1 className="text-2xl md:text-3xl">{idea.title}</h1>
            {storyType ? <Badge className="bg-muted text-muted-foreground">{storyType.label}</Badge> : null}
          </div>
          {!shared ? (
            <>
              {pitch}
              <PrivateIdeaPanel idea={idea} canShare={mine} />
            </>
          ) : (
            <>
              {desktop ? null : <div className={`-mx-5 w-[calc(100%+40px)] ${GRADIENT}`}>{sidebar}</div>}
              <div className="flex flex-col gap-8">
                <ChipNav label="Section" options={sections} value={section} onChange={setSection} />
                {section === "pitch" ? (
                  pitch
                ) : section === "litmus" ? (
                  <LitmusTab tests={litmus} />
                ) : section === "pre-research" ? (
                  <PreResearchTab research={research} />
                ) : (
                  <SourcesTab ideaId={ideaId} />
                )}
              </div>
            </>
          )}
        </main>
        {shared && desktop ? (
          <aside className={`min-w-0 overflow-hidden lg:col-span-4 lg:mt-page lg:rounded-tl-panel ${GRADIENT}`}>{sidebar}</aside>
        ) : null}
      </div>

      {mine ? (
        <SidePanel
          open={editingPitch}
          onOpenChange={(open) => {
            if (open) return
            if (pitchFormRef.current) pitchFormRef.current.requestClose()
            else setEditingPitch(false)
          }}
        >
          <IdeaForm
            ref={pitchFormRef}
            idea={idea}
            heading={(text) => <SidePanelTitle>{text}</SidePanelTitle>}
            onDone={() => setEditingPitch(false)}
            onClose={() => setEditingPitch(false)}
          />
        </SidePanel>
      ) : null}
    </div>
  )
}
