import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion"
import type { NoteBlock } from "../types"

const ORDERED = /^\d+\.\s+/
const UNORDERED = /^[-*]\s+/

/** ponytail: headings, lists and paragraphs only, the text a form config holds. No inline marks. */
function NoteText({ markdown }: { markdown: string }) {
  return markdown.split(/\n\s*\n/).map((chunk, index) => {
    const lines = chunk.trim().split("\n")
    const key = `${index}-${chunk.length}`
    if (chunk.startsWith("#")) return <h3 key={key} className="text-base font-medium">{chunk.replace(/^#+\s*/, "")}</h3>
    if (lines.every((line) => ORDERED.test(line))) {
      return <ol key={key} className="list-decimal space-y-1 pl-5">{lines.map((line) => <li key={line}>{line.replace(ORDERED, "")}</li>)}</ol>
    }
    if (lines.every((line) => UNORDERED.test(line))) {
      return <ul key={key} className="list-disc space-y-1 pl-5">{lines.map((line) => <li key={line}>{line.replace(UNORDERED, "")}</li>)}</ul>
    }
    return <p key={key}>{chunk}</p>
  })
}

/** A guidance box. A collapsible one is an accordion that starts closed, its title as the button. */
export function NoteBlockView({ block }: { block: NoteBlock }) {
  if (block.collapsible) {
    return (
      <aside className="rounded-card bg-surface px-panel text-sm">
        <Accordion type="single" collapsible>
          <AccordionItem value="note" className="border-none">
            <AccordionTrigger>{block.title ?? "More"}</AccordionTrigger>
            <AccordionContent className="flex flex-col gap-4">
              <NoteText markdown={block.markdown} />
            </AccordionContent>
          </AccordionItem>
        </Accordion>
      </aside>
    )
  }
  return (
    <aside className="flex flex-col gap-4 rounded-card bg-surface p-panel text-sm">
      {block.title ? <h2 className="text-base font-medium">{block.title}</h2> : null}
      <NoteText markdown={block.markdown} />
    </aside>
  )
}
