import { BlockNoteEditor } from "@blocknote/core"
import { expect, test } from "vitest"
import { schema } from "./schema"

test("the schema holds exactly WL-REQ-1871's block set", () => {
  expect(Object.keys(schema.blockSpecs).sort()).toEqual(
    [
      "bulletListItem", "callout", "checkListItem", "codeBlock", "heading", "image",
      "numberedListItem", "paragraph", "quote", "source", "table",
    ].sort(),
  )
})

test("heading carries an anchor, source its markdown, callout its kind", () => {
  const editor = BlockNoteEditor.create({
    schema,
    initialContent: [
      { type: "heading", content: "T" },
      { type: "heading", props: { anchor: "sec-1" }, content: "U" },
      { type: "source", props: { markdown: "<b>x</b>" } },
      { type: "callout", props: { kind: "tip" }, content: "c", children: [{ type: "paragraph", content: "kid" }] },
    ],
  })
  const [h0, h1, src, callout] = editor.document
  expect(h0.props).toMatchObject({ anchor: "" })
  expect(h1.props).toMatchObject({ anchor: "sec-1" })
  expect(src.props).toMatchObject({ markdown: "<b>x</b>" })
  expect(callout.props).toMatchObject({ kind: "tip" })
  expect(callout.children).toHaveLength(1)
})
