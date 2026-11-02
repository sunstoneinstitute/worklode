import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { HttpResponse, http } from "msw"
import { expect, it, vi } from "vitest"
import { server } from "@/mocks/node"
import { renderWithRouter } from "@/test/render"
import { SchemaForm } from "../SchemaForm"
import type { FormConfig } from "../types"

// The editor's floating fields sit inside the form's DOM. AnchorField stops the
// keydown from bubbling, so this stands in for it where jsdom can't open its menu.
vi.mock("@/editor/BodyEditor", () => ({
  BodyEditor: () => <input aria-label="Anchor" onKeyDown={(event) => event.stopPropagation()} />,
}))
vi.mock("@/editor/BodyToolbar", () => ({ BodyToolbar: () => null }))

const config: FormConfig = {
  id: "body",
  create: { title: "New", submit: { method: "POST", url: "/api/v1/things", label: "Create" } },
  schema: { type: "object", properties: { blocks: { type: "array", title: "Body" } } },
  layout: { type: "single", columns: { main: [{ field: "blocks", widget: "richtext" }] } },
}

it("does not submit the form on Enter in a field inside the editor", async () => {
  const post = vi.fn(() => HttpResponse.json({}))
  server.use(http.post("/api/v1/things", post))
  await renderWithRouter(<SchemaForm config={config} onDone={() => {}} onClose={() => {}} />)
  await userEvent.type(await screen.findByLabelText("Anchor"), "x{Enter}")
  await new Promise((resolve) => setTimeout(resolve, 20))
  expect(post).not.toHaveBeenCalled()
})
