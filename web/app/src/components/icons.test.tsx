import { render } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { DownloadIcon, PreviewIcon, ViewColumnIcon } from "@/components/icons"

describe("new action icons", () => {
  it.each([
    [ViewColumnIcon, "wlicon-view-column"],
    [DownloadIcon, "wlicon-download"],
    [PreviewIcon, "wlicon-preview"],
  ])("renders the right glyph class", (IconComponent, expectedClass) => {
    const { container } = render(<IconComponent />)
    const icon = container.querySelector("i")

    expect(icon).toHaveClass(expectedClass)
    expect(icon).toHaveAttribute("aria-hidden", "true")
  })

  it("applies a custom size as a font-size", () => {
    const { container } = render(<ViewColumnIcon size={20} />)

    expect(container.querySelector("i")).toHaveStyle({ fontSize: "20px" })
  })
})
