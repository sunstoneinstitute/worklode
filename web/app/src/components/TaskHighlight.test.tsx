import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { TaskHighlight } from "./TaskHighlight"

describe("TaskHighlight", () => {
  it("shows the task and its detail, with the important symbol", () => {
    const { container } = render(<TaskHighlight title="Review the pitch" detail="Due Friday" />)

    expect(screen.getByRole("status")).toHaveTextContent("Review the pitch")
    expect(screen.getByText("Due Friday")).toBeInTheDocument()
    expect(container.querySelector("i.wlicon-error")).toBeInTheDocument()
  })

  it("becomes a link with an arrow when it has an href", () => {
    const { container } = render(<TaskHighlight title="Review the pitch" href="#/ideas" />)

    expect(screen.getByRole("link", { name: /Review the pitch/ })).toHaveAttribute("href", "#/ideas")
    expect(container.querySelector("i.wlicon-arrow-forward")).toBeInTheDocument()
  })
})
