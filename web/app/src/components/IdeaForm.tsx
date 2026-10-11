import { type FormEvent, type ReactNode, useId, useState } from "react"
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  FORM_INPUT,
  FORM_INPUT_GROUP,
  FORM_INPUT_GROUP_INPUT,
  FORM_SELECT_TRIGGER,
  FORM_TEXTAREA,
} from "@/components/ui/form-fields"
import { Textarea } from "@/components/ui/textarea"
import { AddIcon, CloseIcon } from "@/components/icons"
import { IDEA_TYPE_OPTIONS, STORY_TYPE_OPTIONS } from "@/lib/content-types"
import { IDEA_STATUS_OPTIONS, type Idea, newIdeaId } from "@/lib/ideas"

// Who "submits" a new idea until there's a signed-in user
const CURRENT_USER_ID = "u1"

function todayIso() {
  return new Date().toISOString().slice(0, 10)
}

/** Guidance for a Story Pitch, from the wireframe. */
function PitchGuidance() {
  return (
    // Surface-colored on the background-colored form, like ConfirmDialog's
    // preview card
    <aside className="flex flex-col gap-4 rounded-card bg-surface p-panel text-sm">
      <h2 className="text-base font-medium">What should my pitch contain?</h2>
      <p>Your pitch must answer these questions:</p>
      <ol className="list-decimal space-y-1 pl-5">
        <li>What is the core question that this story wants to address/answer?</li>
        <li>Why is this story important for Sunstone? How is it aligned with the Sunstone 30?</li>
        <li>Do you know about data we can use to find answers or help tell the story?</li>
      </ol>
      <p>BONUS:</p>
      <ol className="list-decimal pl-5">
        <li>How do you want to tell this story: Which sources, visuals, formats do you envision?</li>
      </ol>
      <Accordion type="single" collapsible className="border-t border-divider-line/60">
        <AccordionItem value="ai-use" className="border-none">
          <AccordionTrigger>A note about AI use</AccordionTrigger>
          {/* Placeholder — the real note hasn't been written yet */}
          <AccordionContent>
            You're welcome to use AI tools to help shape your pitch. Tell us where you did,
            and make sure the core question and reasoning are your own.
          </AccordionContent>
        </AccordionItem>
      </Accordion>
    </aside>
  )
}

/**
 * The New Idea form, used both as a full page (/ideas/new) and inside the
 * slide-in panel from the Ideas table. Fields follow the chosen idea type:
 * - General Idea (the default): Title, body, type.
 * - Story Pitch: adds Story Type (Data Story / Short Story), Status, an
 *   optional Litmus Test Result ID, and the "What should my pitch
 *   contain?" guidance.
 *
 * Wide (by the form's own width — a container query, from @3xl / 48rem):
 * two columns on a 12-column grid, split 8/4 — title/body/submit left,
 * type and its fields right.
 * Narrower: one column — title, type and its fields, body, guidance, submit.
 */
type IdeaFormProps = {
  onSubmit: (idea: Idea) => void
  onClose: () => void
  /** The panel passes its own accessible dialog title here */
  renderHeading?: (text: string) => ReactNode
}

const defaultHeading = (text: string) => <h1 className="text-2xl md:text-3xl">{text}</h1>

/** The heading + close X row shared by the type chooser and the form */
function FormHeader({
  heading = "New Idea",
  onClose,
  renderHeading,
}: Pick<IdeaFormProps, "onClose" | "renderHeading"> & { heading?: string }) {
  return (
    <div className="flex items-start justify-between gap-4">
      {(renderHeading ?? defaultHeading)(heading)}
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="flex cursor-pointer items-center transition-colors hover:text-highlight"
      >
        <CloseIcon size={28} />
      </button>
    </div>
  )
}

/**
 * New Idea, type first: a card per idea type (like the Add Document
 * wireframe), then the form with that type already chosen. The form's own
 * type picker still lets you switch.
 */
export function NewIdeaFlow(props: IdeaFormProps) {
  const [type, setType] = useState<string | null>(null)

  if (type) return <IdeaForm {...props} initialType={type} />

  return (
    <div className="flex flex-col gap-8 text-text-primary">
      <FormHeader onClose={props.onClose} renderHeading={props.renderHeading} />
      <div role="group" aria-label="Choose idea type" className="grid gap-5 sm:grid-cols-3">
        {IDEA_TYPE_OPTIONS.map((option) => (
          <button
            key={option.value}
            type="button"
            onClick={() => setType(option.value)}
            className="flex cursor-pointer items-center justify-between gap-4 rounded-card bg-surface px-panel py-8 text-left text-xl transition-colors hover:bg-primary hover:text-on-primary dark:hover:bg-highlight dark:hover:text-text-dark"
          >
            {option.label}
            <AddIcon size={28} />
          </button>
        ))}
      </div>
    </div>
  )
}

