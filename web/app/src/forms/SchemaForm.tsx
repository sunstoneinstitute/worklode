import { type FormEvent, type ReactNode, type Ref, useEffect, useImperativeHandle, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { type BodyConflict, type LockKind, submitForm } from "@/api/mutations"
import { HttpError, useMe } from "@/api/queries"
import { Button } from "@/components/ui/button"
import { UnsavedChangesDialog } from "@/components/ui/unsaved-changes-dialog"
import { ConflictPane } from "@/editor/ConflictPane"
import { useBodyLock } from "@/editor/use-body-lock"
import { useDraft } from "@/hooks/use-draft"
import { useLeaveGuard } from "@/hooks/use-leave-guard"
import { cn } from "cn"
import { NoteBlockView } from "./blocks/note"
import { BodyLockNotes } from "./BodyLockNotes"
import { FormField } from "./FormField"
import { layoutFor } from "./layouts"
import { OlderDraftDialog, draftTime } from "./OlderDraftDialog"
import { cellErrorsFor, fieldItems, fillUrl, isBlock, requiredFields, startingValues, submittedValues, validate, valuesDiffer, visibleColumns, visibleFields } from "./rules"
import type { FieldItem, FormConfig, FormValues, NoteBlock } from "./types"

export type SchemaFormHandle = { requestClose: () => void }

type Outcome = "saved" | "stay" | { error: string }

const LINK = "cursor-pointer underline underline-offset-2 hover:text-highlight"

/**
 * An edit or create page built from a form config (WL-REQ-1886). The page
 * reads `record` from its own GET route; with none the form creates. A form
 * with a `richtext` field names its subject's `lockKind`, and edits that field
 * under the body lock (WL-REQ-1892).
 */
export function SchemaForm({
  config,
  record,
  lockKind,
  title,
  intro,
  heading = (text) => <h1 className="text-2xl md:text-3xl">{text}</h1>,
  noun = "it",
  invalidates,
  onDone,
  onClose,
  ref,
}: {
  config: FormConfig
  record?: FormValues
  lockKind?: LockKind
  /** In place of the config's title */
  title?: string
  /** Shown under the heading */
  intro?: ReactNode
  /** Renders the title; a side panel passes its own accessible title */
  heading?: (text: string) => ReactNode
  /** Names the record in a save failure: "the idea" */
  noun?: string
  /** The query refetched after a save */
  invalidates?: readonly unknown[]
  onDone: () => void
  onClose: () => void
  ref?: Ref<SchemaFormHandle>
}) {
  const mode = record ? config.edit : config.create
  if (!mode) throw new Error(`form ${config.id} has no ${record ? "edit" : "create"} mode`)
  const { layout, schema } = config
  const queryClient = useQueryClient()
  const actor = useMe().id ?? ""
  const recordId = record ? String(record.id) : undefined
  const locked = (record?.field_access ?? {}) as Record<string, string>
  const [initial, setInitial] = useState(() => startingValues(config, record))
  const allLocked = record !== undefined && visibleFields(layout, initial).every((field) => field in locked)
  const draft = useDraft({
    actor,
    form: config.id,
    recordId,
    initial,
    base: record?.updated_at as string | undefined,
    locked,
    disabled: allLocked,
  })
  const { values } = draft
  // Saved: nothing is unsaved until a field changes again, whatever `initial` still says
  const [saved, setSaved] = useState(false)
  const settled = useRef(false)
  const dirty = draft.changed && !saved
  // Set while a save is in flight: state would lag behind a quick second Ctrl+S
  const inFlight = useRef(false)

  const [showErrors, setShowErrors] = useState(false)
  const [serverErrors, setServerErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState<string>()
  const [leaveError, setLeaveError] = useState<string>()
  const [saving, setSaving] = useState(false)
  const [viewingOlder, setViewingOlder] = useState(false)
  // Part of each field's key: a widget that reads its value once remounts on a reset
  const [resets, setResets] = useState(0)
  const guard = useLeaveGuard({ dirty, onClose })
  useImperativeHandle(ref, () => ({ requestClose: guard.requestClose }))

  // The richtext field of a record, which this form edits under a lock
  const bodyItem = record ? fieldItems(layout).find((item) => item.widget === "richtext" && !(item.field in locked)) : undefined
  const bodyField = lockKind ? bodyItem?.field : undefined
  const lock = useBodyLock(lockKind ?? "task", bodyField ? recordId : undefined, {
    dirty,
    onBeforeRelease: () => {
      if (!settled.current) draft.flush(values)
    },
  })
  const held = lock.status === "held"
  const [sha, setSha] = useState(String(record?.body_sha256 ?? ""))
  const [conflict, setConflict] = useState<BodyConflict | null>(null)
  const lost = lock.status === "lost"
  useEffect(() => {
    if (lost && dirty) draft.flush(values)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once per loss
  }, [lost])

  const body = submittedValues(layout, schema, values, initial, locked)
  // A lock this page does not hold keeps the text out of the save
  const heldBack = bodyItem !== undefined && !held && JSON.stringify(values[bodyItem.field]) !== JSON.stringify(initial[bodyItem.field])
  if (bodyItem && !held) delete body[bodyItem.field]
  const clientErrors = showErrors ? validate(schema, body) : {}
  const errors = { ...clientErrors, ...serverErrors }
  const required = requiredFields(schema, values)
  const Layout = layoutFor(layout.type)
  const fill = (url: string) => fillUrl(url, { ...values, id: recordId })

  const setField = (field: string, value: unknown) => {
    settled.current = false
    inFlight.current = false
    setSaved(false)
    draft.setValues((current) => ({ ...current, [field]: value }))
    setServerErrors((current) => Object.fromEntries(Object.entries(current).filter(([key]) => key !== field && !key.startsWith(`${field}/`))))
    if (field === bodyField && lock.status === "held") lock.touch()
  }

  function showFieldErrors(fields: Record<string, string>) {
    const onForm = new Set(visibleFields(layout, values))
    const entries = Object.entries(fields)
    setServerErrors(Object.fromEntries(entries.filter(([key]) => onForm.has(key.split("/")[0] ?? key))))
    const others = entries.filter(([key]) => !onForm.has(key.split("/")[0] ?? key)).map(([, message]) => message)
    setFormError(others.length > 0 ? others.join(" ") : undefined)
  }

  async function save(against = sha): Promise<Outcome> {
    if (inFlight.current) return "stay"
    setShowErrors(true)
    setFormError(undefined)
    if (Object.keys(validate(schema, body)).length > 0) return "stay"
    inFlight.current = true
    setSaving(true)
    try {
      const checks = held && bodyField && bodyField in body ? { if_body_sha256: against, lock_session: lock.session } : {}
      const result = await submitForm(mode!.submit.method, fill(mode!.submit.url), { ...body, ...checks })
      if (!result.ok) {
        if ("conflict" in result) setConflict(result.conflict)
        else if (held) lock.lose({ reason: "held", by: "", lock: result.lock })
        return "stay"
      }
      setServerErrors({})
      setConflict(null)
      if (!heldBack) {
        draft.clear()
        settled.current = true
        setSaved(true)
      }
      guard.release()
      if (invalidates) await queryClient.invalidateQueries({ queryKey: invalidates })
      return "saved"
    } catch (error) {
      if (error instanceof HttpError && (error.status === 422 || error.status === 403) && error.fields) {
        showFieldErrors(error.fields)
        return "stay"
      }
      return { error: error instanceof Error ? error.message : "unknown error" }
    } finally {
      if (!settled.current) inFlight.current = false
      setSaving(false)
    }
  }

  async function submit(against?: string) {
    const outcome = await save(against)
    if (outcome === "saved") onDone()
    else if (outcome !== "stay") toast.error(`Couldn't save ${noun}: ${outcome.error}`)
  }

  async function saveAndLeave() {
    setLeaveError(undefined)
    const outcome = await save()
    if (outcome === "saved") guard.proceed()
    else if (outcome === "stay") guard.stay()
    else setLeaveError(`Couldn't save ${noun}: ${outcome.error}`)
  }

  function discardDraft() {
    draft.discard()
    setResets((count) => count + 1)
    setShowErrors(false)
    setServerErrors({})
    setFormError(undefined)
  }

  function keepMine() {
    const next = conflict?.body_sha256 ?? ""
    setSha(next)
    void submit(next)
  }

  function takeTheirs() {
    if (!conflict || !bodyField) return
    // Their text is the new baseline; whatever else changed stays unsaved
    const theirs = { [bodyField]: conflict.blocks ?? [] }
    const start = { ...initial, ...theirs }
    const next = { ...values, ...theirs }
    setSha(conflict.body_sha256 ?? "")
    setInitial(start)
    draft.setValues(next)
    if (!valuesDiffer(next, start)) draft.clear()
    setConflict(null)
    setResets((count) => count + 1)
  }

  const renderItem = (item: FieldItem | NoteBlock) => {
    if (isBlock(item)) return <NoteBlockView block={item} />
    const property = schema.properties[item.field]
    if (!property) return null
    const field = (
      <FormField
        key={resets}
        item={item}
        schema={property}
        value={values[item.field]}
        onChange={(value) => setField(item.field, value)}
        error={errors[item.field]}
        cellErrors={cellErrorsFor(errors, item.field)}
        required={required.has(item.field)}
        lockedReason={locked[item.field]}
        readOnly={item.field === bodyItem?.field && !held}
        fill={fill}
      />
    )
    if (item.field !== bodyItem?.field) return field
    return (
      <div className={cn("flex flex-col gap-4", conflict && "grid gap-6 lg:grid-cols-2")}>
        {field}
        {conflict ? <ConflictPane conflict={conflict} saving={saving} onKeepMine={keepMine} onTakeTheirs={takeTheirs} /> : null}
      </div>
    )
  }

  return (
    <>
      <form
        onSubmit={(event: FormEvent) => {
          event.preventDefault()
          // A nested form bubbles here: the editor's own, or a dialog's through its portal
          if (event.target === event.currentTarget) void submit()
        }}
        onKeyDown={(event) => {
          if (event.key === "s" && (event.metaKey || event.ctrlKey) && !allLocked) {
            event.preventDefault()
            void submit()
          }
        }}
        noValidate
        className="@container flex flex-col gap-6 text-text-primary"
      >
        {heading(title ?? mode.title)}
        {intro}
        {draft.restored ? (
          <p role="status" className="text-sm">
            Draft restored ·{" "}
            {draft.notRestored.map(([field, reason]) => (
              <span key={field}>
                {schema.properties[field]?.title ?? field} was not restored: {reason} ·{" "}
              </span>
            ))}
            <button type="button" onClick={discardDraft} className={LINK}>
              Discard
            </button>
          </p>
        ) : null}
        {draft.older ? (
          <p role="status" className="text-sm">
            You have an older draft from {draftTime(draft.older.saved_at)} ·{" "}
            <button type="button" onClick={() => setViewingOlder(true)} className={LINK}>
              View
            </button>{" "}
            ·{" "}
            <button type="button" onClick={draft.discardOlder} className={LINK}>
              Discard
            </button>
          </p>
        ) : null}
        {bodyField && recordId && lockKind ? <BodyLockNotes kind={lockKind} id={recordId} lock={lock} /> : null}
        {formError ? (
          <p role="alert" className="text-sm text-destructive">
            {formError}
          </p>
        ) : null}
        {/* oxlint-disable-next-line react/static-components -- layoutFor picks from a fixed set of module-level components */}
        <Layout
          columns={visibleColumns(layout, values)}
          renderItem={renderItem}
          submit={
            allLocked ? undefined : (
              <div className="flex items-center gap-4">
                <Button type="submit" disabled={saving}>
                  {saving ? "Saving…" : mode.submit.label}
                </Button>
                <Button type="button" variant="outline" onClick={guard.requestClose}>
                  Cancel
                </Button>
              </div>
            )
          }
        />
      </form>
      {draft.older ? (
        <OlderDraftDialog open={viewingOlder} onOpenChange={setViewingOlder} config={config} older={draft.older.values} current={values} savedAt={draft.older.saved_at} />
      ) : null}
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
    </>
  )
}
