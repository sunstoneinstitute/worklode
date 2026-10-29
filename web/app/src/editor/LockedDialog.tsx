import { useRef, useState } from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { toast } from "sonner"
import { type BodyLock, type LockKind, unlockBody } from "@/api/mutations"
import { useCrew } from "@/api/queries"
import { CloseIcon } from "@/components/icons"
import { Dialog, DialogDescription, DialogOverlay, DialogPortal, DialogTitle } from "@/components/ui/dialog"

// ConfirmDialog's look (confirm-dialog.tsx)
const BUTTON = "h-8 cursor-pointer rounded-full px-5 text-sm transition-colors disabled:cursor-default disabled:opacity-50"
const FILLED = `${BUTTON} border border-button-bg bg-button-bg text-button-text hover:border-primary hover:bg-primary dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark`
const OUTLINED = `${BUTTON} border border-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark`
const TITLE = "text-center text-lg font-medium md:text-xl"

type Step = "locked" | "confirm" | "unlocked"

/**
 * What opening a locked body shows: who is editing it, then OK or Read
 * only, and Unlock when the server says this user may (`can_unlock`).
 * Unlock asks first, then says "Unlocked." with Go to document. Mount it
 * per use: it starts on the locked step.
 */
export function LockedDialog({
  open,
  kind,
  id,
  lock,
  onClose,
  onReadOnly,
  onGoToDocument,
}: {
  open: boolean
  kind: LockKind
  id: string
  lock: BodyLock
  onClose: () => void
  onReadOnly: () => void
  onGoToDocument: () => void
}) {
  const [step, setStep] = useState<Step>("locked")
  const [unlocking, setUnlocking] = useState(false)
  const crew = useCrew()
  const firstRef = useRef<HTMLButtonElement>(null)
  const holder = lock.holder ?? ""
  const name = crew.data?.find((member) => member.actor === holder)?.display_name || holder

  async function unlock() {
    setUnlocking(true)
    try {
      await unlockBody(kind, id)
      setStep("unlocked")
    } catch {
      toast.error("Couldn't unlock it")
    } finally {
      setUnlocking(false)
    }
  }

  // Wait for the crew, so the holder's id never flashes in place of their name
  if (crew.isPending) return null

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogPortal>
        <DialogOverlay className="bg-overlay-bg supports-backdrop-filter:backdrop-blur-xs" />
        <DialogPrimitive.Content
          onOpenAutoFocus={(event) => {
            event.preventDefault()
            firstRef.current?.focus()
          }}
          className="fixed top-1/2 left-1/2 z-50 flex min-h-89 w-[calc(100%-2.5rem)] max-w-185 -translate-x-1/2 -translate-y-1/2 flex-col items-center justify-center gap-7.5 rounded-panel bg-background px-panel py-16 text-text-primary shadow-lg shadow-primary/10 outline-none"
        >
          {step === "locked" && (
            <>
              <DialogTitle className={TITLE}>Locked: this is being edited by {name}.</DialogTitle>
              <DialogDescription className="sr-only">Choose how to continue.</DialogDescription>
              <div className="flex flex-wrap items-center justify-center gap-6">
                <button ref={firstRef} type="button" onClick={onClose} className={FILLED}>
                  OK
                </button>
                <button type="button" onClick={onReadOnly} className={OUTLINED}>
                  Read only
                </button>
                {lock.can_unlock && (
                  <button type="button" onClick={() => setStep("confirm")} className={OUTLINED}>
                    Unlock
                  </button>
                )}
              </div>
            </>
          )}
          {step === "confirm" && (
            <>
              <DialogTitle className={TITLE}>Unlock this?</DialogTitle>
              <DialogDescription className="-mt-4 text-center text-sm">
                {name}'s unsaved changes, kept only in their browser, won't be in it.
              </DialogDescription>
              <div className="flex items-center gap-6">
                <button type="button" disabled={unlocking} onClick={() => void unlock()} className={FILLED}>
                  Unlock
                </button>
                <button ref={firstRef} type="button" disabled={unlocking} onClick={() => setStep("locked")} className={OUTLINED}>
                  Cancel
                </button>
              </div>
            </>
          )}
          {step === "unlocked" && (
            <>
              <DialogTitle className={TITLE}>Unlocked.</DialogTitle>
              <DialogDescription className="sr-only">It is free to edit.</DialogDescription>
              <div className="flex items-center gap-6">
                <button ref={firstRef} type="button" onClick={onGoToDocument} className={FILLED}>
                  Go to document
                </button>
                <button type="button" onClick={onClose} className={OUTLINED}>
                  OK
                </button>
              </div>
            </>
          )}
          <DialogPrimitive.Close
            aria-label="Close"
            className="absolute top-8 right-8 flex cursor-pointer items-center transition-colors hover:text-highlight"
          >
            <CloseIcon size={24} />
          </DialogPrimitive.Close>
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  )
}
