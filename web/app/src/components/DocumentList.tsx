import { useCallback } from "react"
import { Button } from "@/components/ui/button"
import { ChipNav } from "@/components/ui/chip-nav"
import { DataTable, type DataTableColumn, type RowAction } from "@/components/ui/data-table"
import { DownloadIcon, FactCheckIcon, PencilIcon, TrashIcon } from "@/components/icons"
import { useCategoryParam, useTableUrlState } from "@/hooks/use-table-url-state"
import { CREW } from "@/lib/crew"

const CATEGORIES = [
  { value: "spec", label: "Spec" },
  { value: "plan", label: "Plan" },
  { value: "meeting-notes", label: "Meeting Notes" },
  { value: "deliverables", label: "Deliverables" },
  { value: "sources", label: "Sources" },
] as const

type Category = (typeof CATEGORIES)[number]["value"]

export type ProjectDocument = {
  id: string
  name: string
  category: Category
  type: string
  status: string
  /** Where it came from — Sources only */
  source?: string
  /** Which milestone it's due for — Deliverables only */
  milestone?: string
  /** ISO date — Deliverables only */
  due?: string
  /** ISO date the meeting happened — Meeting Notes only */
  date?: string
  /** User ids — Meeting Notes only */
  participants?: string[]
}

const DOCUMENTS: ProjectDocument[] = [
  { id: "d1", name: "Project spec", category: "spec", type: "document", status: "final" },
  { id: "d2", name: "Data requirements", category: "spec", type: "spreadsheet", status: "in-review" },
  { id: "d3", name: "Research plan", category: "plan", type: "document", status: "final" },
  { id: "d4", name: "Publication timeline", category: "plan", type: "spreadsheet", status: "draft" },
  { id: "d5", name: "Kickoff meeting", category: "meeting-notes", type: "document", status: "final", date: "2026-08-02", participants: ["u1", "u2", "u3", "u4"] },
  { id: "d6", name: "Weekly sync, Sep 15", category: "meeting-notes", type: "document", status: "final", date: "2026-09-15", participants: ["u1", "u3"] },
  { id: "d7", name: "Interactive map", category: "deliverables", type: "link", status: "in-review", milestone: "First draft", due: "2026-10-05" },
  { id: "d8", name: "Methodology note", category: "deliverables", type: "pdf", status: "draft", milestone: "Publication", due: "2026-11-12" },
  { id: "d9", name: "Grid outage dataset", category: "sources", type: "dataset", status: "final", source: "EIA Form 861" },
  { id: "d10", name: "Utility interview transcripts", category: "sources", type: "document", status: "in-review", source: "Crew interviews" },
]

// Placeholder file types until the real list is decided
const TYPE_OPTIONS = [
  { value: "document", label: "Document" },
  { value: "spreadsheet", label: "Spreadsheet" },
  { value: "pdf", label: "PDF" },
  { value: "slides", label: "Slides" },
  { value: "dataset", label: "Dataset" },
  { value: "link", label: "Link" },
]

const STATUS_OPTIONS = [
  { value: "draft", label: "Draft" },
  { value: "in-review", label: "In Review", colorVar: "--color-accent" },
  { value: "final", label: "Final", colorVar: "--color-accent-success" },
]

const NAME: DataTableColumn<ProjectDocument> = {
  key: "name",
  label: "Name",
  type: "text",
  width: "w-80",
  filterable: false,
}
const TYPE: DataTableColumn<ProjectDocument> = {
  key: "type",
  label: "Type",
  type: "select",
  width: "w-40",
  options: TYPE_OPTIONS,
}
const STATUS: DataTableColumn<ProjectDocument> = {
  key: "status",
  label: "Status",
  type: "select",
  width: "w-35",
  options: STATUS_OPTIONS,
}
const SOURCE: DataTableColumn<ProjectDocument> = {
  key: "source",
  label: "Source",
  type: "text",
  width: "w-50",
  filterable: false,
}
const MILESTONE: DataTableColumn<ProjectDocument> = {
  key: "milestone",
  label: "Milestone",
  type: "text",
  width: "w-40",
  filterable: false,
}

