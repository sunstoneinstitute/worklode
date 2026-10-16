import { type FormEvent, useCallback, useMemo, useState } from "react"
import { useCreateProjectDocument, useDeleteDocument, useUpdateDocument } from "@/api/mutations"
import { useCrew, useProjectDocuments } from "@/api/queries"
import type { ProjectDocument } from "@/api/types"
import { TrashIcon } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { DataTable, type DataTableColumn, type DataTableUser, type TableHelpers } from "@/components/ui/data-table"
import { Input } from "@/components/ui/input"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { useTableUrlState } from "@/hooks/use-table-url-state"
import { DOCUMENT_CATEGORY_OPTIONS, DOCUMENT_STATUS_OPTIONS, DOCUMENT_TYPE_OPTIONS } from "@/lib/content-types"

const NAME: DataTableColumn<ProjectDocument> = {
  key: "name",
  label: "Name",
  type: "text",
  width: "w-70",
  filterable: false,
  render: (document) =>
    document.url ? (
      <a
        href={document.url}
        target="_blank"
        rel="noreferrer"
        className="underline-offset-2 transition-colors hover:text-highlight hover:underline"
      >
        {document.name}
      </a>
    ) : (
      document.name
    ),
}
const CATEGORY: DataTableColumn<ProjectDocument> = {
  key: "category",
  label: "Category",
  type: "select",
  width: "w-40",
  options: DOCUMENT_CATEGORY_OPTIONS,
}
const TYPE: DataTableColumn<ProjectDocument> = {
  key: "type",
  label: "Type",
  type: "select",
  width: "w-40",
  options: DOCUMENT_TYPE_OPTIONS,
}
const STATUS: DataTableColumn<ProjectDocument> = {
  key: "status",
  label: "Status",
  type: "select",
  width: "w-35",
  options: DOCUMENT_STATUS_OPTIONS,
}
const SOURCE: DataTableColumn<ProjectDocument> = {
  key: "source",
  label: "Source",
  type: "text",
  width: "w-50",
  filterable: false,
}
const ACTIONS: DataTableColumn<ProjectDocument> = {
  key: "actions",
  label: "",
  type: "actions",
  actions: [
    {
      id: "delete",
      label: "Delete document",
      icon: <TrashIcon size={20} />,
      variant: "destructive",
      confirm: (document) => `Delete ${document.name}?`,
      onSelect: (_document, { removeRow }) => removeRow(),
    },
  ],
}

/** The Sources columns, for other Sources tables (an idea's sources) */
export const SOURCE_COLUMNS = [NAME, TYPE, STATUS, SOURCE, ACTIONS]

// Module-level so DataTable gets a stable array (see TaskList)
const COLUMNS: DataTableColumn<ProjectDocument>[] = [
  NAME,
  CATEGORY,
  TYPE,
  STATUS,
  SOURCE,
  { key: "milestone", label: "Milestone", type: "text", width: "w-40", filterable: false },
  { key: "due", label: "Due", type: "date", width: "w-40" },
  { key: "date", label: "Date", type: "date", width: "w-40" },
  { key: "participants", label: "Participants", type: "users", width: "w-45" },
  ACTIONS,
]

function getDocumentId(document: ProjectDocument) {
  return document.id ?? ""
}

/** A project's documents register (WL-REQ-1798), the Documents page's Files & links tab */
export function DocumentList({ projectId }: { projectId: string }) {
  const documents = useProjectDocuments(projectId)
  const crew = useCrew()
  const createDocument = useCreateProjectDocument(projectId)
  const updateMutate = useUpdateDocument().mutate
  const deleteMutate = useDeleteDocument().mutate
  const createMutate = createDocument.mutate
  const urlState = useTableUrlState(COLUMNS)
  const users = useMemo<DataTableUser[]>(
    () => (crew.data ?? []).map((c) => ({ id: c.actor ?? "", name: c.display_name || (c.actor ?? "") })),
    [crew.data],
  )
  const onUpdateRow = useCallback(
    (id: string, patch: Partial<ProjectDocument>) => updateMutate({ id, patch }),
    [updateMutate],
  )
  const onRemoveRow = useCallback((id: string) => deleteMutate(id), [deleteMutate])
  const onAddRow = useCallback(
    (row: ProjectDocument) =>
      createMutate({ name: row.name, url: row.url, category: row.category, type: "document", status: "draft" }),
    [createMutate],
  )

  if (documents.isPending) return <LoadingBlock label="Loading documents" className="h-80" />
  if (documents.isError) {
    return <LoadError message="Couldn't load documents." onRetry={() => void documents.refetch()} />
  }

  return (
    <DataTable
      columns={COLUMNS}
      data={documents.data}
      getRowId={getDocumentId}
      users={users}
      onUpdateRow={onUpdateRow}
      onRemoveRow={onRemoveRow}
      onAddRow={onAddRow}
      title="Files & links"
      {...urlState}
      toolbarActions={({ addRow }) => <NewDocument addRow={addRow} pending={createDocument.isPending} />}
    />
  )
}

function NewDocument({ addRow, pending }: { pending: boolean } & TableHelpers<ProjectDocument>) {
  const [name, setName] = useState("")
  const [url, setUrl] = useState("")
  const [category, setCategory] = useState("")

  function submit(event: FormEvent) {
    event.preventDefault()
    addRow({ name: name.trim(), url: url.trim(), category })
    setName("")
    setUrl("")
  }

  return (
    <form onSubmit={submit} className="flex flex-wrap items-center gap-2.5">
      <Input
        aria-label="New document name"
        placeholder="New document"
        value={name}
        onChange={(e) => setName(e.target.value)}
        className="w-56"
      />
      <Input
        aria-label="New document URL"
        placeholder="URL"
        type="url"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        className="w-56"
      />
      <Select value={category} onValueChange={setCategory}>
        <SelectTrigger aria-label="Category" className="w-44">
          <SelectValue placeholder="Category" />
        </SelectTrigger>
        <SelectContent>
          {DOCUMENT_CATEGORY_OPTIONS.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Button type="submit" size="sm" disabled={!name.trim() || !category || pending}>
        Add document
      </Button>
    </form>
  )
}
