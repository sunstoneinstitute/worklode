import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeAll, expect, test, vi } from "vitest"
import { renderWithRouter } from "@/test/render"
import { BodyEditor, type Blocks } from "./BodyEditor"
import { BodyView } from "./BodyView"

const paragraph = (text: string): Blocks => [{ type: "paragraph", content: text }]

// ProseMirror measures text and BlockNote's side menu hit-tests the pointer,
// neither of which jsdom lays out
beforeAll(() => {
  for (const proto of [Range.prototype, Text.prototype]) {
    Object.assign(proto, { getClientRects: () => [], getBoundingClientRect: () => new DOMRect() })
  }
  Object.assign(document, { elementFromPoint: () => null, elementsFromPoint: () => [] })
})

test("reports blocks on edit, not on mount", async () => {
  const onChange = vi.fn()
  await renderWithRouter(<BodyEditor blocks={paragraph("hi")} onChange={onChange} />)
  expect(onChange).not.toHaveBeenCalled()
  await userEvent.type(screen.getByRole("textbox", { name: "Body" }), " there")
  expect(onChange).toHaveBeenLastCalledWith(expect.arrayContaining([expect.objectContaining({ type: "paragraph" })]))
})

test("a source block shows its Markdown verbatim and reports edits as props.markdown", async () => {
  const onChange = vi.fn()
  const markdown = "<b>raw</b>\n{{< include x.qmd >}}"
  await renderWithRouter(<BodyEditor blocks={[{ type: "source", props: { markdown } }]} onChange={onChange} />)
  const textarea = screen.getByRole("textbox", { name: "Markdown source" })
  expect(textarea).toHaveValue(markdown)
  await userEvent.type(textarea, "!")
  expect(onChange).toHaveBeenLastCalledWith(
    expect.arrayContaining([expect.objectContaining({ type: "source", props: { markdown: `${markdown}!` } })]),
  )
})

test("a heading shows its anchor and a callout its kind, read-only", async () => {
  await renderWithRouter(
    <BodyView
      blocks={[
        { type: "heading", props: { level: 2, anchor: "sec-3" }, content: "Scope" },
        { type: "callout", props: { kind: "warning" }, content: "Careful" },
      ]}
    />,
  )
  expect(screen.getByRole("textbox", { name: "Body" })).toHaveAttribute("contenteditable", "false")
  expect(screen.getByText("#sec-3")).toBeInTheDocument()
  expect(screen.getByText("warning")).toBeInTheDocument()
})
