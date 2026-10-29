import { BlockNoteSchema, createBlockSpec, defaultBlockSpecs, defaultProps } from "@blocknote/core"

// The body editor's schema: exactly the block set of WL-REQ-1871.

const levels = [1, 2, 3, 4, 5, 6] as const

// The default heading with an `anchor` prop for `## Title {#id}`. Toggle
// headings are left out with the rest of the toggle blocks.
const heading = createBlockSpec(
  {
    type: "heading",
    propSchema: { ...defaultProps, level: { default: 1, values: levels }, anchor: { default: "" } },
    content: "inline",
  },
  {
    parse: (el) => (/^H[1-6]$/.test(el.tagName) ? { level: Number(el.tagName[1]) as (typeof levels)[number] } : undefined),
    render: (block) => {
      const dom = document.createElement(`h${block.props.level}`)
      return { dom, contentDOM: dom }
    },
  },
)

// Markdown outside WL-REQ-1871's list, kept verbatim and edited as plain text.
const source = createBlockSpec(
  { type: "source", propSchema: { markdown: { default: "" } }, content: "none" },
  {
    render: (block, editor) => {
      const dom = document.createElement("textarea")
      dom.className = "font-mono w-full"
      dom.spellcheck = false
      dom.value = block.props.markdown
      dom.addEventListener("input", () => editor.updateBlock(block, { props: { markdown: dom.value } }))
      return { dom }
    },
  },
)

// A Quarto callout (`::: {.callout-note}`); its children nest under it.
const callout = createBlockSpec(
  {
    type: "callout",
    propSchema: { kind: { default: "note", values: ["note", "warning", "important", "tip", "caution"] } },
    content: "inline",
  },
  {
    render: (block) => {
      const dom = document.createElement("div")
      dom.dataset.calloutKind = block.props.kind
      return { dom, contentDOM: dom }
    },
  },
)

const { paragraph, bulletListItem, numberedListItem, checkListItem, table, codeBlock, quote, image } = defaultBlockSpecs

export const schema = BlockNoteSchema.create({
  blockSpecs: {
    paragraph, heading: heading(), bulletListItem, numberedListItem, checkListItem,
    table, codeBlock, quote, image, source: source(), callout: callout(),
  },
})
