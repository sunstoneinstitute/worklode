import { format } from "date-fns"
import { Dialog as DialogPrimitive } from "radix-ui"
import { Button } from "@/components/ui/button"
import { Dialog, DialogDescription, DialogOverlay, DialogPortal, DialogTitle } from "@/components/ui/dialog"
import { type Blocks } from "@/editor/BodyEditor"
import { BodyView } from "@/editor/BodyView"
import { displayText, fieldItems, fillUrl } from "./rules"
import type { FieldItem, FormConfig, FormValues } from "./types"
import { RowsWidget } from "./widgets/rows"

export function draftTime(savedAt: string) {
  const date = new Date(savedAt)
  return Number.isNaN(date.getTime()) ? "earlier" : format(date, "EEE HH:mm")
}

/** An older draft read-only beside the current values, field by field, so they can be copied over (WL-REQ-1891) */
export function OlderDraftDialog({
  open,
  onOpenChange,
  config,
  older,
  current,
  savedAt,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  config: FormConfig
  older: FormValues
  current: FormValues
  savedAt: string
}) {
  const differing = fieldItems(config.layout).filter(({ field }) => JSON.stringify(older[field]) !== JSON.stringify(current[field]))
  const cell = (item: FieldItem, value: unknown) => {
    const schema = config.schema.properties[item.field]
    if (item.widget === "richtext") return <BodyView blocks={(value ?? []) as Blocks} />
    if (item.widget === "rows" && schema) {
      return (
        <RowsWidget id={`older-${item.field}`} label={item.field} value={value} onChange={() => {}} schema={schema} item={item} invalid={false} required={false} readOnly fill={(url) => fillUrl(url, current)} />
      )
    }
    return displayText(schema, value) || "(empty)"
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogPortal>
        <DialogOverlay className="bg-overlay-bg supports-backdrop-filter:backdrop-blur-xs" />
        <DialogPrimitive.Content className="fixed top-1/2 left-1/2 z-50 flex max-h-9/10 w-full max-w-3xl -translate-x-1/2 -translate-y-1/2 flex-col gap-5 overflow-y-auto rounded-panel bg-background p-panel text-text-primary">
          <DialogTitle className="text-lg font-medium">Your draft from {draftTime(savedAt)}</DialogTitle>
          <DialogDescription className="text-sm">Read only. Copy what you still want into the form.</DialogDescription>
          {differing.map((item) => (
            <section key={item.field} className="flex flex-col gap-2 text-sm">
              <h3 className="font-semibold">{config.schema.properties[item.field]?.title ?? item.field}</h3>
              <div className="grid gap-4 md:grid-cols-2">
                <div>
                  <p className="text-xs text-muted-foreground">Your draft</p>
                  {cell(item, older[item.field])}
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Current</p>
                  {cell(item, current[item.field])}
                </div>
              </div>
            </section>
          ))}
          <DialogPrimitive.Close asChild>
            <Button type="button" variant="outline" className="self-start">
              Close
            </Button>
          </DialogPrimitive.Close>
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  )
}
