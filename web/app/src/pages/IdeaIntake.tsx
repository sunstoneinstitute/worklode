import { type FormEvent, useState } from "react"
import { Link } from "@tanstack/react-router"
import { SOURCE_COLUMNS, type ProjectDocument } from "@/components/DocumentList"
import { TaskHighlight } from "@/components/TaskHighlight"
import { CheckIcon, CloseIcon, PencilIcon } from "@/components/icons"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ChipNav } from "@/components/ui/chip-nav"
import { DataTable } from "@/components/ui/data-table"
import { Field, FieldLabel } from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { FORM_TEXTAREA } from "@/components/ui/form-fields"
import { SidePanel, SidePanelTitle } from "@/components/ui/side-panel"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { useMediaQuery } from "@/hooks/use-media-query"
import { STORY_TYPE_OPTIONS } from "@/lib/content-types"
import {
  type Idea,
  type IntakeDecision,
  type IntakeProgress,
  getIdeas,
  updateIdea,
} from "@/lib/ideas"

const GRADIENT = "bg-gradient-to-br from-gradient-start to-gradient-end"

// Same filled button style as ConfirmDialog's Yes
const FILLED_BUTTON =
  "h-8 whitespace-nowrap cursor-pointer rounded-full border border-button-bg bg-button-bg px-4 text-sm text-button-text transition-colors hover:border-primary hover:bg-primary dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
// The less prominent choices: outlined, filling with the outline's color
// on hover (highlight with dark text in dark mode), like other outlined buttons
const QUIET_BUTTON =
  "h-8 whitespace-nowrap cursor-pointer rounded-full border border-text-primary px-4 text-sm transition-colors hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"

const DECISION_LABELS: Record<IntakeDecision, string> = {
  accept: "Accepted",
  narrow: "Narrowed",
  park: "Parked",
  stop: "Stopped",
}

/**
 * The decision the Intake flow is waiting on, as buttons for the task
 * highlight: first "Authorize AI pre-research", then (once authorized and
 * the Science Lead has accepted) "Accept" — each with Narrow, Park and
 * Stop as quieter alternatives. What each decision leads to isn't
 * designed yet; it's just recorded.
 */
