import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeAll, beforeEach, expect, test, vi } from "vitest"
import type { components } from "@/api/schema"
import { db } from "@/mocks/db"
import { server } from "@/mocks/node"
import { renderApp, renderWithRouter } from "@/test/render"
import { BodyView } from "./BodyView"
import { charToPos } from "./threads"

// ProseMirror measures text and BlockNote's side menu hit-tests the pointer,
// neither of which jsdom lays out
beforeAll(() => {
  for (const proto of [Range.prototype, Text.prototype]) {
    Object.assign(proto, { getClientRects: () => [], getBoundingClientRect: () => new DOMRect() })
  }
  Object.assign(document, { elementFromPoint: () => null, elementsFromPoint: () => [] })
})

beforeEach(() => {
  db.projects.push({ id: "web", key: "WEB", name: "Web", horizon: "standing", repos: [] })
  db.rules.push({ id: 7, ref: "WEB-REQ-7", project: "web", heading: "Seven", kind: "requirement", status: "draft", version: 1, body: "v1 vøice text\n", arranged_in: [{ doc: 89 }] })
  db.docs.push({ id: 89, ref: "WEB-SPEC-89", project: "web", kind: "spec", number: 89, title: "Web spec", status: "draft", version: 1, body: "spec text\n" })
  db.docs.push({ id: 90, ref: "WEB-PLAN-90", project: "web", kind: "plan", number: 90, title: "Web plan", status: "draft", version: 2, body: "plan ø text here\n" })
  db.tasks.push({ id: "WEB-9", project: "web", kind: "feature", title: "Ninth task", state: "ready", priority: "medium", body: "task text\n" })
})

const body = () => screen.findByRole("textbox", { name: "Body" })
type Tiptap = { view: { state: { doc: Parameters<typeof charToPos>[0] } }; commands: { setTextSelection: (r: { from: number; to: number }) => void } }

// Selects characters [from, to) of the body's text, as a drag would
async function selectChars(from: number, to: number) {
  const editor = (await body()) as unknown as { editor: Tiptap }
  const doc = editor.editor.view.state.doc
  act(() => editor.editor.commands.setTextSelection({ from: charToPos(doc, from, false)!, to: charToPos(doc, to, true)! }))
}

const thread = (fields: Partial<components["schemas"]["ReviewThread"]>): components["schemas"]["ReviewThread"] => ({
  intent: "note", status: "open", author: "ada", messages: [{ id: 1, author: "ada", body: "a remark" }], ...fields,
})

