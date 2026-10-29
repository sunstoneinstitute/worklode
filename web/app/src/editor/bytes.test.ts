import { expect, test } from "vitest"
import { bytesToSelection, selectionToBytes, type Block } from "./bytes"

test("selection inside a bold run maps to bytes after the marker", () => {
  const blocks: Block[] = [{ id: "1", type: "paragraph", props: {}, children: [], range: { start: 0, end: 9 },
    content: [{ type: "text", text: "a ", styles: {}, offset: 0 }, { type: "text", text: "bø", styles: { bold: true }, offset: 5 }] }]
  // "a **bø**": "ø" is 2 bytes; selecting "ø" (chars 3..4 of the paragraph text) is bytes 6..8
  expect(selectionToBytes(blocks, { from: 3, to: 4 })).toEqual({ start: 6, end: 8 })
  expect(bytesToSelection(blocks, { start: 6, end: 8 })).toEqual({ from: 3, to: 4 })
})

// "# Hø\n\nSee [lïnk](u) and\n\n- kid\n"
const doc: Block[] = [
  { id: "h", type: "heading", range: { start: 0, end: 5 }, content: [{ type: "text", text: "Hø", offset: 2 }] },
  { id: "p", type: "paragraph", range: { start: 7, end: 27 }, content: [
    { type: "text", text: "See ", offset: 7 },
    { type: "link", href: "u", offset: 11, content: [{ type: "text", text: "lïnk", offset: 12 }] },
    { type: "text", text: " and", offset: 21 },
  ], children: [
    { id: "c", type: "bulletListItem", range: { start: 27, end: 33 }, content: [{ type: "text", text: "kid", offset: 30 }] },
  ] },
]

test("a selection across blocks and a link maps to bytes in each run", () => {
  // chars: "Hø" 0..2, "See " 2..6, "lïnk" 6..10, " and" 10..14, "kid" 14..17
  expect(selectionToBytes(doc, { from: 1, to: 8 })).toEqual({ start: 3, end: 15 })
  expect(bytesToSelection(doc, { start: 3, end: 15 })).toEqual({ from: 1, to: 8 })
  expect(selectionToBytes(doc, { from: 15, to: 17 })).toEqual({ start: 31, end: 33 })
})

test("a block boundary maps forward for start and back for end", () => {
  expect(selectionToBytes(doc, { from: 2, to: 6 })).toEqual({ start: 7, end: 11 })
  expect(selectionToBytes(doc, { from: 0, to: 2 })).toEqual({ start: 2, end: 5 })
})

test("bytes inside Markdown markers snap to the nearest text", () => {
  // byte 11 is "[", 17..21 is "](u)": start snaps forward, end back
  expect(bytesToSelection(doc, { start: 11, end: 20 })).toEqual({ from: 6, to: 10 })
})

test("out of range maps to null", () => {
  expect(selectionToBytes(doc, { from: 0, to: 99 })).toBeNull()
  expect(bytesToSelection(doc, { start: 0, end: 99 })).toBeNull()
})