function IntakeActions({
  progress,
  onChange,
}: {
  progress: IntakeProgress
  onChange: (next: IntakeProgress) => void
}) {
  const decide = (decision: IntakeDecision) => onChange({ ...progress, decision })

  return (
    <>
      {/* The main action on its own line; the alternatives below it, quieter */}
      {progress.preResearchAuthorized ? (
        <button type="button" className={FILLED_BUTTON} onClick={() => decide("accept")}>
          Accept
        </button>
      ) : (
        <button
          type="button"
          className={FILLED_BUTTON}
          onClick={() => onChange({ ...progress, preResearchAuthorized: true })}
        >
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
        {/* Red for the one that ends it: a soft red fill, solid on hover.
            Dark text, since red text on this card is under 3:1 */}
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

/**
 * The Intake summary: status only — a three-step progress bar (Litmus →
 * AI Pre-Research → Decision) and a line saying where things stand. The
 * decision itself is made from the task highlight above it.
 *
 */
function IntakeSummary({ progress }: { progress: IntakeProgress }) {
  const steps = [
    { label: "Litmus", done: true },
    { label: "AI Pre-Research", done: progress.preResearchAuthorized },
    { label: "Decision", done: progress.decision !== undefined },
  ]
  const status = progress.decision
    ? `Decision recorded: ${DECISION_LABELS[progress.decision]}`
    : progress.preResearchAuthorized
      ? "AI pre-research authorized and the Science Lead has accepted. Waiting on your decision."
      : "Litmus test complete. Waiting on authorization for AI pre-research."

  // A header band across the top of the gradient sidebar (per the
  // placement wireframe): flush to its edges, in the icons' blue
  // (icons-alt) at 90% with a darken blend over the gradient — each
  // channel takes the darker of the two, so the gradient's deeper end
  // still reads through. White text: 6.0:1 at the gradient's light end,
  // 7.4:1 at its dark end. Dark mode blends normally: its gradient is
  // already darker than icons-alt, so darken would make the band vanish.
  // The fill is its own layer, so the blend doesn't touch the text.
  // Left-aligned with the task
  // card below: its left edge is the padding (20px, 30px at 2xl) + the
  // overlapping icon's net width (60 − 6 − 32 = 22px) + the 16px gap.
  return (
    <section className="relative isolate flex flex-col items-start gap-4 py-panel pr-5 pl-14.5 text-left text-text-light 2xl:pr-page 2xl:pl-17">
      <div
        aria-hidden="true"
        className="absolute inset-0 -z-10 bg-icons-alt/90 mix-blend-darken dark:mix-blend-normal"
      />
      <h2 className="text-lg md:text-xl/8">Intake summary</h2>
      {/* One continuous bar: segments edge to edge, split by thin gaps in
          the band's color rather than borders. Finished steps are
          highlight with dark text (8.6:1); the rest a faint white with
          white text (4.8:1 — /15 would dip under 4.5). Labels wrap in
          narrow sidebars */}
      <ol
        aria-label="Intake progress"
        className="flex w-full max-w-md gap-0.5 overflow-hidden rounded-full text-xs leading-tight"
      >
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

/** The submitted pitch, in full (it has its own tab), with an Edit button */
function PitchCard({ text, onEdit }: { text: string; onEdit: () => void }) {
  return (
    <section aria-label="Pitch" className="relative rounded-card bg-surface p-panel pr-16">
      <button
        type="button"
        aria-label="Edit pitch"
        onClick={onEdit}
        className="absolute top-5 right-5 flex cursor-pointer items-center transition-colors hover:text-highlight"
      >
        <PencilIcon size={22} />
      </button>
      <p className="text-base whitespace-pre-line">{text || "No pitch text yet."}</p>
    </section>
  )
}

/** The side panel's pitch editor */
function EditPitchForm({
  text,
  onSave,
  onClose,
}: {
  text: string
  onSave: (text: string) => void
  onClose: () => void
}) {
  const [draft, setDraft] = useState(text)
  const handleSubmit = (event: FormEvent) => {
    event.preventDefault()
    onSave(draft.trim())
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-8 text-text-primary">
      <div className="flex items-start justify-between gap-4">
        <SidePanelTitle>Edit Pitch</SidePanelTitle>
        <button
          type="button"
          aria-label="Close"
          onClick={onClose}
          className="flex cursor-pointer items-center transition-colors hover:text-highlight"
        >
          <CloseIcon size={28} />
        </button>
      </div>
      {/* Placeholder for the BlockNote editor, like the New Idea form */}
      <Field>
        <FieldLabel htmlFor="edit-pitch">Pitch</FieldLabel>
        <Textarea
          id="edit-pitch"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          className={`min-h-80 lg:min-h-160 ${FORM_TEXTAREA}`}
        />
      </Field>
      <Button type="submit" className={`self-start ${FILLED_BUTTON}`}>
        Save
      </Button>
    </form>
  )
}

// Placeholder litmus results until they come from the Litmus Test system
const LITMUS_TESTS = [
  {
    value: "2026-08-30",
    label: "Latest (Aug 30, 2026)",
    summary:
      "Strong fit. The core question is clear and answerable, the data is public and well maintained, and the story connects a global trend to specific places.",
    recommendations: [
      "Narrow to three valleys at different stages of retreat.",
      "Confirm river discharge records go back far enough to show change.",
      "Line up at least one on-the-ground interview per valley.",
    ],
  },
  {
    value: "2026-07-12",
    label: "Jul 12, 2026",
    summary:
      "Promising but broad. The pitch covers every Alpine glacier; a tighter geographic focus would make the story more tellable.",
    recommendations: ["Pick a smaller set of places.", "Identify the main dataset."],
  },
]

const LITMUS_VIEWS = [
  { value: "summary", label: "Summary" },
  { value: "recommendations", label: "Recommendations" },
] as const

function LitmusTab() {
  const [testValue, setTestValue] = useState(LITMUS_TESTS[0].value)
  const test = LITMUS_TESTS.find((candidate) => candidate.value === testValue) ?? LITMUS_TESTS[0]

  // Summary / Recommendations as underlined tabs (left), the test's date
  // picker (right) — a different look from the section chips above, so
  // the two levels of navigation don't blur together. No "Litmus Test(s)"
  // heading: the Litmus chip already says it.
  return (
    <Tabs defaultValue="summary" className="gap-5">
      <div className="flex flex-wrap items-center justify-between gap-5">
        <TabsList variant="line" className="border-b border-divider-line/25">
          {LITMUS_VIEWS.map((view) => (
            <TabsTrigger key={view.value} value={view.value} className="cursor-pointer px-6 md:flex-none">
              {view.label}
            </TabsTrigger>
          ))}
        </TabsList>
        <Select value={testValue} onValueChange={setTestValue}>
          <SelectTrigger aria-label="Litmus test" className="w-56">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {LITMUS_TESTS.map((candidate) => (
              <SelectItem key={candidate.value} value={candidate.value}>
                {candidate.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <TabsContent value="summary" className="min-h-100 rounded-card bg-surface p-panel text-base">
        <p>{test.summary}</p>
      </TabsContent>
      <TabsContent value="recommendations" className="min-h-100 rounded-card bg-surface p-panel text-base">
        <ol className="list-decimal space-y-2 pl-5">
          {test.recommendations.map((recommendation) => (
            <li key={recommendation}>{recommendation}</li>
          ))}
        </ol>
      </TabsContent>
    </Tabs>
  )
}

// Placeholder until AI pre-research has a design and real output
const PRE_RESEARCH_FINDINGS = [
  "Glacier volume and extent data is public and goes back over a century for the Swiss Alps.",
  "River discharge records exist for all three candidate valleys, but coverage before 1960 is patchy.",
  "Two recent studies model downstream water supply under glacier loss; both are open access.",
]

function PreResearchTab() {
  return (
    <div className="flex flex-col gap-5">
      <h2 className="text-2xl">AI Pre-Research</h2>
      <div className="flex flex-col gap-4 rounded-card bg-surface p-panel">
        <p className="text-sm text-muted-foreground">Placeholder findings</p>
        <ul className="list-disc space-y-2 pl-5">
          {PRE_RESEARCH_FINDINGS.map((finding) => (
            <li key={finding}>{finding}</li>
          ))}
        </ul>
      </div>
    </div>
  )
}

// Placeholder sources for an idea, in the same shape as the Documents
// page's Sources (so the same columns and actions apply)
const IDEA_SOURCES: ProjectDocument[] = [
  { id: "is1", name: "Swiss glacier mass balance, 1914–2025", category: "sources", type: "dataset", status: "final", source: "GLAMOS" },
  { id: "is2", name: "Alpine ice extent from satellite", category: "sources", type: "dataset", status: "in-review", source: "Copernicus" },
  { id: "is3", name: "Rhône valley farmer interviews", category: "sources", type: "document", status: "draft", source: "Crew interviews" },
]

let nextSourceId = IDEA_SOURCES.length + 1

function SourcesTab() {
  return (
    <DataTable
      columns={SOURCE_COLUMNS}
      data={IDEA_SOURCES}
      getRowId={(source) => source.id}
      title="Sources"
      toolbarActions={({ addRow }) => (
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            addRow({
              id: `is${nextSourceId++}`,
              name: "Untitled source",
              category: "sources",
              type: "document",
              status: "draft",
            })
          }
          // Outlined buttons fill with their border color on hover; in dark
          // mode, highlight with dark text (white on it is only ~3:1)
          className="rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
        >
          Add Source
        </Button>
      )}
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
 * An idea's Intake view — where Edit takes an idea in intake. The title and
 * story type, then chips for its sections — Pitch (with an Edit button that
 * opens a side panel), Litmus and Sources, plus AI Pre-Research once it's
 * authorized.
 */
export function IdeaIntake({ ideaId }: { ideaId: string }) {
  const [idea, setIdea] = useState<Idea | undefined>(() =>
    getIdeas().find((candidate) => candidate.id === ideaId),
  )
  const [editingPitch, setEditingPitch] = useState(false)
  const [section, setSection] = useState<Section>("pitch")
  const desktop = useMediaQuery("(min-width: 1024px)")

  const save = (patch: Partial<Idea>) => {
    updateIdea(ideaId, patch)
    setIdea((current) => current && { ...current, ...patch })
  }

  if (!idea) {
    return (
      <div className="min-h-screen bg-background pt-13 lg:pt-19">
        <main className="flex flex-col gap-4 p-5 pt-8 text-text-primary lg:p-page">
          <h1 className="text-2xl md:text-3xl">Idea not found</h1>
          <Link to="/ideas" className="self-start text-sm underline underline-offset-2 hover:text-highlight">
            Back to Ideas
          </Link>
        </main>
      </div>
    )
  }

  const progress = idea.intake ?? { preResearchAuthorized: false }
  // AI Pre-Research only once it's been authorized
  const sections = SECTIONS.filter(
    (option) => option.value !== "pre-research" || progress.preResearchAuthorized,
  )
  const storyType = STORY_TYPE_OPTIONS.find((option) => option.value === idea.storyType)

  // The task, with the decision's buttons (while one is waiting), above the
  // status-only Intake summary, on
  // the gradient — in the sidebar on desktop, a band on mobile
  // The sidebar's contents: the Intake summary as a band across the top,
  // then the task (with the decision's buttons) while one is waiting
  const sidebar = (
    <>
      <IntakeSummary progress={progress} />
      {progress.decision ? null : (
        <div className="px-5 py-panel 2xl:px-panel 2xl:pr-page">
          <TaskHighlight
            title="You have a decision to make on this pitch"
            detail={
              progress.preResearchAuthorized
                ? "The Science Lead has accepted this pitch. Your acceptance is also needed to start the project."
                : "Your decision is required to authorize pre-research for this idea."
            }
          >
            <IntakeActions progress={progress} onChange={(intake) => save({ intake })} />
          </TaskHighlight>
        </div>
      )}
    </>
  )

  // Project Overview's layout: content on the left (8 of 12 columns), the
  // task and Intake summary in the gradient sidebar where Upcoming Meetings sits
  // there — flush to the right edge, starting level with the content,
  // stretched to the bottom of the window. On mobile the summary is a
  // gradient band after the pitch instead.
  return (
    <div className="min-h-screen bg-background pt-13 lg:flex lg:flex-col lg:pt-19">
      <div className="flex flex-col px-5 pb-5 lg:grid lg:flex-1 lg:grid-cols-12 lg:items-stretch lg:p-0">
      <main className="flex min-w-0 flex-col gap-8 pt-8 text-text-primary lg:col-span-8 lg:p-page">
        <div className="flex flex-col items-start gap-2">
          <h1 className="text-2xl md:text-3xl">{idea.title}</h1>
          {storyType ? <Badge className="bg-muted text-muted-foreground">{storyType.label}</Badge> : null}
        </div>
        {/* Mobile: a gradient band here, above the sections; desktop has it in the sidebar.
            Rendered in one place only, so it's never in the page twice */}
        {desktop ? null : (
          <div className={`-mx-5 w-[calc(100%+40px)] ${GRADIENT}`}>{sidebar}</div>
        )}

        {/* Sections as chips, like the data tables' categories */}
        <div className="flex flex-col gap-8">
          <ChipNav label="Section" options={sections} value={section} onChange={setSection} />
          {section === "pitch" ? (
            <PitchCard text={idea.body ?? ""} onEdit={() => setEditingPitch(true)} />
          ) : section === "litmus" ? (
            <LitmusTab />
          ) : section === "pre-research" ? (
            <PreResearchTab />
          ) : (
            <SourcesTab />
          )}
        </div>
      </main>

      {desktop ? (
        <aside className={`min-w-0 overflow-hidden lg:col-span-4 lg:mt-page lg:rounded-tl-panel ${GRADIENT}`}>
          {sidebar}
        </aside>
      ) : null}
      </div>

      <SidePanel open={editingPitch} onOpenChange={setEditingPitch}>
        <EditPitchForm
          text={idea.body ?? ""}
          onClose={() => setEditingPitch(false)}
          onSave={(body) => {
            save({ body })
            setEditingPitch(false)
          }}
        />
      </SidePanel>
    </div>
  )
}
