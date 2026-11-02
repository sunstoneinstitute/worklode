import { type KeyboardEvent, useState } from "react"
import { type Blocks, BodyEditor, type BodyEditorInstance } from "@/editor/BodyEditor"
import { BodyToolbar } from "@/editor/BodyToolbar"
import type { WidgetProps } from "./types"

/** Enter in the editor's own fields (the heading anchor) must not submit the form around it */
function holdImplicitSubmit(event: KeyboardEvent<HTMLDivElement>) {
  const { target } = event
  if (event.key === "Enter" && target instanceof HTMLInputElement && target.form?.contains(event.currentTarget)) event.preventDefault()
}

/** The body editor as a form field (WL-REQ-1892). Its value is the block list, read when it mounts. */
export function RichTextWidget({ value, onChange, readOnly = false }: WidgetProps) {
  const [editor, setEditor] = useState<BodyEditorInstance | null>(null)
  return (
    <div className="flex flex-col gap-3" onKeyDownCapture={holdImplicitSubmit}>
      <BodyToolbar editor={editor} disabled={readOnly} />
      <BodyEditor blocks={(value ?? []) as Blocks} readOnly={readOnly} onEditor={setEditor} onChange={onChange} />
    </div>
  )
}
