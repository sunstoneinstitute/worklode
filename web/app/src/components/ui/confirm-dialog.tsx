import { type ReactNode, useRef } from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { CloseIcon } from "@/components/icons"
import { Dialog, DialogDescription, DialogOverlay, DialogPortal, DialogTitle } from "@/components/ui/dialog"

/**
 * An "are you sure?" alert over a translucent backdrop (Figma's
 * overlay-bg — surface at 60%, flips with the theme): the question,
 * an optional preview of what it's about (`children`), Yes / No, and a
 * close X. Only Yes confirms — No, the X, Escape and a
 * click on the backdrop all cancel. Focus starts on No, so a stray Enter
 * never confirms something destructive.
 */
export function ConfirmDialog({
  open,
  message,
  confirmLabel = "Yes",
  cancelLabel = "No",
  onConfirm,
  onOpenChange,
  destructive = false,
  children,
}: {
  open: boolean
  message: string
  confirmLabel?: string
  cancelLabel?: string
  onConfirm: () => void
  /** Called with false whenever the dialog closes, confirmed or not */
  onOpenChange: (open: boolean) => void
  /**
   * Whether confirming destroys something (a delete). Marks the dialog
   * data-destructive, which the preview picks up to tint itself red.
   */
  destructive?: boolean
  /** A small preview of the item the question is about */
  children?: ReactNode
}) {
  const cancelRef = useRef<HTMLButtonElement>(null)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogPortal>
        <DialogOverlay className="bg-overlay-bg supports-backdrop-filter:backdrop-blur-xs" />
        <DialogPrimitive.Content
          data-destructive={destructive ? "" : undefined}
          onOpenAutoFocus={(event) => {
            event.preventDefault()
            cancelRef.current?.focus()
          }}
          className="group/confirm fixed top-1/2 left-1/2 z-50 flex min-h-89 w-[calc(100%-2.5rem)] max-w-185 -translate-x-1/2 -translate-y-1/2 flex-col items-center justify-center gap-7.5 rounded-panel bg-background px-panel py-16 text-text-primary shadow-lg shadow-primary/10 outline-none data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95"
        >
          <DialogTitle className="text-center text-lg font-medium md:text-xl">{message}</DialogTitle>
          {/* The message is the whole alert; this just satisfies Radix's
              description slot for screen readers without repeating it */}
          <DialogDescription className="sr-only">Confirm or cancel.</DialogDescription>
          {children}
          <div className="flex items-center gap-6">
            <button
              type="button"
              onClick={() => {
                onConfirm()
                onOpenChange(false)
              }}
              className="h-8 cursor-pointer rounded-full border border-button-bg bg-button-bg px-5 text-sm text-button-text transition-colors hover:border-primary hover:bg-primary dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
            >
              {confirmLabel}
            </button>
            <DialogPrimitive.Close asChild>
              {/* Outlined buttons fill with their border color on hover
                  (highlight with dark text in dark mode, like Yes) */}
              <button
                ref={cancelRef}
                type="button"
                className="h-8 cursor-pointer rounded-full border border-text-primary px-5 text-sm transition-colors hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
              >
                {cancelLabel}
              </button>
            </DialogPrimitive.Close>
          </div>
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
