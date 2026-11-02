import { useState } from "react"
import type { LockKind } from "@/api/mutations"
import { useCrew } from "@/api/queries"
import { Button } from "@/components/ui/button"
import { LockedDialog } from "@/editor/LockedDialog"
import { type BodyLockState, LOST_REASON } from "@/editor/use-body-lock"

/** What a form's richtext field says about its lock (WL-REQ-1892); only that field is held back */
export function BodyLockNotes({ kind, id, lock }: { kind: LockKind; id: string; lock: BodyLockState }) {
  const [dialog, setDialog] = useState(true)
  const crew = useCrew()
  if (lock.status === "locked") {
    const holder = lock.lock.holder ?? ""
    const name = crew.data?.find((member) => member.actor === holder)?.display_name || holder
    return dialog ? (
      <LockedDialog
        open
        kind={kind}
        id={id}
        lock={lock.lock}
        onClose={() => setDialog(false)}
        onReadOnly={() => setDialog(false)}
        onGoToDocument={() => {
          setDialog(true)
          lock.retry()
        }}
      />
    ) : (
      <p role="status" className="text-sm">
        Read only: {name} is editing this text.
      </p>
    )
  }
  if (lock.status === "lost") {
    return (
      <div role="status" className="flex flex-wrap items-center gap-4 text-sm">
        <p>
          You no longer hold the lock ({LOST_REASON[lock.lost.reason ?? ""] ?? "it was lost"}), so this text is read-only. Your changes are kept in this browser.
        </p>
        <Button type="button" variant="outline" onClick={lock.retry}>
          Take the lock again
        </Button>
      </div>
    )
  }
  if (lock.status === "failed") {
    return (
      <div role="status" className="flex flex-wrap items-center gap-4 text-sm">
        <p>Couldn't take the lock, so this text is read-only.</p>
        <Button type="button" variant="outline" onClick={lock.retry}>
          Retry
        </Button>
      </div>
    )
  }
  return null
}
