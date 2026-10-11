import { useCallback, useMemo, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { IdeaForm, NewIdeaFlow } from "@/components/IdeaForm"
import { Button } from "@/components/ui/button"
import { ChipNav } from "@/components/ui/chip-nav"
import { DataTable, type DataTableColumn } from "@/components/ui/data-table"
import { ForwardIcon, PencilIcon, TrashIcon } from "@/components/icons"
import { useCategoryParam, useTableUrlState } from "@/hooks/use-table-url-state"
import { IDEA_TYPE_OPTIONS, STORY_TYPE_OPTIONS } from "@/lib/content-types"
import { CREW } from "@/lib/crew"
import { IDEA_STATUS_OPTIONS, getIdeas, setIdeas, type Idea } from "@/lib/ideas"
import { SidePanel, SidePanelTitle } from "@/components/ui/side-panel"

// One chip per idea type, like Documents' categories, plus All (the
// default) for when you can't find something. A type chip stands in for
// the Type column; All keeps it, since its rows are mixed.
const CATEGORIES = [
  { value: "all", label: "All" },
  { value: "general", label: "General Ideas" },
  { value: "story-pitch", label: "Story Pitches" },
] as const

type Category = (typeof CATEGORIES)[number]["value"]

// Columns take the Edit action's handler, since editing opens this
// component's panel. Type only on All; Story Type on All and Story
// Pitches, where it applies.
function ideaColumns(
  onEdit: (idea: Idea, save: (patch: Partial<Idea>) => void) => void,
  open: { intake: (ideaId: string) => void; edit: (ideaId: string) => void },
  { type, storyType }: { type: boolean; storyType: boolean },
): DataTableColumn<Idea>[] {
  return [
  {
    key: "title",
    label: "Title",
    type: "text",
    width: "w-70",
    filterable: false,
    // The title opens the idea in the slide-in panel (the pencil opens it as
    // a full page instead) — or, once in intake, goes to its Intake view
    render: (idea, { updateRow }) => (
      <button
        type="button"
        onClick={() => {
          if (idea.status === "intake") open.intake(idea.id)
          else onEdit(idea, updateRow)
        }}
        className="cursor-pointer text-left underline-offset-2 transition-colors hover:text-highlight hover:underline"
      >
        {idea.title}
      </button>
    ),
  },
  { key: "author", label: "Author", type: "user", width: "w-50" },
  ...(type
    ? [{ key: "type", label: "Type", type: "select", width: "w-40", options: IDEA_TYPE_OPTIONS } as const]
    : []),
  ...(storyType
    ? [{ key: "storyType", label: "Story Type", type: "select", width: "w-40", options: STORY_TYPE_OPTIONS } as const]
    : []),
  { key: "status", label: "Status", type: "select", width: "w-35", options: IDEA_STATUS_OPTIONS },
  { key: "submitted", label: "Submitted", type: "date", width: "w-40" },
  {
    key: "actions",
    label: "",
    type: "actions",
    actions: [
      // Story Pitches not yet in intake: moves the pitch into the intake
      // flow, after a confirm
      {
        id: "forward",
        label: "Forward idea",
        icon: <ForwardIcon size={20} />,
        confirm: "Are you sure you want to move this idea to the intake flow?",
        when: (idea) => idea.type === "story-pitch" && idea.status !== "intake",
        onSelect: (_idea, { updateRow }) => updateRow({ status: "intake" }),
      },
      {
        id: "edit",
        label: "Edit idea",
        icon: <PencilIcon size={20} />,
        // Opens the form as a full page (the title opens it in the panel)
        // — or, once the idea is in intake, goes to its Intake view instead
        onSelect: (idea) => {
          if (idea.status === "intake") open.intake(idea.id)
          else open.edit(idea.id)
        },
      },
      {
        id: "delete",
        label: "Delete idea",
        icon: <TrashIcon size={20} />,
        variant: "destructive",
        onSelect: (_idea, { removeRow }) => removeRow(),
      },
    ],
  },
]
}

/**
 * The Ideas table, narrowed by chips like the Documents page: All (the
 * default), General Ideas, Story Pitches. "New Idea" opens the New Idea flow (choose a type,
 * then the form), and clicking an idea's title opens the same form filled
 * in with that idea (its pencil opens it as a full page), in a panel that
 * slides in from the right on desktop and up from the bottom on mobile;
 * the same form is also a full page at /ideas/new. Rows come from the
 * shared ideas store (src/lib/ideas.ts), and every change is written back
 * to it, so ideas added from the full page show up here too.
 */
export function IdeaList() {
  const [formOpen, setFormOpen] = useState(false)
  // The idea being edited, and how to save it back to its row
  const [editing, setEditing] = useState<{
    idea: Idea
    save: (patch: Partial<Idea>) => void
  } | null>(null)
  const [category, setCategory] = useCategoryParam(
    CATEGORIES.map((option) => option.value),
    "all",
  )
  const navigate = useNavigate()
  // Stable across renders (setEditing and navigate never change), so
  // DataTable doesn't rebuild its columns every render — one set per chip
  const columnsByCategory = useMemo(() => {
    const onEdit = (idea: Idea, save: (patch: Partial<Idea>) => void) => setEditing({ idea, save })
    const open = {
      intake: (ideaId: string) => void navigate({ to: "/ideas/$ideaId/intake", params: { ideaId } }),
      edit: (ideaId: string) => void navigate({ to: "/ideas/$ideaId/edit", params: { ideaId } }),
    }
    const plain = ideaColumns(onEdit, open, { type: false, storyType: false })
    return {
      all: ideaColumns(onEdit, open, { type: true, storyType: true }),
      general: plain,
      "story-pitch": ideaColumns(onEdit, open, { type: false, storyType: true }),
    }
  }, [navigate])
  const urlState = useTableUrlState(columnsByCategory[category])
  const inCategory = useCallback(
    (idea: Idea) => category === "all" || idea.type === category,
    [category],
  )
  // Follow an idea to its chip after adding or editing it, so it doesn't
  // land out of sight under another one (All already shows everything)
  const showCategoryOf = (idea: Idea) => {
    if (category !== "all") setCategory(idea.type as Category)
  }
  const close = () => {
    setFormOpen(false)
    setEditing(null)
  }

  return (
    <DataTable
      columns={columnsByCategory[category]}
      {...urlState}
      data={getIdeas()}
      rowFilter={inCategory}
      toolbarBelow={
        <ChipNav label="Idea type" options={CATEGORIES} value={category} onChange={setCategory} />
      }
      getRowId={(idea) => idea.id}
      onChange={setIdeas}
      users={CREW}
      title="Ideas"
      toolbarActions={({ addRow }) => (
        <>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setFormOpen(true)}
            // Outlined buttons fill with their border color on hover; in dark
            // mode, highlight with dark text (white on it is only ~3:1)
            className="rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
          >
            New Idea
          </Button>
          <SidePanel open={formOpen || editing !== null} onOpenChange={(open) => !open && close()}>
            {editing ? (
              <IdeaForm
                idea={editing.idea}
                renderHeading={(text) => <SidePanelTitle>{text}</SidePanelTitle>}
                onClose={close}
                onSubmit={(idea) => {
                  editing.save(idea)
                  showCategoryOf(idea)
                  close()
                }}
              />
            ) : (
              <NewIdeaFlow
                renderHeading={(text) => <SidePanelTitle>{text}</SidePanelTitle>}
                onClose={close}
                onSubmit={(idea) => {
                  addRow(idea)
                  showCategoryOf(idea)
                  close()
                }}
              />
            )}
          </SidePanel>
        </>
      )}
    />
  )
}