test("a selection starts a thread anchored to its bytes and quote", async () => {
  const sent: components["schemas"]["OpenThreadInput"][] = []
  server.events.on("request:start", async ({ request }) => {
    if (request.method === "POST" && request.url.endsWith("/api/v1/docs/89/threads")) sent.push(await request.clone().json())
  })
  await renderApp("/projects/web/rule/7/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  await selectChars(3, 8) // "vøice": ø is two bytes
  const form = await screen.findByRole("form", { name: "New comment" })
  expect(within(form).getByText("vøice")).toBeInTheDocument()
  await userEvent.selectOptions(within(form).getByLabelText("Intent"), "question")
  await userEvent.type(within(form).getByLabelText("Comment"), "why?")
  await userEvent.click(within(form).getByRole("button", { name: "Comment" }))
  await waitFor(() => expect(db.threads).toHaveLength(1))
  server.events.removeAllListeners()
  expect(sent[0]).toEqual({ anchor: { rule: 7, version: 1, byte_start: 3, byte_end: 9, quote: "vøice" }, intent: "question", body: "why?" })
  await waitFor(() => expect(document.querySelector(".bn-thread-mark")).toHaveTextContent("vøice"))
  expect(screen.getByRole("region", { name: "Comments" })).toHaveTextContent("Question")
})

test("open threads are highlighted as decorations and the saved body carries no marks", async () => {
  db.threads.push(
    thread({ id: 1, anchor: { doc: 90, version: 2, byte_start: 8, byte_end: 12, quote: "text" } }),
    thread({ id: 2, status: "resolved", anchor: { doc: 90, version: 2, byte_start: 0, byte_end: 4, quote: "plan" } }),
    thread({ id: 3, anchor: { doc: 90, version: 1, byte_start: 0, byte_end: 4, quote: "plan" } }),
  )
  const sent: string[] = []
  server.events.on("request:start", async ({ request }) => {
    if (request.method === "PUT") sent.push(await request.clone().text())
  })
  await renderApp("/projects/web/plan/90/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  await waitFor(() => expect(document.querySelectorAll(".bn-thread-mark")).toHaveLength(1))
  expect(document.querySelector('.bn-thread-mark[data-thread-id="1"]')).toHaveTextContent("text")
  await userEvent.type(await body(), "new ")
  await userEvent.click(screen.getByRole("button", { name: "Save" }))
  await waitFor(() => expect(db.docs.find((d) => d.id === 90)?.body).toBe("new plan ø text here\n"))
  server.events.removeAllListeners()
  expect(sent[0]).not.toMatch(/thread|comment/)
  // The highlight follows the text through the edit
  expect(document.querySelector(".bn-thread-mark")).toHaveTextContent("text")
})

test("the panel lists every thread with quote, intent, replies and resolution; obsolete is struck through", async () => {
  db.threads.push(
    thread({ id: 1, intent: "change_request", anchor: { doc: 90, version: 2, byte_start: 8, byte_end: 12, quote: "text" },
      messages: [{ id: 1, author: "ada", body: "fix this" }, { id: 2, author: "stig", body: "on it" }] }),
    thread({ id: 2, status: "obsolete", anchor: { doc: 90, version: 2, byte_start: 0, byte_end: 4, quote: "gone" } }),
    thread({ id: 3, status: "resolved", resolved_by: "stig", anchor: { doc: 90, version: 2, byte_start: 0, byte_end: 4, quote: "plan" } }),
  )
  await renderApp("/projects/web/plan/90/edit")
  const panel = await screen.findByRole("region", { name: "Comments" })
  await waitFor(() => expect(panel.querySelectorAll("li")).toHaveLength(3))
  const [open, obsolete, resolved] = panel.querySelectorAll("li")
  expect(open).toHaveTextContent("Change request")
  expect(open).toHaveTextContent("fix this")
  expect(open).toHaveTextContent("on it")
  expect(obsolete).toHaveTextContent("gone")
  expect(obsolete).toHaveClass("line-through")
  expect(resolved).toHaveTextContent("Resolved by stig")

  await userEvent.type(within(open).getByRole("textbox", { name: "Reply" }), "thanks")
  await userEvent.click(within(open).getByRole("button", { name: "Reply" }))
  await waitFor(() => expect(db.threads[0].messages).toHaveLength(3))
  await userEvent.click(within(panel.querySelector("li")!).getByRole("button", { name: "Resolve" }))
  await waitFor(() => expect(db.threads[0].status).toBe("resolved"))
})

test("a task body gets no threads", async () => {
  const urls: string[] = []
  server.events.on("request:start", ({ request }) => void urls.push(request.url))
  await renderApp("/projects/web/feature/9/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  server.events.removeAllListeners()
  expect(screen.queryByRole("region", { name: "Comments" })).not.toBeInTheDocument()
  expect(urls.some((u) => u.includes("threads"))).toBe(false)
})

test("BodyView highlights a range and reports a selection's anchor", async () => {
  const source = [{ id: "p", type: "paragraph", range: { start: 0, end: 9 }, content: [{ type: "text", text: "read only", offset: 0 }] }]
  const onSelect = vi.fn()
  await renderWithRouter(
    <BodyView blocks={source as never} comments={{ source, body: "read only\n", highlights: [{ id: 4, start: 5, end: 9 }], onSelect }} />,
  )
  await waitFor(() => expect(document.querySelector('.bn-thread-mark[data-thread-id="4"]')).toHaveTextContent("only"))
  await selectChars(0, 4)
  expect(onSelect).toHaveBeenLastCalledWith({ start: 0, end: 4, quote: "read" })
})
