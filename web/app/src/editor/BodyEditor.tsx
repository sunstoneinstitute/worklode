import { useRef } from "react"
import { BlockNoteSchema } from "@blocknote/core"
import { SideMenuExtension } from "@blocknote/core/extensions"
import {
  BlockColorsItem,
  DragHandleMenu,
  RemoveBlockItem,
  SideMenu,
  SideMenuController,
  TableColumnHeaderItem,
  TableRowHeaderItem,
  createReactBlockSpec,
  useBlockNoteEditor,
  useCreateBlockNote,
  useDictionary,
  useExtensionState,
} from "@blocknote/react"
import { BlockNoteView } from "@blocknote/shadcn"
import "@blocknote/shadcn/style.css"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useHtmlDark } from "@/hooks/use-dark-mode"
import { schema } from "./schema"
import { type BodyComments, threadHighlights, useThreadMarks } from "./threads"

const { heading, source, callout } = schema.blockSpecs

// schema.ts's block set, with React renderers for its three custom specs
export const bodySchema = BlockNoteSchema.create({
  blockSpecs: {
    ...schema.blockSpecs,
    heading: createReactBlockSpec(heading.config, {
      parse: heading.implementation.parse,
      render: ({ block, contentRef }) => {
        const Tag = `h${block.props.level}` as "h1"
        return (
          <div className="bn-heading">
            <Tag ref={contentRef} />
            {block.props.anchor ? (
              <span className="bn-anchor" contentEditable={false}>
                #{block.props.anchor}
              </span>
            ) : null}
          </div>
        )
      },
    })(),
    source: createReactBlockSpec(source.config, {
      render: ({ block, editor }) => (
        <textarea
          aria-label="Markdown source"
          className="bn-source"
          spellCheck={false}
          readOnly={!editor.isEditable}
          value={block.props.markdown}
          onChange={(e) => editor.updateBlock(block, { props: { markdown: e.target.value } })}
        />
      ),
    })(),
    callout: createReactBlockSpec(callout.config, {
      render: ({ block, contentRef }) => (
        <div className="bn-callout">
          <Badge variant="secondary" contentEditable={false}>
            {block.props.kind}
          </Badge>
          <div ref={contentRef} />
        </div>
      ),
    })(),
  },
})

export type Blocks = (typeof bodySchema.PartialBlock)[]

/**
 * Our Button for BlockNote's menus, but not a submit button unless asked.
 * BlockNote leaves `type` off some buttons (Add block, Nest, Unnest), and an
 * edit page puts the editor inside a form, where such a button submits it.
 */
export function EditorButton(props: React.ComponentProps<typeof Button>) {
  return <Button type="button" {...props} />
}

// BlockNote renders these in place of its own. Only components without
// portals may be passed (BlockNote's shadcn guide); our Card is the blue
// quick-link card, not a neutral surface, so it is left out.
const SHADCN_COMPONENTS = {
  Badge: { Badge },
  Button: { Button: EditorButton },
  Input: { Input },
  Label: { Label },
  Tabs: { Tabs, TabsContent, TabsList, TabsTrigger },
}

// A heading's `{#id}`, edited in its drag-handle menu
function AnchorField() {
  const editor = useBlockNoteEditor(bodySchema)
  const block = useExtensionState(SideMenuExtension, { editor, selector: (s) => s?.block })
  if (block?.type !== "heading") return null
  return (
    // The menu's typeahead would swallow the keys
    <div className="bn-anchor-field" onKeyDown={(e) => e.stopPropagation()}>
      <Label htmlFor={`anchor-${block.id}`}>Anchor</Label>
      <Input
        id={`anchor-${block.id}`}
        key={block.id}
        defaultValue={block.props.anchor}
        onChange={(e) => editor.updateBlock(block.id, { props: { anchor: e.target.value } })}
      />
    </div>
  )
}

function BodyDragHandleMenu() {
  const dict = useDictionary()
  return (
    <DragHandleMenu>
      <RemoveBlockItem>{dict.drag_handle.delete_menuitem}</RemoveBlockItem>
      <BlockColorsItem>{dict.drag_handle.colors_menuitem}</BlockColorsItem>
      <TableRowHeaderItem>{dict.drag_handle.header_row_menuitem}</TableRowHeaderItem>
      <TableColumnHeaderItem>{dict.drag_handle.header_column_menuitem}</TableColumnHeaderItem>
      <AnchorField />
    </DragHandleMenu>
  )
}

// One editor for editing and reading. It reads `blocks` once, when made:
// to show other blocks, remount it (a new key).
export function useBodyEditor(blocks: Blocks) {
  return useCreateBlockNote({
    schema: bodySchema,
    initialContent: blocks.length ? blocks : undefined,
    domAttributes: { editor: { "aria-label": "Body" } },
    extensions: [() => ({ key: "threadHighlights", prosemirrorPlugins: [threadHighlights] })],
  })
}

interface BodyEditorProps {
  blocks: Blocks
  onChange: (blocks: (typeof bodySchema.Block)[]) => void
  readOnly?: boolean
  /** Thread highlights and the selection a thread starts from; none on a task body (WL-REQ-1317) */
  comments?: BodyComments
}

/** A document body in BlockNote. Reports blocks on edits that change them, never on open. */
export function BodyEditor({ blocks, onChange, readOnly = false, comments }: BodyEditorProps) {
  const isDark = useHtmlDark()
  const editor = useBodyEditor(blocks)
  const onSelectionChange = useThreadMarks(editor, comments)
  // BlockNote reports transactions that leave the document as it was
  const last = useRef<string>(undefined)
  last.current ??= JSON.stringify(editor.document)
  const report = () => {
    const json = JSON.stringify(editor.document)
    if (json === last.current) return
    last.current = json
    onChange(editor.document)
  }
  return (
    <BlockNoteView
      className="bn-field flex min-h-80 flex-col"
      editor={editor}
      editable={!readOnly}
      theme={isDark ? "dark" : "light"}
      sideMenu={false}
      shadCNComponents={SHADCN_COMPONENTS as never}
      onChange={report}
      onSelectionChange={onSelectionChange}
    >
      <SideMenuController sideMenu={(props) => <SideMenu {...props} dragHandleMenu={BodyDragHandleMenu} />} />
    </BlockNoteView>
  )
}
