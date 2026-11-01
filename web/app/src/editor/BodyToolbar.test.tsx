import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeAll, beforeEach, expect, test, vi } from "vitest"
import { db } from "@/mocks/db"
import { renderApp } from "@/test/render"

const SELECT_WIDTH = 128
const BUTTON_WIDTH = 32

// ProseMirror measures text and BlockNote's side menu hit-tests the pointer,
// neither of which jsdom lays out
beforeAll(() => {
  for (const proto of [Range.prototype, Text.prototype]) {
    Object.assign(proto, { getClientRects: () => [], getBoundingClientRect: () => new DOMRect() })
  }
  Object.assign(document, { elementFromPoint: () => null, elementsFromPoint: () => [] })
})

let resize: () => void = () => {}

/** Stands in for layout: the toolbar is `width` wide and each control its size in the page's CSS. */
function layOut(width: number) {
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(function (this: HTMLElement) {
    return this.getAttribute("role") === "toolbar" ? width : 0
  })
  vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockImplementation(function (this: HTMLElement) {
    if (this.tagName === "SELECT") return 0 // Radix's hidden native select
    return this.getAttribute("role") === "combobox" ? SELECT_WIDTH : BUTTON_WIDTH
  })
}

beforeEach(() => {
  localStorage.clear()
  db.projects.push({ id: "web", key: "WEB", name: "Web", horizon: "standing", repos: [] })
  db.rules.push({ id: 7, ref: "WEB-REQ-7", project: "web", heading: "Seven", kind: "requirement", status: "draft", body: "v1 text\n" })
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(callback: () => void) {
        resize = callback
      }
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  )
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

const rule = () => db.rules.find((r) => r.id === 7)!
const body = () => screen.findByRole("textbox", { name: "Body" })
const more = () => screen.queryByRole("button", { name: "More formatting" })

async function open() {
  await renderApp("/projects/web/rule/7/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
}

async function selectAll() {
  await userEvent.click(await body())
  await userEvent.keyboard("{Control>}a{/Control}")
}

async function save() {
  await userEvent.click(screen.getByRole("button", { name: "Save" }))
  await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeDisabled())
}

test("a narrow row keeps Save and Close and moves what does not fit into the ... menu", async () => {
  layOut(300)
  await open()
  // 128 for the select, three buttons at 36, and the ... button's 36
  expect(screen.getByRole("button", { name: "Save" })).toBeVisible()
  expect(screen.getByRole("button", { name: "Close" })).toBeVisible()
  expect(within(screen.getByRole("toolbar")).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual([
    "Bold",
    "Italic",
    "Strikethrough",
    "More formatting",
  ])
  await userEvent.click(more()!)
  const items = screen.getAllByRole("menuitemcheckbox")
  expect(items.map((i) => i.textContent)).toEqual([
    "Inline code",
    "Link",
    "Bullet list",
    "Numbered list",
    "Task list",
    "Quote",
    "Code block",
    "Callout",
    "Table",
  ])
})

test("at 480px the ... button holds the hidden controls", async () => {
  layOut(480)
  await open()
  expect(screen.getByRole("button", { name: "Save" })).toBeVisible()
  expect(screen.getByRole("button", { name: "Close" })).toBeVisible()
  await userEvent.click(more()!)
  expect(screen.getAllByRole("menuitemcheckbox").map((i) => i.textContent)).toEqual(["Quote", "Code block", "Callout", "Table"])
})

test("a menu item applies its control and shows its state", async () => {
  layOut(480)
  await open()
  await userEvent.click(await body())
  await userEvent.click(more()!)
  await userEvent.click(screen.getByRole("menuitemcheckbox", { name: "Quote" }))
  await userEvent.click(more()!)
  expect(screen.getByRole("menuitemcheckbox", { name: "Quote" })).toBeChecked()
})

test("a row too narrow for the select moves the block types into the menu", async () => {
  layOut(100)
  await open()
  expect(screen.queryByRole("combobox", { name: "Block type" })).not.toBeInTheDocument()
  await userEvent.click(more()!)
  expect(screen.getAllByRole("menuitemcheckbox").slice(0, 4).map((i) => i.textContent)).toEqual([
    "Paragraph",
    "Heading 2",
    "Heading 3",
    "Heading 4",
  ])
})

test("a wide row has no ... button, and shrinking it brings one", async () => {
  layOut(1200)
  await open()
  expect(more()).not.toBeInTheDocument()
  layOut(480)
  resize()
  expect(await screen.findByRole("button", { name: "More formatting" })).toBeInTheDocument()
})

test.each([
  ["Bold", "**v1 text**"],
  ["Italic", "*v1 text*"],
  ["Strikethrough", "~~v1 text~~"],
  ["Inline code", "`v1 text`"],
  ["Bullet list", "- v1 text"],
  ["Numbered list", "1. v1 text"],
  ["Task list", "- [ ] v1 text"],
  ["Quote", "> v1 text"],
  ["Code block", "```"],
  ["Callout", "::: {.callout-note}\nv1 text\n:::"],
  ["Table", "|---|---|---|"],
])("%s saves as its stored spelling and shows active", async (label, spelling) => {
  await open()
  await selectAll()
  await userEvent.click(screen.getByRole("button", { name: label }))
  if (label !== "Table") expect(screen.getByRole("button", { name: label })).toHaveAttribute("aria-pressed", "true")
  await save()
  expect(rule().body).toContain(spelling)
})

test("Link saves as its stored spelling", async () => {
  await open()
  expect(screen.getByRole("button", { name: "Link" })).toBeDisabled()
  await selectAll()
  await userEvent.click(screen.getByRole("button", { name: "Link" }))
  await userEvent.type(screen.getByRole("textbox", { name: "URL" }), "https://example.org")
  await userEvent.click(screen.getByRole("button", { name: "Apply" }))
  await save()
  expect(rule().body).toContain("[v1 text](https://example.org)")
})

test.each([
  ["Heading 2", "## v1 text"],
  ["Heading 3", "### v1 text"],
  ["Heading 4", "#### v1 text"],
])("the block type %s saves as its stored spelling", async (label, spelling) => {
  await open()
  await userEvent.click(await body())
  await userEvent.click(screen.getByRole("combobox", { name: "Block type" }))
  expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["Paragraph", "Heading 2", "Heading 3", "Heading 4"])
  await userEvent.click(screen.getByRole("option", { name: label }))
  expect(screen.getByRole("combobox", { name: "Block type" })).toHaveTextContent(label)
  await save()
  expect(rule().body).toContain(spelling)
})

test("a second click on an active list control turns the block back into a paragraph", async () => {
  await open()
  await userEvent.click(await body())
  const button = screen.getByRole("button", { name: "Bullet list" })
  await userEvent.click(button)
  await userEvent.click(button)
  expect(button).toHaveAttribute("aria-pressed", "false")
  expect(screen.getByRole("combobox", { name: "Block type" })).toHaveTextContent("Paragraph")
})

test("bold text converts to a code block", async () => {
  await open()
  await selectAll()
  await userEvent.click(screen.getByRole("button", { name: "Bold" }))
  await userEvent.click(screen.getByRole("button", { name: "Code block" }))
  await save()
  expect(rule().body).toContain("```")
  expect(screen.getByRole("button", { name: "Bold" })).toBeDisabled()
})

test("with the lock held by someone else every control is disabled", async () => {
  layOut(480)
  db.locks.set("rule:7", { kind: "rule", subject_id: "7", holder: "someone", can_unlock: false })
  await renderApp("/projects/web/rule/7/edit")
  await userEvent.click(await screen.findByRole("button", { name: /read.only/i }))
  const toolbar = screen.getByRole("toolbar")
  expect(within(toolbar).getByRole("combobox")).toBeDisabled()
  for (const button of within(toolbar).getAllByRole("button")) {
    if (button !== more()) expect(button).toBeDisabled()
  }
  await userEvent.click(more()!)
  for (const item of screen.getAllByRole("menuitemcheckbox")) expect(item).toHaveAttribute("aria-disabled", "true")
})
