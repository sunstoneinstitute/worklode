import type { ReactNode, Ref } from "react"
import { queryKeys } from "@/api/queries"
import type { Idea } from "@/api/types"
import { ideaForm } from "@/forms/configs"
import { SchemaForm, type SchemaFormHandle } from "@/forms/SchemaForm"

/** Lets a container (the Intake page's side panel) close the form through its leave guard */
export type IdeaFormHandle = SchemaFormHandle

/** The New and Edit Idea form (WL-REQ-1806). A new idea is private; there is no status field. */
export function IdeaForm({
  idea,
  onDone,
  onClose,
  heading,
  ref,
}: {
  idea?: Idea
  onDone: () => void
  onClose: () => void
  /** Renders the form's title; a side panel passes its own accessible title */
  heading?: (text: string) => ReactNode
  ref?: Ref<IdeaFormHandle>
}) {
  return (
    <SchemaForm
      config={ideaForm}
      record={idea}
      noun="the idea"
      invalidates={queryKeys.ideas.all}
      heading={heading}
      onDone={onDone}
      onClose={onClose}
      ref={ref}
      intro={
        <p className="text-sm">
          {idea?.status === "intake"
            ? "This idea is shared: everyone can see it in intake."
            : "Private: only you can see this idea until you share it from the Ideas list."}
        </p>
      }
    />
  )
}
