import { type FormEvent, type ReactNode, useLayoutEffect, useRef, useState } from "react"
import { useEditorState } from "@blocknote/react"
import {
  BoldIcon,
  CodeIcon,
  EllipsisIcon,
  InfoIcon,
  ItalicIcon,
  LinkIcon,
  ListChecksIcon,
  ListIcon,
  ListOrderedIcon,
  QuoteIcon,
  SquareCodeIcon,
  StrikethroughIcon,
  TableIcon,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import type { BodyEditorInstance } from "./BodyEditor"

const GAP = 4 // gap-1 between the row's controls

// The block types WL-REQ-1896 offers. BlockNote's BlockTypeSelect has no disabled state.
const BLOCK_TYPES = [
  { value: "paragraph", label: "Paragraph", update: { type: "paragraph" } },
  { value: "2", label: "Heading 2", update: { type: "heading", props: { level: 2 } } },
  { value: "3", label: "Heading 3", update: { type: "heading", props: { level: 3 } } },
  { value: "4", label: "Heading 4", update: { type: "heading", props: { level: 4 } } },
]

const STYLES = [
  { style: "bold", label: "Bold", icon: <BoldIcon /> },
  { style: "italic", label: "Italic", icon: <ItalicIcon /> },
  { style: "strike", label: "Strikethrough", icon: <StrikethroughIcon /> },
  { style: "code", label: "Inline code", icon: <CodeIcon /> },
] as const

const CONVERSIONS = [
  { type: "bulletListItem", label: "Bullet list", icon: <ListIcon /> },
  { type: "numberedListItem", label: "Numbered list", icon: <ListOrderedIcon /> },
  { type: "checkListItem", label: "Task list", icon: <ListChecksIcon /> },
  { type: "quote", label: "Quote", icon: <QuoteIcon /> },
  { type: "codeBlock", label: "Code block", icon: <SquareCodeIcon /> },
  { type: "callout", label: "Callout", icon: <InfoIcon /> },
] as const

const NEW_TABLE = {
  type: "table",
  content: {
    type: "tableContent",
    headerRows: 1,
    rows: [{ cells: ["", "", ""] }, { cells: ["", "", ""] }],
  },
} as const

function selectedBlocks(editor: BodyEditorInstance) {
  return editor.getSelection()?.blocks ?? [editor.getTextCursorPosition().block]
}

function readState(editor: BodyEditorInstance) {
  const blocks = selectedBlocks(editor)
  const [first] = blocks
  return {
    blockType: first.type === "paragraph" ? "paragraph" : first.type === "heading" ? String(first.props.level) : "",
    types: blocks.map((b) => b.type as string),
    styles: Object.keys(editor.getActiveStyles()),
    link: editor.getSelectedLinkUrl() ?? "",
    hasRange: !editor.prosemirrorState.selection.empty,
    // A table, image or source block has no text to convert; a code block takes no styles
    convertible: blocks.every((b) => Array.isArray(b.content)),
    styleable: blocks.some((b) => b.content !== undefined && b.type !== "codeBlock"),
  }
}

function convert(editor: BodyEditorInstance, update: { type: string; props?: object }) {
  editor.transact(() => {
    for (const block of selectedBlocks(editor)) editor.updateBlock(block.id, update as never)
  })
  editor.focus()
}

/** How many of `widths` fit in `room`, leaving space for the `...` button when some do not. */
function fittingCount(widths: number[], room: number) {
  const fit = (limit: number) => {
    let used = -GAP
    return widths.findIndex((w) => (used += w + GAP) > limit)
  }
  const all = fit(room)
  // The `...` button is the size of the icon buttons, the last of which is widths.at(-1)
  return all < 0 ? widths.length : fit(room - widths.at(-1)! - GAP)
}

interface Control {
  label: string
  icon: ReactNode
  active: boolean
  enabled: boolean
  run: () => void
}

/**
 * The fixed formatting toolbar (WL-REQ-1896). Plain buttons over the editor
 * API: BlockNote's toolbar components render nothing while the editor is
 * read-only, and need the context of a BlockNoteView. `editor` is null while
 * no editor is mounted, as when the lock is lost.
 */
export function BodyToolbar({ editor, disabled }: { editor: BodyEditorInstance | null; disabled: boolean }) {
  const state = useEditorState({ editor, selector: () => (editor ? readState(editor) : null) })
  const row = useRef<HTMLDivElement>(null)
  const widths = useRef<number[]>([])
  // Row slots shown: the select, then the buttons. The first render shows them all, to measure them.
  const [shown, setShown] = useState(1 + STYLES.length + 1 + CONVERSIONS.length + 1)
  const [linkOpen, setLinkOpen] = useState(false)

  useLayoutEffect(() => {
    const el = row.current!
    const measure = () => {
      if (!el.clientWidth) return
      // Buttons only: Radix mounts a hidden native select beside the select's trigger
      if (!widths.current.length) widths.current = [...el.querySelectorAll<HTMLElement>(":scope > button")].map((c) => c.offsetWidth)
      setShown(fittingCount(widths.current, el.clientWidth))
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  const off = disabled || !state
  const inEditor = (run: (editor: BodyEditorInstance) => void) => () => {
    if (editor) run(editor)
  }
  const controls: Control[] = [
    ...STYLES.map(({ style, label, icon }) => ({
      label,
      icon,
      active: !!state?.styles.includes(style),
      enabled: !!state?.styleable,
      run: inEditor((e) => {
        e.toggleStyles({ [style]: true })
        e.focus()
      }),
    })),
    {
      label: "Link",
      icon: <LinkIcon />,
      active: !!state?.link,
      enabled: !!state?.styleable && state.hasRange,
      run: () => setLinkOpen(true),
    },
    ...CONVERSIONS.map(({ type, label, icon }) => {
      const active = !!state?.types.every((t) => t === type)
      return {
        label,
        icon,
        active,
        enabled: !!state?.convertible,
        run: inEditor((e) => convert(e, { type: active ? "paragraph" : type })),
      }
    }),
    {
      label: "Table",
      icon: <TableIcon />,
      active: !!state?.types.every((t) => t === "table"),
      enabled: true,
      run: inEditor((e) => {
        e.insertBlocks([NEW_TABLE as never], selectedBlocks(e).at(-1)!, "after")
        e.focus()
      }),
    },
  ]
  const hidden = controls.slice(Math.max(shown - 1, 0))
  const typesHidden = shown === 0

  return (
    <div ref={row} aria-label="Formatting" role="toolbar" className="flex min-w-0 flex-1 items-center gap-1 overflow-hidden whitespace-nowrap">
      {!typesHidden && (
        <Select
          value={state?.blockType ?? ""}
          disabled={off || !state.convertible}
          onValueChange={(value) => {
            // Inside a <form> Radix's hidden select also reports "" as the value settles
            const type = BLOCK_TYPES.find((t) => t.value === value)
            if (type) inEditor((e) => convert(e, type.update))()
          }}
        >
          <SelectTrigger aria-label="Block type" size="sm" className="w-32 shrink-0">
            <SelectValue placeholder="Block type" />
          </SelectTrigger>
          <SelectContent
            onCloseAutoFocus={(e) => {
              e.preventDefault()
              editor?.focus()
            }}
          >
            {BLOCK_TYPES.map((t) => (
              <SelectItem key={t.value} value={t.value}>
                {t.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
      {controls.slice(0, Math.max(shown - 1, 0)).map((c) => (
        <Button
          key={c.label}
          type="button"
          variant="ghost"
          size="icon"
          title={c.label}
          aria-label={c.label}
          aria-pressed={c.active}
          disabled={off || !c.enabled}
          className="aria-pressed:bg-muted"
          // Keeps the editor's focus and selection
          onMouseDown={(e) => e.preventDefault()}
          onClick={c.run}
        >
          {c.icon}
        </Button>
      ))}
      {(hidden.length > 0 || typesHidden) && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button type="button" variant="ghost" size="icon" aria-label="More formatting" className="ml-auto">
              <EllipsisIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            onCloseAutoFocus={(e) => {
              e.preventDefault()
              if (!linkOpen) editor?.focus()
            }}
          >
            {typesHidden &&
              BLOCK_TYPES.map((t) => (
                <DropdownMenuCheckboxItem
                  key={t.value}
                  checked={state?.blockType === t.value}
                  disabled={off || !state.convertible}
                  onSelect={inEditor((e) => convert(e, t.update))}
                >
                  {t.label}
                </DropdownMenuCheckboxItem>
              ))}
            {hidden.map((c) => (
              <DropdownMenuCheckboxItem key={c.label} checked={c.active} disabled={off || !c.enabled} onSelect={c.run}>
                {c.icon}
                {c.label}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      {linkOpen && editor && <LinkDialog editor={editor} url={state?.link ?? ""} onClose={() => setLinkOpen(false)} />}
    </div>
  )
}

function LinkDialog({ editor, url, onClose }: { editor: BodyEditorInstance; url: string; onClose: () => void }) {
  const [value, setValue] = useState(url)
  function submit(e: FormEvent) {
    e.preventDefault()
    editor.createLink(value.trim())
    onClose()
  }
  return (
    <Dialog open onOpenChange={onClose}>
      <DialogContent
        onCloseAutoFocus={(e) => {
          e.preventDefault()
          editor.focus()
        }}
      >
        <DialogHeader>
          <DialogTitle>Link</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <Input aria-label="URL" autoFocus required value={value} onChange={(e) => setValue(e.target.value)} />
          <DialogFooter>
            <Button type="submit">Apply</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
