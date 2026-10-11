import { Link } from "@tanstack/react-router"
import { Button } from "@/components/ui/button"
import { DataTable, type DataTableColumn } from "@/components/ui/data-table"
import { PencilIcon, TrashIcon } from "@/components/icons"
import { useTableUrlState } from "@/hooks/use-table-url-state"
import { STORY_TYPE_OPTIONS } from "@/lib/content-types"
import { CREW } from "@/lib/crew"

type Project = {
  id: string
  title: string
  author: string
  type: string
  status: string
  submitted: string
}

// The Grid and Melting Glaciers match Home's quick links
const PROJECTS: Project[] = [
  { id: "the-grid", title: "The Grid", author: "u1", type: "data-story", status: "accepted", submitted: "2026-07-14" },
  { id: "melting-glaciers", title: "Melting Glaciers", author: "u2", type: "data-story", status: "accepted", submitted: "2026-07-30" },
  { id: "night-shift", title: "Night shift at the reservoir", author: "u3", type: "short-story", status: "litmus", submitted: "2026-09-08" },
  { id: "urban-heat-islands", title: "Urban heat islands", author: "u4", type: "data-story", status: "on-hold", submitted: "2026-08-21" },
  // Came over from Ideas once it reached project status
  { id: "grid-outages", title: "Grid outages, block by block", author: "u1", type: "data-story", status: "litmus", submitted: "2026-08-18" },
]

const STATUS_OPTIONS = [
  { value: "litmus", label: "Litmus", colorVar: "--color-accent" },
  { value: "on-hold", label: "On Hold" },
  { value: "accepted", label: "Accepted", colorVar: "--color-accent-success" },
]

const COLUMNS: DataTableColumn<Project>[] = [
  {
    key: "title",
    label: "Title",
    type: "text",
    width: "w-70",
    filterable: false,
    // The title opens that project's overview. Still a text column
    // underneath, so sorting and search keep working off the title itself.
    render: (project) => (
      <Link
        to="/projects/$projectId"
        params={{ projectId: project.id }}
        className="underline-offset-2 transition-colors hover:text-highlight hover:underline"
      >
        {project.title}
      </Link>
    ),
  },
  { key: "author", label: "Author", type: "user", width: "w-50" },
  { key: "type", label: "Type", type: "select", width: "w-45", options: STORY_TYPE_OPTIONS },
  { key: "status", label: "Status", type: "select", width: "w-35", options: STATUS_OPTIONS },
  { key: "submitted", label: "Submitted", type: "date", width: "w-40" },
  {
    key: "actions",
    label: "",
    type: "actions",
    actions: [
      {
        id: "edit",
        label: "Edit project",
        icon: <PencilIcon size={20} />,
        onSelect: () => {},
      },
      {
        id: "delete",
        label: "Delete project",
        icon: <TrashIcon size={20} />,
        variant: "destructive",
        onSelect: (_project, { removeRow }) => removeRow(),
      },
    ],
  },
]

let nextProjectId = PROJECTS.length + 1

function blankProject(): Project {
  return {
    id: `project-${nextProjectId++}`,
    title: "Untitled project",
    author: "",
    type: "data-story",
    status: "litmus",
    submitted: "",
  }
}

export function ProjectList() {
  const urlState = useTableUrlState(COLUMNS)

  return (
    <DataTable
      columns={COLUMNS}
      {...urlState}
      data={PROJECTS}
      getRowId={(project) => project.id}
      users={CREW}
      title="Projects"
      toolbarActions={({ addRow }) => (
        <Button
          variant="outline"
          size="sm"
          onClick={() => addRow(blankProject())}
          // Outlined buttons fill with their border color on hover; in dark
          // mode, highlight with dark text (white on it is only ~3:1)
          className="rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
        >
          New Project
        </Button>
      )}
    />
  )
}