const DUE: DataTableColumn<ProjectDocument> = {
  key: "due",
  label: "Due Date",
  type: "date",
  width: "w-40",
}

const DATE: DataTableColumn<ProjectDocument> = {
  key: "date",
  label: "Date",
  type: "date",
  width: "w-40",
}
const PARTICIPANTS: DataTableColumn<ProjectDocument> = {
  key: "participants",
  label: "Participants",
  type: "users",
  width: "w-45",
}

// Placeholders, like Edit elsewhere — no download/review flow exists yet
const DOWNLOAD: RowAction<ProjectDocument> = {
  id: "download",
  label: "Download source",
  icon: <DownloadIcon size={20} />,
  onSelect: () => {},
}
const EDIT: RowAction<ProjectDocument> = {
  id: "edit",
  label: "Edit document",
  icon: <PencilIcon size={20} />,
  onSelect: () => {},
}
const REVIEW: RowAction<ProjectDocument> = {
  id: "review",
  label: "Review deliverable",
  icon: <FactCheckIcon size={20} />,
  onSelect: () => {},
}
const DELETE: RowAction<ProjectDocument> = {
  id: "delete",
  label: "Delete document",
  icon: <TrashIcon size={20} />,
  variant: "destructive",
  onSelect: (_document, { removeRow }) => removeRow(),
}

function actionsColumn(actions: RowAction<ProjectDocument>[]): DataTableColumn<ProjectDocument> {
  return { key: "actions", label: "", type: "actions", actions }
}

// Each category's own columns and row actions: Date + Participants only on
// Meeting Notes, Source + Download only on Sources, Milestone + Due Date +
// Review only on Deliverables. Module-level constants
// so DataTable gets a stable array per category (a fresh array each render
// would rebuild every column).
const COLUMNS_BY_CATEGORY: Record<Category, DataTableColumn<ProjectDocument>[]> = {
  spec: [NAME, TYPE, STATUS, actionsColumn([EDIT, DELETE])],
  plan: [NAME, TYPE, STATUS, actionsColumn([EDIT, DELETE])],
  "meeting-notes": [NAME, TYPE, STATUS, DATE, PARTICIPANTS, actionsColumn([EDIT, DELETE])],
  deliverables: [NAME, TYPE, STATUS, MILESTONE, DUE, actionsColumn([EDIT, REVIEW, DELETE])],
  sources: [NAME, TYPE, STATUS, SOURCE, actionsColumn([DOWNLOAD, EDIT, DELETE])],
}

/** The Sources chip's columns and actions, for other Sources tables (Intake) */
export const SOURCE_COLUMNS = COLUMNS_BY_CATEGORY.sources

let nextDocumentId = DOCUMENTS.length + 1

/**
 * A project's documents, one category at a time: the chip navigation under
 * the toolbar picks the category (Spec first), and the table shows only
 * that category's documents, with that category's columns and actions.
 * "New Document" adds to the category showing.
 */
export function DocumentList() {
  const [category, setCategory] = useCategoryParam(
    CATEGORIES.map((option) => option.value),
    "spec",
  )
  const urlState = useTableUrlState(COLUMNS_BY_CATEGORY[category])
  const inCategory = useCallback(
    (document: ProjectDocument) => document.category === category,
    [category],
  )

  return (
    <DataTable
      columns={COLUMNS_BY_CATEGORY[category]}
      {...urlState}
      data={DOCUMENTS}
      getRowId={(document) => document.id}
      users={CREW}
      title="Documents"
      rowFilter={inCategory}
      toolbarBelow={
        <ChipNav
          label="Document category"
          options={CATEGORIES}
          value={category}
          onChange={setCategory}
        />
      }
      toolbarActions={({ addRow }) => (
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            addRow({
              id: `d${nextDocumentId++}`,
              name: "Untitled document",
              category,
              type: "document",
              status: "draft",
            })
          }
          // Outlined buttons fill with their border color on hover; in dark
          // mode, highlight with dark text (white on it is only ~3:1)
          className="rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
        >
          New Document
        </Button>
      )}
    />
  )
}
