import { BlockNoteView } from "@blocknote/shadcn"
import "@blocknote/shadcn/style.css"
import { useHtmlDark } from "@/hooks/use-dark-mode"
import { type Blocks, useBodyEditor } from "./BodyEditor"
import { type BodyComments, useThreadMarks } from "./threads"

/** A document body, read-only. Reads `blocks` once: remount it (a new key) to show other blocks. */
export function BodyView({ blocks, comments }: { blocks: Blocks; comments?: BodyComments }) {
  const isDark = useHtmlDark()
  const editor = useBodyEditor(blocks)
  const onSelectionChange = useThreadMarks(editor, comments)
  return (
    <BlockNoteView
      className="bn-view"
      editor={editor}
      editable={false}
      theme={isDark ? "dark" : "light"}
      onSelectionChange={onSelectionChange}
    />
  )
}
