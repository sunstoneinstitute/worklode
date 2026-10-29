import { act, cleanup, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { HttpResponse, http } from "msw"
import { beforeAll, beforeEach, expect, test } from "vitest"
import type { components } from "@/api/schema"
import { db } from "@/mocks/db"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

// userEvent.type puts the caret at the start of the editor, so typed text
// comes first

// ProseMirror measures text and BlockNote's side menu hit-tests the pointer,
// neither of which jsdom lays out
beforeAll(() => {
  for (const proto of [Range.prototype, Text.prototype]) {
    Object.assign(proto, { getClientRects: () => [], getBoundingClientRect: () => new DOMRect() })
  }
  Object.assign(document, { elementFromPoint: () => null, elementsFromPoint: () => [] })
})

beforeEach(() => {
  localStorage.clear()
  db.projects.push({ id: "web", key: "WEB", name: "Web", horizon: "standing", repos: [] })
  db.rules.push({ id: 7, ref: "WEB-REQ-7", project: "web", heading: "Seven", kind: "requirement", status: "draft", body: "v1 text\n" })
  db.docs.push({ id: 89, ref: "WEB-SPEC-89", project: "web", kind: "spec", number: 89, title: "Web spec", status: "draft", body: "spec text\n" })
  db.tasks.push({ id: "WEB-9", project: "web", kind: "feature", title: "Ninth task", state: "ready", priority: "medium", body: "task text\n" })
})

const rule = () => db.rules.find((r) => r.id === 7)!
const body = () => screen.findByRole("textbox", { name: "Body" })

async function typeAndSave(text: string) {
  await userEvent.type(await body(), text)
  await userEvent.click(screen.getByRole("button", { name: "Save" }))
}

test("a moved body shows both texts and re-saves against the new hash", async () => {
  await renderApp("/projects/web/rule/7/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  rule().body = "agent wrote this\n" // rewritten in place under the editor, version unchanged
  await typeAndSave("mine")
  const view = await screen.findByRole("region", { name: "Newer stored text" })
  expect(view).toHaveTextContent("agent wrote this")
  await userEvent.click(screen.getByRole("button", { name: "Keep mine" }))
  await waitFor(() => expect(rule().body).toContain("mine"))
  expect(screen.queryByRole("region", { name: "Newer stored text" })).not.toBeInTheDocument()
})

test("Save sends blocks, the loaded hash and the lock session; the next save uses the saved hash", async () => {
  const sent: components["schemas"]["EditRuleInput"][] = []
  server.events.on("request:start", async ({ request }) => {
    if (request.method === "PUT" && request.url.includes("/api/v1/rules/")) sent.push(await request.clone().json())
  })
  await renderApp("/projects/web/rule/7/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  await typeAndSave(" one")
  await waitFor(() => expect(rule().body).toBe(" onev1 text\n"))
  await typeAndSave(" two")
  await waitFor(() => expect(rule().body).toBe(" two onev1 text\n"))
  server.events.removeAllListeners()
  expect(sent[0]).toEqual(
    expect.objectContaining({
      heading: "Seven",
      blocks: expect.arrayContaining([expect.objectContaining({ type: "paragraph" })]),
      if_body_sha256: await sha("v1 text\n"),
      lock_session: db.locks.get("rule:7")?.session,
    }),
  )
  expect(sent[0]).not.toHaveProperty("body")
  expect(sent[1].if_body_sha256).toBe(await sha(" onev1 text\n"))
})

test("Take theirs reloads the stored text into the editor", async () => {
  await renderApp("/projects/web/rule/7/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  rule().body = "agent wrote this\n"
  await typeAndSave("mine")
  await screen.findByRole("region", { name: "Newer stored text" })
  rule().body = "agent wrote more\n" // the reload reads the server, not the conflict's copy
  await userEvent.click(screen.getByRole("button", { name: "Take theirs" }))
  await waitFor(() => expect(screen.queryByRole("region", { name: "Newer stored text" })).not.toBeInTheDocument())
  expect(await body()).toHaveTextContent("agent wrote more")
  expect(await body()).not.toHaveTextContent("mine")
  await typeAndSave("!")
  await waitFor(() => expect(rule().body).toBe("!agent wrote more\n"))
})

test("a save refused for the lock goes to the lost-lock path", async () => {
  server.use(
    http.put("/api/v1/rules/:id", () => {
      const lock: components["schemas"]["BodyLock"] = { kind: "rule", subject_id: "7", holder: "u2" }
      return HttpResponse.json(lock, { status: 409 })
    }),
  )
  await renderApp("/projects/web/rule/7/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  await typeAndSave(" lost")
  expect(await screen.findByRole("status")).toHaveTextContent(/no longer hold the lock/i)
  expect(await screen.findByRole("region", { name: "Your unsaved changes" })).toHaveTextContent("lostv1 text")
  expect(screen.queryByRole("region", { name: "Newer stored text" })).not.toBeInTheDocument()
})

const draftKey = () => Object.keys(localStorage).find((k) => k.includes(":body:rule:7"))

test("a lost lock keeps the unsaved blocks read-only until retaken", async () => {
  const sent: components["schemas"]["EditRuleInput"][] = []
  server.events.on("request:start", async ({ request }) => {
    if (request.method === "PUT" && request.url.includes("/api/v1/rules/")) sent.push(await request.clone().json())
  })
  await renderApp("/projects/web/rule/7/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  await userEvent.type(await body(), "mine ")
  db.locks.end("rule:7", { reason: "unlocked", by: "u2" })
  await userEvent.click(screen.getByRole("button", { name: "Save" }))
  const kept = await screen.findByRole("region", { name: "Your unsaved changes" })
  expect(kept).toHaveTextContent("mine v1 text")
  expect(screen.getByRole("status")).toHaveTextContent(/someone unlocked it/i)
  expect(JSON.parse(localStorage.getItem(draftKey()!)!).sha).toBe(await sha("v1 text\n"))
  await userEvent.click(screen.getByRole("button", { name: "Take the lock again" }))
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  expect(await body()).toHaveTextContent("mine v1 text")
  await userEvent.click(screen.getByRole("button", { name: "Save" }))
  await waitFor(() => expect(rule().body).toBe("mine v1 text\n"))
  server.events.removeAllListeners()
  expect(sent.at(-1)?.if_body_sha256).toBe(await sha("v1 text\n"))
  expect(draftKey()).toBeUndefined()
})

test("an existing draft opens before the server's blocks, and Discard draft goes back to them", async () => {
  await renderApp("/projects/web/rule/7/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  await userEvent.type(await body(), "draft ")
  window.dispatchEvent(new Event("pagehide")) // writes the draft before the release
  expect(draftKey()).toBeDefined()
  cleanup()
  await renderApp("/projects/web/rule/7/edit")
  expect(await body()).toHaveTextContent("draft v1 text")
  expect(await screen.findByRole("status")).toHaveTextContent(/draft restored/i)
  await userEvent.click(screen.getByRole("button", { name: "Discard draft" }))
  await waitFor(async () => expect(await body()).not.toHaveTextContent("draft"))
  expect(draftKey()).toBeUndefined()
})

test("leaving with unsaved changes asks first", async () => {
  const { router } = await renderApp("/projects/web/rule/7/edit")
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  await userEvent.type(await body(), " unsaved")
  // The blocked navigation never settles, so it is not awaited
  act(() => void router.navigate({ to: "/projects" }))
  expect(await screen.findByRole("dialog", { name: "You have unsaved changes." })).toBeInTheDocument()
  expect(router.state.location.pathname).toBe("/projects/web/rule/7/edit")
})

test("/projects/web/spec/89/edit opens the document editor", async () => {
  await renderApp("/projects/web/spec/89/edit")
  expect(await screen.findByRole("heading", { name: "WEB-SPEC-89: Web spec" })).toBeInTheDocument()
  expect(await body()).toHaveTextContent("spec text")
})

test("/projects/web/feature/9/edit opens the task editor and saves the task", async () => {
  await renderApp("/projects/web/feature/9/edit")
  expect(await screen.findByRole("heading", { name: "WEB-9: Ninth task" })).toBeInTheDocument()
  await waitFor(async () => expect(await body()).toHaveAttribute("contenteditable", "true"))
  await typeAndSave(" more")
  await waitFor(() => expect(db.tasks.find((t) => t.id === "WEB-9")?.body).toBe(" moretask text\n"))
})

test("a kind that names neither a document nor a task is not found", async () => {
  await renderApp("/projects/web/widget/9/edit")
  expect(await screen.findByRole("heading", { name: "Page not found" })).toBeInTheDocument()
})

async function sha(text: string) {
  const { sha256Hex } = await import("@/editor/bytes")
  return sha256Hex(text)
}
