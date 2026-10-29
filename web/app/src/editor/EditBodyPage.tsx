import { useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { type BodyConflict, saveBody } from "@/api/mutations"
import { type BodySubject, type BodyTarget, HttpError, type ReviewThread, browser, useBodySubject, useThreads } from "@/api/queries"
import type { components } from "@/api/schema"
import { Button } from "@/components/ui/button"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { UnsavedChangesDialog } from "@/components/ui/unsaved-changes-dialog"
import { useLeaveGuard } from "@/hooks/use-leave-guard"
import { NotFound } from "@/pages/NotFound"
import { cn } from "cn"
import { type Blocks, BodyEditor } from "./BodyEditor"
import { BodyView } from "./BodyView"
import { CommentPanel } from "./CommentPanel"
import { LockedDialog } from "./LockedDialog"
import { sha256Hex } from "./bytes"
import { type BodyComments, type SelectionAnchor, type ThreadRange } from "./threads"
import { useBodyLock } from "./use-body-lock"

type EditorBlock = components["schemas"]["EditorBlock"]

/**
 * The edit page of a document, rule or task body (WL-REQ-1868). It loads
 * the body as blocks, edits it under the body lock, and saves on Save or
 * Ctrl/Cmd+S against the body_sha256 it loaded. `viewHref` is the
 * subject's cockpit page, where Close goes.
 */
export function EditBodyPage({ target, viewHref }: { target: BodyTarget; viewHref: string }) {
  const subject = useBodySubject(target)
  if (subject.error instanceof HttpError && subject.error.status === 404) return <NotFound />
  return (
    <main className="flex flex-col gap-6 p-5 pt-8 text-text-primary lg:p-page">
      {subject.isPending ? (
        <LoadingBlock label="Loading" className="h-80" />
      ) : subject.isError ? (
        <LoadError message="Couldn't load it." onRetry={() => void subject.refetch()} />
      ) : (
        <BodyEditPage
          key={subject.data.lockId}
          target={target}
          loaded={subject.data}
          viewHref={viewHref}
          reload={async () => (await subject.refetch()).data}
        />
      )}
    </main>
  )
}

function BodyEditPage({
  target,
  loaded,
  viewHref,
  reload,
}: {
  target: BodyTarget
  loaded: BodySubject
  viewHref: string
  reload: () => Promise<BodySubject | undefined>
}) {
  // The blocks the editor was mounted with and the hash the next save is checked against
  const [base, setBase] = useState({ blocks: loaded.blocks, sha: loaded.body_sha256, key: 0 })
  const current = useRef<EditorBlock[]>(loaded.blocks)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [conflict, setConflict] = useState<BodyConflict | null>(null)
  const [dialog, setDialog] = useState(true)
  const [leaveError, setLeaveError] = useState<string>()
  // A spec's text changes through its rules (WL-REQ-1300); the server refuses its blocks
  const spec = loaded.docKind === "spec"
  const lock = useBodyLock(target.kind, loaded.lockId, { dirty })
  const guard = useLeaveGuard({ dirty, onClose: () => browser.assign(viewHref) })
  const editable = lock.status === "held" && !spec
  // Threads on a document or rule body; a task body has none (WL-REQ-1317)
  const threads = useThreads(loaded.threads)
  const [pending, setPending] = useState<SelectionAnchor | null>(null)
  const highlights = useMemo(() => openRanges(threads.data ?? [], loaded), [threads.data, loaded])
  // Anchors count into the body as loaded: a reloaded body has its own blocks
  const comments: BodyComments | undefined =
    loaded.threads && base.blocks === loaded.blocks ? { source: loaded.blocks, body: loaded.body, highlights, onSelect: setPending } : undefined

  async function save(against: string): Promise<boolean> {
    if (lock.status !== "held" || saving) return false
    setSaving(true)
    try {
      const result = await saveBody(target, loaded, { blocks: current.current, if_body_sha256: against, lock_session: lock.session })
      if (result.ok) {
        const sha = await sha256Hex(result.body)
        setBase((b) => ({ ...b, sha }))
        setDirty(false)
        setConflict(null)
        return true
      }
      if ("conflict" in result) setConflict(result.conflict)
      else {
        setConflict(null)
        lock.lose({ reason: "held", by: "", lock: result.lock })
      }
    } catch (e) {
      toast.error(`Couldn't save: ${(e as Error).message}`)
    } finally {
      setSaving(false)
    }
    return false
  }

  async function takeTheirs() {
    const fresh = await reload()
    if (!fresh) return
    current.current = fresh.blocks
    setBase((b) => ({ blocks: fresh.blocks, sha: fresh.body_sha256, key: b.key + 1 }))
    setDirty(false)
    setConflict(null)
  }

  return (
    <div
      className="flex flex-col gap-6"
      onKeyDown={(e) => {
        if (e.key === "s" && (e.metaKey || e.ctrlKey)) {
          e.preventDefault()
          void save(base.sha)
        }
      }}
    >
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-2xl md:text-3xl">{loaded.title}</h1>
        <div className="flex items-center gap-4">
          <Button type="button" disabled={!editable || !dirty || saving} onClick={() => void save(base.sha)}>
            {saving ? "Saving…" : "Save"}
          </Button>
          <Button type="button" variant="outline" onClick={guard.requestClose}>
            Close
          </Button>
        </div>
      </div>
      {spec && <p className="text-sm">A spec's text changes through its rules, so this is read-only.</p>}
      {lock.status === "locked" && !dialog && <p role="status">Read only: someone else is editing this.</p>}
      {lock.status === "lost" && (
        <div role="status" className="flex flex-wrap items-center gap-4">
          <p>You no longer hold the lock, so this is read-only. Your changes are still in the editor.</p>
          <Button type="button" variant="outline" onClick={lock.retry}>
            Take the lock again
          </Button>
        </div>
      )}
      {lock.status === "failed" && (
        <div role="status" className="flex flex-wrap items-center gap-4">
          <p>Couldn't take the lock, so this is read-only.</p>
          <Button type="button" variant="outline" onClick={lock.retry}>
            Retry
          </Button>
        </div>
      )}
      <div className={loaded.threads ? "grid gap-6 lg:grid-cols-4" : ""}>
      <div className={cn(conflict && "grid gap-6 lg:grid-cols-2", loaded.threads && "lg:col-span-3")}>
        <BodyEditor
          key={base.key}
          blocks={base.blocks as Blocks}
          readOnly={!editable}
          comments={comments}
          onChange={(blocks) => {
            current.current = blocks as unknown as EditorBlock[]
            setDirty(true)
            if (lock.status === "held") lock.touch()
          }}
        />
        {conflict && (
          <section aria-label="Newer stored text" className="flex flex-col gap-4">
            <p className="text-sm">The stored text changed since you opened it. This is the newer text.</p>
            <div className="flex items-center gap-4">
              <Button type="button" disabled={saving} onClick={() => void save(conflict.body_sha256 ?? "")}>
                Keep mine
              </Button>
              <Button type="button" variant="outline" disabled={saving} onClick={() => void takeTheirs()}>
                Take theirs
              </Button>
            </div>
            <BodyView key={conflict.body_sha256} blocks={(conflict.blocks ?? []) as Blocks} />
          </section>
        )}
      </div>
      {loaded.threads && (
        <CommentPanel subject={loaded.threads} version={loaded.version} threads={threads.data ?? []} pending={pending} onOpened={() => setPending(null)} />
      )}
      </div>
      {lock.status === "locked" && dialog && (
        <LockedDialog
          open
          kind={target.kind}
          id={loaded.lockId}
          lock={lock.lock}
          onClose={() => setDialog(false)}
          onReadOnly={() => setDialog(false)}
          onGoToDocument={() => {
            setDialog(true)
            lock.retry()
          }}
        />
      )}
      <UnsavedChangesDialog
        open={guard.held}
        saving={saving}
        error={leaveError}
        onSave={() =>
          void save(base.sha).then((ok) => {
            if (ok) guard.proceed()
            else setLeaveError("Couldn't save. Your changes are still in the editor.")
          })
        }
        onDiscard={guard.proceed}
        onKeepEditing={() => {
          setLeaveError(undefined)
          guard.stay()
        }}
      />
    </div>
  )
}

/**
 * The byte ranges of the open threads anchored to this subject's loaded
 * version. A spec's threads on its arranged rules carry rule bytes, which do
 * not map onto the rendered body, so they are listed without a highlight.
 */
function openRanges(threads: ReviewThread[], loaded: BodySubject): ThreadRange[] {
  const subject = loaded.threads
  return threads.flatMap((t) => {
    const a = t.anchor
    const mine = subject?.kind === "doc" ? a?.doc === subject.id && !a?.rule : a?.rule === subject?.id
    if (t.status !== "open" || !mine || a?.version !== loaded.version || a.byte_end == null || a.byte_start == null) return []
    return [{ id: t.id ?? 0, start: a.byte_start, end: a.byte_end }]
  })
}