export function IdeaForm({
  onSubmit,
  onClose,
  renderHeading = defaultHeading,
  initialType = "general",
  idea,
}: IdeaFormProps & {
  /** The type the form opens on (the chooser in NewIdeaFlow sets it) */
  initialType?: string
  /**
   * An existing idea to edit: the form opens filled in with it, headed
   * "Edit Idea" with a Save button, and submits it back with the same id,
   * author and submitted date.
   */
  idea?: Idea
}) {
  const id = useId()
  const [title, setTitle] = useState(idea?.title ?? "")
  const [body, setBody] = useState(idea?.body ?? "")
  const [type, setType] = useState(idea?.type ?? initialType)
  const [storyType, setStoryType] = useState(idea?.storyType ?? "data-story")
  const [status, setStatus] = useState(idea?.status ?? "draft")
  const [litmusTestId, setLitmusTestId] = useState(idea?.litmusTestId ?? "")
  const [showErrors, setShowErrors] = useState(false)

  const isPitch = type === "story-pitch"
  const titleMissing = title.trim() === ""

  const handleSubmit = (event: FormEvent) => {
    event.preventDefault()
    if (titleMissing) {
      setShowErrors(true)
      return
    }
    onSubmit({
      id: idea?.id ?? newIdeaId(),
      title: title.trim(),
      author: idea?.author ?? CURRENT_USER_ID,
      type,
      storyType: isPitch ? storyType : undefined,
      // Only a Story Pitch picks its status here; others start as drafts
      // (or keep their status, when editing)
      status: isPitch ? status : (idea?.status ?? "draft"),
      submitted: idea?.submitted ?? todayIso(),
      body: body.trim() || undefined,
      litmusTestId: isPitch && litmusTestId.trim() ? litmusTestId.trim() : undefined,
    })
  }

  return (
    // A container: the layout follows the form's own width (the panel caps
    // at 1024px, the full page doesn't), not the viewport's
    <form onSubmit={handleSubmit} noValidate className="@container flex flex-col gap-8 text-text-primary">
      <FormHeader
        heading={idea ? "Edit Idea" : "New Idea"}
        onClose={onClose}
        renderHeading={renderHeading}
      />

      {/* The two column wrappers are `contents` on mobile, so their
          children interleave into one column via `order` */}
      <div className="flex flex-col gap-5 @3xl:grid @3xl:grid-cols-12 @3xl:items-start @3xl:gap-10">
        <div className="contents @3xl:col-span-8 @3xl:flex @3xl:flex-col @3xl:gap-5">
          <Field data-invalid={showErrors && titleMissing ? true : undefined} className="order-1">
            <FieldLabel htmlFor={`${id}-title`}>Title</FieldLabel>
            <Input
              id={`${id}-title`}
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              placeholder="Idea title goes here"
              className={FORM_INPUT}
              aria-invalid={showErrors && titleMissing}
            />
            {showErrors && titleMissing ? <FieldError>Give your idea a title.</FieldError> : null}
          </Field>
          {/* Placeholder for the BlockNote editor */}
          <Field className="order-4">
            <FieldLabel htmlFor={`${id}-body`} className="sr-only">
              Idea
            </FieldLabel>
            <Textarea
              id={`${id}-body`}
              value={body}
              onChange={(event) => setBody(event.target.value)}
              placeholder="Describe your idea…"
              className={`min-h-80 @3xl:min-h-160 ${FORM_TEXTAREA}`}
            />
          </Field>
          {/* Same filled button as ConfirmDialog's Yes */}
          <Button
            type="submit"
            className="order-6 self-start rounded-full border-button-bg bg-button-bg px-5 text-button-text hover:border-primary hover:bg-primary dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
          >
            {idea ? "Save" : "Submit"}
          </Button>
        </div>

        <div className="contents @3xl:col-span-4 @3xl:flex @3xl:flex-col @3xl:gap-5">
          <Field className="order-2">
            <FieldLabel htmlFor={`${id}-type`}>Choose idea type:</FieldLabel>
            <Select value={type} onValueChange={setType}>
              <SelectTrigger id={`${id}-type`} className={FORM_SELECT_TRIGGER}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {IDEA_TYPE_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>

          {isPitch ? (
            <>
              <Field className="order-3">
                <FieldLabel htmlFor={`${id}-story-type`}>Story Type</FieldLabel>
                <Select value={storyType} onValueChange={setStoryType}>
                  <SelectTrigger id={`${id}-story-type`} className={FORM_SELECT_TRIGGER}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {STORY_TYPE_OPTIONS.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field className="order-3">
                <FieldLabel htmlFor={`${id}-status`}>Status</FieldLabel>
                <Select value={status} onValueChange={setStatus}>
                  <SelectTrigger id={`${id}-status`} className={FORM_SELECT_TRIGGER}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {IDEA_STATUS_OPTIONS.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field className="order-3">
                <FieldLabel htmlFor={`${id}-litmus`}>Litmus Test Result ID</FieldLabel>
                <InputGroup className={FORM_INPUT_GROUP}>
                  <InputGroupInput
                    className={FORM_INPUT_GROUP_INPUT}
                    id={`${id}-litmus`}
                    value={litmusTestId}
                    onChange={(event) => setLitmusTestId(event.target.value)}
                  />
                  {litmusTestId ? (
                    <InputGroupAddon align="inline-end">
                      <InputGroupButton
                        aria-label="Clear Litmus Test Result ID"
                        size="icon-xs"
                        onClick={() => setLitmusTestId("")}
                      >
                        <CloseIcon size={16} />
                      </InputGroupButton>
                    </InputGroupAddon>
                  ) : null}
                </InputGroup>
                <FieldDescription>
                  Optional. If you have already run a litmus test, this helps us import the
                  results instead of re-running.
                </FieldDescription>
              </Field>
              <div className="order-5 @3xl:mt-5">
                <PitchGuidance />
              </div>
            </>
          ) : null}
        </div>
      </div>
    </form>
  )
}
