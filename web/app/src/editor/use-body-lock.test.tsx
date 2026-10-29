import { useState } from "react"
import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { HttpResponse, http } from "msw"
import { afterEach, beforeAll, expect, test, vi } from "vitest"
import type { components } from "@/api/schema"
import { db } from "@/mocks/db"
import { server } from "@/mocks/node"
import { renderWithRouter } from "@/test/render"
import { BodyEditor } from "./BodyEditor"
import { LockedDialog } from "./LockedDialog"
import { useBodyLock } from "./use-body-lock"

// ProseMirror measures text and BlockNote's side menu hit-tests the pointer,
// neither of which jsdom lays out
beforeAll(() => {
  for (const proto of [Range.prototype, Text.prototype]) {
    Object.assign(proto, { getClientRects: () => [], getBoundingClientRect: () => new DOMRect() })
  }
  Object.assign(document, { elementFromPoint: () => null, elementsFromPoint: () => [] })
})

afterEach(() => vi.useRealTimers())

// What an edit page does with the hook (WL-1361 builds the real one)
function Harness({ onBeforeRelease }: { onBeforeRelease?: () => void }) {
  const [dirty, setDirty] = useState(false)
  const [dialog, setDialog] = useState(true)
  const lock = useBodyLock("rule", "7", { dirty, onBeforeRelease })
  return (
    <>
      <p data-testid="status">{lock.status}</p>
      {lock.status === "locked" && dialog && (
        <LockedDialog
          open
          kind="rule"
          id="7"
          lock={lock.lock}
          onClose={() => setDialog(false)}
          onReadOnly={() => setDialog(false)}
          onGoToDocument={lock.retry}
        />
      )}
      <BodyEditor
        blocks={[{ type: "paragraph", content: "text" }]}
        readOnly={lock.status !== "held"}
        onChange={() => {
          setDirty(true)
          if (lock.status === "held") lock.touch()
        }}
      />
    </>
  )
}

const status = () => screen.getByTestId("status")

test("another holder gets the read-only view and the dialog", async () => {
  db.crew.push({ actor: "u2", display_name: "Bea Ortiz" })
  db.locks.set("rule:7", { holder: "u2", can_unlock: false })
  await renderWithRouter(<Harness />)
  expect(await screen.findByRole("dialog", { name: /being edited/i })).toHaveTextContent("Bea Ortiz")
  expect(screen.queryByRole("button", { name: "Unlock" })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole("button", { name: "Read only" }))
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
  expect(screen.getByRole("textbox", { name: "Body" })).toHaveAttribute("contenteditable", "false")
})

test("Unlock shows when the caller may, asks first, and frees the lock", async () => {
  db.crew.push({ actor: "u2", display_name: "Bea Ortiz" })
  db.locks.set("rule:7", { holder: "u2", can_unlock: true })
  await renderWithRouter(<Harness />)
  await userEvent.click(await screen.findByRole("button", { name: "Unlock" }))
  expect(screen.getByRole("dialog", { name: "Unlock this?" })).toHaveTextContent("Bea Ortiz")
  await userEvent.click(screen.getByRole("button", { name: "Unlock" }))
  expect(await screen.findByRole("dialog", { name: "Unlocked." })).toBeInTheDocument()
  await userEvent.click(screen.getByRole("button", { name: "Go to document" }))
  await waitFor(() => expect(status()).toHaveTextContent("held"))
  expect(screen.getByRole("textbox", { name: "Body" })).toHaveAttribute("contenteditable", "true")
})

test("a check-in at 60 s carries dirty after typing, and a 409 loses the lock", async () => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] })
  const checkIns: components["schemas"]["LockCheckIn"][] = []
  server.use(
    http.put("/api/v1/locks/:kind/:id", async ({ request }) => {
      checkIns.push((await request.json()) as components["schemas"]["LockCheckIn"])
      const lost: components["schemas"]["LockLost"] = { reason: "unlocked", by: "u3", lock: { holder: "" } }
      return HttpResponse.json(lost, { status: 409 })
    }),
  )
  await renderWithRouter(<Harness />)
  await waitFor(() => expect(status()).toHaveTextContent("held"))
  await userEvent.type(screen.getByRole("textbox", { name: "Body" }), " more")
  await vi.advanceTimersByTimeAsync(60_000)
  await waitFor(() => expect(status()).toHaveTextContent("lost"))
  expect(checkIns).toEqual([
    expect.objectContaining({ session: db.locks.get("rule:7")?.session, dirty: true, last_activity: expect.any(String) }),
  ])
  expect(screen.getByRole("textbox", { name: "Body" })).toHaveAttribute("contenteditable", "false")
})

test("pagehide with unsaved changes still releases the lock, after onBeforeRelease", async () => {
  const heldAtBeforeRelease: boolean[] = []
  await renderWithRouter(<Harness onBeforeRelease={() => heldAtBeforeRelease.push(db.locks.has("rule:7"))} />)
  await waitFor(() => expect(status()).toHaveTextContent("held"))
  await userEvent.type(screen.getByRole("textbox", { name: "Body" }), " unsaved")
  window.dispatchEvent(new Event("pagehide"))
  await waitFor(() => expect(db.locks.has("rule:7")).toBe(false))
  expect(heldAtBeforeRelease).toEqual([true])
})
