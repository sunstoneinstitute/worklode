import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it } from "vitest"
import { getIdeas, resetIdeas } from "@/lib/ideas"
import { renderWithRouter } from "@/test/render"
import { EditIdea } from "./EditIdea"

describe("EditIdea", () => {
  beforeEach(() => resetIdeas())

  it("edits the idea as a full page, saves it to the store, and returns to Ideas", async () => {
    const user = userEvent.setup()
    const { router } = await renderWithRouter(<EditIdea ideaId="i2" />, { path: "/ideas/i2/edit" })

    const title = screen.getByRole("textbox", { name: "Title" })
    expect(title).toHaveValue("Reusable map legend")
    await user.clear(title)
    await user.type(title, "Map legend kit")
    await user.click(screen.getByRole("button", { name: "Save" }))

    expect(getIdeas().find((idea) => idea.id === "i2")?.title).toBe("Map legend kit")
    expect(router.state.location.pathname).toBe("/ideas")
  })
})
