import { useState } from "react"
import { type Blocks, BodyEditor, type BodyEditorInstance } from "@/editor/BodyEditor"
import { BodyToolbar } from "@/editor/BodyToolbar"
import type { WidgetProps } from "./types"

/** The body editor as a form field (WL-REQ-1892). Its value is the block list, read when it mounts. */
export function RichTextWidget({ value, onChange, readOnly = false }: WidgetProps) {
  const [editor, setEditor] = useState<BodyEditorInstance | null>(null)
  return (
    <div className="flex flex-col gap-3">
      <BodyToolbar editor={editor} disabled={readOnly} />
      <BodyEditor blocks={(value ?? []) as Blocks} readOnly={readOnly} onEditor={setEditor} onChange={onChange} />
    </div>
  )
}
