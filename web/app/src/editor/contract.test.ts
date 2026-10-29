import { ServerBlockNoteEditor } from "@blocknote/server-util"
import { expect, test } from "vitest"
import pkg from "../../package.json"
import { schema } from "./schema"

// WL-REQ-1867's contract test: the blocks Go writes to
// internal/mdblocks/testdata/golden survive a trip through BlockNote itself,
// via ServerBlockNoteEditor._blocksToProsemirrorNode and
// _prosemirrorNodeToBlocks, the names @blocknote/server-util 0.56.0 exports.
//
// BlockNote 0.56 does not carry a block's `range` or a run's `offset`: its
// ProseMirror nodes have no slot for them, so they are gone on the way back.
// The test compares everything BlockNote owns (type, props, inline text,
// styles, links, table content, children) and a separate test pins the loss,
// so a BlockNote that starts carrying them fails here (gap on WL-PLAN-166).

const BLOCKNOTE_VERSION = pkg.dependencies["@blocknote/core"]
// Every golden file, keyed by its path.
const golden = import.meta.glob<{ blocks: unknown[] }>("../../../../internal/mdblocks/testdata/golden/*.blocks.json", {
  eager: true,
  import: "default",
})

type Json = null | boolean | number | string | Json[] | { [k: string]: Json }
type Specs = Record<string, { config: { propSchema: Record<string, { default?: unknown }> } }>
const specs = schema.blockSpecs as unknown as Specs
const cellDefaults: Record<string, unknown> = { backgroundColor: "default", textColor: "default", textAlignment: "left", colspan: 1, rowspan: 1 }

// Drops what BlockNote fills in on the way back: a prop at its schema default,
// empty `styles`, empty `children`, an undefined field. A bare table cell
// becomes BlockNote's `tableCell` object. Drops `id` (Go leaves it empty,
// BlockNote mints its own), and `range` and `offset`, which BlockNote does not
// carry (see the comment at the top).
function canon(v: unknown): Json {
  if (Array.isArray(v)) return v.map((x) => canon(x))
  if (!v || typeof v !== "object") return (v ?? null) as Json
  const o = v as Record<string, unknown>
  const out: Record<string, Json> = {}
  const defaults = typeof o.type === "string" ? specs[o.type]?.config.propSchema : undefined
  for (const [k, x] of Object.entries(o)) {
    if (x === undefined || ["id", "range", "offset"].includes(k)) continue
    if (["children", "styles", "content"].includes(k) && (Array.isArray(x) ? x.length === 0 : Object.keys(x as object).length === 0)) continue
    if (k === "props") {
      const props = Object.entries(x as Record<string, unknown>).filter(([pk, pv]) =>
        o.type === "tableCell" ? cellDefaults[pk] !== pv : defaults?.[pk]?.default !== pv)
      if (props.length) out.props = Object.fromEntries(props.map(([pk, pv]) => [pk, canon(pv)]))
      continue
    }
    if (k === "cells") {
      out.cells = (x as unknown[]).map((c) => canon(Array.isArray(c) ? { type: "tableCell", content: c } : c))
      continue
    }
    out[k] = k === "content" && Array.isArray(x) ? mergeRuns(x.map((r) => canon(r))) : canon(x)
  }
  return out
}

// Joins adjacent text runs with equal styles, as ProseMirror joins adjacent
// text nodes with equal marks. Go splits a code block into one run per line.
function mergeRuns(runs: Json[]): Json[] {
  const out: Json[] = []
  for (const r of runs) {
    const prev = out.at(-1) as Record<string, Json> | undefined
    const run = r as Record<string, Json>
    if (prev?.type === "text" && run?.type === "text" && JSON.stringify(prev.styles) === JSON.stringify(run.styles)) {
      out[out.length - 1] = { ...prev, text: `${prev.text}${run.text}` }
    } else out.push(r)
  }
  return out
}

test("the contract is pinned to BlockNote 0.56.0", () => {
  // A bump is a deliberate edit of this test.
  expect(BLOCKNOTE_VERSION).toBe("0.56.0")
  expect(pkg.dependencies["@blocknote/server-util"]).toBe("0.56.0")
  expect(Object.keys(golden).length).toBeGreaterThanOrEqual(11)
})

test(`BlockNote ${BLOCKNOTE_VERSION} drops range and offset`, () => {
  const editor = ServerBlockNoteEditor.create({ schema })
  const blocks = [{ type: "paragraph", content: [{ type: "text", text: "x", styles: {}, offset: 3 }], range: { start: 3, end: 4 } }]
  const [back] = editor._prosemirrorNodeToBlocks(editor._blocksToProsemirrorNode(blocks as never)) as unknown as Record<string, unknown>[]
  expect(back).not.toHaveProperty("range")
  expect((back.content as Record<string, unknown>[])[0]).not.toHaveProperty("offset")
})

for (const [file, { blocks }] of Object.entries(golden)) {
  test(`${file.split("/").at(-1)} survives BlockNote ${BLOCKNOTE_VERSION}`, () => {
    const editor = ServerBlockNoteEditor.create({ schema })
    const back = editor._prosemirrorNodeToBlocks(editor._blocksToProsemirrorNode(blocks as never))
    expect(canon(back)).toEqual(canon(blocks))
  })
}
