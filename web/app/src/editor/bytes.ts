import type { components } from "@/api/schema"

// Maps an editor selection to a byte span in the stored Markdown and back
// (WL-RULE-1870). A selection counts characters (UTF-16 units, as ProseMirror
// does) through the text of every block in document order, children after
// their parent, with nothing between blocks. Bytes come from the run `offset`
// fields and the UTF-8 length of the run text.

export type Block = components["schemas"]["EditorBlock"]
type Run = components["schemas"]["InlineContent"]
type TableCell = Run[] | { content?: Run[] }
type TableContent = { rows?: { cells?: TableCell[] }[] }

export type Selection = { from: number; to: number }
export type Bytes = { start: number; end: number }

type Span = { text: string; char: number; byte: number }

const utf8 = new TextEncoder()
const byteLength = (s: string) => utf8.encode(s).length

// Text runs in document order, each with its character and byte start.
const spans = (blocks: Block[]): Span[] => {
  const out: Span[] = []
  let char = 0
  const addRuns = (runs: Run[]) => {
    for (const run of runs) {
      if (run.type === "link") addRuns(run.content ?? [])
      else if (run.text) {
        out.push({ text: run.text, char, byte: run.offset ?? 0 })
        char += run.text.length
      }
    }
  }
  const walk = (list: Block[]) => {
    for (const block of list) {
      const content = block.content as Run[] | TableContent | undefined
      if (Array.isArray(content)) addRuns(content)
      else for (const row of content?.rows ?? []) for (const cell of row.cells ?? []) addRuns(Array.isArray(cell) ? cell : (cell.content ?? []))
      walk(block.children ?? [])
    }
  }
  walk(blocks)
  return out
}

// A position on a run boundary belongs to the next run for a start, to the
// previous run for an end.
const charToByte = (all: Span[], pos: number, isEnd: boolean) => {
  const span = all.find((s) => {
    const end = s.char + s.text.length
    return isEnd ? s.char < pos && pos <= end : s.char <= pos && pos < end
  })
  return span ? span.byte + byteLength(span.text.slice(0, pos - span.char)) : null
}

// A byte inside Markdown syntax snaps forward to the next run for a start,
// back to the previous run's end for an end.
const byteToChar = (all: Span[], byte: number, isEnd: boolean) => {
  const last = all.at(-1)
  if (!last || byte < all[0].byte || byte > last.byte + byteLength(last.text)) return null
  const ordered = isEnd ? [...all].reverse() : all
  const span = ordered.find((s) => (isEnd ? s.byte < byte : s.byte + byteLength(s.text) > byte))
  if (!span) return null
  let char = 0
  let at = span.byte
  for (const ch of span.text) {
    if (at >= byte) break
    at += byteLength(ch)
    char += ch.length
  }
  return span.char + char
}

export const selectionToBytes = (blocks: Block[], sel: Selection): Bytes | null => {
  const all = spans(blocks)
  const start = charToByte(all, sel.from, false)
  const end = sel.to === sel.from ? start : charToByte(all, sel.to, true)
  return start === null || end === null ? null : { start, end }
}

export const bytesToSelection = (blocks: Block[], bytes: Bytes): Selection | null => {
  const all = spans(blocks)
  const from = byteToChar(all, bytes.start, false)
  const to = bytes.end === bytes.start ? from : byteToChar(all, bytes.end, true)
  return from === null || to === null ? null : { from, to }
}

/** Hex SHA-256 of a body's UTF-8 bytes: the server's body_sha256 */
export async function sha256Hex(body: string): Promise<string> {
  const sum = await crypto.subtle.digest("SHA-256", utf8.encode(body))
  return Array.from(new Uint8Array(sum), (b) => b.toString(16).padStart(2, "0")).join("")
}
