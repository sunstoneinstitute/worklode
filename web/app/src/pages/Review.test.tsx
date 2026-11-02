import { screen, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { db } from "@/mocks/db"
import { renderApp } from "@/test/render"

function seedReview() {
  db.reviews.push({
    id: 7, subject: "doc", doc: 1, state: "changes_requested", base_ref: "1", round: 1, opened_by: "ada", opened_at: "2026-10-30T09:00:00Z", reviewers: ["stig"],
    rounds: [{ review: 7, round: 1, verdict: "changes_requested", actor: "stig", focused: true, client: "web", overall_note: "", base_ref: "1", submitted_at: "2026-10-31T09:00:00Z" }],
    threads: [
      { id: 1, review: 7, intent: "question", status: "open", author: "stig", anchor: { doc: 1, doc_anchor: "sec-2", quote: "ship it on Friday" }, messages: [{ id: 1, thread: 1, author: "stig", body: "Why Friday?", role: "reviewer", seq: 1 }] },
      { id: 2, review: 7, intent: "change_request", status: "obsolete", author: "stig", anchor: { doc: 1, doc_anchor: "sec-3", quote: "removed paragraph" }, messages: [] },
    ],
  })
  db.guides[7] = {
    overview: "Start with the rollout section.", round: 1,
    files: [{ path: "go.sum", scrutiny: false, skims: [{ from: 1, to: 40, decided_by: "policy" }] }],
  }
}

describe("/projects/$projectId/reviews/$reviewId", () => {
  it("shows the verdict, both threads with quote and section, and the policy skim, with no form", async () => {
    seedReview()
    await renderApp("/projects/p1/reviews/7")
    expect(await screen.findByText(/Round 1: changes_requested by stig/)).toBeInTheDocument()
    const question = (await screen.findByText("ship it on Friday")).closest("li")!
    expect(within(question).getByText("Section sec-2")).toBeInTheDocument()
    expect(within(question).getByText("Why Friday?")).toBeInTheDocument()
    const obsolete = screen.getByText("removed paragraph").closest("li")!
    expect(obsolete).toHaveClass("line-through")
    expect(await screen.findByText("Lines 1-40: skimmed by policy")).toBeInTheDocument()
    const main = screen.getByRole("main")
    expect(within(main).queryByRole("button")).not.toBeInTheDocument()
    expect(within(main).queryByRole("textbox")).not.toBeInTheDocument()
  })

  it("shows no skims for an unfocused round", async () => {
    seedReview()
    db.reviews.find((r) => r.id === 7)!.rounds![0].focused = false
    await renderApp("/projects/p1/reviews/7")
    expect(await screen.findByText("go.sum")).toBeInTheDocument()
    expect(screen.queryByText(/skimmed by/)).not.toBeInTheDocument()
  })

  it("lists the project's open reviews on the documents page, not another project's", async () => {
    await renderApp("/projects/p1/documents")
    const link = await screen.findByRole("link", { name: "doc 1" })
    expect(link).toHaveAttribute("href", "/projects/p1/reviews/1")
    expect(screen.queryByRole("link", { name: "doc 4" })).not.toBeInTheDocument()
  })
})
