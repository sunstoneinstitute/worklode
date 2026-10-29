import { BlockNoteView } from "@blocknote/shadcn"
import "@blocknote/shadcn/style.css"
import { useHtmlDark } from "@/hooks/use-dark-mode"
import { type Blocks, useBodyEditor } from "./BodyEditor"

/** A document body, read-only. Reads `blocks` once: remount it (a new key) to show other blocks. */
export function BodyView({ blocks }: { blocks: Blocks }) {
  const isDark = useHtmlDark()
  const editor = useBodyEditor(blocks)
  return <BlockNoteView className="bn-view" editor={editor} editable={false} theme={isDark ? "dark" : "light"} />
}
