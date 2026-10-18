import { type FormEvent, type ReactNode, useState } from "react"
import { toast } from "sonner"
import { useCreateIdea, useUpdateIdea } from "@/api/mutations"
import { HttpError, useMe } from "@/api/queries"
import type { Idea } from "@/api/types"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { FORM_INPUT, FORM_SELECT_TRIGGER, FORM_TEXTAREA } from "@/components/ui/form-fields"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { UnsavedChangesDialog } from "@/components/ui/unsaved-changes-dialog"
import { useDraft } from "@/hooks/use-draft"
import { useLeaveGuard } from "@/hooks/use-leave-guard"
import { IDEA_TYPE_OPTIONS, STORY_TYPE_OPTIONS } from "@/lib/content-types"

type Values = { title: string; type: string; body: string; story_type: string; litmus_test_id: string }
type Outcome = "saved" | "invalid" | { error: string }

const FIELD_LABELS: Record<keyof Values, string> = {
  title: "Title",
  type: "Type",
  body: "Idea",
  story_type: "Story type",
  litmus_test_id: "Litmus test result ID",
}

function startingValues(idea?: Idea): Values {
  return {
    title: idea?.title ?? "",
    type: idea?.type || "general",
    body: idea?.body ?? "",
    story_type: idea?.story_type ?? "",
    litmus_test_id: idea?.litmus_test_id ?? "",
  }
}

/**
 * The New and Edit Idea form (WL-REQ-1806). Values and field errors live in
 * React state (WL-REQ-1810); the server's 422 decides what is valid
 * (WL-REQ-1811). A new idea is private; there is no status field.
 */
export function IdeaForm({ idea, onDone, onClose }: { idea?: Idea; onDone: () => void; onClose: () => void }) {
  const actor = useMe().id ?? ""
  const [initial] = useState(() => startingValues(idea))
  const draft = useDraft({ actor, form: "idea", recordId: idea?.id, initial })
  const { values } = draft
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [leaveError, setLeaveError] = useState<string>()
  const create = useCreateIdea()
  const update = useUpdateIdea()
  const saving = create.isPending || update.isPending
  const guard = useLeaveGuard({ dirty: draft.changed, onClose })
  const pitch = values.type === "story-pitch"
  const shown = new Set<string>(["title", "type", "body", ...(pitch ? ["story_type", "litmus_test_id"] : [])])

  const set = (field: keyof Values) => (value: string) => draft.setValues((v) => ({ ...v, [field]: value }))

  async function save(): Promise<Outcome> {
    // Saves a round trip; the server checks the same
    if (!values.title.trim()) {
      setErrors({ title: "Give your idea a title." })
      return "invalid"
    }
    const body = pitch ? values : { ...values, story_type: "", litmus_test_id: "" }
    try {
      if (idea?.id) await update.mutateAsync({ id: idea.id, patch: body })
      else await create.mutateAsync(body)
    } catch (error) {
      if (error instanceof HttpError && error.status === 422 && error.fields) {
        setErrors(error.fields)
        return "invalid"
      }
      return { error: error instanceof Error ? error.message : "Couldn't save the idea" }
    }
    setErrors({})
    draft.clear()
    guard.release()
    return "saved"
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    const outcome = await save()
    if (outcome === "saved") onDone()
    else if (outcome !== "invalid") toast.error(`Couldn't save the idea: ${outcome.error}`)
  }

  async function saveAndLeave() {
    setLeaveError(undefined)
    const outcome = await save()
    if (outcome === "saved") guard.proceed()
    else if (outcome === "invalid") guard.stay()
    else setLeaveError(`Couldn't save the idea: ${outcome.error}`)
  }

  const unshown = Object.entries(errors).filter(([field]) => !shown.has(field))
  const fieldError = (field: keyof Values) =>
    errors[field] ? <FieldError id={`idea-${field}-error`}>{errors[field]}</FieldError> : null
  const described = (field: keyof Values) => (errors[field] ? `idea-${field}-error` : undefined)

  return (
    <form onSubmit={submit} noValidate className="flex max-w-185 flex-col gap-6 text-text-primary">
      <h1 className="text-2xl md:text-3xl">{idea ? "Edit idea" : "New idea"}</h1>
      <p className="text-sm">
        {idea?.status === "intake"
          ? "This idea is shared: everyone can see it in intake."
          : "Private: only you can see this idea until you share it from the Ideas list."}
      </p>
      {draft.restored ? (
        <p className="text-sm">
          Draft restored ·{" "}
          <button type="button" onClick={draft.discard} className="cursor-pointer underline underline-offset-2 hover:text-highlight">
            Discard
          </button>
        </p>
      ) : null}
      {unshown.length > 0 ? (
        <div role="alert" className="text-sm text-destructive">
          {unshown.map(([field, message]) => (
            <p key={field}>{message}</p>
          ))}
        </div>
      ) : null}

      <Labelled field="title" error={fieldError("title")}>
        <Input
          id="idea-title"
          className={FORM_INPUT}
          placeholder="Idea title goes here"
          value={values.title}
          aria-invalid={errors.title ? true : undefined}
          aria-describedby={described("title")}
          onChange={(e) => set("title")(e.target.value)}
        />
      </Labelled>
      <Labelled field="type" error={fieldError("type")}>
        <Select value={values.type} onValueChange={set("type")}>
          <SelectTrigger id="idea-type" className={FORM_SELECT_TRIGGER} aria-describedby={described("type")}>
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
      </Labelled>
      {pitch ? (
        <>
          <Labelled field="story_type" error={fieldError("story_type")}>
            <Select value={values.story_type} onValueChange={set("story_type")}>
              <SelectTrigger id="idea-story_type" className={FORM_SELECT_TRIGGER} aria-describedby={described("story_type")}>
                <SelectValue placeholder="Choose a story type" />
              </SelectTrigger>
              <SelectContent>
                {STORY_TYPE_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Labelled>
          <Labelled field="litmus_test_id" error={fieldError("litmus_test_id")}>
            <Input
              id="idea-litmus_test_id"
              className={FORM_INPUT}
              value={values.litmus_test_id}
              aria-describedby={described("litmus_test_id")}
              onChange={(e) => set("litmus_test_id")(e.target.value)}
            />
          </Labelled>
        </>
      ) : null}
      <Labelled field="body" error={fieldError("body")}>
        <Textarea
          id="idea-body"
          className={`min-h-40 ${FORM_TEXTAREA}`}
          placeholder="Describe your idea…"
          value={values.body}
          aria-describedby={described("body")}
          onChange={(e) => set("body")(e.target.value)}
        />
      </Labelled>

      <div className="flex items-center gap-4">
        <Button type="submit" disabled={saving}>
          {idea ? "Save" : "Submit"}
        </Button>
        <Button type="button" variant="outline" onClick={guard.requestClose}>
          Cancel
        </Button>
      </div>

      <UnsavedChangesDialog
        open={guard.held}
        saving={saving}
        error={leaveError}
        onSave={() => void saveAndLeave()}
        onDiscard={() => {
          draft.discard()
          guard.proceed()
        }}
        onKeepEditing={() => {
          setLeaveError(undefined)
          guard.stay()
        }}
      />
    </form>
  )
}

function Labelled({ field, error, children }: { field: keyof Values; error: ReactNode; children: ReactNode }) {
  return (
    <Field>
      <FieldLabel htmlFor={`idea-${field}`}>{FIELD_LABELS[field]}</FieldLabel>
      {children}
      {error}
    </Field>
  )
}
