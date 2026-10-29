import { useEffect, useRef } from "react"
import type { BlockNoteEditor } from "@blocknote/core"
import { Plugin, PluginKey } from "prosemirror-state"
import { Decoration, DecorationSet } from "prosemirror-view"
import { type Block, bytesToSelection, selectionToBytes } from "./bytes"

// Thread highlights and thread anchors in the body editor (WL-REQ-1317). A
// highlight is a ProseMirror decoration, never a mark, so it never enters the
// blocks or the saved Markdown. Both directions go through bytes.ts against the
// blocks as loaded, which carry the byte offsets of the stored Markdown.

type Editor = BlockNoteEditor<any, any, any>
type Doc = Editor["prosemirrorState"]["doc"]

/** A thread's byte range in the loaded version's stored Markdown */
export type ThreadRange = { id: number; start: number; end: number }
/** A selection as a thread anchor: its byte range and the quoted bytes */
export type SelectionAnchor = { start: number; end: number; quote: string }

/** What a body needs to show threads and start them */
export type BodyComments = {
  /** The blocks as loaded from the server, with their byte offsets */
  source: Block[]
  /** The stored Markdown the offsets count into */
  body: string
  highlights: ThreadRange[]
  /** A selection mapped to an anchor, or null for none or one that does not map */
  onSelect?: (anchor: SelectionAnchor | null) => void
}

const highlightKey = new PluginKey<DecorationSet>("threadHighlights")

/** Holds the highlights; a transaction with highlightKey meta replaces them, any other maps them through its edits */
export const threadHighlights = new Plugin<DecorationSet>({
  key: highlightKey,
  state: {
    init: () => DecorationSet.empty,
    apply: (tr, set) => tr.getMeta(highlightKey) ?? set.map(tr.mapping, tr.doc),
  },
  props: { decorations: (state) => highlightKey.getState(state) },
})

// Text nodes in document order with their character start, the order bytes.ts counts in
const texts = (doc: Doc) => {
  const out: { pos: number; char: number; len: number }[] = []
  let char = 0
  doc.descendants((node, pos) => {
    if (!node.isText) return
    out.push({ pos, char, len: node.text?.length ?? 0 })
    char += node.text?.length ?? 0
  })
  return out
}

/** Characters before a ProseMirror position */
export const posToChar = (doc: Doc, pos: number) =>
  texts(doc).reduce((n, t) => n + (pos >= t.pos + t.len ? t.len : Math.max(0, pos - t.pos)), 0)

/** The position of a character offset: a start goes to the next text node, an end to the previous one */
export const charToPos = (doc: Doc, char: number, isEnd: boolean) => {
  const t = texts(doc).find((t) => (isEnd ? t.char < char && char <= t.char + t.len : t.char <= char && char < t.char + t.len))
  return t ? t.pos + char - t.char : null
}

const utf8 = new TextEncoder()
const fromUtf8 = new TextDecoder()

/**
 * Wires `comments` into an editor made by useBodyEditor: paints the
 * highlights and returns the onSelectionChange handler for BlockNoteView.
 * Once the text is edited a selection no longer maps to the loaded bytes, so
 * it reports null.
 */
export function useThreadMarks(editor: Editor, comments: BodyComments | undefined) {
  const loaded = useRef<Doc>(undefined)
  const highlights = comments?.highlights
  const source = comments?.source
  useEffect(() => {
    if (!highlights || !source) return
    const paint = () => {
      const view = editor.prosemirrorView
      const doc = view.state.doc
      loaded.current ??= doc
      const decorations = highlights.flatMap((h) => {
        const chars = bytesToSelection(source, { start: h.start, end: h.end })
        const from = chars && charToPos(doc, chars.from, false)
        const to = chars && charToPos(doc, chars.to, true)
        if (from == null || to == null || from >= to) return []
        return [Decoration.inline(from, to, { class: "bn-thread-mark", "data-thread-id": String(h.id) })]
      })
      view.dispatch(view.state.tr.setMeta(highlightKey, DecorationSet.create(doc, decorations)))
    }
    if (editor.domElement) paint()
    else return editor.onMount(paint)
  }, [editor, highlights, source])

  if (!comments?.onSelect) return undefined
  const { onSelect } = comments
  return () => {
    const { doc, selection } = editor.prosemirrorState
    loaded.current ??= doc
    if (selection.empty || !doc.eq(loaded.current)) return onSelect(null)
    const bytes = selectionToBytes(comments.source, { from: posToChar(doc, selection.from), to: posToChar(doc, selection.to) })
    if (!bytes || bytes.start >= bytes.end) return onSelect(null)
    onSelect({ ...bytes, quote: fromUtf8.decode(utf8.encode(comments.body).slice(bytes.start, bytes.end)) })
  }
}
