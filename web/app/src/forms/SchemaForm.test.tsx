import { createPortal } from "react-dom"
import { fireEvent, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { HttpResponse, delay, http } from "msw"
import { describe, expect, it, vi } from "vitest"
import { server } from "@/mocks/node"
import { renderWithRouter } from "@/test/render"
import { SchemaForm } from "./SchemaForm"
import type { FormConfig, FormValues } from "./types"

const config: FormConfig = {
  id: "thing",
  create: { title: "New thing", submit: { method: "POST", url: "/api/v1/things", label: "Create" } },
  edit: { title: "Edit thing", submit: { method: "PATCH", url: "/api/v1/things/{id}", label: "Save" } },
  defaults: { type: "a" },
  schema: {
    type: "object",
    required: ["title"],
    properties: {
      title: { type: "string", title: "Title", errorMessage: "Name it." },
      type: { type: "string", title: "Type", oneOf: [{ const: "a", title: "Plain" }, { const: "b", title: "Fancy" }] },
      story: { type: "string", title: "Story" },
      due: { type: "string", title: "Due", format: "date" },
      odd: { type: "string", title: "Odd" },
    },
  },
  layout: {
    type: "main-sidebar",
    columns: {
      main: ["title", { field: "story", showWhen: { field: "type", equals: "b" } }, { field: "odd", widget: "no-such-widget" }],
      sidebar: ["type", "due", { block: "note", title: "Tip", markdown: "## Heads up\n\n1. One\n2. Two" }],
    },
  },
}

function sends(method: "post" | "patch", url: string) {
  const bodies: FormValues[] = []
  server.use(
    http[method](url, async ({ request }) => {
      bodies.push((await request.json()) as FormValues)
      return HttpResponse.json({})
    }),
  )
  return bodies
}

async function open(record?: FormValues, onDone = vi.fn(), form = config) {
  await renderWithRouter(<SchemaForm config={form} record={record} onDone={onDone} onClose={() => {}} />)
  await screen.findAllByRole("heading")
}
const key = "worklode:v1:draft:actor-1:thing:1"
const stored = (values: FormValues, base = "t1") =>
  localStorage.setItem(key, JSON.stringify({ values, base, saved_at: "2026-11-01T10:00:00Z" }))
const record = { id: "1", title: "Saved", type: "a", story: "", due: "", odd: "", updated_at: "t1" }

describe("SchemaForm", () => {
  it("renders a config: note, unknown widget as text, default widgets", async () => {
    await open()
    expect(screen.getByRole("heading", { name: "New thing" })).toBeInTheDocument()
    expect(screen.getByRole("heading", { name: "Heads up" })).toBeInTheDocument()
    expect(screen.getByRole("list")).toHaveTextContent("OneTwo")
    expect(screen.getByLabelText("Odd").tagName).toBe("INPUT")
    expect(screen.getByLabelText("Due")).toHaveAttribute("type", "date")
  })

  it("posts the shown fields trimmed and leaves a hidden one out", async () => {
    const bodies = sends("post", "/api/v1/things")
    const onDone = vi.fn()
    await open(undefined, onDone)
    await userEvent.type(screen.getByLabelText("Title"), "  Ocean  ")
    expect(screen.queryByLabelText("Story")).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "Create" }))
    await waitFor(() => expect(onDone).toHaveBeenCalled())
    expect(bodies[0]).toEqual({ title: "Ocean", odd: "", type: "a", due: "" })
  })

  it("checks in the browser first, and sends nothing while invalid", async () => {
    const bodies = sends("post", "/api/v1/things")
    await open()
    await userEvent.click(screen.getByRole("button", { name: "Create" }))
    expect(await screen.findByText("Name it.")).toBeInTheDocument()
    expect(bodies).toEqual([])
  })

  it("gives the server's message precedence until the field changes", async () => {
    server.use(http.post("/api/v1/things", () => HttpResponse.json({ error: "bad", fields: { title: "Taken." } }, { status: 422 })))
    await open()
    await userEvent.type(screen.getByLabelText("Title"), "x")
    await userEvent.click(screen.getByRole("button", { name: "Create" }))
    expect(await screen.findByText("Taken.")).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText("Title"), "y")
    expect(screen.queryByText("Taken.")).not.toBeInTheDocument()
  })

  it("shows a 403's reasons under their fields", async () => {
    server.use(http.patch("/api/v1/things/1", () => HttpResponse.json({ error: "no", fields: { due: "due is fixed" } }, { status: 403 })))
    await open(record)
    await userEvent.click(screen.getByRole("button", { name: "Save" }))
    expect(await screen.findByText("due is fixed")).toBeInTheDocument()
  })
})

