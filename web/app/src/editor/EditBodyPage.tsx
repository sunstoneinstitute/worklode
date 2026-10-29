import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { type BodyConflict, saveBody } from "@/api/mutations"
import { type BodySubject, type BodyTarget, HttpError, browser, useBodySubject, useMe } from "@/api/queries"
import type { components } from "@/api/schema"
import { Button } from "@/components/ui/button"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { UnsavedChangesDialog } from "@/components/ui/unsaved-changes-dialog"
import { useDraft } from "@/hooks/use-draft"
import { useLeaveGuard } from "@/hooks/use-leave-guard"
import { NotFound } from "@/pages/NotFound"
import { type Blocks, BodyEditor } from "./BodyEditor"
import { BodyView } from "./BodyView"
import { LockedDialog } from "./LockedDialog"
import { sha256Hex } from "./bytes"
import { useBodyLock } from "./use-body-lock"

type EditorBlock = components["schemas"]["EditorBlock"]

const LOST_REASON: Record<string, string> = {
  unlocked: "someone unlocked it",
  inactive: "it sat idle too long",
  expired: "it expired",
  held: "someone else holds it now",
}

/** The blocks of a stored draft, or null when it holds none */
function draftBlocks(raw: string): EditorBlock[] | null {
  try {
    const blocks: unknown = JSON.parse(raw)
    return Array.isArray(blocks) ? (blocks as EditorBlock[]) : null
  } catch {
    return null
  }
}

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
  // The browser draft (WL-REQ-1813): unsaved blocks and the hash they were loaded against
  const actor = useMe().id ?? ""
  const draft = useDraft({ actor, form: `body:${target.kind}`, recordId: loaded.lockId, initial: { blocks: "", sha: "" } })
  const [stored] = useState(() => (draft.restored ? draftBlocks(draft.values.blocks) : null))
  // The blocks the editor was mounted with and the hash the next save is checked against
  const [base, setBase] = useState({ blocks: stored ?? loaded.blocks, sha: stored ? draft.values.sha : loaded.body_sha256, key: 0 })
  const current = useRef<EditorBlock[]>(stored ?? loaded.blocks)
  const [dirty, setDirty] = useState(stored !== null)
  const [offer, setOffer] = useState(stored !== null)
  const [saving, setSaving] = useState(false)
  const [conflict, setConflict] = useState<BodyConflict | null>(null)
  const [dialog, setDialog] = useState(true)
  const [leaveError, setLeaveError] = useState<string>()
  // A spec's text changes through its rules (WL-REQ-1300); the server refuses its blocks
  const spec = loaded.docKind === "spec"
  const keepDraft = () => draft.flush({ blocks: JSON.stringify(current.current), sha: base.sha })
  const lock = useBodyLock(target.kind, loaded.lockId, { dirty, onBeforeRelease: keepDraft })
  const lost = lock.status === "lost"
  useEffect(() => {
    if (lost && dirty) keepDraft()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once per loss
  }, [lost])
  // The blocks held when the lock was lost, captured on the transition
  const [kept, setKept] = useState<EditorBlock[] | null>(null)
  if (lost !== (kept !== null)) setKept(lost ? current.current : null)
  const guard = useLeaveGuard({ dirty, onClose: () => browser.assign(viewHref) })
  const editable = lock.status === "held" && !spec

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
        setOffer(false)
        draft.clear()
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

  function discardDraft() {
    draft.clear()
    current.current = loaded.blocks
    setBase((b) => ({ blocks: loaded.blocks, sha: loaded.body_sha256, key: b.key + 1 }))
    setDirty(false)
    setOffer(false)
  }

  function takeAgain() {
    // The editor comes back with the blocks kept at the loss, checked against the hash it loaded
    setBase((b) => ({ ...b, blocks: current.current, key: b.key + 1 }))
    if (lock.status === "lost") lock.retry()
  }

  async function takeTheirs() {
    const fresh = await reload()
    if (!fresh) return
    current.current = fresh.blocks
    setBase((b) => ({ blocks: fresh.blocks, sha: fresh.body_sha256, key: b.key + 1 }))
    setDirty(false)
    setConflict(null)
    draft.clear()
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
          <p>
            You no longer hold the lock ({LOST_REASON[lock.lost.reason ?? ""] ?? "it was lost"}), so this is read-only. Your changes are kept in this browser.
          </p>
          <Button type="button" variant="outline" onClick={takeAgain}>
            Take the lock again
          </Button>
        </div>
      )}
      {offer && lock.status !== "lost" && (
        <div role="status" className="flex flex-wrap items-center gap-4">
          <p>Draft restored: these are your unsaved changes from earlier.</p>
          <Button type="button" variant="outline" onClick={discardDraft}>
            Discard draft
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
      <div className={conflict ? "grid gap-6 lg:grid-cols-2" : ""}>
        {lock.status === "lost" && kept ? (
          <section aria-label="Your unsaved changes">
            <BodyView key={base.key} blocks={kept as Blocks} />
          </section>
        ) : (
        <BodyEditor
          key={base.key}
          blocks={base.blocks as Blocks}
          readOnly={!editable}
          onChange={(blocks) => {
            current.current = blocks as unknown as EditorBlock[]
            setDirty(true)
            if (lock.status === "held") lock.touch()
          }}
        />
        )}
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
