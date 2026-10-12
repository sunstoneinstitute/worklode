import { useProjects } from "@/api/queries"
import { DataTable, type DataTableColumn } from "@/components/ui/data-table"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { useTableUrlState } from "@/hooks/use-table-url-state"

type Row = { id: string; name: string; key: string; horizon: string; repos: string }

const COLUMNS: DataTableColumn<Row>[] = [
  {
    key: "name",
    label: "Name",
    type: "text",
    width: "w-70",
    filterable: false,
    // A plain link: the public host redirects /projects/{id} to the legacy
    // page until that page is ported (WL-REQ-1305).
    render: (row) => (
      <a
        href={`/projects/${row.id}`}
        className="underline-offset-2 transition-colors hover:text-highlight hover:underline"
      >
        {row.name}
      </a>
    ),
  },
  { key: "key", label: "Key", type: "text", width: "w-30", filterable: false },
  { key: "horizon", label: "Horizon", type: "text", width: "w-40", filterable: false },
  { key: "repos", label: "Repos", type: "text", filterable: false },
]

// Module-level so DataTable gets a stable function; a new one each render
// would rebuild every column
function getRowId(row: Row) {
  return row.id
}

export function Projects() {
  const projects = useProjects()
  const urlState = useTableUrlState(COLUMNS)

  let content
  if (projects.isPending) content = <LoadingBlock label="Loading projects" className="h-64" />
  else if (projects.isError)
    content = <LoadError message="Couldn't load projects." onRetry={() => void projects.refetch()} />
  else {
    const rows: Row[] = projects.data.map((project) => ({
      id: project.id ?? "",
      name: project.name ?? "",
      key: project.key ?? "",
      horizon: project.horizon ?? "",
      repos: (project.repos ?? []).map((mapping) => mapping.repo).join(", "),
    }))
    content = (
      // The table copies `data` when it mounts; a new key per fetch shows a refetch
      <DataTable
        key={projects.dataUpdatedAt}
        columns={COLUMNS}
        {...urlState}
        data={rows}
        getRowId={getRowId}
        title="Projects"
      />
    )
  }

  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 pt-8 lg:p-page">{content}</main>
    </div>
  )
}