describe("submitting", () => {
  it("ignores a form nested in it, as a dialog's portal bubbles it through React", async () => {
    const bodies = sends("post", "/api/v1/things")
    const onDone = vi.fn()
    const nested = createPortal(
      <form onSubmit={(event) => event.preventDefault()}>
        <button type="submit">Inner</button>
      </form>,
      document.body,
    )
    await renderWithRouter(<SchemaForm config={config} intro={nested} onDone={onDone} onClose={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Title"), "x")
    await userEvent.click(screen.getByRole("button", { name: "Inner" }))
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(bodies).toEqual([])
    expect(onDone).not.toHaveBeenCalled()
  })

  it("sends one save for a repeated Ctrl+S", async () => {
    let saves = 0
    server.use(
      http.patch("/api/v1/things/1", async () => {
        saves += 1
        await delay(30)
        return HttpResponse.json({})
      }),
    )
    const onDone = vi.fn()
    await open(record, onDone)
    const title = screen.getByLabelText("Title")
    fireEvent.keyDown(title, { key: "s", ctrlKey: true })
    fireEvent.keyDown(title, { key: "s", ctrlKey: true })
    await waitFor(() => expect(onDone).toHaveBeenCalled())
    fireEvent.keyDown(title, { key: "s", ctrlKey: true })
    await new Promise((resolve) => setTimeout(resolve, 60))
    expect(saves).toBe(1)
  })
})

describe("field access", () => {
  it("shows a locked field as text with its reason and never sends it", async () => {
    const bodies = sends("patch", "/api/v1/things/1")
    await open({ ...record, field_access: { type: "type is fixed" } })
    expect(screen.queryByRole("combobox", { name: "Type" })).not.toBeInTheDocument()
    expect(screen.getByText("Plain")).toBeInTheDocument()
    expect(screen.getByText("type is fixed")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]).not.toHaveProperty("type")
  })

  it("shows no Save and keeps no draft when every shown field is locked", async () => {
    stored({ ...record, title: "mine" })
    await open({ ...record, field_access: { title: "x", type: "x", due: "x", odd: "x" } })
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument()
    expect(screen.queryByText(/Draft restored/)).not.toBeInTheDocument()
  })
})

describe("drafts", () => {
  it("restores a draft on the same base, says which locked field was not restored", async () => {
    stored({ ...record, title: "mine", type: "b" })
    await open({ ...record, field_access: { type: "kind cannot change once set" } })
    expect(screen.getByLabelText("Title")).toHaveValue("mine")
    expect(screen.getByRole("status")).toHaveTextContent("Draft restored · Type was not restored: kind cannot change once set · Discard")
    await userEvent.click(screen.getByRole("button", { name: "Discard" }))
    expect(screen.getByLabelText("Title")).toHaveValue("Saved")
  })

  it("offers a draft from before a newer save, and View shows it beside the current", async () => {
    stored({ ...record, title: "mine" }, "t0")
    await open(record)
    expect(screen.getByLabelText("Title")).toHaveValue("Saved")
    expect(screen.getByRole("status")).toHaveTextContent(/You have an older draft from .* · View · Discard/)
    await userEvent.click(screen.getByRole("button", { name: "View" }))
    const dialog = screen.getByRole("dialog")
    expect(within(dialog).getByText("mine")).toBeInTheDocument()
    expect(within(dialog).getByText("Saved")).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole("button", { name: "Close" }))
    await userEvent.click(screen.getByRole("button", { name: "Discard" }))
    expect(screen.queryByRole("status")).not.toBeInTheDocument()
  })
})

describe("rows", () => {
  const rows: FormConfig = {
    id: "rows",
    edit: { title: "Rows", submit: { method: "PATCH", url: "/api/v1/things/{id}", label: "Save" } },
    schema: {
      type: "object",
      properties: {
        items: {
          type: "array",
          title: "Items",
          items: {
            type: "object",
            title: "item",
            required: ["name"],
            properties: {
              name: { type: "string", errorMessage: "Name the item." },
              date: { type: "string", format: "date" },
              current: { type: "boolean", maxContains: 1 },
              done: { type: "boolean", readOnly: true },
            },
          },
        },
      },
    },
    layout: {
      type: "single",
      columns: {
        main: [
          {
            field: "items",
            widget: "rows",
            sortBy: "date",
            lockedWhen: "done",
            columns: [
              { field: "name", title: "Name", widget: "text" },
              { field: "date", title: "Date", widget: "date" },
              { field: "current", title: "Current", widget: "radio" },
            ],
          },
        ],
      },
    },
  }
  const items = [
    { name: "b", date: "2026-02-01", current: true, done: false },
    { name: "old", date: "", current: false, done: true },
    { name: "fixed", date: "2026-03-01", current: false, done: true },
  ]

  it("edits, sorts, chooses one radio row, drops readOnly and honours lockedWhen", async () => {
    const bodies = sends("patch", "/api/v1/things/1")
    await open({ id: "1", items, updated_at: "t1" }, vi.fn(), rows)
    expect(screen.getByText("fixed")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Remove item 3" })).not.toBeInTheDocument()
    expect(screen.queryByLabelText("Name: item 2")).not.toBeInTheDocument()
    expect(screen.getByLabelText("Date: item 2")).toBeInTheDocument()

    await userEvent.click(screen.getByLabelText("Current: item 3"))
    await userEvent.click(screen.getByRole("button", { name: "Add item" }))
    await userEvent.type(screen.getByLabelText("Name: item 4"), "new")
    await userEvent.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    const sent = bodies[0].items as FormValues[]
    expect(sent.map((row) => row.name)).toEqual(["b", "fixed", "old", "new"])
    expect(sent.find((row) => row.current)?.name).toBe("fixed")
    expect(sent[0]).not.toHaveProperty("done")
  })

  it("shows a cell's error on its row", async () => {
    await open({ id: "1", items: [{ name: "", date: "", current: false }], updated_at: "t1" }, vi.fn(), rows)
    await userEvent.click(screen.getByRole("button", { name: "Save" }))
    expect(await screen.findByText("Name the item.")).toBeInTheDocument()
  })
})
