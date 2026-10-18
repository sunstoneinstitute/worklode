import { useRef } from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { CloseIcon } from "@/components/icons"
import { Dialog, DialogDescription, DialogOverlay, DialogPortal, DialogTitle } from "@/components/ui/dialog"

// ConfirmDialog's look (confirm-dialog.tsx)
const BUTTON = "h-8 cursor-pointer rounded-full px-5 text-sm transition-colors disabled:cursor-default disabled:opacity-50"
const FILLED = `${BUTTON} border border-button-bg bg-button-bg text-button-text hover:border-primary hover:bg-primary dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark`
const OUTLINED = `${BUTTON} border border-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark`
// Soft red with dark text: red text on the background is under 3:1
const DISCARD = `${BUTTON} bg-destructive/20 hover:bg-destructive hover:text-text-dark`

/**
 * Asked when leaving a form with unsaved changes (WL-REQ-1812): Save &
 * leave, Discard, or Keep editing. Focus starts on Keep editing, so a stray
 * Enter never saves or discards; the X, Escape and a click on the backdrop
 * all keep editing. While saving, every button is disabled and the dialog
 * can't be dismissed.
 */
export function UnsavedChangesDialog({
  open,
  saving,
  error,
  onSave,
  onDiscard,
  onKeepEditing,
}: {
  open: boolean
  saving: boolean
  /** Why the last Save & leave failed, shown above the buttons */
  error?: string
  onSave: () => void
  onDiscard: () => void
  onKeepEditing: () => void
}) {
  const keepEditingRef = useRef<HTMLButtonElement>(null)

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !saving) onKeepEditing()
      }}
    >
      <DialogPortal>
        <DialogOverlay className="bg-overlay-bg supports-backdrop-filter:backdrop-blur-xs" />
        <DialogPrimitive.Content
          onOpenAutoFocus={(event) => {
            event.preventDefault()
            keepEditingRef.current?.focus()
          }}
          className="fixed top-1/2 left-1/2 z-50 flex min-h-89 w-[calc(100%-2.5rem)] max-w-185 -translate-x-1/2 -translate-y-1/2 flex-col items-center justify-center gap-7.5 rounded-panel bg-background px-panel py-16 text-text-primary shadow-lg shadow-primary/10 outline-none"
        >
          <DialogTitle className="text-center text-lg font-medium md:text-xl">You have unsaved changes.</DialogTitle>
          <DialogDescription className="-mt-4 text-center text-sm">
            Save them before you leave, or they will be lost.
          </DialogDescription>
          {error ? (
            <p role="alert" className="text-center text-sm text-destructive">
              {error}
            </p>
          ) : null}
          <div className="flex flex-wrap items-center justify-center gap-6">
            <button type="button" disabled={saving} onClick={onSave} className={FILLED}>
              {saving ? "Saving…" : "Save & leave"}
            </button>
            <button type="button" disabled={saving} onClick={onDiscard} className={DISCARD}>
              Discard
            </button>
            <button ref={keepEditingRef} type="button" disabled={saving} onClick={onKeepEditing} className={OUTLINED}>
              Keep editing
            </button>
          </div>
          <DialogPrimitive.Close
            aria-label="Close"
            disabled={saving}
            className="absolute top-8 right-8 flex cursor-pointer items-center transition-colors hover:text-highlight"
          >
            <CloseIcon size={24} />
          </DialogPrimitive.Close>
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  )
}
